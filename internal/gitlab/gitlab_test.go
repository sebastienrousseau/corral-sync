// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: GPL-3.0-only

package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/sebastienrousseau/corral-sync/internal/remote"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(status int, body io.ReadCloser) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: body}
}

func textResponse(status int, body string) *http.Response {
	return response(status, io.NopCloser(strings.NewReader(body)))
}

func jsonResponse(t *testing.T, status int, value any) *http.Response {
	t.Helper()
	var b strings.Builder
	if err := json.NewEncoder(&b).Encode(value); err != nil {
		t.Fatal(err)
	}
	return textResponse(status, b.String())
}

func newTestClient(handler roundTripFunc) *Client {
	c := New("https://gitlab.test/", "token", "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.http = &http.Client{Transport: handler}
	return c
}

func TestEnsureRepoCreatesAndCachesNamespace(t *testing.T) {
	namespaceCalls := 0
	c := newTestClient(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("PRIVATE-TOKEN") != "token" {
			t.Fatal("missing token header")
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v4/namespaces/team":
			namespaceCalls++
			return jsonResponse(t, http.StatusOK, map[string]any{"id": 7, "full_path": "team"}), nil
		case "POST /api/v4/projects":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["namespace_id"] != float64(7) || payload["visibility"] != "public" {
				t.Fatalf("payload = %+v", payload)
			}
			return jsonResponse(t, http.StatusCreated, projectJSON{PathWithNamespace: "team/repo", SSHURLToRepo: "git@gitlab.test:team/repo.git", Visibility: "public"}), nil
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return nil, nil
	})
	c.namespace = "team"
	for range 2 {
		url, err := c.EnsureRepo(context.Background(), remote.Repo{Name: "repo", Visibility: remote.Public})
		if err != nil || url != "git@gitlab.test:team/repo.git" {
			t.Fatalf("EnsureRepo = %q, %v", url, err)
		}
	}
	if c.Name() != "gitlab" || namespaceCalls != 1 {
		t.Fatalf("name=%q namespace calls=%d", c.Name(), namespaceCalls)
	}
}

func TestEnsureRepoReusesConflict(t *testing.T) {
	c := newTestClient(func(r *http.Request) (*http.Response, error) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v4/user":
			return jsonResponse(t, http.StatusOK, map[string]string{"username": "alice"}), nil
		case "POST /api/v4/projects":
			return textResponse(http.StatusBadRequest, `{"message":"has already been taken"}`), nil
		case "GET /api/v4/projects/alice/repo":
			return jsonResponse(t, http.StatusOK, projectJSON{PathWithNamespace: "alice/repo", SSHURLToRepo: "git@gitlab.test:alice/repo.git", Visibility: "private"}), nil
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return nil, nil
	})
	url, err := c.EnsureRepo(context.Background(), remote.Repo{Name: "repo"})
	if err != nil || url == "" {
		t.Fatalf("EnsureRepo = %q, %v", url, err)
	}
}

func TestEnsureRepoFailureBranches(t *testing.T) {
	if _, err := newTestClient(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("offline")
	}).EnsureRepo(context.Background(), remote.Repo{Name: "repo", Visibility: remote.Private}); err == nil {
		t.Fatal("expected namespace transport error")
	}
	if _, err := newTestClient(nil).EnsureRepo(context.Background(), remote.Repo{Name: "bad/name", Visibility: remote.Private}); err == nil {
		t.Fatal("expected invalid repo")
	}

	oldMarshal := marshalJSON
	oldRequest := newRequest
	t.Cleanup(func() { marshalJSON, newRequest = oldMarshal, oldRequest })
	marshalJSON = func(any) ([]byte, error) { return nil, errors.New("marshal") }
	c := newTestClient(nil)
	c.nsCurrent = "alice"
	if _, err := c.EnsureRepo(context.Background(), remote.Repo{Name: "repo", Visibility: remote.Private}); err == nil {
		t.Fatal("expected marshal error")
	}
	marshalJSON = oldMarshal

	c = newTestClient(nil)
	c.nsCurrent = "alice"
	newRequest = func(context.Context, string, string, io.Reader) (*http.Request, error) {
		return nil, errors.New("request")
	}
	if _, err := c.EnsureRepo(context.Background(), remote.Repo{Name: "repo", Visibility: remote.Private}); err == nil {
		t.Fatal("expected request error")
	}
	newRequest = oldRequest

	for name, handler := range map[string]roundTripFunc{
		"post transport": func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") },
		"created decode": func(*http.Request) (*http.Response, error) { return textResponse(http.StatusCreated, "{"), nil },
		"bad request": func(*http.Request) (*http.Response, error) {
			return textResponse(http.StatusBadRequest, "invalid"), nil
		},
		"default": func(*http.Request) (*http.Response, error) {
			return textResponse(http.StatusInternalServerError, "failed"), nil
		},
		"fatal": func(*http.Request) (*http.Response, error) {
			return textResponse(http.StatusUnauthorized, "bad token"), nil
		},
		"read": func(*http.Request) (*http.Response, error) {
			return response(http.StatusInternalServerError, errorReadCloser{}), nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			client := newTestClient(handler)
			client.nsCurrent = "alice"
			_, err := client.EnsureRepo(context.Background(), remote.Repo{Name: "repo", Visibility: remote.Private})
			if err == nil {
				t.Fatal("expected error")
			}
			if name == "fatal" && !remote.IsFatal(err) {
				t.Fatalf("expected fatal error: %v", err)
			}
		})
	}
}

