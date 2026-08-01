// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: GPL-3.0-only

// Package crawler walks the corral base directory, finds every local Git
// repository and yields a [remote.Repo] value the orchestrator can hand to
// each configured provider.
package crawler

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sebastienrousseau/corral-sync/internal/remote"
)

var (
	walkDir      = filepath.WalkDir
	lstat        = os.Lstat
	relativePath = filepath.Rel
)

// Walk finds every local repository under baseDir and returns them as
// provider-neutral [remote.Repo] values.
//
// A "repository" is any directory that directly contains a `.git` entry
// (directory or file — worktree/submodule links use a file). We do not
// descend into a repo's working tree, so nested submodules are not
// double-counted.
//
// Visibility is deduced from the presence of a `Public` or `Private`
// segment anywhere in the path relative to baseDir — matching the layout
// corral produces. If neither is present we default to [remote.Private]
// because it is the safer failure mode: leaking a private repo as public
// is much worse than the reverse, which the user notices immediately.
func Walk(baseDir string) ([]remote.Repo, error) {
	var repos []remote.Repo
	seen := make(map[string]string)

	err := walkDir(baseDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if filepath.Clean(path) == filepath.Clean(baseDir) {
				return err
			}
			// Permission errors mid-walk should not abort — cron users
			// often have stray directories they can't read. Skip
			// silently; the orchestrator logs the resulting count.
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if d.Name() == ".git" {
			// The .git subdir itself is not a repo root and can have
			// very deep object trees. Skip it.
			return fs.SkipDir
		}

		// Is this a repo root? (Does it contain a .git entry?)
		if info, statErr := lstat(filepath.Join(path, ".git")); statErr == nil &&
			(info.IsDir() || info.Mode().IsRegular()) {
			name := filepath.Base(path)
			key := strings.ToLower(name)
			if previous, exists := seen[key]; exists {
				return fmt.Errorf("repository name collision %q between %s and %s", name, previous, path)
			}
			seen[key] = path
			repos = append(repos, remote.Repo{
				Name:       name,
				LocalPath:  path,
				Visibility: visibilityFromPath(baseDir, path),
			})
			// Do not descend into a repo's working tree — mirroring
			// does not need it, and skipping saves time.
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].LocalPath < repos[j].LocalPath })
	return repos, nil
}

// visibilityFromPath returns [remote.Public] if any segment of the path
// between baseDir and repoPath is literally "Public"; [remote.Private]
// otherwise. Case-sensitive on purpose — corral produces `Public` /
// `Private` with those exact capitalisations.
func visibilityFromPath(baseDir, repoPath string) remote.Visibility {
	rel, err := relativePath(baseDir, repoPath)
	if err != nil {
		return remote.Private
	}
	public := false
	for _, seg := range strings.Split(rel, string(filepath.Separator)) {
		switch seg {
		case "Public":
			public = true
		case "Private":
			return remote.Private
		}
	}
	if public {
		return remote.Public
	}
	return remote.Private
}
