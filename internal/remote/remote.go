// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: GPL-3.0-only

// Package remote defines the abstractions shared between the GitLab and Gitea
// provider clients. Keeping them here — instead of duplicating on each side —
// makes the orchestrator provider-agnostic: it only ever talks to
// [Provider.EnsureRepo], never to a GitLab-specific or Gitea-specific type.
package remote

import "context"

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

	// EnsureRepo creates the repository on the remote if it does not
	// already exist, and returns the SSH or HTTPS clone URL that
	// git push should target. Existing-repo is not an error.
	EnsureRepo(ctx context.Context, r Repo) (cloneURL string, err error)
}
