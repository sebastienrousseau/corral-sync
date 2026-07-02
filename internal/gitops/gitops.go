// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: GPL-3.0-only

// Package gitops wraps the local git binary. We shell out via os/exec —
// no libgit2, no go-git — so behaviour exactly matches what a user would
// see on the CLI, including ssh key pickup from ssh-agent.
//
// Every command runs with a non-interactive environment (GIT_TERMINAL_
// PROMPT=0, GIT_ASKPASS=echo) so a cron-driven invocation never blocks
// waiting for a password on missing credentials — it fails loudly
// instead.
package gitops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// nonInteractiveEnv is the environment overlay that guarantees a git
// invocation cannot stall on a credential prompt. Cron users never see
// stdin, so a prompt would hang forever otherwise.
var nonInteractiveEnv = []string{
	"GIT_TERMINAL_PROMPT=0",
	"GIT_ASKPASS=echo",
	"GCM_INTERACTIVE=Never",
}

// EnsureRemote adds a remote at name→url on the repo at repoDir. If a
// remote with that name already exists and its URL matches, we no-op.
// If the URL differs we update it via `git remote set-url`, which keeps
// the local state converging with the desired state on every run.
func EnsureRemote(ctx context.Context, repoDir, name, url string) error {
	current, err := runOut(ctx, repoDir, "git", "remote", "get-url", name)
	if err == nil {
		if strings.TrimSpace(current) == url {
			return nil // already correct
		}
		// The remote exists but points somewhere else. Update in place.
		return run(ctx, repoDir, "git", "remote", "set-url", name, url)
	}
	// If the remote doesn't exist, `get-url` exits 2 with "No such
	// remote". Any other error propagates.
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return err
	}
	return run(ctx, repoDir, "git", "remote", "add", name, url)
}

// PushAllWithPrune uploads every local branch to the remote and deletes
// remote branches that no longer exist locally.
//
// `git push --prune --all <remote>` does exactly that in one round trip:
//   - --all pushes every local branch (including newly-created ones)
//   - --prune deletes remote branches whose local counterpart is gone
//
// Together they produce absolute-parity branch-refs on the remote for
// the ones we own; branches created directly on the remote will be
// deleted (which is what "mirror" means, and what --prune promises).
func PushAllWithPrune(ctx context.Context, repoDir, remoteName string) error {
	return run(ctx, repoDir, "git", "push", "--prune", "--all", remoteName)
}

// PushTagsWithPrune uploads every local tag and deletes remote tags that
// no longer exist locally. Tags follow the same mirror semantics as
// branches: the local tag namespace is authoritative.
//
// `--prune` and `--tags` compose: any remote-side tag missing from the
// local repo is deleted.
func PushTagsWithPrune(ctx context.Context, repoDir, remoteName string) error {
	return run(ctx, repoDir, "git", "push", "--prune", "--tags", remoteName)
}

// run executes a git command in repoDir, discarding its output; errors
// come back with the exit status and any captured stderr for context.
func run(ctx context.Context, dir string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), nonInteractiveEnv...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// runOut returns stdout for commands where we care about it (like
// `git remote get-url`). Non-zero exit is propagated so the caller can
// distinguish "remote missing" from "some other error".
func runOut(ctx context.Context, dir string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), nonInteractiveEnv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return stdout.String(), nil
}
