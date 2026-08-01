// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: GPL-3.0-only

package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sebastienrousseau/corral-sync/internal/remote"
)

type fakeProvider struct {
	name  string
	url   string
	err   error
	calls atomic.Int64
}

func (p *fakeProvider) Name() string { return p.name }
func (p *fakeProvider) EnsureRepo(context.Context, remote.Repo) (string, error) {
	p.calls.Add(1)
	return p.url, p.err
}

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func withGitSeams(t *testing.T) {
	t.Helper()
	oldEmpty, oldRemote, oldAll, oldTags := isEmpty, ensureRemote, pushAll, pushTags
	t.Cleanup(func() { isEmpty, ensureRemote, pushAll, pushTags = oldEmpty, oldRemote, oldAll, oldTags })
	isEmpty = func(context.Context, string) (bool, error) { return false, nil }
	ensureRemote = func(context.Context, string, string, string) error { return nil }
	pushAll = func(context.Context, string, string) error { return nil }
	pushTags = func(context.Context, string, string) error { return nil }
}

func TestRunSuccessDryRunAndCancellation(t *testing.T) {
	withGitSeams(t)
	p := &fakeProvider{name: "mirror", url: "git@example.com:a.git"}
	repos := []remote.Repo{{Name: "a", Visibility: remote.Private}, {Name: "b", Visibility: remote.Public}}
	got := Run(context.Background(), []remote.Provider{p}, repos, 0, time.Second, false, testLogger())
	if got.Processed != 2 || got.Errors != 0 || p.calls.Load() != 2 {
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
}

func TestProcessOneEmptyAndFailures(t *testing.T) {
	withGitSeams(t)
	p := &fakeProvider{name: "mirror", url: "git@example.com:a.git"}
	r := remote.Repo{Name: "a", Visibility: remote.Private}
	disabled := &sync.Map{}
	providerErrs := &atomic.Int64{}

	isEmpty = func(context.Context, string) (bool, error) { return true, nil }
	if err := processOne(context.Background(), []remote.Provider{p}, r, time.Second, false, disabled, providerErrs, testLogger()); err != nil || p.calls.Load() != 0 {
		t.Fatalf("empty result = %v, calls=%d", err, p.calls.Load())
	}
	isEmpty = func(context.Context, string) (bool, error) { return false, errors.New("inspect") }
	if err := processOne(context.Background(), []remote.Provider{p}, r, time.Second, false, disabled, providerErrs, testLogger()); err == nil {
		t.Fatal("expected inspect error")
	}
	isEmpty = func(context.Context, string) (bool, error) { return false, nil }

	p.err = errors.New("ensure")
	if err := processOne(context.Background(), []remote.Provider{p}, r, time.Second, false, disabled, providerErrs, testLogger()); err == nil {
		t.Fatal("expected ensure error")
	}
	p.err = nil
	ensureRemote = func(context.Context, string, string, string) error { return errors.New("remote") }
	if err := processOne(context.Background(), []remote.Provider{p}, r, time.Second, false, disabled, providerErrs, testLogger()); err == nil {
		t.Fatal("expected remote error")
	}
	ensureRemote = func(context.Context, string, string, string) error { return nil }
	pushAll = func(context.Context, string, string) error { return errors.New("branches") }
	if err := processOne(context.Background(), []remote.Provider{p}, r, time.Second, false, disabled, providerErrs, testLogger()); err == nil {
		t.Fatal("expected branch error")
	}
	pushAll = func(context.Context, string, string) error { return nil }
	pushTags = func(context.Context, string, string) error { return errors.New("tags") }
	if err := processOne(context.Background(), []remote.Provider{p}, r, time.Second, false, disabled, providerErrs, testLogger()); err == nil {
		t.Fatal("expected tag error")
	}
}

func TestFatalProviderDisabledOnce(t *testing.T) {
	withGitSeams(t)
	p := &fakeProvider{name: "mirror", err: remote.Fatal(errors.New("bad token"))}
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
