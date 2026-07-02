# Maintainers

corral-sync is currently maintained by a single person. This document
records the current maintainer, the external services and accounts
that are load-bearing for the project, and the procedure for
succession or fork.

## Current maintainer

| Role       | Name               | Contact                            | Time zone      |
|------------|--------------------|------------------------------------|----------------|
| Maintainer | Sebastien Rousseau | <sebastian.rousseau@gmail.com>     | Europe/London  |
|            |                    | GitHub: [@sebastienrousseau](https://github.com/sebastienrousseau) |                |

Best-effort response windows:

- **Security advisories** (private vulnerability reporting):
  first response within **48 hours**.
- **Pull requests**: first review within **7 days**.
- **Issues**: triaged within **7 days**.

## External services and accounts

| # | Service                          | Account / Location                                | Purpose                                    | Configuration reference                     |
|---|----------------------------------|---------------------------------------------------|--------------------------------------------|---------------------------------------------|
| 1 | GitHub repository                | `github.com/sebastienrousseau/corral-sync`        | Source of truth for code, issues, releases | this repository                             |
| 2 | GitHub Actions                   | Same repo                                         | CI + release + SLSA provenance             | `.github/workflows/`                        |
| 3 | GitHub Container Registry (ghcr) | `ghcr.io/sebastienrousseau/corral-sync`           | Multi-arch OCI images                      | `.goreleaser.yaml`                          |
| 4 | Signing key (SSH ed25519)        | `SHA256:kIOPAavp1TCEauTr1tTIN3cv+tSs6F9m/4lZjuM9tqk` | Signs release tags and commits          | `.github/workflows/release.yml`             |
| 5 | Sigstore keyless signing         | Fulcio + Rekor (via GitHub OIDC)                  | Cosigns every release artefact             | `.goreleaser.yaml` (signs blocks)           |
| 6 | Dependabot / Scorecard           | GitHub-native, tied to the repo                   | Vulnerability alerts, OpenSSF score        | `.github/dependabot.yml`                    |

## Succession procedure

The single-maintainer model creates real bus-factor risk. This is the
concrete plan for handing over.

### Voluntary hand-off (planned)

1. **Announce**: open a public issue at least two weeks before the change.
2. **Add the new maintainer as a repository owner** (or transfer to an org).
3. **Update `MAINTAINERS.md`** with the new contact + response windows.
4. **Rotate signing key** in a coordinated release: outgoing publishes
   revocation note, incoming publishes new fingerprint in
   `GOVERNANCE.md`.
5. **Transfer ghcr.io namespace** by rewriting `.goreleaser.yaml` (new
   releases publish under the new owner; prior releases stay under the
   old namespace).

### Community fork (unplanned)

If the maintainer becomes unresponsive for **≥ 6 months**, the
community is explicitly encouraged to fork. GPL-3.0-only permits it.
A community fork:

- May keep the name **only after** the old repo is archived by the
  owner; otherwise it adopts a distinguishing name.
- Publishes its own signing key fingerprint in its `GOVERNANCE.md`.
- Reconstitutes external services under its own accounts.

### Emergency (compromise or coercion)

1. Publish a security advisory naming the last known-good release tag.
2. Rotate the SSH signing key.
3. Revoke the compromised GitHub PAT / OIDC subject.
4. Rekor-recorded Sigstore signatures remain publicly verifiable for
   pre-compromise releases.

## Contact

Non-security: open an issue. Security-sensitive: follow
[SECURITY.md](SECURITY.md).
