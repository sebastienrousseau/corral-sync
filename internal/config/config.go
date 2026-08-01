// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: GPL-3.0-only

// Package config parses environment variables and CLI flags into a single
// [Config] value that every other package can read. Centralising it here
// keeps main.go small and makes tests deterministic — pass a Config, get
// predictable behaviour.
package config

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	statPath     = os.Stat
	absolutePath = filepath.Abs
	userHomeDir  = os.UserHomeDir
)

// Config is the resolved configuration for a corral-sync run. Every field
// has a stable meaning independent of where it came from (env, flag, or
// default), so the orchestrator can treat it as immutable input.
type Config struct {
	// BaseDir is the root of the corral-managed local mirror. Every path
	// under it is scanned for `.git` directories.
	BaseDir string

	// GitLabToken is a GitLab Personal Access Token with `api` scope
	// (needed for POST /api/v4/projects). Empty disables GitLab mirroring.
	GitLabToken string
	// GitLabURL is the GitLab base URL. Defaults to https://gitlab.com.
	GitLabURL string
	// GitLabNamespace is the namespace (user or group) under which repos
	// are created. Defaults to the authenticated user's namespace when
	// empty — the GitLab client resolves it via the API on first use.
	GitLabNamespace string

	// GiteaToken is a Gitea personal access token with repo write scope.
	// Empty disables Gitea mirroring.
	GiteaToken string
	// GiteaURL is the base URL of the Gitea instance (self-hosted).
	// No sensible default — either set it or leave GiteaToken empty.
	GiteaURL string
	// GiteaOwner is the Gitea user or org that owns the mirrored repos.
	// Defaults to the authenticated user when empty.
	GiteaOwner string

	// Workers is the number of repositories processed concurrently.
	// Higher values speed up large mirrors at the cost of API rate
	// limits and local git parallelism. 4 is a safe default.
	Workers int
	// Timeout bounds all work for one repository/provider pair.
	Timeout time.Duration

	// DryRun causes the orchestrator to log every intended action
	// without performing any HTTP request or git command. Useful when
	// wiring up cron for the first time.
	DryRun bool

	// LogLevel filters slog records. debug/info/warn/error.
	LogLevel slog.Level
}

