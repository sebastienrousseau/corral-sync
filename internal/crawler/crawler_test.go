// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: GPL-3.0-only

package crawler

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/sebastienrousseau/corral-sync/internal/remote"
)

func TestWalkFindsRepositoriesDeterministically(t *testing.T) {
	base := t.TempDir()
	private := filepath.Join(base, "Private", "go", "zeta")
	public := filepath.Join(base, "Public", "go", "alpha")
	worktree := filepath.Join(base, "Public", "rust", "beta")
	for _, dir := range []string{private, public, worktree} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{private, public} {
		if err := os.Mkdir(filepath.Join(dir, ".git"), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: elsewhere"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(public, "nested", ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	got, err := Walk(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Name != "zeta" || got[1].Name != "alpha" || got[2].Name != "beta" {
		t.Fatalf("unexpected repositories: %+v", got)
	}
	if got[0].Visibility != remote.Private || got[1].Visibility != remote.Public {
		t.Fatalf("unexpected visibility: %+v", got)
	}
}

func TestWalkErrorsAndIgnoresUnsafeGitEntry(t *testing.T) {
	if _, err := Walk(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected missing root error")
	}
	base := t.TempDir()
	repo := filepath.Join(base, "Public", "go", "repo")
	if err := os.MkdirAll(repo, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("elsewhere", filepath.Join(repo, ".git")); err != nil {
		t.Fatal(err)
	}
	got, err := Walk(base)
	if err != nil || len(got) != 0 {
		t.Fatalf("unsafe .git entry result = %+v, %v", got, err)
	}
}

func TestWalkRejectsRemoteNameCollisions(t *testing.T) {
	base := t.TempDir()
	for _, path := range []string{
		filepath.Join(base, "Public", "go", "Repo", ".git"),
		filepath.Join(base, "Private", "rust", "repo", ".git"),
	} {
		if err := os.MkdirAll(path, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Walk(base); err == nil {
		t.Fatal("expected repository name collision")
	}
}

func TestWalkInjectedBranches(t *testing.T) {
	oldWalk, oldLstat := walkDir, lstat
	t.Cleanup(func() { walkDir, lstat = oldWalk, oldLstat })
	walkDir = func(root string, fn fs.WalkDirFunc) error {
		if err := fn(root, fakeDirEntry{name: "root", dir: true}, nil); err != nil {
			return err
		}
		if err := fn(filepath.Join(root, "denied"), nil, errors.New("denied")); err != nil {
			return err
		}
		return fn(filepath.Join(root, "file"), fakeDirEntry{name: "file"}, nil)
	}
	lstat = func(string) (fs.FileInfo, error) { return nil, errors.New("missing") }
	if got, err := Walk("root"); err != nil || len(got) != 0 {
		t.Fatalf("injected walk = %+v, %v", got, err)
	}
	walkDir = func(root string, fn fs.WalkDirFunc) error {
		return fn(root, nil, errors.New("root failed"))
	}
	if _, err := Walk("root"); err == nil {
		t.Fatal("expected root walk error")
	}
	walkDir = func(root string, fn fs.WalkDirFunc) error {
		if got := fn(filepath.Join(root, ".git"), fakeDirEntry{name: ".git", dir: true}, nil); !errors.Is(got, fs.SkipDir) {
			t.Fatalf(".git callback = %v", got)
		}
		return nil
	}
	if got, err := Walk("root"); err != nil || len(got) != 0 {
		t.Fatalf(".git walk = %+v, %v", got, err)
	}
}

func TestVisibilityPrivateWinsAndRelFailure(t *testing.T) {
	path := filepath.Join("base", "Public", "Private", "repo")
	if got := visibilityFromPath("base", path); got != remote.Private {
		t.Fatalf("visibility = %q", got)
	}
	if got := visibilityFromPath("base", filepath.Join("base", "other", "repo")); got != remote.Private {
		t.Fatalf("default visibility = %q", got)
	}
	oldRel := relativePath
	t.Cleanup(func() { relativePath = oldRel })
	relativePath = func(string, string) (string, error) { return "", errors.New("bad") }
	if got := visibilityFromPath("base", "repo"); got != remote.Private {
		t.Fatalf("failure visibility = %q", got)
	}
}

type fakeDirEntry struct {
	name string
	dir  bool
}

func (d fakeDirEntry) Name() string               { return d.name }
func (d fakeDirEntry) IsDir() bool                { return d.dir }
func (d fakeDirEntry) Type() fs.FileMode          { return 0 }
func (d fakeDirEntry) Info() (fs.FileInfo, error) { return nil, errors.New("unused") }

func FuzzVisibilityFromPath(f *testing.F) {
	f.Add("Public", "go", "repo")
	f.Add("Private", "rust", "repo")
	f.Fuzz(func(t *testing.T, visibility, language, name string) {
		base := "base"
		_ = visibilityFromPath(base, filepath.Join(base, visibility, language, name))
	})
}
