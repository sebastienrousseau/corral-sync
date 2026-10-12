// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

// Package remote defines the abstractions shared between the GitLab and Gitea
// provider clients. Keeping them here — instead of duplicating on each side —
// makes the orchestrator provider-agnostic: it only ever talks to
// [Provider.EnsureRepo], never to a GitLab-specific or Gitea-specific type.
package remote

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Visibility describes how a repository should be exposed on the remote
// provider. The value is deduced from the local directory layout produced by
// corral, e.g. `~/Code/Public/go/foo` → [Public]; `~/Code/Private/rust/bar`
// → [Private]. If the layout gives no hint the orchestrator defaults to
// [Private] as the safer choice.
type Visibility string

const (
	// Public repos are world-readable on the destination provider.
	Public Visibility = "public"
	// Private repos are only visible to the authenticated user (and any
	// members they explicitly share access with).
	Private Visibility = "private"
)

// Repo is the neutral, provider-independent description of one repository
// that the orchestrator wants to mirror. Both providers accept the same
// three fields on create; the client is responsible for translating them
// into whatever JSON shape the provider's API expects.
type Repo struct {
	// Name is the last path segment of the local .git parent directory —
	// this becomes the repo name (and path/slug) on the remote.
	Name string
	// Description is optional. Left empty by default; corral does not
	// persist upstream descriptions today, so we don't invent one.
	Description string
	// Visibility is [Public] or [Private]. See [Visibility] doc.
	Visibility Visibility
	// LocalPath is the absolute path to the local repository (the
	// directory that contains `.git`). The orchestrator hands it to the
	// git-ops layer for `git remote add` + `git push`.
	LocalPath string
}

// Provider is the surface the orchestrator relies on. Every concrete client
// (GitLab, Gitea, or a future Codeberg / self-hosted whatever) implements it.
//
// EnsureRepo must be idempotent. If the destination repo already exists it
// must return the CloneURL and a nil error, not treat the pre-existing repo
// as a failure. Concrete providers translate whichever HTTP status their API
// uses to signal "already exists" (Gitea returns 409, GitLab returns 400
// with a "has already been taken" message).
type Provider interface {
	// Name is the short identifier used in git remote names and log
	// records — "gitlab" or "gitea". Must be stable; changing it would
	// leave orphan remotes on every local clone.
	Name() string

	// Host is the lower-cased hostname the provider's API lives on, without
	// a port — "gitlab.com", "gitea.example.com". The orchestrator compares
	// it against each repository's origin so a clone that corral pulled
	// *from* this forge is never pushed back onto it with --prune.
	Host() string

	// EnsureRepo creates the repository on the remote if it does not
	// already exist, and returns the SSH or HTTPS clone URL that
	// git push should target. Existing-repo is not an error.
	EnsureRepo(ctx context.Context, r Repo) (cloneURL string, err error)
}

// ValidateRepo rejects values that could create ambiguous provider paths or
// accidentally weaken a repository's visibility.
func ValidateRepo(r Repo) error {
	if r.Name == "" || r.Name == "." || r.Name == ".." || len(r.Name) > 100 ||
		strings.ContainsAny(r.Name, "/\\\r\n\x00") {
		return fmt.Errorf("invalid repository name %q", r.Name)
	}
	if r.Visibility != Public && r.Visibility != Private {
		return fmt.Errorf("invalid repository visibility %q", r.Visibility)
	}
	return nil
}

// ValidateCloneURL accepts authenticated SSH transport and credential-free
// HTTPS URLs. Local paths, insecure HTTP, embedded credentials, and option-like
// values are rejected before they can reach git.
func ValidateCloneURL(raw string) error {
	if raw == "" || strings.HasPrefix(raw, "-") || strings.ContainsAny(raw, "\r\n\x00") {
		return errors.New("invalid clone URL")
	}
	if strings.HasPrefix(raw, "git@") {
		hostPath := strings.TrimPrefix(raw, "git@")
		parts := strings.SplitN(hostPath, ":", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.HasPrefix(parts[1], "/") {
			return errors.New("invalid SSH clone URL")
		}
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errors.New("invalid clone URL")
	}
	switch u.Scheme {
	case "https":
		if u.User != nil {
			return errors.New("HTTPS clone URL must not contain credentials")
		}
	case "ssh":
		if u.User == nil || u.User.Username() == "" {
			return errors.New("SSH clone URL must include a user")
		}
	default:
		return fmt.Errorf("unsupported clone URL scheme %q", u.Scheme)
	}
	return nil
}

// CanonicalHost extracts the lower-cased hostname from a remote URL in any
// of the forms git accepts — https://, ssh://, and the scp-like
// `git@host:owner/repo.git` — without its port or user. It returns "" for
// anything it cannot read as a remote, including a local path.
//
// It exists for one comparison: whether a repository's origin lives on the
// forge a provider would push to. corral clones from GitLab, Gitea, Forgejo,
// Codeberg and Bitbucket as well as GitHub, so a tree it manages can contain
// a clone whose origin *is* the destination. Mirroring that clone back would
// mean `git push --prune` against its own upstream, and with a single-branch
// clone --prune deletes every branch the local copy does not have.
func CanonicalHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		// A URL with a scheme. file:// has no host, and that is the answer:
		// a local path is not on any forge.
		u, err := url.Parse(raw)
		if err != nil {
			return ""
		}
		return strings.ToLower(u.Hostname())
	}
	// scp-like: [user@]host:path. A slash before the colon is a path, as
	// git reads it, and a single letter before it is a Windows drive.
	colon := strings.Index(raw, ":")
	if colon <= 0 || strings.Contains(raw[:colon], "/") {
		return ""
	}
	host := raw[:colon]
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	if len(host) <= 1 {
		return ""
	}
	return strings.ToLower(host)
}
