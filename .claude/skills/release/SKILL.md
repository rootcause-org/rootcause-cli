---
name: release
description: Cut a rootcause-cli release so consuming projects can pull the latest version. Use when asked to release, tag, ship, publish, or bump the version of rootcause-cli, or to make a new `rc` build available to consumers. Runs scripts/release.sh (verify → tag → push → wait for binaries → warm the Go proxy).
---

# Releasing rootcause-cli

## Intent

A release here is **three things landing together**, not just a git tag:

1. **The git tag** `vX.Y.Z` (annotated, on `main`).
2. **The GitHub Release** with prebuilt binaries — built by [GoReleaser](https://goreleaser.com) via [`.github/workflows/release.yml`](../../../.github/workflows/release.yml) when the tag is pushed. This is what lets people install `rc` without Go.
3. **The Go module proxy** (`proxy.golang.org`) ingesting the tag — this is the step that's easy to forget. Until the proxy has it, a consuming project's `go get github.com/rootcause-org/rootcause-cli@latest` keeps resolving the **old pseudo-version**, so they silently *don't* get your release.

[`scripts/release.sh`](../../../scripts/release.sh) does and verifies all three. Always prefer it over running the steps by hand — the hand path is how step 3 gets skipped.

## How to release

```bash
scripts/release.sh patch         # 0.1.0 -> 0.1.1   (bug-fix pass, the usual)
scripts/release.sh minor         # 0.1.0 -> 0.2.0   (new command/flag)
scripts/release.sh major         # 0.1.0 -> 1.0.0   (breaking surface change)
scripts/release.sh v0.3.0        # or pin an explicit version
scripts/release.sh patch --dry-run   # run gates + print the plan, change nothing
```

Pick the bump by what changed in the surface (semver): patch = fixes, minor = additive, major = breaking.

The script **refuses to release** a dirty tree, a non-`main` branch, a checkout out of sync with
origin, or a version whose tag already exists — so a release is always reproducible from a known good
commit. It runs `go build/vet/test` as hard gates (lint is advisory — see below), tags + pushes, waits
for the GitHub Release assets to appear, then warms the proxy and prints the consumer install lines.

**Before running:** make sure the release-worthy change is already committed and pushed to `main`
(the script releases `HEAD`). Typical flow: land the fix → `scripts/release.sh patch`.

## What "done" looks like

```bash
go get   github.com/rootcause-org/rootcause-cli@vX.Y.Z          # resolves immediately
go install github.com/rootcause-org/rootcause-cli/cmd/rc@vX.Y.Z
```

`@latest` resolves to the new version too, but the proxy's `@latest`/version-list endpoints are cached
and can lag a few minutes after the explicit `@vX.Y.Z` already works. That lag is normal; don't re-cut.

## Notes / gotchas

- **Lint is advisory, not a gate.** The render layer intentionally ignores write-to-buffer errors, so
  `golangci-lint` reports pre-existing `errcheck` findings. The script prints them but never blocks on
  them. Don't "fix" those as part of a release.
- **Homebrew is wired up — as a macOS *cask*.** Each release GoReleaser pushes `Casks/rc.rb` to
  `rootcause-org/homebrew-tap` (auth: `HOMEBREW_TAP_GITHUB_TOKEN` secret), so `brew install
  rootcause-org/tap/rc` just works on macOS. It's a cask, **not** a formula, on purpose: a source
  formula installs through a Homebrew sandbox + Ruby PTY that fails on some macOS setups (`can't get
  Master/Slave device`); a cask links the prebuilt binary instead. Don't re-add a `brews:` formula —
  a formula named `rc` would shadow the cask on bare `brew install` and reintroduce the bug. Linux/WSL
  and Windows use [`scripts/install.sh`](../../../scripts/install.sh) /
  [`scripts/install.ps1`](../../../scripts/install.ps1); `go install` works everywhere.
- **Manual fallback** (if `scripts/release.sh` is unavailable) — do all three steps, especially #3:
  ```bash
  git tag -a vX.Y.Z -m "rootcause-cli vX.Y.Z" && git push origin vX.Y.Z
  gh run watch $(gh run list --workflow=release.yml --limit 1 --json databaseId --jq '.[0].databaseId')
  GOPROXY=https://proxy.golang.org go list -m github.com/rootcause-org/rootcause-cli@vX.Y.Z   # warm the proxy
  ```
