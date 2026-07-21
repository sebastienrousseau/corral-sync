// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: GPL-3.0-only

package gitea

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/sebastienrousseau/corral-sync/internal/remote"
)

func TestEnsureRepoReusesExistingRepoBeforeCreate(t *testing.T) {
	var posted bool
	client := New("https://gitea.test", "token", "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	client.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/user":
			return jsonResponse(t, http.StatusOK, map[string]string{"login": "alice"}), nil
		case "GET /api/v1/repos/alice/existing":
			return jsonResponse(t, http.StatusOK, repoJSON{
				Name:     "existing",
				FullName: "alice/existing",
				SSHURL:   "git@gitea.example.com:alice/existing.git",
			}), nil
		case "POST /api/v1/user/repos":
			posted = true
			t.Fatal("unexpected create request for an existing repository")
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		return nil, nil
	})}

	cloneURL, err := client.EnsureRepo(context.Background(), remote.Repo{Name: "existing"})
	if err != nil {
		t.Fatalf("EnsureRepo returned error: %v", err)
	}
	if posted {
		t.Fatal("EnsureRepo posted before reusing existing repository")
	}
	if cloneURL != "git@gitea.example.com:alice/existing.git" {
		t.Fatalf("clone URL = %q", cloneURL)
	}
}

func TestEnsureRepoCreatesUnderConfiguredOrgOwner(t *testing.T) {
	var sawOrgCreate bool
	client := New("https://gitea.test", "token", "team", slog.New(slog.NewTextHandler(io.Discard, nil)))
	client.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/user":
			return jsonResponse(t, http.StatusOK, map[string]string{"login": "alice"}), nil
		case "GET /api/v1/repos/team/new-repo":
			return textResponse(http.StatusNotFound, "404 page not found"), nil
		case "POST /api/v1/orgs/team/repos":
			sawOrgCreate = true
			var payload struct {
				Name    string `json:"name"`
				Private bool   `json:"private"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode payload: %v", err)
			}
			if payload.Name != "new-repo" || payload.Private {
				t.Fatalf("unexpected payload: %+v", payload)
			}
			return jsonResponse(t, http.StatusCreated, repoJSON{
				Name:     "new-repo",
				FullName: "team/new-repo",
				SSHURL:   "git@gitea.example.com:team/new-repo.git",
			}), nil
		case "POST /api/v1/user/repos":
			t.Fatal("unexpected personal create request for org owner")
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		return nil, nil
	})}

	cloneURL, err := client.EnsureRepo(context.Background(), remote.Repo{
		Name:       "new-repo",
		Visibility: remote.Public,
	})
	if err != nil {
		t.Fatalf("EnsureRepo returned error: %v", err)
	}
	if !sawOrgCreate {
		t.Fatal("EnsureRepo did not use the org create endpoint")
	}
	if cloneURL != "git@gitea.example.com:team/new-repo.git" {
		t.Fatalf("clone URL = %q", cloneURL)
	}
}

func TestEnsureRepoMarksCreateQuotaAsFatal(t *testing.T) {
	client := New("https://gitea.test", "token", "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	client.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/user":
			return jsonResponse(t, http.StatusOK, map[string]string{"login": "alice"}), nil
		case "GET /api/v1/repos/alice/new-repo":
			return textResponse(http.StatusNotFound, "404 page not found"), nil
		case "POST /api/v1/user/repos":
			return textResponse(http.StatusForbidden, `{"message":"user has reached maximum limit of repositories [limit: -1]"}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		return nil, nil
	})}

	_, err := client.EnsureRepo(context.Background(), remote.Repo{Name: "new-repo"})
	if err == nil {
		t.Fatal("EnsureRepo returned nil error")
	}
	if !remote.IsFatal(err) {
		t.Fatalf("expected fatal error, got %T: %v", err, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonResponse(t *testing.T, status int, v any) *http.Response {
	t.Helper()
	var b strings.Builder
	if err := json.NewEncoder(&b).Encode(v); err != nil {
		t.Fatalf("write response: %v", err)
	}
	resp := textResponse(status, b.String())
	resp.Header.Set("Content-Type", "application/json")
	return resp
}

func textResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
