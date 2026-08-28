#!/bin/bash
# Canonical source: rootcause-cli/scripts/cloud-setup.sh.
# Published by .github/workflows/release.yml to the RootCause release mirror.
# Install: curl -fsSL https://app.replypen.com/install/cloud.sh | bash
#
# Every download targets a host available in our cloud-agent environments without the GitHub proxy:
#   rc   -> S3 mirror (*.amazonaws.com)
#   uv   -> PyPI wheel (files.pythonhosted.org)
#   pnpm -> corepack / npm registry tarball (registry.npmjs.org)
# uv and pnpm are pinned + checksum-verified: this script can already read the environment's
# long-lived secrets, so nothing unreviewed may execute here. rc follows the release checksums in
# our HTTPS mirror. Re-runs are idempotent.
set -euo pipefail

UV_VERSION=0.12.7
PNPM_VERSION=11.24.0
# arch-independent JS payload (dist/) that the platform launcher binary loads
PNPM_PKG_SHA256=d1eab2433172661cc36a18ec85fce93f771db1962717329cc01ec9c2824ca24f

RC_MIRROR="${RC_RELEASE_MIRROR:-https://kampkompas-eu-central-1.s3.eu-central-1.amazonaws.com/cloud-bootstrap/rc}"
RC_MIRROR="${RC_MIRROR%/}"
[ -n "$RC_MIRROR" ] || { echo "RC_RELEASE_MIRROR must not be empty" >&2; exit 1; }

case "${RC_CLOUD_PLATFORM:-}" in
  claude|codex|generic) cloud_platform="$RC_CLOUD_PLATFORM" ;;
  "")
    if [ "${CLAUDE_CODE_REMOTE:-}" = true ]; then
      cloud_platform=claude
    else
      cloud_platform=generic
    fi
    ;;
  *) echo "unsupported RC_CLOUD_PLATFORM: $RC_CLOUD_PLATFORM (want claude, codex, or generic)" >&2; exit 1 ;;
esac
printf 'RootCause cloud setup (%s)\n' "$cloud_platform"

case "$(uname -m)" in
  x86_64|amd64)
    RC_ARCH=amd64
    UV_WHEEL=uv-${UV_VERSION}-py3-none-manylinux_2_17_x86_64.manylinux2014_x86_64.whl
    UV_WHEEL_PATH=8a/01/616cc5f80952eef6fa48c5dbd6ea8aeed04a494e7dfbe9f5f65cfcf85ad9
    UV_SHA256=4545e87c7ac64af317d8daffd279e23e93b0e05035662363033d3525923339d2
    PNPM_PKG=linux-x64
    PNPM_SHA256=2e9ef74a1cdd3d78dfe7911812c50c8b57f8cab6f91498c7438d1bba27aa220d
    ;;
  aarch64|arm64)
    RC_ARCH=arm64
    UV_WHEEL=uv-${UV_VERSION}-py3-none-manylinux_2_17_aarch64.manylinux2014_aarch64.musllinux_1_1_aarch64.whl
    UV_WHEEL_PATH=2e/5b/0e42dd88d9928b51a4e85d89d8aed46933951187b508ae68c145b06e3334
    UV_SHA256=fe9a871bd638ee6d2fd73bf40c2ee98153e44d06f796a03fcecf9d12b36d42d8
    PNPM_PKG=linux-arm64
    PNPM_SHA256=84400c0d4a3be91df18aa1262ea42670b3aa9b70972c33302b0c12f9be928b72
    ;;
  *) echo "unsupported cloud architecture: $(uname -m)" >&2; exit 1 ;;
esac

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

if [ "$(id -u)" = 0 ]; then sudo=""; elif command -v sudo >/dev/null 2>&1; then sudo=sudo; else sudo=""; fi
if [ -w /usr/local/bin ]; then bindir=/usr/local/bin; else bindir="$HOME/.local/bin"; fi
libdir="$HOME/.local/share"
mkdir -p "$bindir" "$libdir"
case ":$PATH:" in *":$bindir:"*) ;; *) PATH="$bindir:$PATH"; export PATH ;; esac

