#!/usr/bin/env bash
#
# release.sh — cut a rootcause-cli release, end to end, the same way every time.
#
# WHY THIS EXISTS: a release here is more than "git tag". For consumers to actually pull the new
# version we need three things to land together: (1) the GitHub Release with prebuilt binaries
# (GoReleaser, via .github/workflows/release.yml), (2) the git tag itself, and (3) the Go module
# proxy must have ingested the tag so `go get <module>@latest` resolves it. Miss step 3 and consuming
# projects keep getting a stale pseudo-version even though the tag exists. This script does all three
# and verifies them, so nobody has to remember the ritual.
#
# USAGE:
#   scripts/release.sh v0.2.0      # explicit version
#   scripts/release.sh patch       # bump patch from the latest vX.Y.Z tag (0.1.0 -> 0.1.1)
#   scripts/release.sh minor       # 0.1.0 -> 0.2.0
#   scripts/release.sh major       # 0.1.0 -> 1.0.0
#
# FLAGS:
#   --dry-run   run the quality gates and print the plan, but do not tag/push/release.
#
# PRECONDITIONS (checked, not assumed): clean tree, on the default branch, in sync with origin,
# `gh` authenticated, `go` available. The script refuses to release a dirty or behind checkout.

set -euo pipefail

MODULE="github.com/rootcause-org/rootcause-cli"
MAIN_BRANCH="main"
GOPROXY_URL="https://proxy.golang.org"
EXPECTED_ASSETS=7 # 6 OS/arch archives + checksums.txt — keep in sync with .goreleaser.yaml
RELEASE_TIMEOUT=600 # seconds to wait for the GitHub Release assets to appear

cd "$(git rev-parse --show-toplevel)"

die() { printf '\033[31merror:\033[0m %s\n' "$*" >&2; exit 1; }
step() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
ok() { printf '\033[32m  ✓\033[0m %s\n' "$*"; }

DRY_RUN=0
ARG=""
for a in "$@"; do
  case "$a" in
    --dry-run) DRY_RUN=1 ;;
    -*) die "unknown flag: $a" ;;
    *) [ -z "$ARG" ] || die "unexpected extra argument: $a"; ARG="$a" ;;
  esac
done
[ -n "$ARG" ] || die "usage: scripts/release.sh <vX.Y.Z|patch|minor|major> [--dry-run]"

# --- resolve the target version -------------------------------------------------------------------

latest_tag() { git tag -l 'v*' --sort=-v:refname | head -1; }

bump() {
  local part="$1" cur v major rest minor patch
  cur="$(latest_tag)"; cur="${cur:-v0.0.0}"
  v="${cur#v}"
  major="${v%%.*}"
  rest="${v#*.}"
  minor="${rest%%.*}"
  patch="${rest#*.}"
  case "$part" in
    major) major=$((major + 1)); minor=0; patch=0 ;;
    minor) minor=$((minor + 1)); patch=0 ;;
    patch) patch=$((patch + 1)) ;;
  esac
  printf 'v%s.%s.%s' "$major" "$minor" "$patch"
}

case "$ARG" in
  patch|minor|major) VERSION="$(bump "$ARG")" ;;
  v[0-9]*) VERSION="$ARG" ;;
  *) die "version must be vX.Y.Z or one of: patch|minor|major (got: $ARG)" ;;
esac
[[ "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "invalid version: $VERSION (want vX.Y.Z)"

step "Releasing $MODULE $VERSION (latest tag: $(latest_tag || echo none))"

# --- preconditions --------------------------------------------------------------------------------

step "Preconditions"
command -v gh >/dev/null || die "gh (GitHub CLI) not found"
command -v go >/dev/null || die "go not found"
gh auth status >/dev/null 2>&1 || die "gh not authenticated (run: gh auth login)"
git rev-parse "refs/tags/$VERSION" >/dev/null 2>&1 && die "tag $VERSION already exists"

cur_branch="$(git rev-parse --abbrev-ref HEAD)"
[ "$cur_branch" = "$MAIN_BRANCH" ] || die "not on $MAIN_BRANCH (on $cur_branch)"
[ -z "$(git status --porcelain)" ] || die "working tree is dirty — commit or stash first"

git fetch --quiet origin "$MAIN_BRANCH"
[ "$(git rev-parse HEAD)" = "$(git rev-parse "origin/$MAIN_BRANCH")" ] \
  || die "local $MAIN_BRANCH is not in sync with origin/$MAIN_BRANCH — push/pull first"
ok "clean, on $MAIN_BRANCH, in sync with origin"

# --- quality gates --------------------------------------------------------------------------------

step "Quality gates (build / vet / test)"
go build ./... && ok "build"
go vet ./...   && ok "vet"
go test ./...  && ok "test"
# Lint is advisory: the repo has known, intentional errcheck findings in the render layer (writes to
# an output buffer). Surface lint output but never block a release on it.
if command -v golangci-lint >/dev/null; then
  golangci-lint run >/dev/null 2>&1 && ok "lint (clean)" || printf '\033[33m  ! lint reported findings (advisory, not blocking) — run: golangci-lint run\033[0m\n'
fi

if [ "$DRY_RUN" = 1 ]; then
  step "Dry run — would tag $VERSION on $(git rev-parse --short HEAD), push, release, and warm the proxy."
  exit 0
fi

# --- tag + push -----------------------------------------------------------------------------------

step "Tag + push"
git tag -a "$VERSION" -m "rootcause-cli $VERSION"
ok "created annotated tag $VERSION"
git push origin "$VERSION"
ok "pushed tag (triggers .github/workflows/release.yml → GoReleaser)"

# --- wait for the GitHub Release ------------------------------------------------------------------

step "Waiting for the GitHub Release (binaries) — up to ${RELEASE_TIMEOUT}s"
deadline=$(( $(date +%s) + RELEASE_TIMEOUT ))
while :; do
  count="$(gh release view "$VERSION" --json assets --jq '.assets | length' 2>/dev/null || echo 0)"
  if [ "${count:-0}" -ge "$EXPECTED_ASSETS" ]; then
    ok "release published with $count assets"
    break
  fi
  [ "$(date +%s)" -lt "$deadline" ] || die "timed out waiting for release assets (have ${count:-0}/$EXPECTED_ASSETS) — check: gh run list --workflow=release.yml"
  sleep 10
done

# --- warm the Go module proxy ---------------------------------------------------------------------
#
# This is the step people forget. Requesting the explicit version forces proxy.golang.org to ingest
# the tag's .info/.mod/.zip, so consumers' `go get <module>@latest` will resolve it (the @latest/list
# endpoints are cached and may lag a few minutes, but explicit `@version` works immediately once warm).

step "Warming the Go module proxy"
for attempt in 1 2 3 4 5; do
  if GOPROXY="$GOPROXY_URL" go list -m "$MODULE@$VERSION" >/dev/null 2>&1; then
    ok "proxy resolves $MODULE@$VERSION"
    break
  fi
  [ "$attempt" -lt 5 ] || die "proxy did not ingest $VERSION after 5 tries — retry later: GOPROXY=$GOPROXY_URL go list -m $MODULE@$VERSION"
  sleep 6
done

# --- done -----------------------------------------------------------------------------------------

step "Released $VERSION ✓"
cat <<EOF

Consumers can now pull it:
  go get $MODULE@$VERSION          # pin (resolves immediately)
  go install $MODULE/cmd/rc@$VERSION
  go get $MODULE@latest            # may lag a few min until the proxy list cache refreshes

Release page: $(gh release view "$VERSION" --json url --jq .url 2>/dev/null || echo "gh release view $VERSION")
EOF
