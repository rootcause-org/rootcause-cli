---
name: release
description: Cut a rootcause-cli release so consuming projects can pull the latest version. Use when asked to release, tag, ship, publish, or bump the version of rootcause-cli, or to make a new `rc` build available to consumers. Runs scripts/release.sh (verify → push and verify main → tag → wait for binaries → warm the Go proxy).
---

# Releasing rootcause-cli

## Intent

A release here is **five things landing together**, not just a git tag:

1. **The exact tested `HEAD` on `origin/main`**, pushed and read back before tagging so GitHub never trails a published binary.
2. **The git tag** `vX.Y.Z` (annotated at that exact commit).
3. **The GitHub Release** with prebuilt binaries — built by [GoReleaser](https://goreleaser.com) via [`.github/workflows/release.yml`](../../../.github/workflows/release.yml) when the tag is pushed. This is what lets people install `rc` without Go.
4. **The Homebrew cask** in `rootcause-org/homebrew-tap` at that same version, so macOS upgrades agree with GitHub latest.
5. **The Go module proxy** (`proxy.golang.org`) ingesting the tag — this is the step that's easy to forget. Until the proxy has it, a consuming project's `go get github.com/rootcause-org/rootcause-cli@latest` keeps resolving the **old pseudo-version**, so they silently *don't* get your release.

[`scripts/release.sh`](../../../scripts/release.sh) does and verifies all five. Always prefer it over
running the steps by hand — the hand path is how the main push or proxy warmup gets skipped.

## How to release

```bash
scripts/release.sh patch         # 0.1.0 -> 0.1.1   (bug-fix pass, the usual)
scripts/release.sh minor         # 0.1.0 -> 0.2.0   (new command/flag)
scripts/release.sh major         # 0.1.0 -> 1.0.0   (breaking surface change)
scripts/release.sh v0.3.0        # or pin an explicit version
scripts/release.sh patch --dry-run   # run gates + print the plan, change nothing
```

Pick the bump by what changed in the surface (semver): patch = fixes, minor = additive, major = breaking.

The script **refuses to release** a dirty tree, a non-`main` branch, a checkout behind/diverged from
origin, or a version whose tag already exists — so a release is always reproducible from a known good
commit. A local `main` ahead of origin is valid. It captures the SHA, runs `go build/vet/test` as hard
gates (lint is advisory — see below), refuses if HEAD/worktree changed during those gates, pushes that
SHA to `origin/main`, verifies the remote ref equals it, then tags the same SHA. Finally it waits for
the exact tag/SHA workflow, verifies GitHub latest plus the tap cask agree, and verifies both explicit
and `@latest` proxy resolution.

**Before running:** commit the release-worthy change on local `main`; do not manually push it. The
script publishes and verifies `main` as part of the release. Typical flow: land the fix →
`scripts/release.sh patch`.

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
- **Manual fallback** (if `scripts/release.sh` is unavailable) — do all five steps, especially the
  main, cask, and proxy verification:
  ```bash
  sha="$(git rev-parse HEAD)"
  git push origin "$sha:refs/heads/main"
  test "$(git ls-remote --exit-code origin refs/heads/main | awk '{print $1}')" = "$sha"
  git tag -a vX.Y.Z "$sha" -m "rootcause-cli vX.Y.Z" && git push origin vX.Y.Z
  run_id="$(gh run list --workflow=release.yml --branch vX.Y.Z --json databaseId,headSha --jq ".[] | select(.headSha == \"$sha\") | .databaseId" | head -1)"
  gh run watch "$run_id" --exit-status
  test "$(gh api repos/rootcause-org/rootcause-cli/releases/latest --jq .tag_name)" = vX.Y.Z
  gh api -H 'Accept: application/vnd.github.raw+json' repos/rootcause-org/homebrew-tap/contents/Casks/rc.rb | grep 'version "X.Y.Z"'
  GOPROXY=https://proxy.golang.org go list -m github.com/rootcause-org/rootcause-cli@vX.Y.Z   # warm the proxy
  test "$(GOPROXY=https://proxy.golang.org go list -m -f '{{.Version}}' github.com/rootcause-org/rootcause-cli@latest)" = vX.Y.Z
  ```
