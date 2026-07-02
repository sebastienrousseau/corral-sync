# corral-sync

Mirror a [corral](https://github.com/sebastienrousseau/corral)-organised
local repository tree to **GitLab** and **Gitea** with absolute-parity
semantics (`git push --prune`), driven by a bounded worker pool.

Companion to `corral`. Runs after it:

```
corralctl ...      →  local ~/Code/{Public,Private}/{lang}/{repo}
corral-sync ...    →  local  →  GitLab + Gitea
```

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
- **Parity via prune** — every push is `--prune --all` then `--prune
  --tags`, so branches/tags that disappeared locally disappear on the
  remote.
- **Concurrency** — worker pool over repos; providers run sequentially
  per repo to avoid `.git/` contention.
- **Non-interactive git env** — `GIT_TERMINAL_PROMPT=0`, `GIT_ASKPASS=echo`,
  `GCM_INTERACTIVE=Never`; a cron run without a credential helper fails
  loudly instead of hanging.

## Deployment

See [DEPLOYMENT.md](DEPLOYMENT.md) for compile + Keychain + crontab.

## License

GPL-3.0-only. See LICENSE.
