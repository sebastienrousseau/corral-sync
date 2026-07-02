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
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sebastienrousseau/corral-sync/internal/remote"
)

// Client is the Gitea API client.
type Client struct {
	baseURL string
	token   string
	owner   string
	http    *http.Client
	log     *slog.Logger

	ownerMu sync.Mutex
}

// New builds a Client. baseURL is the Gitea instance root (e.g.
// https://gitea.example.com). Owner may be empty; the client resolves the
// authenticated user on first use.
func New(baseURL, token, owner string, logger *slog.Logger) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		owner:   owner,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
		log: logger.With(slog.String("provider", "gitea")),
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
	owner, err := c.resolveOwner(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve owner: %w", err)
	}

	payload := map[string]any{
		"name":           r.Name,
		"description":    r.Description,
		"private":        r.Visibility == remote.Private,
		"auto_init":      false,
		"default_branch": "main",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}

	// We always use /user/repos — the token's owning user. Mirroring
	// under an org is a follow-up: just look up whether owner matches an
	// org and switch endpoints.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/user/repos", bytes.NewReader(body))
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
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusCreated:
		var rj repoJSON
		if err := json.NewDecoder(resp.Body).Decode(&rj); err != nil {
			return "", fmt.Errorf("decode 201: %w", err)
		}
		c.log.Info("created repo", slog.String("full_name", rj.FullName))
		return rj.SSHURL, nil

	case http.StatusConflict:
		rj, err := c.getRepo(ctx, owner, r.Name)
		if err != nil {
			return "", fmt.Errorf("resolve existing repo %s/%s: %w", owner, r.Name, err)
		}
		c.log.Debug("repo exists, reusing", slog.String("full_name", rj.FullName))
		return rj.SSHURL, nil

	default:
		msg, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("gitea create returned %d: %s", resp.StatusCode, truncate(msg))
	}
}

// resolveOwner fetches the token owner via GET /user if none was
// configured. Cheap and cached under a small mutex.
func (c *Client) resolveOwner(ctx context.Context) (string, error) {
	c.ownerMu.Lock()
	defer c.ownerMu.Unlock()

	if c.owner != "" {
		return c.owner, nil
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := c.getJSON(ctx, "/api/v1/user", &user); err != nil {
		return "", err
	}
	if user.Login == "" {
		return "", fmt.Errorf("gitea /user returned empty login")
	}
	c.owner = user.Login
	return c.owner, nil
}

// getRepo fetches an existing repository by owner and name.
func (c *Client) getRepo(ctx context.Context, owner, name string) (*repoJSON, error) {
	path := "/api/v1/repos/" + owner + "/" + name
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GET repo returned %d: %s", resp.StatusCode, truncate(msg))
	}
	var rj repoJSON
	if err := json.NewDecoder(resp.Body).Decode(&rj); err != nil {
		return nil, err
	}
	return &rj, nil
}

// getJSON is a small helper for authenticated GET-then-decode requests.
func (c *Client) getJSON(ctx context.Context, path string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GET %s returned %d: %s", path, resp.StatusCode, truncate(msg))
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

func truncate(b []byte) string {
	const limit = 512
	if len(b) <= limit {
		return string(b)
	}
	return string(b[:limit]) + "…(truncated)"
}