// Load reads env + flags (in that order, flags win) and returns a validated
// [Config]. It is safe to call once per process; do not call it from tests
// that mutate os.Args.
//
// Recognised env vars:
//
//	GL_TOKEN, GL_URL, GL_NAMESPACE   - GitLab
//	GITEA_TOKEN, GITEA_URL, GITEA_OWNER - Gitea
//	CORRAL_SYNC_BASE_DIR              - corral base dir
//	CORRAL_SYNC_WORKERS               - worker pool size
//	CORRAL_SYNC_LOG_LEVEL             - debug|info|warn|error
func Load(args []string) (Config, error) {
	fs := flag.NewFlagSet("corral-sync", flag.ContinueOnError)

	c := Config{
		BaseDir:         envOr("CORRAL_SYNC_BASE_DIR", defaultBaseDir()),
		GitLabToken:     os.Getenv("GL_TOKEN"),
		GitLabURL:       envOr("GL_URL", "https://gitlab.com"),
		GitLabNamespace: os.Getenv("GL_NAMESPACE"),
		GiteaToken:      os.Getenv("GITEA_TOKEN"),
		GiteaURL:        os.Getenv("GITEA_URL"),
		GiteaOwner:      os.Getenv("GITEA_OWNER"),
		Timeout:         parseDurationOr("CORRAL_SYNC_TIMEOUT", 5*time.Minute),
	}

	// Flags override env. Flag names deliberately use hyphenated form
	// (Go convention for CLI tools).
	fs.StringVar(&c.BaseDir, "base-dir", c.BaseDir, "root of the corral local mirror")
	fs.StringVar(&c.GitLabURL, "gitlab-url", c.GitLabURL, "GitLab base URL")
	fs.StringVar(&c.GitLabNamespace, "gitlab-namespace", c.GitLabNamespace, "GitLab namespace (empty = authenticated user)")
	fs.StringVar(&c.GiteaURL, "gitea-url", c.GiteaURL, "Gitea base URL (self-hosted)")
	fs.StringVar(&c.GiteaOwner, "gitea-owner", c.GiteaOwner, "Gitea owner (empty = authenticated user)")
	workers := fs.Int("workers", parseIntOr("CORRAL_SYNC_WORKERS", 4), "concurrent workers")
	fs.DurationVar(&c.Timeout, "timeout", c.Timeout, "maximum time per repository/provider operation")
	fs.BoolVar(&c.DryRun, "dry-run", false, "log actions without executing them")
	levelStr := fs.String("log-level", envOr("CORRAL_SYNC_LOG_LEVEL", "info"), "debug|info|warn|error")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	c.Workers = *workers
	if c.Workers < 1 || c.Workers > 64 {
		return Config{}, errors.New("workers must be between 1 and 64")
	}
	if c.Timeout <= 0 {
		return Config{}, errors.New("timeout must be greater than zero")
	}

	lvl, err := parseLevel(*levelStr)
	if err != nil {
		return Config{}, err
	}
	c.LogLevel = lvl

	// A base dir without a `.git` at the root is fine (it is a parent of
	// per-repo directories). But it must exist — otherwise every walk
	// step is a wasted syscall.
	if fi, err := statPath(c.BaseDir); err != nil {
		return Config{}, fmt.Errorf("base-dir %q: %w", c.BaseDir, err)
	} else if !fi.IsDir() {
		return Config{}, fmt.Errorf("base-dir %q is not a directory", c.BaseDir)
	}

	// At least one provider must be configured, otherwise the run is a
	// no-op. Fail loud instead of quietly succeeding on the first cron
	// invocation.
	if c.GitLabToken == "" && c.GiteaToken == "" {
		return Config{}, errors.New("neither GL_TOKEN nor GITEA_TOKEN is set — nothing to mirror")
	}
	if c.GiteaToken != "" && c.GiteaURL == "" {
		return Config{}, errors.New("GITEA_TOKEN set but GITEA_URL is empty")
	}
	if err := validateToken("GL_TOKEN", c.GitLabToken); err != nil {
		return Config{}, err
	}
	if err := validateToken("GITEA_TOKEN", c.GiteaToken); err != nil {
		return Config{}, err
	}
	if c.GitLabToken != "" {
		if err := validateProviderURL("GL_URL", c.GitLabURL); err != nil {
			return Config{}, err
		}
	}
	if c.GiteaToken != "" {
		if err := validateProviderURL("GITEA_URL", c.GiteaURL); err != nil {
			return Config{}, err
		}
	}
	if fs.NArg() != 0 {
		return Config{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	abs, err := absolutePath(c.BaseDir)
	if err != nil {
		return Config{}, fmt.Errorf("resolve base-dir: %w", err)
	}
	c.BaseDir = filepath.Clean(abs)

	return c, nil
}

func validateProviderURL(name, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%s must be an HTTPS origin without credentials, query, or fragment", name)
	}
	return nil
}

func validateToken(name, token string) error {
	if strings.ContainsAny(token, "\r\n") {
		return fmt.Errorf("%s contains invalid control characters", name)
	}
	return nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func parseIntOr(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n < 1 {
		return fallback
	}
	return n
}

func parseDurationOr(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

func parseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("unknown log level %q", s)
}

// defaultBaseDir returns ~/Code, matching the convention corral uses on the
// maintainer's own machine. If $HOME is unset (unusual in cron but possible)
// the caller must set --base-dir or CORRAL_SYNC_BASE_DIR explicitly.
func defaultBaseDir() string {
	home, err := userHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Code")
}
