// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

// Package gitops wraps the local git binary. We shell out via os/exec —
// no libgit2, no go-git — so behaviour exactly matches what a user would
// see on the CLI, including ssh key pickup from ssh-agent.
//
// Every command runs with a non-interactive environment (GIT_TERMINAL_
// PROMPT=0, GIT_ASKPASS=/bin/true) so a cron-driven invocation never blocks
// waiting for a password on missing credentials — it fails loudly
// instead.
package gitops

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"unicode"

	"github.com/sebastienrousseau/corral-sync/internal/remote"
)

// nonInteractiveEnv is the environment overlay that guarantees a git
// invocation cannot stall on a credential prompt. Cron users never see
// stdin, so a prompt would hang forever otherwise.
//
// The same four variables corral sets, so the two binaries fail the same
// way on the same missing credential. SSH_ASKPASS matters on a desktop:
// without it an SSH key with a passphrase and no agent pops a GUI prompt
// that a cron job can neither see nor dismiss.
var nonInteractiveEnv = []string{
	"GIT_TERMINAL_PROMPT=0",
	"GIT_ASKPASS=/bin/true",
	"SSH_ASKPASS=/bin/true",
	"GCM_INTERACTIVE=Never",
}

// mirrorRefspecs is the whole of what a mirror pushes, in one round trip.
//
// Branches are pushed without force: a non-fast-forward is refused and
// surfaced, which is the safety net that stops a stale local clobbering the
// remote. Tags carry the `+` that forces them, because git refuses to move
// an existing tag by default and a tag re-pointed at origin — the canonical
// source — would otherwise fail forever with "already exists". Tags are
// lightweight, movable pointers and the local namespace is authoritative.
//
// `--prune` applies per refspec, so a remote branch or tag with no local
// counterpart is deleted. That is what "mirror" means and what parity
// promises.
var mirrorRefspecs = []string{
	"refs/heads/*:refs/heads/*",
	"+refs/tags/*:refs/tags/*",
}

// IsEmpty reports whether the local repo at repoDir has no commits
// (unborn HEAD). `git push` on such a repo fails with "No refs in common
// and none specified; doing nothing" — a real upstream state, not a bug on
// our side. Callers should SKIP the push instead of surfacing the git error.
//
// Matches corral's internal/git.IsEmpty implementation so the two
// binaries agree on which repositories are "not yet worth pushing".
func IsEmpty(ctx context.Context, repoDir string) (bool, error) {
	// #nosec G204 -- fixed binary; repoDir is a local path.
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "rev-parse", "--verify", "-q", "HEAD^{commit}")
	cmd.Env = append(cmd.Environ(), nonInteractiveEnv...)
	err := cmd.Run()
	if err == nil {
		return false, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return true, nil
	}
	return false, fmt.Errorf("inspect repository: %w", err)
}

// OriginURL returns the URL of the repository's `origin` remote, or "" when
// it has none. Any other failure — a directory that is not a repository,
// an unreadable config — is returned as an error, because the caller is
// about to decide whether a push is safe and must not guess.
func OriginURL(ctx context.Context, repoDir string) (string, error) {
	out, err := runOut(ctx, repoDir, "git", "config", "--get", "remote.origin.url")
	if err == nil {
		return strings.TrimSpace(out), nil
	}
	// `git config --get` exits 1 when the key is absent. Anything else is a
	// real failure.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return "", nil
	}
	return "", err
}

// EnsureRemote adds a remote at name→url on the repo at repoDir. If a
// remote with that name already exists and its URL matches, we no-op.
// If the URL differs we update it via `git remote set-url`, which keeps
// the local state converging with the desired state on every run.
func EnsureRemote(ctx context.Context, repoDir, name, url string) error {
	if err := validateRemoteName(name); err != nil {
		return err
	}
	if err := remote.ValidateCloneURL(url); err != nil {
		return err
	}
	current, err := runOut(ctx, repoDir, "git", "config", "--get", "remote."+name+".url")
	if err == nil {
		if strings.TrimSpace(current) == url {
			return nil // already correct
		}
		// The remote exists but points somewhere else. Update in place.
		return run(ctx, repoDir, "git", "remote", "set-url", "--", name, url)
	}
	// If the remote doesn't exist, `config --get` exits 1. Any other
	// error propagates.
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		return err
	}
	return run(ctx, repoDir, "git", "remote", "add", "--", name, url)
}

// PushMirror brings the remote's branches and tags to parity with the
// local repository in a single `git push`:
//
//	git push --prune --no-verify <remote> refs/heads/*:refs/heads/* +refs/tags/*:refs/tags/*
//
// It replaces the earlier pair of `--prune --all` and `--prune --tags
// --force` invocations, which cost two connections, two authentications
// and two ref advertisements per repository per provider. The semantics
// are unchanged and are pinned by the tests: branches are never forced,
// tags are, and both namespaces are pruned.
//
// --no-verify skips any local pre-push hook (e.g. a repo whose hook runs
// a build/test that can't succeed in this environment) so it never blocks
// the mirror.
func PushMirror(ctx context.Context, repoDir, remoteName string) error {
	if err := validateRemoteName(remoteName); err != nil {
		return err
	}
	args := append([]string{"push", "--prune", "--no-verify", remoteName}, mirrorRefspecs...)
	return run(ctx, repoDir, "git", args...)
}

func validateRemoteName(name string) error {
	if name == "" || strings.HasPrefix(name, "-") || strings.ContainsAny(name, "./\\\r\n\x00") {
		return fmt.Errorf("invalid remote name %q", name)
	}
	for _, r := range name {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("invalid remote name %q", name)
		}
	}
	return nil
}

type limitedBuffer struct {
	b         strings.Builder
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	const limit = 4096
	n := len(p)
	remaining := limit - b.b.Len()
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
			b.truncated = true
		}
		_, _ = b.b.Write(p)
	} else if len(p) > 0 {
		b.truncated = true
	}
	return n, nil
}

func (b *limitedBuffer) String() string {
	s := b.b.String()
	if b.truncated {
		s += "...(truncated)"
	}
	return s
}

// run executes a git command in repoDir, discarding its output; errors
// come back with the exit status and any captured stderr for context.
func run(ctx context.Context, dir string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), nonInteractiveEnv...)
	var stderr limitedBuffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// runOut returns stdout for commands where we care about it (like
// `git config --get`). Non-zero exit is propagated so the caller can
// distinguish "key missing" from "some other error".
func runOut(ctx context.Context, dir string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), nonInteractiveEnv...)
	var stdout, stderr limitedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
