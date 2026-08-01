// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/sebastienrousseau/corral-sync/internal/config"
	"github.com/sebastienrousseau/corral-sync/internal/orchestrator"
	"github.com/sebastienrousseau/corral-sync/internal/remote"
)

func withMainSeams(t *testing.T) {
	t.Helper()
	oldExit, oldStderr, oldLoad, oldWalk, oldRun, oldSignal := exitProcess, stderr, loadConfig, walkRepos, runOrchestrator, signalContext
	t.Cleanup(func() {
		exitProcess, stderr, loadConfig, walkRepos, runOrchestrator, signalContext = oldExit, oldStderr, oldLoad, oldWalk, oldRun, oldSignal
	})
	stderr = io.Discard
	loadConfig = func([]string) (config.Config, error) {
		return config.Config{BaseDir: t.TempDir(), Workers: 1, Timeout: time.Second, DryRun: true}, nil
	}
	walkRepos = func(string) ([]remote.Repo, error) {
		return []remote.Repo{{Name: "repo", Visibility: remote.Private}}, nil
	}
	runOrchestrator = func(context.Context, []remote.Provider, []remote.Repo, int, time.Duration, bool, *slog.Logger) orchestrator.Result {
		return orchestrator.Result{Processed: 1}
	}
}

func TestMainReportsFailure(t *testing.T) {
	withMainSeams(t)
	var out bytes.Buffer
	stderr = &out
	loadConfig = func([]string) (config.Config, error) { return config.Config{}, errors.New("bad config") }
	code := 0
	exitProcess = func(got int) { code = got }
	oldArgs := os.Args
	os.Args = []string{"corral-sync"}
	t.Cleanup(func() { os.Args = oldArgs })
	main()
	if code != 1 || !bytes.Contains(out.Bytes(), []byte("bad config")) {
		t.Fatalf("main = code %d, output %q", code, out.String())
	}
}

func TestRunBranches(t *testing.T) {
	withMainSeams(t)
	normalSignal := signalContext
	loadConfig = func([]string) (config.Config, error) { return config.Config{}, errors.New("load") }
	if err := run(nil); err == nil {
		t.Fatal("expected load error")
	}
	loadConfig = func([]string) (config.Config, error) {
		return config.Config{BaseDir: "base", Workers: 1, Timeout: time.Second}, nil
	}
	walkRepos = func(string) ([]remote.Repo, error) { return nil, errors.New("walk") }
	if err := run(nil); err == nil {
		t.Fatal("expected crawl error")
	}
	walkRepos = func(string) ([]remote.Repo, error) { return nil, nil }
	if err := run(nil); err != nil {
		t.Fatalf("empty run: %v", err)
	}

	walkRepos = func(string) ([]remote.Repo, error) {
		return []remote.Repo{{Name: "repo", Visibility: remote.Private}}, nil
	}
	loadConfig = func([]string) (config.Config, error) {
		return config.Config{BaseDir: "base", Workers: 2, Timeout: time.Second, GitLabToken: "gl", GiteaToken: "gt", GiteaURL: "https://gitea.test"}, nil
	}
	providerCount := 0
	runOrchestrator = func(_ context.Context, providers []remote.Provider, _ []remote.Repo, workers int, timeout time.Duration, _ bool, _ *slog.Logger) orchestrator.Result {
		providerCount = len(providers)
		if workers != 2 || timeout != time.Second {
			t.Fatalf("options = %d, %v", workers, timeout)
		}
		return orchestrator.Result{Errors: 1}
	}
	if err := run(nil); err == nil || providerCount != 2 {
		t.Fatalf("provider error = %v, count=%d", err, providerCount)
	}

	runOrchestrator = func(context.Context, []remote.Provider, []remote.Repo, int, time.Duration, bool, *slog.Logger) orchestrator.Result {
		return orchestrator.Result{Processed: 1}
	}
	signalContext = func(parent context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(parent)
		cancel()
		return ctx, func() {}
	}
	if err := run(nil); err == nil {
		t.Fatal("expected interruption error")
	}
	signalContext = normalSignal
	if err := run(nil); err != nil {
		t.Fatalf("successful run: %v", err)
	}
}
