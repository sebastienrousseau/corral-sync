// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package gitops

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitCommand(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func initRepo(t *testing.T, commit bool) string {
	t.Helper()
	dir := t.TempDir()
	gitCommand(t, dir, "init", "-b", "main")
	gitCommand(t, dir, "config", "user.name", "Test")
	gitCommand(t, dir, "config", "user.email", "test@example.com")
	gitCommand(t, dir, "config", "commit.gpgsign", "false")
	if commit {
		commitFile(t, dir, "README", "test")
	}
	return dir
}

func commitFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, dir, "add", name)
	gitCommand(t, dir, "commit", "-q", "-m", "add "+name)
}

func remoteRefs(t *testing.T, bare string) map[string]string {
	t.Helper()
	refs := map[string]string{}
	for _, line := range strings.Split(gitCommand(t, bare, "for-each-ref", "--format=%(refname) %(objectname)"), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 {
			refs[fields[0]] = fields[1]
		}
	}
	return refs
}

func TestIsEmpty(t *testing.T) {
	empty := initRepo(t, false)
	got, err := IsEmpty(context.Background(), empty)
	if err != nil || !got {
		t.Fatalf("empty = %v, %v", got, err)
	}
	full := initRepo(t, true)
	got, err = IsEmpty(context.Background(), full)
	if err != nil || got {
		t.Fatalf("full = %v, %v", got, err)
	}
	if _, err := IsEmpty(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected inspection error")
	}
}

func TestOriginURL(t *testing.T) {
	repo := initRepo(t, true)
	ctx := context.Background()
	got, err := OriginURL(ctx, repo)
	if err != nil || got != "" {
		t.Fatalf("no origin = %q, %v", got, err)
	}
	gitCommand(t, repo, "remote", "add", "origin", "git@gitlab.com:owner/repo.git")
	got, err = OriginURL(ctx, repo)
	if err != nil || got != "git@gitlab.com:owner/repo.git" {
		t.Fatalf("origin = %q, %v", got, err)
	}
	if _, err := OriginURL(ctx, filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected error for a missing directory")
	}
}

func TestEnsureRemote(t *testing.T) {
	repo := initRepo(t, true)
	ctx := context.Background()
	if err := EnsureRemote(ctx, repo, "mirror", "https://example.com/a.git"); err != nil {
		t.Fatal(err)
	}
	if err := EnsureRemote(ctx, repo, "mirror", "https://example.com/a.git"); err != nil {
		t.Fatal(err)
	}
	if err := EnsureRemote(ctx, repo, "mirror", "git@example.com:a.git"); err != nil {
		t.Fatal(err)
	}
	got, err := runOut(ctx, repo, "git", "remote", "get-url", "mirror")
	if err != nil || strings.TrimSpace(got) != "git@example.com:a.git" {
		t.Fatalf("remote = %q, %v", got, err)
	}
	if err := EnsureRemote(ctx, repo, "bad/name", "https://example.com/a.git"); err == nil {
		t.Fatal("expected bad remote name")
	}
	if err := EnsureRemote(ctx, repo, "mirror", "file:///tmp/a"); err == nil {
		t.Fatal("expected bad URL")
	}
	if err := EnsureRemote(ctx, filepath.Join(repo, "missing"), "other", "https://example.com/a.git"); err == nil {
		t.Fatal("expected git config error")
	}
}

// TestPushMirrorSemantics pins the three properties the security model
// relies on (C2): branches are never forced, tags are, and both namespaces
// are pruned to parity with the local repository — in one push.
func TestPushMirrorSemantics(t *testing.T) {
	ctx := context.Background()
	repo := initRepo(t, true)
	bare := t.TempDir()
	gitCommand(t, bare, "init", "--bare")
	gitCommand(t, repo, "remote", "add", "mirror", bare)
	gitCommand(t, repo, "branch", "feature")
	gitCommand(t, repo, "tag", "v1")
	first := gitCommand(t, repo, "rev-parse", "HEAD")

	if err := PushMirror(ctx, repo, "mirror"); err != nil {
		t.Fatal(err)
	}
	refs := remoteRefs(t, bare)
	if refs["refs/heads/main"] != first || refs["refs/heads/feature"] != first || refs["refs/tags/v1"] != first {
		t.Fatalf("first push refs = %v", refs)
	}

	// Local moves on: a branch is deleted, a tag is re-pointed, and the
	// remote grows a branch and a tag nobody has locally.
	gitCommand(t, repo, "branch", "-D", "feature")
	commitFile(t, repo, "second", "2")
	second := gitCommand(t, repo, "rev-parse", "HEAD")
	gitCommand(t, repo, "tag", "-f", "v1")
	gitCommand(t, bare, "update-ref", "refs/heads/remote-only", first)
	gitCommand(t, bare, "update-ref", "refs/tags/remote-tag", first)

	if err := PushMirror(ctx, repo, "mirror"); err != nil {
		t.Fatal(err)
	}
	refs = remoteRefs(t, bare)
	if refs["refs/heads/main"] != second {
		t.Fatalf("main not fast-forwarded: %v", refs)
	}
	if refs["refs/tags/v1"] != second {
		t.Fatalf("tag not force-updated: %v", refs)
	}
	for _, gone := range []string{"refs/heads/feature", "refs/heads/remote-only", "refs/tags/remote-tag"} {
		if _, exists := refs[gone]; exists {
			t.Fatalf("%s was not pruned: %v", gone, refs)
		}
	}

	// A branch that diverged from the remote must be refused, and the
	// remote left untouched — the one thing a mirror must never do is
	// rewrite history it did not produce.
	gitCommand(t, repo, "reset", "-q", "--hard", first)
	commitFile(t, repo, "diverged", "3")
	err := PushMirror(ctx, repo, "mirror")
	if err == nil || !strings.Contains(err.Error(), "non-fast-forward") && !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("expected a refused non-fast-forward push, got %v", err)
	}
	if got := remoteRefs(t, bare)["refs/heads/main"]; got != second {
		t.Fatalf("remote main rewritten to %s after a refused push", got)
	}

	if err := PushMirror(ctx, repo, "bad/name"); err == nil {
		t.Fatal("expected invalid remote error")
	}
	if err := PushMirror(ctx, repo, "missing"); err == nil {
		t.Fatal("expected missing remote error")
	}
}

func TestCommandHelpersAndLimitedBuffer(t *testing.T) {
	dir := t.TempDir()
	if err := run(context.Background(), dir, "git", "version"); err != nil {
		t.Fatal(err)
	}
	if _, err := runOut(context.Background(), dir, "git", "not-a-command"); err == nil {
		t.Fatal("expected command failure")
	}
	if err := run(context.Background(), dir, "git", "not-a-command"); err == nil {
		t.Fatal("expected run failure")
	}
	for _, name := range []string{"", "-bad", "bad.name", "white space", "line\nfeed"} {
		if err := validateRemoteName(name); err == nil {
			t.Fatalf("expected invalid remote %q", name)
		}
	}
	if err := validateRemoteName("mirror"); err != nil {
		t.Fatal(err)
	}
	var b limitedBuffer
	payload := strings.Repeat("x", 5000)
	n, err := b.Write([]byte(payload))
	if err != nil || n != len(payload) || !strings.HasSuffix(b.String(), "...(truncated)") {
		t.Fatalf("limited buffer = %d, %v, %d", n, err, len(b.String()))
	}
	if n, err := b.Write([]byte("more")); err != nil || n != 4 {
		t.Fatalf("second write = %d, %v", n, err)
	}
}
