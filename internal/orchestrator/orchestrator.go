// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

// Package orchestrator runs the sync across every provider with a bounded
// worker pool. It owns concurrency; every other package is single-threaded
// per call.
package orchestrator

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sebastienrousseau/corral-sync/internal/gitops"
	"github.com/sebastienrousseau/corral-sync/internal/remote"
)

var (
	isEmpty      = gitops.IsEmpty
	originURL    = gitops.OriginURL
	ensureRemote = gitops.EnsureRemote
	pushMirror   = gitops.PushMirror
)

// Result summarises one run. Written to slog so cron output stays
// structured, but also returned so a caller (or a test) can assert
// against it.
type Result struct {
	// Processed counts repositories that completed without error,
	// including ones where every provider was skipped.
	Processed int
	// Skipped counts repository/provider pairs that were deliberately not
	// pushed: an empty repository counts once, a repository whose origin
	// is the destination forge counts once per such provider.
	Skipped int
	// Errors counts failed repositories plus providers that were disabled
	// for the rest of the run.
	Errors int
}

// counters is what the workers share. Atomics rather than a result channel:
// there are three numbers and nothing else to collect.
type counters struct {
	processed    atomic.Int64
	skipped      atomic.Int64
	errs         atomic.Int64
	providerErrs atomic.Int64
	disabled     sync.Map
}

// Run mirrors every repo through every provider. workers controls
// concurrency across repositories — per-repo work is sequential because
// git pushes to the same repo would contend on the .git/ index anyway.
//
// The pool pattern is standard: a job channel fed from the main goroutine,
// workers pulling until the channel closes, results collected via atomics.
func Run(ctx context.Context, providers []remote.Provider, repos []remote.Repo, workers int, timeout time.Duration, dryRun bool, logger *slog.Logger) Result {
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan remote.Repo)
	var c counters

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
				if err := processOne(ctx, providers, r, timeout, dryRun, &c, lg); err != nil {
					c.errs.Add(1)
					lg.Error("repo failed", slog.String("err", err.Error()))
					continue
				}
				c.processed.Add(1)
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
		Processed: int(c.processed.Load()),
		Skipped:   int(c.skipped.Load()),
		Errors:    int(c.errs.Load() + c.providerErrs.Load()),
	}
}

// processOne mirrors one repo through every provider in sequence. We do
// providers sequentially per repo so the per-provider errors are
// attributable and so we don't run two git-push commands in parallel
// against the same .git directory (which git supports but doesn't love).
//
// Two read-only inspections run first, in dry-run mode as well, so a
// dry run previews exactly the decisions a real run would make:
//
//   - An empty local repo (unborn HEAD) is a legitimate state — the
//     upstream may have been created and never pushed to. It is skipped
//     with an INFO log rather than failing on `git push`.
//   - A repo whose origin lives on a provider's host is never pushed to
//     that provider. corral clones from GitLab, Gitea, Forgejo, Codeberg
//     and Bitbucket as well as GitHub, so the tree can hold a clone whose
//     origin *is* the destination; mirroring it back would run
//     `git push --prune` against its own upstream, and a single-branch
//     clone would delete every branch it does not carry.
func processOne(ctx context.Context, providers []remote.Provider, r remote.Repo, timeout time.Duration, dryRun bool, c *counters, log *slog.Logger) error {
	empty, err := isEmpty(ctx, r.LocalPath)
	if err != nil {
		return err
	}
	if empty {
		c.skipped.Add(1)
		log.Info("skipping empty repo (no commits yet)")
		return nil
	}
	origin, err := originURL(ctx, r.LocalPath)
	if err != nil {
		return err
	}
	originHost := remote.CanonicalHost(origin)

	for _, p := range providers {
		lg := log.With(slog.String("provider", p.Name()))
		if _, ok := c.disabled.Load(p.Name()); ok {
			continue
		}
		if originHost != "" && originHost == p.Host() {
			c.skipped.Add(1)
			lg.Info("origin is on this provider; not mirroring a repository onto itself",
				slog.String("origin_host", originHost))
			continue
		}
		if dryRun {
			lg.Info("dry-run: would ensure repo + push branches and tags")
			continue
		}

		opCtx, cancel := context.WithTimeout(ctx, timeout)
		err := mirror(opCtx, p, r, lg)
		cancel()
		if err == nil {
			lg.Info("mirrored")
			continue
		}
		if !remote.IsFatal(err) {
			return err
		}
		if _, loaded := c.disabled.LoadOrStore(p.Name(), err.Error()); !loaded {
			c.providerErrs.Add(1)
			lg.Error("provider disabled", slog.String("err", err.Error()))
		}
	}
	return nil
}

// mirror is the per-provider unit of work: ensure the destination exists,
// point a remote at it, push. It is separate from processOne so the
// timeout context is created and cancelled in exactly one place.
func mirror(ctx context.Context, p remote.Provider, r remote.Repo, lg *slog.Logger) error {
	cloneURL, err := p.EnsureRepo(ctx, r)
	if err != nil {
		return err
	}
	lg.Debug("ensured", slog.String("clone_url", cloneURL))
	if err := ensureRemote(ctx, r.LocalPath, p.Name(), cloneURL); err != nil {
		return err
	}
	return pushMirror(ctx, r.LocalPath, p.Name())
}