fetch() { # url dest expected-sha256
  curl -fsSL --retry 3 --retry-delay 1 "$1" -o "$2" || return 1
  got="$(sha256sum "$2" | awk '{print $1}')"
  [ "$got" = "$3" ] || { echo "checksum mismatch for $1 (got $got, want $3)" >&2; return 1; }
}

# --- rc (RootCause CLI) — the only connection to production data --------------
# The release workflow publishes the latest tag, archive, and checksums to this public mirror.
latest_rc_tag() {
  local tag
  tag="$(curl -fsSL --retry 3 --retry-delay 1 "${RC_MIRROR}/latest")"
  [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
    echo "invalid rc mirror tag: $tag" >&2
    return 1
  }
  printf '%s\n' "$tag"
}

verify_rc_archive() { # tag archive checksums dest
  local tag="$1" archive="$2" checksums="$3" dest="$4" expected got
  expected="$(awk -v wanted="$archive" '$2 == wanted || $2 == "*" wanted {print $1; exit}' "$checksums")"
  [[ "$expected" =~ ^[0-9a-fA-F]{64}$ ]] || {
    echo "missing checksum for $archive in $checksums" >&2
    return 1
  }
  curl -fsSL --retry 3 --retry-delay 1 "${RC_MIRROR}/${tag}/${archive}" -o "$dest"
  got="$(sha256sum "$dest" | awk '{print $1}')"
  [ "$got" = "$expected" ] || {
    echo "checksum mismatch for ${RC_MIRROR}/${tag}/${archive} (got $got, want $expected)" >&2
    return 1
  }
}

rc_tag="$(latest_rc_tag)"
if [ "$(rc --version 2>/dev/null | awk '{print $3}')" != "${rc_tag#v}" ]; then
  asset="rc_${rc_tag#v}_linux_${RC_ARCH}.tar.gz"
  curl -fsSL --retry 3 --retry-delay 1 "${RC_MIRROR}/${rc_tag}/checksums.txt" -o "$tmp/rc-checksums.txt"
  verify_rc_archive "$rc_tag" "$asset" "$tmp/rc-checksums.txt" "$tmp/rc.tar.gz"
  tar -xzf "$tmp/rc.tar.gz" -C "$tmp" rc
  install -m 0755 "$tmp/rc" "$bindir/rc"
  hash -r
fi

# --- uv (Python runner for report/analysis scripts) --------------------------
# PyPI wheel instead of the GitHub release: files.pythonhosted.org is allowlisted and proxy-free.
# The wheel is just a zip; the binaries live in uv-<version>.data/scripts/. No pip involved.
unzip_wheel() { # wheel destdir
  if command -v python3 >/dev/null 2>&1; then
    python3 -m zipfile -e "$1" "$2"
  elif command -v unzip >/dev/null 2>&1; then
    unzip -qo "$1" -d "$2"
  else
    $sudo apt-get install -y -qq unzip >/dev/null 2>&1 \
      || { $sudo apt-get update -qq >/dev/null 2>&1 && $sudo apt-get install -y -qq unzip >/dev/null 2>&1; }
    unzip -qo "$1" -d "$2"
  fi
}

if [ "${RC_CLOUD_SKIP_UV:-0}" != 1 ]; then
  if [ "$(uv --version 2>/dev/null | awk '{print $2}')" != "$UV_VERSION" ]; then
    fetch "https://files.pythonhosted.org/packages/${UV_WHEEL_PATH}/${UV_WHEEL}" "$tmp/uv.whl" "$UV_SHA256"
    unzip_wheel "$tmp/uv.whl" "$tmp/uvwhl"
    install -m 0755 "$tmp/uvwhl/uv-${UV_VERSION}.data/scripts/uv" \
      "$tmp/uvwhl/uv-${UV_VERSION}.data/scripts/uvx" "$bindir/"
    hash -r
  fi
fi

