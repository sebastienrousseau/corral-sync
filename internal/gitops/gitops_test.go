// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: GPL-3.0-only

package gitops

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitCommand(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func initRepo(t *testing.T, commit bool) string {
	t.Helper()
	dir := t.TempDir()
	gitCommand(t, dir, "init", "-b", "main")
	gitCommand(t, dir, "config", "user.name", "Test")
	gitCommand(t, dir, "config", "user.email", "test@example.com")
	if commit {
		if err := os.WriteFile(filepath.Join(dir, "README"), []byte("test"), 0o600); err != nil {
			t.Fatal(err)
		}
		gitCommand(t, dir, "add", "README")
		gitCommand(t, dir, "commit", "-m", "initial")
	}
	return dir
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

func TestPushOperations(t *testing.T) {
	repo := initRepo(t, true)
	bare := t.TempDir()
	gitCommand(t, bare, "init", "--bare")
	gitCommand(t, repo, "remote", "add", "mirror", bare)
	gitCommand(t, repo, "tag", "v1")
	if err := PushAllWithPrune(context.Background(), repo, "mirror"); err != nil {
		t.Fatal(err)
	}
	if err := PushTagsWithPrune(context.Background(), repo, "mirror"); err != nil {
		t.Fatal(err)
	}
	if err := PushAllWithPrune(context.Background(), repo, "bad/name"); err == nil {
		t.Fatal("expected invalid remote error")
	}
	if err := PushTagsWithPrune(context.Background(), repo, "-bad"); err == nil {
		t.Fatal("expected invalid tag remote error")
	}
	if err := PushAllWithPrune(context.Background(), repo, "missing"); err == nil {
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
