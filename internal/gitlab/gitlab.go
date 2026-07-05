// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: GPL-3.0-only

// Package gitlab implements the [remote.Provider] surface against the
// GitLab REST API v4. It authenticates with a Personal Access Token
// (`PRIVATE-TOKEN` header) and creates projects idempotently: if the
// target namespace already owns a project with the requested path we
// resolve the existing project and return its clone URL instead of
// erroring out.
package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sebastienrousseau/corral-sync/internal/remote"
)

// Client is the GitLab API client. It is safe for concurrent use — the
// underlying http.Client is, and the namespace-cache mutex protects the
// only mutable state.
type Client struct {
	baseURL   string
	token     string
	namespace string
	http      *http.Client
	log       *slog.Logger

	nsMu      sync.Mutex
	nsID      int // 0 = not resolved yet
	nsCurrent string
}

// New builds a Client. baseURL should not end with a slash (we add path
// segments assuming it is bare). namespace may be empty; in that case the
// client resolves the token's owning user on first use.
func New(baseURL, token, namespace string, logger *slog.Logger) *Client {
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		token:     token,
		namespace: namespace,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
		log: logger.With(slog.String("provider", "gitlab")),
	}
}

// Name satisfies [remote.Provider]. Stable identifier used as the git
// remote name — do not change without a migration plan.
func (c *Client) Name() string { return "gitlab" }

// projectJSON is the subset of GitLab's Project object we care about.
// GitLab returns many more fields; leaving them out means our decoder is
// forward-compatible with new fields.
type projectJSON struct {
	ID                int    `json:"id"`
	Path              string `json:"path"`
	PathWithNamespace string `json:"path_with_namespace"`
	SSHURLToRepo      string `json:"ssh_url_to_repo"`
	HTTPURLToRepo     string `json:"http_url_to_repo"`
}

// EnsureRepo satisfies [remote.Provider].
//
// The create endpoint is POST /api/v4/projects. GitLab expects:
//
//	{
//	  "name":                  "<repo>",     // human name (== path here)
//	  "path":                  "<repo>",     // URL slug
//	  "namespace_id":          <int>,        // optional; empty = user's namespace
//	  "description":           "...",
//	  "visibility":            "public"|"private"|"internal",
//	  "initialize_with_readme": false
//	}
//
// On conflict GitLab replies 400 with a body like
// {"message":{"name":["has already been taken"], "path":["has already been taken"]}}.
// Some deployments return 409. We treat both as "already exists" and
// resolve the existing project via GET /projects/<url-encoded-path>.
func (c *Client) EnsureRepo(ctx context.Context, r remote.Repo) (string, error) {
	ns, err := c.resolveNamespace(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve namespace: %w", err)
	}

	payload := map[string]any{
		"name":                   r.Name,
		"path":                   r.Name,
		"description":            r.Description,
		"visibility":             string(r.Visibility),
		"initialize_with_readme": false,
	}
	// GitLab defaults `namespace_id` to the authenticated user's personal
	// namespace when the field is absent. We include it only when the
	// caller pinned a specific namespace (in which case c.nsID was
	// resolved via /api/v4/namespaces/<path> in resolveNamespace, not
	// from /api/v4/user — those two IDs are distinct).
	if c.nsID != 0 {
		payload["namespace_id"] = c.nsID
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v4/projects", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("POST projects: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusCreated: // 201
		var p projectJSON
		if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
			return "", fmt.Errorf("decode 201: %w", err)
		}
		c.log.Info("created project", slog.String("path", p.PathWithNamespace))
		return p.SSHURLToRepo, nil

	case http.StatusBadRequest, http.StatusConflict:
		msg, _ := io.ReadAll(resp.Body)
		if !alreadyExists(msg) {
			return "", fmt.Errorf("gitlab create returned %d: %s", resp.StatusCode, truncate(msg))
		}
		// Already exists — resolve the existing project and return its URL.
		p, err := c.getProject(ctx, ns, r.Name)
		if err != nil {
			return "", fmt.Errorf("resolve existing project %s/%s: %w", ns, r.Name, err)
		}
		c.log.Debug("project exists, reusing", slog.String("path", p.PathWithNamespace))
		return p.SSHURLToRepo, nil

	default:
		msg, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("gitlab create returned %d: %s", resp.StatusCode, truncate(msg))
	}
}

// alreadyExists returns true if the GitLab error body describes a
// name/path collision, in either the 400 shape GitLab.com uses or the 409
// shape some self-hosted deployments use.
func alreadyExists(body []byte) bool {
	// Fast text match first — cheaper than JSON decoding an unknown shape
	// and correct for both response formats.
	s := strings.ToLower(string(body))
	return strings.Contains(s, "has already been taken") ||
		strings.Contains(s, "already exists")
}

// getProject fetches an existing project by namespace/path. The URL path
// must be percent-encoded once: GitLab uses `%2F` for the slash between
// namespace and repo.
func (c *Client) getProject(ctx context.Context, namespace, name string) (*projectJSON, error) {
	full := namespace + "/" + name
	u := c.baseURL + "/api/v4/projects/" + url.PathEscape(full)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GET project returned %d: %s", resp.StatusCode, truncate(msg))
	}
	var p projectJSON
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

// resolveNamespace populates c.nsCurrent (and, when a namespace was
// explicitly configured, c.nsID). If no namespace was configured we
// resolve the token owner's username via /api/v4/user and leave nsID at
// zero — EnsureRepo then omits namespace_id from the POST payload, and
// GitLab defaults to the authenticated user's personal namespace.
//
// This avoids a common bug: the user's `id` from /user is NOT the same
// as their personal namespace's `id` on modern GitLab.com. Sending the
// user id as namespace_id makes GitLab reply "namespace: is not valid".
func (c *Client) resolveNamespace(ctx context.Context) (string, error) {
	c.nsMu.Lock()
	defer c.nsMu.Unlock()

	if c.nsCurrent != "" {
		return c.nsCurrent, nil
	}

	if c.namespace == "" {
		// Resolve just the username — GitLab handles namespace_id
		// defaulting for us.
		var user struct {
			Username string `json:"username"`
		}
		if err := c.getJSON(ctx, "/api/v4/user", &user); err != nil {
			return "", err
		}
		c.nsCurrent = user.Username
		return c.nsCurrent, nil
	}

	// Look up a specific namespace by path.
	var ns struct {
		ID       int    `json:"id"`
		FullPath string `json:"full_path"`
	}
	path := "/api/v4/namespaces/" + url.PathEscape(c.namespace)
	if err := c.getJSON(ctx, path, &ns); err != nil {
		return "", err
	}
	if ns.ID == 0 {
		return "", errors.New("namespace not found")
	}
	c.nsID = ns.ID
	c.nsCurrent = ns.FullPath
	return c.nsCurrent, nil
}

// getJSON is a small helper for authenticated GET-then-decode requests.
func (c *Client) getJSON(ctx context.Context, path string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)
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

// truncate keeps log records readable when the API returns a giant HTML
// error page (rare but happens behind buggy reverse proxies).
func truncate(b []byte) string {
	const limit = 512
	if len(b) <= limit {
		return string(b)
	}
	return string(b[:limit]) + "…(truncated)"
}
