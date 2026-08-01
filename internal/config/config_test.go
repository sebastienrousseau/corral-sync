// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: GPL-3.0-only

package config

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func cleanEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"CORRAL_SYNC_BASE_DIR", "CORRAL_SYNC_WORKERS", "CORRAL_SYNC_LOG_LEVEL", "CORRAL_SYNC_TIMEOUT",
		"GL_TOKEN", "GL_URL", "GL_NAMESPACE", "GITEA_TOKEN", "GITEA_URL", "GITEA_OWNER",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadValidConfigurationAndOverrides(t *testing.T) {
	cleanEnv(t)
	base := t.TempDir()
	t.Setenv("CORRAL_SYNC_BASE_DIR", base)
	t.Setenv("GL_TOKEN", "token")
	t.Setenv("CORRAL_SYNC_WORKERS", "3")
	t.Setenv("CORRAL_SYNC_TIMEOUT", "2m")
	t.Setenv("CORRAL_SYNC_LOG_LEVEL", "debug")
	cfg, err := Load([]string{"--workers", "2", "--timeout", "1m", "--log-level", "warning", "--dry-run"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Workers != 2 || cfg.Timeout != time.Minute || cfg.LogLevel != slog.LevelWarn || !cfg.DryRun || !filepath.IsAbs(cfg.BaseDir) {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestLoadValidationFailures(t *testing.T) {
	base := t.TempDir()
	cases := []struct {
		name string
		args []string
		env  map[string]string
	}{
		{"flag", []string{"--unknown"}, map[string]string{"GL_TOKEN": "x"}},
		{"workers-low", []string{"--workers", "0"}, map[string]string{"GL_TOKEN": "x"}},
		{"workers-high", []string{"--workers", "65"}, map[string]string{"GL_TOKEN": "x"}},
		{"timeout", []string{"--timeout", "0s"}, map[string]string{"GL_TOKEN": "x"}},
		{"level", []string{"--log-level", "trace"}, map[string]string{"GL_TOKEN": "x"}},
		{"providers", nil, nil},
		{"gitea-url-missing", nil, map[string]string{"GITEA_TOKEN": "x"}},
		{"gitlab-token-control", nil, map[string]string{"GL_TOKEN": "x\nheader"}},
		{"gitea-token-control", nil, map[string]string{"GITEA_TOKEN": "x\rheader", "GITEA_URL": "https://gitea.test"}},
		{"gitlab-http", nil, map[string]string{"GL_TOKEN": "x", "GL_URL": "http://gitlab.test"}},
		{"gitea-userinfo", nil, map[string]string{"GITEA_TOKEN": "x", "GITEA_URL": "https://u:p@gitea.test"}},
		{"positionals", []string{"extra"}, map[string]string{"GL_TOKEN": "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cleanEnv(t)
			t.Setenv("CORRAL_SYNC_BASE_DIR", base)
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			if _, err := Load(tc.args); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestLoadPathFailures(t *testing.T) {
	cleanEnv(t)
	t.Setenv("GL_TOKEN", "x")
	t.Setenv("CORRAL_SYNC_BASE_DIR", filepath.Join(t.TempDir(), "missing"))
	if _, err := Load(nil); err == nil || !strings.Contains(err.Error(), "base-dir") {
		t.Fatalf("missing path error = %v", err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CORRAL_SYNC_BASE_DIR", file)
	if _, err := Load(nil); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("file path error = %v", err)
	}

	oldAbs := absolutePath
	t.Cleanup(func() { absolutePath = oldAbs })
	absolutePath = func(string) (string, error) { return "", errors.New("abs failed") }
	t.Setenv("CORRAL_SYNC_BASE_DIR", t.TempDir())
	if _, err := Load(nil); err == nil || !strings.Contains(err.Error(), "resolve base-dir") {
		t.Fatalf("absolute path error = %v", err)
	}
}

func TestParsingHelpers(t *testing.T) {
	t.Setenv("VALUE", "  set  ")
	if envOr("VALUE", "fallback") != "set" || envOr("MISSING", "fallback") != "fallback" {
		t.Fatal("envOr failed")
	}
	for value, want := range map[string]int{"": 4, "bad": 4, "0": 4, "7": 7} {
		t.Setenv("INT", value)
		if got := parseIntOr("INT", 4); got != want {
			t.Fatalf("parseIntOr(%q) = %d", value, got)
		}
	}
	for value, want := range map[string]time.Duration{"": time.Second, "bad": time.Second, "0s": time.Second, "2s": 2 * time.Second} {
		t.Setenv("DURATION", value)
		if got := parseDurationOr("DURATION", time.Second); got != want {
			t.Fatalf("parseDurationOr(%q) = %v", value, got)
		}
	}
	for value, want := range map[string]slog.Level{"debug": slog.LevelDebug, "INFO": slog.LevelInfo, "": slog.LevelInfo, "warn": slog.LevelWarn, "warning": slog.LevelWarn, "error": slog.LevelError} {
		if got, err := parseLevel(value); err != nil || got != want {
			t.Fatalf("parseLevel(%q) = %v, %v", value, got, err)
		}
	}
}

func TestDefaultBaseDir(t *testing.T) {
	oldHome := userHomeDir
	t.Cleanup(func() { userHomeDir = oldHome })
	userHomeDir = func() (string, error) { return "/home/test", nil }
	if got := defaultBaseDir(); got != filepath.Join("/home/test", "Code") {
		t.Fatalf("default base = %q", got)
	}
	userHomeDir = func() (string, error) { return "", errors.New("no home") }
	if got := defaultBaseDir(); got != "" {
		t.Fatalf("failed home base = %q", got)
	}
}
