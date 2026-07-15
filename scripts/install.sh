#!/bin/sh
#
# install.sh — install the `rc` CLI on Linux/WSL, or explicitly to RC_INSTALL_DIR.
#
# Detects your OS/arch, downloads the matching prebuilt binary from the latest GitHub Release,
# and drops `rc` somewhere on your PATH. Idempotent: re-run it any time to upgrade.
#
#   curl -fsSL https://raw.githubusercontent.com/rootcause-org/rootcause-cli/main/scripts/install.sh | sh
#
# Knobs (env vars):
#   RC_VERSION       install a specific version instead of latest, e.g. RC_VERSION=v0.5.1
#   RC_INSTALL_DIR   install into this dir instead of auto-picking (/usr/local/bin or ~/.local/bin)

set -eu

REPO="rootcause-org/rootcause-cli"

err() { printf '\033[31merror:\033[0m %s\n' "$*" >&2; exit 1; }
info() { printf '\033[36m==>\033[0m %s\n' "$*"; }

command -v curl >/dev/null 2>&1 || err "curl is required but not found"
command -v tar  >/dev/null 2>&1 || err "tar is required but not found"

# --- detect os/arch ----------------------------------------------------------
os="$(uname -s)"
case "$os" in
  Linux)  os=linux ;;   # WSL reports Linux too — same binary
  Darwin) os=darwin ;;
  *) err "unsupported OS '$os' — on native Windows use scripts/install.ps1, or 'go install $REPO/cmd/rc@latest'" ;;
esac

# Homebrew is the one canonical macOS installation. An explicit RC_INSTALL_DIR remains available for
# managed/portable environments, but the default path must never create a second copy beside a cask.
if [ "$os" = darwin ] && [ "${RC_INSTALL_DIR:-}" = "" ]; then
  [ "${RC_VERSION:-}" = "" ] || err "RC_VERSION pinning requires an explicit RC_INSTALL_DIR on macOS; canonical Homebrew tracks latest"
  command -v brew >/dev/null 2>&1 || err "Homebrew is required for the canonical macOS install (or set RC_INSTALL_DIR explicitly)"
  info "updating the canonical Homebrew installation"
  brew update
  if brew list --cask rc >/dev/null 2>&1; then
    brew upgrade --cask rootcause-org/tap/rc
  else
    brew install --cask rootcause-org/tap/rc
  fi
  canonical="$(brew --prefix)/bin/rc"
  [ -x "$canonical" ] || err "Homebrew completed but $canonical is missing"
  "$canonical" self update --migrate
  "$canonical" --version
  exit 0
fi

arch="$(uname -m)"
case "$arch" in
  x86_64|amd64)  arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) err "unsupported arch '$arch' (need x86_64 or arm64)" ;;
esac

# --- resolve version ---------------------------------------------------------
if [ "${RC_VERSION:-}" != "" ]; then
  tag="$RC_VERSION"
  case "$tag" in v*) ;; *) tag="v$tag" ;; esac
else
  info "resolving latest release"
  tag="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
    | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)"
  [ -n "$tag" ] || err "could not resolve the latest release tag from the GitHub API"
fi
version="${tag#v}"

asset="rc_${version}_${os}_${arch}.tar.gz"
url="https://github.com/$REPO/releases/download/$tag/$asset"

# --- pick an install dir -----------------------------------------------------
if [ "${RC_INSTALL_DIR:-}" != "" ]; then
  bindir="$RC_INSTALL_DIR"
elif [ -w /usr/local/bin ] 2>/dev/null; then
  bindir=/usr/local/bin
elif command -v sudo >/dev/null 2>&1 && [ -d /usr/local/bin ]; then
  bindir=/usr/local/bin
  sudo=sudo
else
  bindir="$HOME/.local/bin"
fi
: "${sudo:=}"

# Never create a second, shadowed copy. Re-running against the selected target is idempotent; choosing
# a different directory requires the operator to resolve the existing installation first.
selected_before="$(command -v rc 2>/dev/null || true)"
if [ -n "$selected_before" ] && { [ ! -e "$bindir/rc" ] || ! [ "$selected_before" -ef "$bindir/rc" ]; }; then
  err "PATH already selects $selected_before; refusing to create a second rc at $bindir/rc (run 'rc self doctor')"
fi

# --- download + extract + install --------------------------------------------
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

info "downloading $asset ($tag)"
curl -fsSL "$url" -o "$tmp/rc.tar.gz" || err "download failed: $url"
curl -fsSL "https://github.com/$REPO/releases/download/$tag/checksums.txt" -o "$tmp/checksums.txt" \
  || err "checksums download failed"
want="$(awk -v asset="$asset" '$2 == asset {print $1; exit}' "$tmp/checksums.txt")"
[ -n "$want" ] || err "checksums.txt has no entry for $asset"
if command -v sha256sum >/dev/null 2>&1; then
  got="$(sha256sum "$tmp/rc.tar.gz" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  got="$(shasum -a 256 "$tmp/rc.tar.gz" | awk '{print $1}')"
else
  err "sha256sum or shasum is required to verify the download"
fi
[ "$got" = "$want" ] || err "checksum mismatch for $asset — refusing to install"
tar -xzf "$tmp/rc.tar.gz" -C "$tmp" rc || err "archive did not contain an 'rc' binary"
chmod +x "$tmp/rc"

$sudo mkdir -p "$bindir"
$sudo mv "$tmp/rc" "$bindir/rc"

info "installed rc $version → $bindir/rc"
"$bindir/rc" --version >/dev/null 2>&1 && info "$("$bindir/rc" --version)"

selected="$(command -v rc 2>/dev/null || true)"
if [ -n "$selected" ] && ! [ "$selected" -ef "$bindir/rc" ]; then
  err "installed $bindir/rc, but PATH still selects $selected; run '$bindir/rc self doctor' and remove the shadowing install"
fi

# --- PATH hint ---------------------------------------------------------------
case ":$PATH:" in
  *":$bindir:"*) ;;
  *) printf '\033[33m! %s is not on your PATH — add this to your shell rc:\033[0m\n    export PATH="%s:$PATH"\n' "$bindir" "$bindir" ;;
esac
