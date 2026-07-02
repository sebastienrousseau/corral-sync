// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: GPL-3.0-only

// corral-sync mirrors a corral-organised local repository tree out to
// GitLab and Gitea with absolute-parity semantics (`git push --prune`).
// It is intended to run as the second step of a daily cron:
//
//	corralctl ...     # pull GitHub -> local
//	corral-sync ...   # push local  -> GitLab + Gitea
//
// See DEPLOYMENT.md for the exact crontab entry.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/sebastienrousseau/corral-sync/internal/config"
	"github.com/sebastienrousseau/corral-sync/internal/crawler"
	"github.com/sebastienrousseau/corral-sync/internal/gitea"
	"github.com/sebastienrousseau/corral-sync/internal/gitlab"
	"github.com/sebastienrousseau/corral-sync/internal/orchestrator"
	"github.com/sebastienrousseau/corral-sync/internal/remote"
)

// Version is injected at build time via -ldflags "-X main.Version=v...".
var Version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		// Never fmt.Println to stderr from here — slog owns stderr. A
		// hard-fail before slog is set up is the only place we print
		// raw text.
		fmt.Fprintln(os.Stderr, "corral-sync:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cfg, err := config.Load(args)
	if err != nil {
		return err
	}

	// JSON handler on stderr so cron log files stay grep-friendly and
	// downstream log shippers can parse without regex.
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))
	logger.Info("starting",
		slog.String("version", Version),
		slog.String("base_dir", cfg.BaseDir),
		slog.Int("workers", cfg.Workers),
		slog.Bool("dry_run", cfg.DryRun),
	)

	// Ctx is cancelled by SIGINT/SIGTERM so a stuck git push doesn't
	// linger past a `kill` from the user.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	repos, err := crawler.Walk(cfg.BaseDir)
	if err != nil {
		return fmt.Errorf("crawl %s: %w", cfg.BaseDir, err)
	}
	logger.Info("crawl complete", slog.Int("repos", len(repos)))
	if len(repos) == 0 {
		logger.Warn("no repositories found under base dir")
		return nil
	}

	var providers []remote.Provider
	if cfg.GitLabToken != "" {
		providers = append(providers, gitlab.New(cfg.GitLabURL, cfg.GitLabToken, cfg.GitLabNamespace, logger))
	}
	if cfg.GiteaToken != "" {
		providers = append(providers, gitea.New(cfg.GiteaURL, cfg.GiteaToken, cfg.GiteaOwner, logger))
	}
	logger.Info("providers configured", slog.Int("count", len(providers)))

	res := orchestrator.Run(ctx, providers, repos, cfg.Workers, cfg.DryRun, logger)
	logger.Info("done",
		slog.Int("processed", res.Processed),
		slog.Int("errors", res.Errors),
		slog.Int("total_repos", len(repos)),
	)

	if res.Errors > 0 {
		// Non-zero exit so cron can email on failure.
		return fmt.Errorf("%d/%d repos failed", res.Errors, len(repos))
	}
	return nil
}
