// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: GPL-3.0-only

// Package orchestrator runs the sync across every provider with a bounded
// worker pool. It owns concurrency; every other package is single-threaded
// per call.
package orchestrator

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/sebastienrousseau/corral-sync/internal/gitops"
	"github.com/sebastienrousseau/corral-sync/internal/remote"
)

// Result summarises one run. Written to slog so cron output stays
// structured, but also returned so a caller (or a test) can assert
// against it.
type Result struct {
	Processed int
	Errors    int
}

// Run mirrors every repo through every provider. workers controls
// concurrency across repositories — per-repo work is sequential because
// git pushes to the same repo would contend on the .git/ index anyway.
//
// The pool pattern is standard: a job channel fed from the main goroutine,
// workers pulling until the channel closes, results collected via atomics
// (only two counters — no need for a result channel).
func Run(ctx context.Context, providers []remote.Provider, repos []remote.Repo, workers int, dryRun bool, logger *slog.Logger) Result {
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan remote.Repo)

	var processed, errs atomic.Int64

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for r := range jobs {
				lg := logger.With(
					slog.Int("worker", id),
					slog.String("repo", r.Name),
					slog.String("visibility", string(r.Visibility)),
				)
				if err := processOne(ctx, providers, r, dryRun, lg); err != nil {
					errs.Add(1)
					lg.Error("repo failed", slog.String("err", err.Error()))
					continue
				}
				processed.Add(1)
			}
		}(i)
	}

enqueue:
	for _, r := range repos {
		select {
		case <-ctx.Done():
			// break out of the outer for, not just the select.
			break enqueue
		case jobs <- r:
		}
	}
	close(jobs)
	wg.Wait()

	return Result{
		Processed: int(processed.Load()),
		Errors:    int(errs.Load()),
	}
}

// processOne mirrors one repo through every provider in sequence. We do
// providers sequentially per repo so the per-provider errors are
// attributable and so we don't run two git-push commands in parallel
// against the same .git directory (which git supports but doesn't love).
func processOne(ctx context.Context, providers []remote.Provider, r remote.Repo, dryRun bool, log *slog.Logger) error {
	for _, p := range providers {
		lg := log.With(slog.String("provider", p.Name()))
		if dryRun {
			lg.Info("dry-run: would ensure repo + push branches + tags")
			continue
		}

		cloneURL, err := p.EnsureRepo(ctx, r)
		if err != nil {
			return err
		}
		lg.Debug("ensured", slog.String("clone_url", cloneURL))

		if err := gitops.EnsureRemote(ctx, r.LocalPath, p.Name(), cloneURL); err != nil {
			return err
		}
		if err := gitops.PushAllWithPrune(ctx, r.LocalPath, p.Name()); err != nil {
			return err
		}
		if err := gitops.PushTagsWithPrune(ctx, r.LocalPath, p.Name()); err != nil {
			return err
		}
		lg.Info("mirrored")
	}
	return nil
}
