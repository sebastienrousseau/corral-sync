// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: GPL-3.0-only

// Package gitea implements the [remote.Provider] surface against the
// Gitea REST API v1. Gitea's create endpoint returns a proper 409
// Conflict on duplicate, so idempotence is straightforward.
package gitea

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/sebastienrousseau/corral-sync/internal/remote"
)

var (
	marshalJSON = json.Marshal
	newRequest  = http.NewRequestWithContext
)

// Client is the Gitea API client.
type Client struct {
	baseURL string
	token   string
	owner   string
	http    *http.Client
	log     *slog.Logger

	ownerMu sync.Mutex
	dest    destination
}

// New builds a Client. baseURL is the Gitea instance root (e.g.
// https://gitea.example.com). Owner may be empty; the client resolves the
// authenticated user on first use.
func New(baseURL, token, owner string, logger *slog.Logger) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		owner:   owner,
		http:    remote.NewHTTPClient(),
		log:     logger.With(slog.String("provider", "gitea")),
	}
}

// Name satisfies [remote.Provider].
func (c *Client) Name() string { return "gitea" }

// repoJSON is the subset of Gitea's Repository object we care about.
type repoJSON struct {
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	SSHURL   string `json:"ssh_url"`
	CloneURL string `json:"clone_url"`
	HTMLURL  string `json:"html_url"`
	Private  bool   `json:"private"`
}

type destination struct {
	owner      string
	createPath string
}

type responseError struct {
	method string
	path   string
	status int
	body   string
}

func (e *responseError) Error() string {
	return fmt.Sprintf("%s %s returned %d: %s", e.method, e.path, e.status, e.body)
}

