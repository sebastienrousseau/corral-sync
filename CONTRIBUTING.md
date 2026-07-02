# Contributing to corral-sync

corral-sync is a small Go CLI. Contributions are welcome.

## Getting started

1. Fork and clone.
2. Install: **Go 1.26+**, **Make**, **Git**.
3. `git config core.hooksPath .githooks` (optional).
4. `git checkout -b feat/my-change`.
5. Make changes.
6. `make test && make build`.
7. Commit, push, open a PR.

## Commits

**Sign your commits cryptographically and add a DCO sign-off trailer.** Both
are required — signing proves who authored the commit; the DCO sign-off
asserts you have the right to contribute the change under the project licence.

### Cryptographic signing

```bash
git config --global commit.gpgsign true
```

Set up an SSH or GPG signing key per
[GitHub's guide](https://docs.github.com/en/authentication/managing-commit-signature-verification/signing-commits).

### Developer Certificate of Origin (DCO)

Every commit must include a `Signed-off-by:` trailer matching its author. The
full text of the DCO is at <https://developercertificate.org>. Enforced by
the DCO workflow on every PR.

```bash
git commit -s -m "your message"          # sign off a new commit
git commit --amend --signoff             # add sign-off to the last commit
git rebase --signoff <base-sha>          # retroactively sign off a range
```

### Messages

Use imperative present tense: "Add dry-run flag", not "Added dry-run flag."

## Pull request checklist

- [ ] `make test` passes
- [ ] `make build` succeeds
- [ ] README updated if behaviour changed
- [ ] All commits are signed (`git log --show-signature`)
- [ ] All commits carry a DCO sign-off (`git commit -s`)

## Code style

- Standard Go formatting (`gofmt -w .`).
- Every exported symbol documented.
- Follow idiomatic Go.
