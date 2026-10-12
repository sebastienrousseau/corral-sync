// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sebastienrousseau/corral-sync/internal/remote"
)

type fakeProvider struct {
	name  string
	host  string
	url   string
	err   error
	calls atomic.Int64
}

func (p *fakeProvider) Name() string { return p.name }
func (p *fakeProvider) Host() string { return p.host }
func (p *fakeProvider) EnsureRepo(context.Context, remote.Repo) (string, error) {
	p.calls.Add(1)
	return p.url, p.err
}

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func withGitSeams(t *testing.T) {
	t.Helper()
	oldEmpty, oldOrigin, oldRemote, oldPush := isEmpty, originURL, ensureRemote, pushMirror
	t.Cleanup(func() { isEmpty, originURL, ensureRemote, pushMirror = oldEmpty, oldOrigin, oldRemote, oldPush })
	isEmpty = func(context.Context, string) (bool, error) { return false, nil }
	originURL = func(context.Context, string) (string, error) { return "git@github.com:owner/repo.git", nil }
	ensureRemote = func(context.Context, string, string, string) error { return nil }
	pushMirror = func(context.Context, string, string) error { return nil }
}

// noGoroutineLeak asserts that a Run leaves no worker behind. Stdlib only —
// the project has no third-party dependencies and a leak detector is not
// worth becoming the first — so the check is a settle loop on the goroutine
// count rather than a stack-trace diff.
func noGoroutineLeak(t *testing.T, before int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > before {
		t.Fatalf("goroutines leaked: %d before, %d after", before, got)
	}
}

func TestRunSuccessDryRunAndCancellation(t *testing.T) {
	withGitSeams(t)
	before := runtime.NumGoroutine()
	p := &fakeProvider{name: "mirror", host: "mirror.test", url: "git@example.com:a.git"}
	repos := []remote.Repo{{Name: "a", Visibility: remote.Private}, {Name: "b", Visibility: remote.Public}}
	got := Run(context.Background(), []remote.Provider{p}, repos, 0, time.Second, false, testLogger())
	if got.Processed != 2 || got.Errors != 0 || got.Skipped != 0 || p.calls.Load() != 2 {
		t.Fatalf("success result = %+v, calls=%d", got, p.calls.Load())
	}
	p.calls.Store(0)
	got = Run(context.Background(), []remote.Provider{p}, repos, 2, time.Second, true, testLogger())
	if got.Processed != 2 || p.calls.Load() != 0 {
		t.Fatalf("dry result = %+v, calls=%d", got, p.calls.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got = Run(ctx, []remote.Provider{p}, repos, 1, time.Second, false, testLogger())
	if got.Processed != 0 {
		t.Fatalf("canceled result = %+v", got)
	}
	noGoroutineLeak(t, before)
}

func TestProcessOneEmptyAndFailures(t *testing.T) {
	withGitSeams(t)
	p := &fakeProvider{name: "mirror", host: "mirror.test", url: "git@example.com:a.git"}
	r := remote.Repo{Name: "a", Visibility: remote.Private}
	c := &counters{}

	isEmpty = func(context.Context, string) (bool, error) { return true, nil }
	if err := processOne(context.Background(), []remote.Provider{p}, r, time.Second, false, c, testLogger()); err != nil || p.calls.Load() != 0 || c.skipped.Load() != 1 {
		t.Fatalf("empty result = %v, calls=%d, skipped=%d", err, p.calls.Load(), c.skipped.Load())
	}
	isEmpty = func(context.Context, string) (bool, error) { return false, errors.New("inspect") }
	if err := processOne(context.Background(), []remote.Provider{p}, r, time.Second, false, c, testLogger()); err == nil {
		t.Fatal("expected inspect error")
	}
	isEmpty = func(context.Context, string) (bool, error) { return false, nil }
	originURL = func(context.Context, string) (string, error) { return "", errors.New("config") }
	if err := processOne(context.Background(), []remote.Provider{p}, r, time.Second, false, c, testLogger()); err == nil {
		t.Fatal("expected origin error")
	}
	originURL = func(context.Context, string) (string, error) { return "", nil }

	p.err = errors.New("ensure")
	if err := processOne(context.Background(), []remote.Provider{p}, r, time.Second, false, c, testLogger()); err == nil {
		t.Fatal("expected ensure error")
	}
	p.err = nil
	ensureRemote = func(context.Context, string, string, string) error { return errors.New("remote") }
	if err := processOne(context.Background(), []remote.Provider{p}, r, time.Second, false, c, testLogger()); err == nil {
		t.Fatal("expected remote error")
	}
	ensureRemote = func(context.Context, string, string, string) error { return nil }
	pushMirror = func(context.Context, string, string) error { return errors.New("push") }
	if err := processOne(context.Background(), []remote.Provider{p}, r, time.Second, false, c, testLogger()); err == nil {
		t.Fatal("expected push error")
	}
}

// TestOriginGuard is the regression test for the case corral's multi-forge
// support introduced: a clone whose origin is the destination provider must
// not be pushed back onto it, in a real run or a dry run, while the other
// provider still receives it.
func TestOriginGuard(t *testing.T) {
	withGitSeams(t)
	originURL = func(context.Context, string) (string, error) {
		return "ssh://git@GitLab.example.com:2222/owner/repo.git", nil
	}
	gitlab := &fakeProvider{name: "gitlab", host: "gitlab.example.com", url: "git@gitlab.example.com:owner/repo.git"}
	gitea := &fakeProvider{name: "gitea", host: "gitea.example.com", url: "git@gitea.example.com:owner/repo.git"}
	repos := []remote.Repo{{Name: "repo", Visibility: remote.Private}}
	for _, dryRun := range []bool{false, true} {
		gitlab.calls.Store(0)
		gitea.calls.Store(0)
		got := Run(context.Background(), []remote.Provider{gitlab, gitea}, repos, 1, time.Second, dryRun, testLogger())
		if got.Processed != 1 || got.Skipped != 1 || got.Errors != 0 || gitlab.calls.Load() != 0 {
			t.Fatalf("dryRun=%v: result = %+v, gitlab calls=%d", dryRun, got, gitlab.calls.Load())
		}
		wantGitea := int64(1)
		if dryRun {
			wantGitea = 0
		}
		if gitea.calls.Load() != wantGitea {
			t.Fatalf("dryRun=%v: gitea calls = %d, want %d", dryRun, gitea.calls.Load(), wantGitea)
		}
	}
}

func TestFatalProviderDisabledOnce(t *testing.T) {
	withGitSeams(t)
	p := &fakeProvider{name: "mirror", host: "mirror.test", err: remote.Fatal(errors.New("bad token"))}
	repos := []remote.Repo{{Name: "a", Visibility: remote.Private}, {Name: "b", Visibility: remote.Private}}
	got := Run(context.Background(), []remote.Provider{p}, repos, 1, time.Second, false, testLogger())
	if got.Processed != 2 || got.Errors != 1 || p.calls.Load() != 1 {
		t.Fatalf("fatal result = %+v, calls=%d", got, p.calls.Load())
	}
}

func TestRunCountsRepositoryError(t *testing.T) {
	withGitSeams(t)
	isEmpty = func(context.Context, string) (bool, error) { return false, errors.New("failed") }
	got := Run(context.Background(), nil, []remote.Repo{{Name: "a", Visibility: remote.Private}}, 1, time.Second, false, testLogger())
	if got.Errors != 1 || got.Processed != 0 {
		t.Fatalf("error result = %+v", got)
	}
}
