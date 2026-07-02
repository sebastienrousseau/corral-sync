# corral-sync — Security Model & Assurance Case

**Status:** Living document. Initial version: 2026-07-02.
**Owner:** Sebastien Rousseau ([@sebastienrousseau](https://github.com/sebastienrousseau)).
**Scope:** the `corral-sync` binary, the release pipeline that produces it,
and the local + remote state it manages.

## 1. What corral-sync is

corral-sync is a Go CLI that mirrors a corral-organised local repository
tree out to GitLab and Gitea:

- **Read** the local filesystem (finds `.git` roots, deduces visibility).
- **Read + write** the GitLab REST API v4 and Gitea REST API v1 (creates
  repos if missing).
- **Read + write** each local `.git` (adds a remote if missing, runs
  `git push --prune --all` and `git push --prune --tags`).

It is not a service. It is a batch tool invoked from cron.

## 2. Trust boundaries

| # | Boundary                              | Direction    | What crosses it                              |
|---|---------------------------------------|--------------|----------------------------------------------|
| 1 | Shell → `corral-sync` process         | in           | flags, env (`GL_TOKEN`, `GITEA_TOKEN`, URLs) |
| 2 | `corral-sync` → GitLab API            | out          | `PRIVATE-TOKEN` header on HTTPS to `/api/v4` |
| 3 | `corral-sync` → Gitea API             | out          | `Authorization: token` on HTTPS to `/api/v1` |
| 4 | `corral-sync` → local `.git`          | out          | `git` invocations under `.git/`              |
| 5 | `git push` → remote (SSH or HTTPS)    | out          | delegated to system `git` + `ssh-agent`      |

## 3. Security properties (claims)

### C1. Tokens are only used for their intended provider

**Argument.** `GL_TOKEN` is only ever placed into a `PRIVATE-TOKEN` header
directed at the configured `GL_URL` (defaulting to `https://gitlab.com`).
`GITEA_TOKEN` is only ever placed into an `Authorization: token …` header
directed at `GITEA_URL`. No cross-provider header, no cross-provider URL.

**Evidence.** `internal/gitlab/gitlab.go` and `internal/gitea/gitea.go`
are the only files that touch either token; each has one `http.Client`
scoped to its own `baseURL`.

### C2. corral-sync cannot force a git push

**Argument.** The `git push` commands are literally
`git push --prune --all <remote>` and `git push --prune --tags <remote>` —
no `--force`, no `--force-with-lease`. If the remote has commits the local
mirror doesn't, git refuses the push and corral-sync surfaces the error.

**Evidence.** `internal/gitops/gitops.go` contains every `git push`
invocation; there is no `--force` anywhere in the codebase.

### C3. corral-sync fails closed on missing tokens

**Argument.** Config loading rejects a run where neither `GL_TOKEN` nor
`GITEA_TOKEN` is set. Empty tokens are treated as "provider disabled",
not as anonymous access — so a forgotten env var never accidentally
targets an unauthenticated endpoint.

**Evidence.** `internal/config/config.go` `Load()` returns
`"neither GL_TOKEN nor GITEA_TOKEN is set — nothing to mirror"` when
both are empty.

### C4. Cron invocations cannot hang on credential prompts

**Argument.** Every `git` invocation runs with an environment overlay
that disables interactive prompting: `GIT_TERMINAL_PROMPT=0`,
`GIT_ASKPASS=echo`, `GCM_INTERACTIVE=Never`. A missing SSH key or expired
credential helper fails loudly instead of stalling.

**Evidence.** `internal/gitops/gitops.go` `nonInteractiveEnv`.

### C5. The release artefacts you download are the artefacts we built

**Argument.** Every release is:

1. Built by a GitHub-hosted runner from a signed tag on `main`.
2. Signed keylessly with cosign (Sigstore/Fulcio/Rekor).
3. Accompanied by a SLSA v1.0 provenance attestation from
   `actions/attest-build-provenance`.

**Evidence.** `.github/workflows/release.yml` and `.goreleaser.yaml`.

## 4. Threats considered and out of scope

### In scope

- **Token leakage in logs**: tokens are never logged; only presence is.
- **Malicious API response**: JSON decoding into typed structs; the
  decoder rejects unknown top-level types. A malicious `ssh_url_to_repo`
  is limited to whatever `git remote add` accepts as a URL.
- **Supply chain against release**: SHA-pinned actions, cosign, SLSA.
- **Dependency compromise**: stdlib only — zero third-party runtime
  dependencies to compromise.

### Out of scope

- **Compromise of the maintainer's laptop or GitHub account.** Standard
  root-of-trust caveat. Sigstore Rekor entries make any malicious
  release publicly auditable after the fact but do not prevent it.
- **Malicious GitLab/Gitea server**: if the destination server is
  hostile, corral-sync will happily push your code to it. Users are
  expected to point `GL_URL` and `GITEA_URL` at trusted instances.
- **Simultaneous concurrent runs**: two corral-sync processes racing
  against the same `.git/` may collide. Cron users should stagger
  invocations or wrap in `flock`.

## 5. Assumptions

- The user's `GL_TOKEN` has scope `api`; `GITEA_TOKEN` has scope
  `write:repository`.
- `git` ≥ 2.20 is on `PATH`.
- The user's clock is roughly correct (for TLS + Rekor timestamps).
- The local `.git/` tree is trusted; corral-sync inherits its state.

## 6. Compensating controls (bus factor + solo maintainer)

corral-sync has a single maintainer. Mitigations:

- **Public assurance case (this doc).**
- **Documented signing keys** in `GOVERNANCE.md`.
- **Documented external services** in `MAINTAINERS.md`.
- **Fork-and-continue is explicit** under GPL-3.0-only with a 6-month
  unresponsive-maintainer clause.

## 7. Review and update

Re-reviewed on every release that touches:

- Any file in `internal/gitlab`, `internal/gitea`, `internal/gitops`.
- Any file in `.github/workflows/`.
- Any new outbound-network dependency (there should be none — stdlib
  only is a claim we intend to hold).

Otherwise re-reviewed annually.
