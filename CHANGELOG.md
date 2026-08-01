# Changelog

All notable changes to this project are documented here.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.0.4] — 2026-08-01

### Fixed

- Homebrew cask updates now open pull requests against the protected tap, so
  package publication cannot bypass repository policy or fail the release.

## [0.0.3] — 2026-08-01

Security hardening, mirror correctness, and complete unit coverage.

### Added

- Homebrew publishing and direct mise installation through GitHub releases.
- A `--version` command for package-manager and installation verification.
- Bounded HTTP bodies, git output, repository discovery, worker concurrency,
  and per-operation timeouts throughout the mirror pipeline.
- Full tests across configuration, crawler, providers, git operations,
  orchestration, and the entry point, bringing statement coverage to 100%.

### Fixed

- Existing Gitea repositories are reused correctly, while fatal provider
  failures stop dependent work instead of cascading misleading errors.
- Tag synchronization now force-updates divergent tags, prunes removed tags,
  and bypasses local pre-push hooks during automated mirrors.
- API redirects, credential-bearing clone URLs, insecure origins, visibility
  mismatches, duplicate names, option-like git arguments, and symlink escapes
  are rejected before mutation.

### Security

- Release tags must be semantic versions on `main`, and source tests must pass
  before GoReleaser publishes signed, attested artifacts.

## [0.0.2] — 2026-07-05

### Fixed

- **GitLab project creation** was rejected with
  `namespace: is not valid` whenever no explicit namespace was
  configured. Root cause: we set `namespace_id` to the value returned
  by `/api/v4/user`.`id`, but the personal-namespace ID is distinct
  from the user ID on modern GitLab.com. Fix: omit `namespace_id`
  from the create payload when no namespace was pinned; GitLab
  defaults to the authenticated user's personal namespace.
- **Empty local repositories** (unborn HEAD — created on GitHub but
  never pushed to) no longer surface as `git push` errors. corral-sync
  now performs the same `git rev-parse --verify -q HEAD^{commit}`
  probe that corral uses on the pull side, and SKIPs those repos with
  an INFO log ("skipping empty repo (no commits yet)").

## [0.0.1] — 2026-07-02

### Added

- Initial import of `corral-sync`: a Go CLI that mirrors a corral-organised
  local repository tree out to GitLab and Gitea with absolute-parity
  semantics (`git push --prune --all` + `git push --prune --tags`).
- **Providers**: GitLab REST v4 client (`internal/gitlab`) and Gitea REST
  v1 client (`internal/gitea`), both stdlib-only, both idempotent on
  "already exists" (GitLab 400 with `"has already been taken"` or 409,
  Gitea 409 Conflict).
- **Provider abstraction** at `internal/remote` — adding Codeberg /
  self-hosted BitBucket later is a single new file implementing the
  `Provider` interface.
- **Crawler** at `internal/crawler` — walks the corral base dir, finds
  `.git` roots, deduces visibility from `/Public/` vs `/Private/`
  segments (defaults to Private for safety).
- **Concurrent orchestrator** at `internal/orchestrator` — bounded
  worker pool over repositories; providers run sequentially per repo to
  avoid `.git/` contention.
- **Non-interactive git env** — `GIT_TERMINAL_PROMPT=0`, `GIT_ASKPASS=echo`,
  `GCM_INTERACTIVE=Never` on every `git` invocation so cron runs fail
  loudly instead of stalling.
- **Structured logging** via `log/slog` JSON handler on stderr, level
  configurable via `--log-level` or `CORRAL_SYNC_LOG_LEVEL`.
- **`--dry-run`** flag that logs every intended action without touching
  the network or the local git state.
- **Governance**: `LICENSE` (GPL-3.0-only), `README.md`, `DEPLOYMENT.md`,
  `CONTRIBUTING.md` (with DCO requirements), `CODE_OF_CONDUCT.md`
  (Contributor Covenant 2.1), `GOVERNANCE.md`, `MAINTAINERS.md`,
  `SECURITY.md`, `docs/security-model.md` (formal assurance case).
- **CI**: Ubuntu + macOS + Windows test matrix, CodeQL, gosec,
  Dependency Review, DCO check, OpenSSF Scorecard.
- **Release pipeline**: GoReleaser producing multi-arch binaries + OCI
  images to `ghcr.io/sebastienrousseau/corral-sync`, cosign keyless
  signing, SLSA v1.0 provenance via `actions/attest-build-provenance`.

[Unreleased]: https://github.com/sebastienrousseau/corral-sync/compare/v0.0.4...HEAD
[0.0.4]: https://github.com/sebastienrousseau/corral-sync/compare/v0.0.3...v0.0.4
[0.0.3]: https://github.com/sebastienrousseau/corral-sync/compare/v0.0.2...v0.0.3
[0.0.2]: https://github.com/sebastienrousseau/corral-sync/compare/v0.0.1...v0.0.2
