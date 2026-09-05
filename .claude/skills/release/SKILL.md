---
name: release
description: Cut a rootcause-cli release so consuming projects can pull the latest `rc`. Use when asked to release, tag, ship, publish, or bump the version of rootcause-cli, or to make a new `rc` build available to consumers.
---

# Releasing rootcause-cli

```bash
scripts/release.sh patch          # fixes      (also: minor = additive, major = breaking, or vX.Y.Z)
scripts/release.sh patch --dry-run   # gates + plan, changes nothing
```

Commit the change on local `main` first and **do not push it** — publishing and verifying `main` is part
of the release. Pick the bump by what changed in the *command surface*, not by code size.

## Why the script, never by hand

A release is one transaction of six things landing together (tested SHA on `origin/main`, tag, GitHub
Release + GoReleaser binaries, Homebrew cask, Go module proxy, release-mirror objects — cloud-setup.sh, `latest`, checksums). Skip the main
push and GitHub trails the published binary; skip the proxy warmup and every consumer's
`go get …@latest` keeps resolving the **old pseudo-version** while looking successful.

[`scripts/release.sh`](../../../scripts/release.sh) performs and verifies all six; its header comment and
`step` lines are the authoritative sequence, and `internal/releasetest` guards it. It refuses a dirty
tree, a non-`main` branch, a checkout behind/diverged from origin, a version not newer than the highest
published tag, or a HEAD that moved during the gates.

## Gotchas

- **Lint is a blocking gate** (`golangci-lint run` with the repo `.golangci.yml`: standard linters plus the
  depguard/forbidigo layer contracts). CI runs the same config, so a red lint means the commit was never
  green — fix it, don't bypass it.
- **`@latest` lags `@vX.Y.Z`.** The proxy's `@latest`/version-list endpoints are cached for a few minutes
  after the explicit version already resolves. Normal — don't re-cut.
- **Homebrew is a cask, not a formula, on purpose.** Never re-add a `brews:` formula: a formula named `rc`
  shadows the cask on bare `brew install` and reintroduces the sandbox/PTY install failure. Reasoning and
  the tap wiring: [README](../../../README.md#releasing).

## Done looks like

```bash
go install github.com/rootcause-org/rootcause-cli/cmd/rc@vX.Y.Z
```
