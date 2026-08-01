// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: GPL-3.0-only

package gitea

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/sebastienrousseau/corral-sync/internal/remote"
)

func testClient(handler roundTripFunc) *Client {
	c := New("https://gitea.test/", "token", "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.http = &http.Client{Transport: handler}
	return c
}

func TestClientNameAndResponseError(t *testing.T) {
	c := testClient(nil)
	if c.Name() != "gitea" {
		t.Fatalf("name = %q", c.Name())
	}
	err := (&responseError{method: "GET", path: "/x", status: 500, body: "failed"}).Error()
	if !strings.Contains(err, "GET /x returned 500") {
		t.Fatalf("response error = %q", err)
	}
}

func TestEnsureRepoValidationAndExistingFailures(t *testing.T) {
	if _, err := testClient(nil).EnsureRepo(context.Background(), remote.Repo{Name: "bad/name", Visibility: remote.Private}); err == nil {
		t.Fatal("expected repo validation error")
	}
	if _, err := testClient(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("offline")
	}).EnsureRepo(context.Background(), remote.Repo{Name: "repo", Visibility: remote.Private}); err == nil {
		t.Fatal("expected destination resolution error")
	}
	for name, existing := range map[string]repoJSON{
		"visibility": {SSHURL: "git@gitea.test:a/repo.git", Private: false},
		"clone-url":  {SSHURL: "file:///tmp/repo", Private: true},
	} {
		t.Run(name, func(t *testing.T) {
			c := testClient(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/api/v1/user":
					return jsonResponse(t, http.StatusOK, map[string]string{"login": "a"}), nil
				default:
					return jsonResponse(t, http.StatusOK, existing), nil
				}
			})
			if _, err := c.EnsureRepo(context.Background(), remote.Repo{Name: "repo", Visibility: remote.Private}); err == nil {
				t.Fatal("expected existing repository validation error")
			}
		})
	}
	c := testClient(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v1/user" {
			return jsonResponse(t, http.StatusOK, map[string]string{"login": "a"}), nil
		}
		return textResponse(http.StatusInternalServerError, "failed"), nil
	})
	if _, err := c.EnsureRepo(context.Background(), remote.Repo{Name: "repo", Visibility: remote.Private}); err == nil {
		t.Fatal("expected existing lookup error")
	}
}

func TestEnsureRepoCreateFailureBranches(t *testing.T) {
	oldMarshal := marshalJSON
	t.Cleanup(func() { marshalJSON = oldMarshal })
	c := testClient(nil)
	c.dest = destination{owner: "a", createPath: "/api/v1/user/repos"}
	c.http = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return textResponse(http.StatusNotFound, "missing"), nil
	})}
	marshalJSON = func(any) ([]byte, error) { return nil, errors.New("marshal") }
	if _, err := c.EnsureRepo(context.Background(), remote.Repo{Name: "repo", Visibility: remote.Private}); err == nil {
		t.Fatal("expected marshal error")
	}
	marshalJSON = oldMarshal

	oldRequest := newRequest
	t.Cleanup(func() { newRequest = oldRequest })
	newRequest = func(ctx context.Context, method, rawURL string, body io.Reader) (*http.Request, error) {
		if method == http.MethodPost {
			return nil, errors.New("request")
		}
		return oldRequest(ctx, method, rawURL, body)
	}
	if _, err := c.EnsureRepo(context.Background(), remote.Repo{Name: "repo", Visibility: remote.Private}); err == nil {
		t.Fatal("expected request error")
	}
	newRequest = oldRequest

	cases := []struct {
		name    string
		handler roundTripFunc
		fatal   bool
	}{
		{"transport", func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet {
				return textResponse(http.StatusNotFound, "missing"), nil
			}
			return nil, errors.New("offline")
		}, false},
		{"decode", statusAfterNotFound(t, http.StatusCreated, "{"), false},
		{"created visibility", statusAfterNotFound(t, http.StatusCreated, `{"ssh_url":"git@gitea.test:a/repo.git","private":false}`), false},
		{"created URL", statusAfterNotFound(t, http.StatusCreated, `{"ssh_url":"file:///tmp/repo","private":true}`), false},
		{"conflict lookup", func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet {
				return textResponse(http.StatusNotFound, "missing"), nil
			}
			return textResponse(http.StatusConflict, "exists"), nil
		}, false},
		{"default", statusAfterNotFound(t, http.StatusInternalServerError, "failed"), false},
		{"fatal unauthorized", statusAfterNotFound(t, http.StatusUnauthorized, "bad token"), true},
		{"fatal forbidden", statusAfterNotFound(t, http.StatusForbidden, "repository creation forbidden"), true},
		{"read", func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet {
				return textResponse(http.StatusNotFound, "missing"), nil
			}
			return &http.Response{StatusCode: 500, Header: make(http.Header), Body: errorReadCloser{}}, nil
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := testClient(tc.handler)
			client.dest = destination{owner: "a", createPath: "/api/v1/user/repos"}
			_, err := client.EnsureRepo(context.Background(), remote.Repo{Name: "repo", Visibility: remote.Private})
			if err == nil || (tc.fatal && !remote.IsFatal(err)) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func statusAfterNotFound(t *testing.T, status int, body string) roundTripFunc {
	t.Helper()
	return func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			return textResponse(http.StatusNotFound, "missing"), nil
		}
		return textResponse(status, body), nil
	}
}