func TestEnsureRepoCreatedAndConflictValidationFailures(t *testing.T) {
	for name, statusBody := range map[string]struct {
		status int
		body   string
	}{
		"created visibility": {http.StatusCreated, `{"ssh_url_to_repo":"git@gitlab.test:a/repo.git","visibility":"public"}`},
		"created URL":        {http.StatusCreated, `{"ssh_url_to_repo":"file:///tmp/repo","visibility":"private"}`},
		"conflict read":      {http.StatusConflict, "READ_ERROR"},
	} {
		t.Run(name, func(t *testing.T) {
			c := newTestClient(func(*http.Request) (*http.Response, error) {
				if statusBody.body == "READ_ERROR" {
					return response(statusBody.status, errorReadCloser{}), nil
				}
				return textResponse(statusBody.status, statusBody.body), nil
			})
			c.nsCurrent = "a"
			if _, err := c.EnsureRepo(context.Background(), remote.Repo{Name: "repo", Visibility: remote.Private}); err == nil {
				t.Fatal("expected error")
			}
		})
	}

	for name, projectResponse := range map[string]*http.Response{
		"lookup":     textResponse(http.StatusNotFound, "missing"),
		"validation": jsonResponse(t, http.StatusOK, projectJSON{SSHURLToRepo: "git@gitlab.test:a/repo.git", Visibility: "public"}),
	} {
		t.Run("conflict "+name, func(t *testing.T) {
			calls := 0
			c := newTestClient(func(*http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return textResponse(http.StatusConflict, "already exists"), nil
				}
				return projectResponse, nil
			})
			c.nsCurrent = "a"
			if _, err := c.EnsureRepo(context.Background(), remote.Repo{Name: "repo", Visibility: remote.Private}); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestProjectValidationAndConflictDetection(t *testing.T) {
	good := projectJSON{SSHURLToRepo: "git@gitlab.test:a/repo.git", Visibility: "private"}
	if err := validateProject(good, remote.Private); err != nil {
		t.Fatal(err)
	}
	badVisibility := good
	badVisibility.Visibility = "public"
	if err := validateProject(badVisibility, remote.Private); err == nil {
		t.Fatal("expected visibility error")
	}
	badURL := good
	badURL.SSHURLToRepo = "file:///tmp/repo"
	if err := validateProject(badURL, remote.Private); err == nil {
		t.Fatal("expected URL error")
	}
	if !alreadyExists([]byte("HAS ALREADY BEEN TAKEN")) || !alreadyExists([]byte("already exists")) || alreadyExists([]byte("other")) {
		t.Fatal("conflict detection failed")
	}
}

func TestNamespaceAndHTTPHelperBranches(t *testing.T) {
	cases := []struct {
		name      string
		namespace string
		status    int
		body      string
		fatal     bool
	}{
		{"empty-user", "", http.StatusOK, `{"username":""}`, false},
		{"missing-id", "team", http.StatusOK, `{"id":0,"full_path":"team"}`, false},
		{"empty-path", "team", http.StatusOK, `{"id":1,"full_path":""}`, false},
		{"unauthorized", "", http.StatusUnauthorized, "denied", true},
		{"not-found", "team", http.StatusNotFound, "missing", false},
		{"decode", "", http.StatusOK, "{", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(func(*http.Request) (*http.Response, error) { return textResponse(tc.status, tc.body), nil })
			c.namespace = tc.namespace
			_, err := c.resolveNamespace(context.Background())
			if err == nil || (tc.fatal && !remote.IsFatal(err)) {
				t.Fatalf("resolve error = %v", err)
			}
		})
	}

	c := newTestClient(func(*http.Request) (*http.Response, error) {
		return response(http.StatusBadRequest, errorReadCloser{}), nil
	})
	if err := c.getJSON(context.Background(), "/bad", &struct{}{}); err == nil {
		t.Fatal("expected response read error")
	}
	c.baseURL = ":"
	if err := c.getJSON(context.Background(), "/bad", &struct{}{}); err == nil {
		t.Fatal("expected request construction error")
	}
}

func TestGetProjectBranchesAndTruncate(t *testing.T) {
	for name, handler := range map[string]roundTripFunc{
		"transport": func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") },
		"status":    func(*http.Request) (*http.Response, error) { return textResponse(http.StatusNotFound, "missing"), nil },
		"read": func(*http.Request) (*http.Response, error) {
			return response(http.StatusNotFound, errorReadCloser{}), nil
		},
		"decode": func(*http.Request) (*http.Response, error) { return textResponse(http.StatusOK, "{"), nil },
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := newTestClient(handler).getProject(context.Background(), "a", "repo"); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	c := newTestClient(nil)
	c.baseURL = ":"
	if _, err := c.getProject(context.Background(), "a", "repo"); err == nil {
		t.Fatal("expected request error")
	}
	if truncate([]byte("short")) != "short" || !strings.HasSuffix(truncate([]byte(strings.Repeat("x", 600))), "…(truncated)") {
		t.Fatal("truncate failed")
	}
}

type errorReadCloser struct{}

func (errorReadCloser) Read([]byte) (int, error) { return 0, errors.New("read") }
func (errorReadCloser) Close() error             { return nil }
