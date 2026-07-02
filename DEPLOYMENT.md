# corral-sync — deployment & automation guide

Concise, step-by-step. Prerequisites: Go 1.26+, a GitLab account, and a Gitea
instance (self-hosted).

## 1. Compile

Static, stripped binary — no debug symbols, small download when you eventually
package it:

```bash
cd ~/Code/Public/go/corral-sync
go build -ldflags="-s -w -X main.Version=$(git describe --tags --always 2>/dev/null || echo dev)" \
  -o /usr/local/bin/corral-sync .
```

`-s -w` strips the symbol table and DWARF info (~40 % smaller). `-X main.Version`
injects a version string readable from logs.

Cross-compile for a headless Linux host:

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  go build -ldflags="-s -w" -o corral-sync-linux-amd64 .
```

## 2. Provision tokens

### GitLab

1. Log in → **User settings → Access tokens**.
2. Create a token named `corral-sync`, scope `api` (required — `read_api` is not
   enough to create projects), expiry 12 months.
3. Copy it once — it is not shown again.

### Gitea

1. Log in → **Settings → Applications → Manage Access Tokens**.
2. Create a token named `corral-sync`.
3. Select scopes: `write:repository` and `read:user`.
4. Copy it once.

## 3. Store the tokens securely

**Do not commit tokens to shell rc files.** Keep them in the macOS Keychain (or
GNOME Keyring on Linux) and load them into the cron environment via a wrapper.

### macOS keychain

```bash
security add-generic-password -a "$USER" -s corral-sync-gl-token   -w '<paste GitLab token>'
security add-generic-password -a "$USER" -s corral-sync-gitea-token -w '<paste Gitea token>'
```

Read back on demand:

```bash
security find-generic-password -a "$USER" -s corral-sync-gl-token -w
```

## 4. Wrapper script

Cron cannot read the keychain by itself. Wrap the invocation so tokens are
loaded from the keychain at run time, then unset before the process ends.

Save as `~/bin/corral-sync-cron`:

```bash
#!/usr/bin/env bash
set -euo pipefail

# Load tokens from the macOS Keychain. If either lookup fails, the
# corresponding provider is disabled by corral-sync (empty token = skip).
export GL_TOKEN="$(security find-generic-password -a "$USER" -s corral-sync-gl-token   -w 2>/dev/null || true)"
export GITEA_TOKEN="$(security find-generic-password -a "$USER" -s corral-sync-gitea-token -w 2>/dev/null || true)"

export GITEA_URL="https://gitea.example.com"      # <-- your Gitea root
# GL_URL defaults to https://gitlab.com; override for self-hosted:
# export GL_URL="https://gitlab.example.com"

exec /usr/local/bin/corral-sync \
  --base-dir "$HOME/Code" \
  --workers 4 \
  --log-level info
```

```bash
chmod +x ~/bin/corral-sync-cron
```

Test manually first with `--dry-run`:

```bash
~/bin/corral-sync-cron --dry-run   # (add --dry-run to the exec line temporarily)
```

## 5. Crontab entry

The daily flow: pull GitHub → local, then push local → GitLab + Gitea. Chain
them with `&&` so the second stage only runs if the first succeeds; route both
stdout and stderr to a rotating log.

```
# Every day at 05:15 local time.
15 5 * * *  /usr/local/bin/corralctl --yes --owner sebastienrousseau ~/Code >> ~/Library/Logs/corral.log 2>&1 \
              && $HOME/bin/corral-sync-cron >> ~/Library/Logs/corral-sync.log 2>&1
```

Install with:

```bash
crontab -e
```

### Log rotation (macOS `newsyslog`)

Add `/etc/newsyslog.d/corral.conf`:

```
# logfilename                          [owner:group]   mode  count  size  when  flags
/Users/<you>/Library/Logs/corral.log       -           644   7      10240 *     JN
/Users/<you>/Library/Logs/corral-sync.log  -           644   7      10240 *     JN
```

Rotates when a file exceeds 10 MB or once a day, keeps 7 archives.

## 6. Verify the first live run

Watch the logs live from another terminal:

```bash
tail -F ~/Library/Logs/corral-sync.log | jq -r '"\(.time) \(.level) \(.msg) \(.provider // "") \(.repo // "") \(.err // "")"'
```

Expected first-run output — one `created project` or `created repo` line per
new destination, and one `mirrored` line per repo per provider.

On subsequent runs the create step is a no-op; you'll see `repo exists,
reusing` at debug level and one `mirrored` line per provider.

## 7. Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `neither GL_TOKEN nor GITEA_TOKEN is set` | Keychain lookup returned empty | Confirm the keychain entries with `security find-generic-password`. |
| `GET user returned 401` | Token expired or wrong scope | Recreate token with `api` scope (GitLab) or `write:repository` (Gitea). |
| `git push … failed to push some refs` | GitLab/Gitea has commits not in local | Investigate: manual `git pull --rebase gitlab main` from the working tree, or accept divergence. |
| `Permission denied (publickey)` | ssh-agent unavailable to cron | Preload keys before the cron fires, or switch the clone URLs to HTTPS + credential helper. |

## 8. Uninstall

```bash
crontab -l | grep -v corral-sync | crontab -
security delete-generic-password -a "$USER" -s corral-sync-gl-token
security delete-generic-password -a "$USER" -s corral-sync-gitea-token
rm /usr/local/bin/corral-sync ~/bin/corral-sync-cron
```