# --- pnpm --------------------------------------------------------------------
# Preferred: corepack (ships with Node, which the Claude sandbox image provides) — a few-KB shim,
# integrity-checked against registry.npmjs.org. Fallback for a Node-less image: the pinned
# platform package tarball from the same registry (the GitHub release is proxy-blocked).
install_pnpm_corepack() {
  command -v corepack >/dev/null 2>&1 || return 1
  COREPACK_ENABLE_DOWNLOAD_PROMPT=0 corepack enable --install-directory "$bindir" >/dev/null 2>&1 || return 1
  COREPACK_ENABLE_DOWNLOAD_PROMPT=0 corepack prepare "pnpm@${PNPM_VERSION}" --activate >/dev/null 2>&1 || return 1
  hash -r
  [ "$(pnpm --version 2>/dev/null)" = "$PNPM_VERSION" ]
}

install_pnpm_registry() {
  # Two registry tarballs, mirroring what `npm i -g pnpm` assembles: the main package carries
  # dist/pnpm.mjs, the platform package carries the self-contained Node launcher that loads it
  # from ../dist relative to itself. Neither needs a system Node.
  fetch "https://registry.npmjs.org/pnpm/-/pnpm-${PNPM_VERSION}.tgz" "$tmp/pnpm.tgz" "$PNPM_PKG_SHA256" || return 1
  fetch "https://registry.npmjs.org/@pnpm/${PNPM_PKG}/-/${PNPM_PKG}-${PNPM_VERSION}.tgz" \
    "$tmp/pnpm-bin.tgz" "$PNPM_SHA256" || return 1
  rm -rf "$libdir/pnpm-${PNPM_VERSION}"
  mkdir -p "$libdir/pnpm-${PNPM_VERSION}"
  tar -xzf "$tmp/pnpm.tgz" -C "$libdir/pnpm-${PNPM_VERSION}" --strip-components=1
  tar -xzf "$tmp/pnpm-bin.tgz" -C "$libdir/pnpm-${PNPM_VERSION}" --strip-components=1 package/pnpm
  chmod 0755 "$libdir/pnpm-${PNPM_VERSION}/pnpm"
  ln -sfn "$libdir/pnpm-${PNPM_VERSION}/pnpm" "$bindir/pnpm"
  hash -r
  # The bundled Node links against libatomic, which minimal Ubuntu images omit.
  if ! pnpm --version >/dev/null 2>&1; then
    $sudo apt-get install -y -qq libatomic1 >/dev/null 2>&1 \
      || { $sudo apt-get update -qq >/dev/null 2>&1 && $sudo apt-get install -y -qq libatomic1 >/dev/null 2>&1; } \
      || true
  fi
  [ "$(pnpm --version 2>/dev/null)" = "$PNPM_VERSION" ]
}

if [ "${RC_CLOUD_SKIP_PNPM:-0}" != 1 ]; then
  if [ "$(pnpm --version 2>/dev/null)" != "$PNPM_VERSION" ]; then
    install_pnpm_corepack || install_pnpm_registry || {
      rm -f "$bindir/pnpm"
      echo "warning: pnpm ${PNPM_VERSION} could not be installed; rc and uv are ready" >&2
    }
  fi
fi

# --- persist PATH for interactive/session shells ------------------------------
for rcfile in "$HOME/.bashrc" "$HOME/.profile"; do
  [ -e "$rcfile" ] || : > "$rcfile"
  grep -qF "# kamp-cloud-setup PATH" "$rcfile" \
    || printf '%s\n' "" "# kamp-cloud-setup PATH" "export PATH=\"${bindir}:\$PATH\"" >> "$rcfile"
done

hash -r
rc --version
if [ "${RC_CLOUD_SKIP_UV:-0}" = 1 ]; then echo "uv: skipped"; else uv --version; fi
if [ "${RC_CLOUD_SKIP_PNPM:-0}" = 1 ]; then echo "pnpm: skipped"; else pnpm --version 2>/dev/null || echo "pnpm: not installed"; fi