// EnsureRepo satisfies [remote.Provider].
//
// Gitea's create endpoint is POST /api/v1/user/repos (for the token
// owner) or POST /api/v1/orgs/{org}/repos (for an org). The payload:
//
//	{
//	  "name":          "<repo>",
//	  "description":   "...",
//	  "private":       true|false,
//	  "auto_init":     false,
//	  "default_branch": "main"
//	}
//
// Gitea returns 409 Conflict when the repo already exists. On 409 we
// resolve the existing repo via GET /repos/{owner}/{name} and return its
// SSH URL.
func (c *Client) EnsureRepo(ctx context.Context, r remote.Repo) (string, error) {
	if r.Visibility == "" {
		r.Visibility = remote.Private
	}
	if err := remote.ValidateRepo(r); err != nil {
		return "", err
	}
	dest, err := c.resolveDestination(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve destination: %w", err)
	}

	// Check first. On Gitea instances with creation disabled or a zero
	// repository quota, an unnecessary POST fails even when the mirror
	// already exists and could be reused.
	rj, err := c.getRepo(ctx, dest.owner, r.Name)
	if err == nil {
		if err := validateRepo(rj, r.Visibility); err != nil {
			return "", err
		}
		c.log.Debug("repo exists, reusing", slog.String("full_name", rj.FullName))
		return rj.SSHURL, nil
	}
	if !isStatus(err, http.StatusNotFound) {
		return "", fmt.Errorf("check existing repo %s/%s: %w", dest.owner, r.Name, err)
	}

	payload := map[string]any{
		"name":           r.Name,
		"description":    r.Description,
		"private":        r.Visibility == remote.Private,
		"auto_init":      false,
		"default_branch": "main",
	}
	body, err := marshalJSON(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}

	req, err := newRequest(ctx, http.MethodPost, c.baseURL+dest.createPath, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("POST user/repos: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusCreated:
		var rj repoJSON
		if err := remote.DecodeJSON(resp.Body, &rj); err != nil {
			return "", fmt.Errorf("decode 201: %w", err)
		}
		if err := validateRepo(&rj, r.Visibility); err != nil {
			return "", err
		}
		c.log.Info("created repo", slog.String("full_name", rj.FullName))
		return rj.SSHURL, nil

	case http.StatusConflict:
		rj, err := c.getRepo(ctx, dest.owner, r.Name)
		if err != nil {
			return "", fmt.Errorf("resolve existing repo %s/%s: %w", dest.owner, r.Name, err)
		}
		if err := validateRepo(rj, r.Visibility); err != nil {
			return "", err
		}
		c.log.Debug("repo exists, reusing", slog.String("full_name", rj.FullName))
		return rj.SSHURL, nil

	default:
		msg, readErr := remote.ReadBody(resp.Body)
		if readErr != nil {
			return "", readErr
		}
		err := fmt.Errorf("gitea create returned %d: %s", resp.StatusCode, truncate(remote.Redact(msg, c.token)))
		if isFatalCreateError(resp.StatusCode, msg) {
			return "", remote.Fatal(err)
		}
		return "", err
	}
}

func validateRepo(rj *repoJSON, want remote.Visibility) error {
	got := remote.Public
	if rj.Private {
		got = remote.Private
	}
	if got != want {
		return fmt.Errorf("existing Gitea repository visibility is %q, want %q", got, want)
	}
	if err := remote.ValidateCloneURL(rj.SSHURL); err != nil {
		return fmt.Errorf("invalid Gitea clone URL: %w", err)
	}
	return nil
}

// resolveDestination determines both the repository owner used for
// lookups and the create endpoint. Empty owner or owner == token user
// creates personal repositories; any other configured owner is treated as
// an org and uses Gitea's org create endpoint.
func (c *Client) resolveDestination(ctx context.Context) (destination, error) {
	c.ownerMu.Lock()
	defer c.ownerMu.Unlock()

	if c.dest.createPath != "" {
		return c.dest, nil
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := c.getJSON(ctx, "/api/v1/user", &user); err != nil {
		return destination{}, err
	}
	if user.Login == "" {
		return destination{}, fmt.Errorf("gitea /user returned empty login")
	}

	owner := c.owner
	if owner == "" {
		owner = user.Login
	}
	if owner == user.Login {
		c.dest = destination{
			owner:      owner,
			createPath: "/api/v1/user/repos",
		}
		return c.dest, nil
	}

	c.dest = destination{
		owner:      owner,
		createPath: "/api/v1/orgs/" + url.PathEscape(owner) + "/repos",
	}
	return c.dest, nil
}

// getRepo fetches an existing repository by owner and name.
func (c *Client) getRepo(ctx context.Context, owner, name string) (*repoJSON, error) {
	path := "/api/v1/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
	req, err := newRequest(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		msg, readErr := remote.ReadBody(resp.Body)
		if readErr != nil {
			return nil, readErr
		}
		return nil, &responseError{
			method: http.MethodGet,
			path:   path,
			status: resp.StatusCode,
			body:   truncate(remote.Redact(msg, c.token)),
		}
	}
	var rj repoJSON
	if err := remote.DecodeJSON(resp.Body, &rj); err != nil {
		return nil, err
	}
	return &rj, nil
}

// getJSON is a small helper for authenticated GET-then-decode requests.
func (c *Client) getJSON(ctx context.Context, path string, dst any) error {
	req, err := newRequest(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		msg, readErr := remote.ReadBody(resp.Body)
		if readErr != nil {
			return readErr
		}
		err := &responseError{
			method: http.MethodGet,
			path:   path,
			status: resp.StatusCode,
			body:   truncate(remote.Redact(msg, c.token)),
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return remote.Fatal(err)
		}
		return err
	}
	return remote.DecodeJSON(resp.Body, dst)
}

func isStatus(err error, status int) bool {
	var respErr *responseError
	return errors.As(err, &respErr) && respErr.status == status
}

func isFatalCreateError(status int, body []byte) bool {
	if status == http.StatusUnauthorized {
		return true
	}
	if status != http.StatusForbidden {
		return false
	}
	s := strings.ToLower(string(body))
	return strings.Contains(s, "maximum limit of repositories") ||
		strings.Contains(s, "repo creation") ||
		strings.Contains(s, "repository creation") ||
		strings.Contains(s, "permission denied") ||
		strings.Contains(s, "forbidden")
}

func truncate(b []byte) string {
	const limit = 512
	if len(b) <= limit {
		return string(b)
	}
	return string(b[:limit]) + "…(truncated)"
}
