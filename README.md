# corral-sync

> **Superseded by `corralctl sync`.** corral-sync has been folded into
> [corralctl](https://github.com/sebastienrousseau/corralctl), which mirrors
> to GitHub, GitLab, Gitea, Forgejo, Codeberg and Bitbucket from the same
> binary that clones. v0.0.5 is the last standalone feature release; it keeps
> working, but new capability lands in `corralctl sync`. See
> [Migrating to corralctl sync](#migrating-to-corralctl-sync).

Mirror a [corral](https://github.com/sebastienrousseau/corralctl)-organised
local repository tree to **GitLab** and **Gitea** with absolute-parity
semantics (`git push --prune`), driven by a bounded worker pool.

Companion to `corral`. Runs after it:

```
corralctl ...      →  local ~/Code/{Public,Private}/{lang}/{repo}
corral-sync ...    →  local  →  GitLab + Gitea
```

## Install

### mise (macOS / Linux)

```bash
mise use -g github:sebastienrousseau/corral-sync
```

### Homebrew (macOS / Linux)

```bash
brew install sebastienrousseau/tap/corral-sync
```

### Build from source

Requires Go 1.26+ and Git:

```bash
git clone https://github.com/sebastienrousseau/corral-sync.git
cd corral-sync
make install            # installs ~/.local/bin/corral-sync
```

Verify the installed binary with `corral-sync --version`.

## Quick start

```bash
go build -ldflags="-s -w" -o corral-sync .

export GL_TOKEN=<gitlab pat, scope: api>
export GITEA_TOKEN=<gitea pat, scope: write:repository>
export GITEA_URL=https://gitea.example.com

./corral-sync --base-dir ~/Code --workers 4 --dry-run
./corral-sync --base-dir ~/Code --workers 4
```

Structured JSON logs go to stderr. Non-zero exit if any repo failed —
cron will email you on failure.

## Flags & environment

| Flag | Env | Default | Purpose |
|---|---|---|---|
| `--base-dir` | `CORRAL_SYNC_BASE_DIR` | `~/Code` | root of the corral mirror |
| `--gitlab-url` | `GL_URL` | `https://gitlab.com` | GitLab base URL |
| `--gitlab-namespace` | `GL_NAMESPACE` | (token owner) | namespace to create projects under |
| `--gitea-url` | `GITEA_URL` | — | Gitea base URL (required if `GITEA_TOKEN` set) |
| `--gitea-owner` | `GITEA_OWNER` | (token owner) | Gitea user or org |
| `--workers` | `CORRAL_SYNC_WORKERS` | `4` | concurrent repos |
| `--timeout` | `CORRAL_SYNC_TIMEOUT` | `5m` | maximum time per repository/provider operation |
| `--dry-run` | — | `false` | log actions, do nothing |
| `--log-level` | `CORRAL_SYNC_LOG_LEVEL` | `info` | debug/info/warn/error |
| — | `GL_TOKEN` | — | GitLab PAT (empty = disable GitLab) |
| — | `GITEA_TOKEN` | — | Gitea PAT (empty = disable Gitea) |

## Design

- **Provider abstraction** (`internal/remote`) — orchestrator sees a
  neutral `Provider` interface; adding Codeberg / self-hosted BitBucket
  later is a single file.
- **Idempotence** — `EnsureRepo` returns success when the repo already
  exists (GitLab 400 with "has already been taken" or 409, Gitea 409).
  `git remote add` becomes `git remote set-url` if the URL drifts.
- **Parity via prune** — one push per repository per provider,
  `--prune <remote> refs/heads/*:refs/heads/* +refs/tags/*:refs/tags/*`, so
  branches and tags that disappeared locally disappear on the remote.
  Branches are never forced; tags follow the local namespace.
- **Never onto its own forge** — a repository whose `origin` is on a
  provider's host is skipped for that provider. corralctl clones from six
  forges, and pruning a clone against its own upstream is not a mirror.
- **Concurrency** — worker pool over repos; providers run sequentially
  per repo to avoid `.git/` contention.
- **Non-interactive git env** — `GIT_TERMINAL_PROMPT=0`, `GIT_ASKPASS=echo`,
  `GCM_INTERACTIVE=Never`; a cron run without a credential helper fails
  loudly instead of hanging.
- **Fail-closed destinations** — provider API origins must use HTTPS;
  redirects, credential-bearing clone URLs, visibility mismatches, and
  duplicate repository names are rejected before any push.
- **Bounded operations** — API responses, HTTP requests, git commands, and
  worker concurrency all have explicit resource limits.
- **Continuous assurance** — race tests, vulnerability scanning, native Go
  fuzzing, and a 100% statement-coverage gate run in CI.

## Deployment

See [DEPLOYMENT.md](DEPLOYMENT.md) for compile + Keychain + crontab.

## Migrating to corralctl sync

`corralctl sync` does what corral-sync does, for six forges, with the
credentials `corralctl clone` already uses. The remote names `gitlab` and
`gitea` are the same, so clones corral-sync has already configured carry
over without a change.

```bash
brew install sebastienrousseau/tap/corralctl     # or mise, the AUR, go install

export GITLAB_TOKEN="$GL_TOKEN"
export GITEA_TOKEN                                 # unchanged
corralctl sync --to gitlab --to gitea@https://gitea.example.com --dry-run
corralctl sync --to gitlab --to gitea@https://gitea.example.com --protocol ssh
```

| corral-sync | corralctl sync |
|---|---|
| `GL_TOKEN` | `GITLAB_TOKEN` (or `CORRAL_GITLAB_TOKEN`) |
| `GL_URL=https://gl.example.com` | `--to gitlab@https://gl.example.com` |
| `GL_NAMESPACE=group` | `--to gitlab:group` |
| `GITEA_TOKEN` | `GITEA_TOKEN` (unchanged) |
| `GITEA_URL=https://…` | `--to gitea@https://…` |
| `GITEA_OWNER=org` | `--to gitea:org@https://…` |
| `--base-dir`, `CORRAL_SYNC_BASE_DIR` | `--base-dir` |
| `--workers`, `CORRAL_SYNC_WORKERS` | `--concurrency` |
| `--timeout`, `CORRAL_SYNC_TIMEOUT` | `--timeout` |
| `--log-level`, `CORRAL_SYNC_LOG_LEVEL` | `--log-level` |
| `--dry-run` | `--dry-run` |
| pushes over SSH | HTTPS with the token by default; `--protocol ssh` for keys |

The crontab entry in [DEPLOYMENT.md](DEPLOYMENT.md) becomes a single binary
doing both halves:

```text
15 5 * * *  corralctl clone --yes sebastienrousseau ~/Code >> ~/Library/Logs/corral.log 2>&1 \
              && corralctl sync --to gitlab --to gitea@https://gitea.example.com >> ~/Library/Logs/corral-sync.log 2>&1
```

## License

Licensed under either of

- Apache License, Version 2.0 ([LICENSE-APACHE](LICENSE-APACHE))
- MIT license ([LICENSE-MIT](LICENSE-MIT))

at your option. Unless you explicitly state otherwise, any contribution
intentionally submitted for inclusion in the work by you, as defined in the
Apache-2.0 license, shall be dual licensed as above, without any additional
terms or conditions.
