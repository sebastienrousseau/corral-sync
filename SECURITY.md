# Security Policy

corral-sync is a Go CLI that reads a local repository tree and pushes to
external Git-hosting providers. We take security seriously and follow
industry best practices.

## Reporting a vulnerability

Report security issues through
[GitHub's private vulnerability reporting](https://github.com/sebastienrousseau/corral-sync/security/advisories/new).
Do not open a public issue.

You should receive an initial response within **48 hours** and a fix, or a
coordinated disclosure timeline, within **90 days**.

## Supported versions

Only the latest release on the `main` branch is supported.

## Security measures

- All commits are cryptographically signed (SSH ed25519).
- All CI actions pinned to immutable SHAs, not mutable tags.
- Gitleaks / Dependency Review / CodeQL run on every push and PR.
- Every commit carries a DCO sign-off (`git commit -s`) enforced in CI.
- Release binaries are cosign-signed and carry a SLSA v1.0 provenance
  attestation.
- No third-party code is vendored or embedded; stdlib only.
- The threat model and assurance case are documented in
  [docs/security-model.md](docs/security-model.md).