func TestEnsureRepoConflictSuccess(t *testing.T) {
	gets := 0
	c := testClient(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			gets++
			if gets == 1 {
				return textResponse(http.StatusNotFound, "missing"), nil
			}
			return jsonResponse(t, http.StatusOK, repoJSON{SSHURL: "git@gitea.test:a/repo.git", Private: true}), nil
		}
		return textResponse(http.StatusConflict, "exists"), nil
	})
	c.dest = destination{owner: "a", createPath: "/api/v1/user/repos"}
	url, err := c.EnsureRepo(context.Background(), remote.Repo{Name: "repo", Visibility: remote.Private})
	if err != nil || url == "" {
		t.Fatalf("conflict result = %q, %v", url, err)
	}
}

func TestEnsureRepoConflictValidationFailure(t *testing.T) {
	gets := 0
	c := testClient(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			gets++
			if gets == 1 {
				return textResponse(http.StatusNotFound, "missing"), nil
			}
			return jsonResponse(t, http.StatusOK, repoJSON{SSHURL: "git@gitea.test:a/repo.git", Private: false}), nil
		}
		return textResponse(http.StatusConflict, "exists"), nil
	})
	c.dest = destination{owner: "a", createPath: "/api/v1/user/repos"}
	if _, err := c.EnsureRepo(context.Background(), remote.Repo{Name: "repo", Visibility: remote.Private}); err == nil {
		t.Fatal("expected conflict visibility error")
	}
}

func TestDestinationBranches(t *testing.T) {
	c := testClient(func(*http.Request) (*http.Response, error) {
		return jsonResponse(t, http.StatusOK, map[string]string{"login": "alice"}), nil
	})
	c.owner = "alice"
	dest, err := c.resolveDestination(context.Background())
	if err != nil || dest.createPath != "/api/v1/user/repos" {
		t.Fatalf("personal dest = %+v, %v", dest, err)
	}
	if cached, err := c.resolveDestination(context.Background()); err != nil || cached != dest {
		t.Fatalf("cached dest = %+v, %v", cached, err)
	}
	c = testClient(func(*http.Request) (*http.Response, error) {
		return jsonResponse(t, http.StatusOK, map[string]string{"login": ""}), nil
	})
	if _, err := c.resolveDestination(context.Background()); err == nil {
		t.Fatal("expected empty login error")
	}
	c = testClient(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })
	if _, err := c.resolveDestination(context.Background()); err == nil {
		t.Fatal("expected destination transport error")
	}
}

func TestGetRepoAndJSONBranches(t *testing.T) {
	for name, handler := range map[string]roundTripFunc{
		"transport": func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") },
		"read": func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 500, Header: make(http.Header), Body: errorReadCloser{}}, nil
		},
		"decode": func(*http.Request) (*http.Response, error) { return textResponse(http.StatusOK, "{"), nil },
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := testClient(handler).getRepo(context.Background(), "a", "repo"); err == nil {
				t.Fatal("expected getRepo error")
			}
		})
	}
	c := testClient(nil)
	c.baseURL = ":"
	if _, err := c.getRepo(context.Background(), "a", "repo"); err == nil {
		t.Fatal("expected getRepo request error")
	}
	if err := c.getJSON(context.Background(), "/x", &struct{}{}); err == nil {
		t.Fatal("expected getJSON request error")
	}

	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		client := testClient(func(*http.Request) (*http.Response, error) { return textResponse(status, "failed"), nil })
		err := client.getJSON(context.Background(), "/x", &struct{}{})
		if err == nil || ((status == 401 || status == 403) && !remote.IsFatal(err)) {
			t.Fatalf("status %d error = %v", status, err)
		}
	}
	client := testClient(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 500, Header: make(http.Header), Body: errorReadCloser{}}, nil
	})
	if err := client.getJSON(context.Background(), "/x", &struct{}{}); err == nil {
		t.Fatal("expected getJSON read error")
	}
	client = testClient(func(*http.Request) (*http.Response, error) { return textResponse(http.StatusOK, "{"), nil })
	if err := client.getJSON(context.Background(), "/x", &struct{}{}); err == nil {
		t.Fatal("expected getJSON decode error")
	}
}

func TestValidationFatalAndTruncateBranches(t *testing.T) {
	if err := validateRepo(&repoJSON{SSHURL: "git@gitea.test:a/repo.git", Private: true}, remote.Private); err != nil {
		t.Fatal(err)
	}
	if !isFatalCreateError(http.StatusUnauthorized, nil) || isFatalCreateError(http.StatusForbidden, []byte("other")) || isFatalCreateError(http.StatusInternalServerError, []byte("forbidden")) {
		t.Fatal("fatal create classification failed")
	}
	for _, body := range []string{"repo creation disabled", "permission denied", "forbidden"} {
		if !isFatalCreateError(http.StatusForbidden, []byte(body)) {
			t.Fatalf("expected fatal body %q", body)
		}
	}
	if truncate([]byte("short")) != "short" || !strings.HasSuffix(truncate([]byte(strings.Repeat("x", 600))), "…(truncated)") {
		t.Fatal("truncate failed")
	}
}

type errorReadCloser struct{}

func (errorReadCloser) Read([]byte) (int, error) { return 0, errors.New("read") }
func (errorReadCloser) Close() error             { return nil }
