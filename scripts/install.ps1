# install.ps1 — install the `rc` CLI on native Windows (PowerShell) without Go.
#
# Detects your arch, downloads the matching prebuilt rc.exe from the latest GitHub Release,
# installs it under %LOCALAPPDATA%\Programs\rc, and puts that dir on your user PATH.
# Idempotent: re-run to upgrade.
#
#   irm https://raw.githubusercontent.com/rootcause-org/rootcause-cli/main/scripts/install.ps1 | iex
#
# Knobs (env vars):
#   RC_VERSION       install a specific version instead of latest, e.g. $env:RC_VERSION = "v0.5.1"
#   RC_INSTALL_DIR   install into this dir instead of %LOCALAPPDATA%\Programs\rc
#
# (On WSL use scripts/install.sh instead — WSL is Linux.)

$ErrorActionPreference = "Stop"
$repo = "rootcause-org/rootcause-cli"

# --- detect arch -------------------------------------------------------------
$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  "AMD64" { "amd64" }
  "ARM64" { "arm64" }
  default { throw "unsupported arch '$($env:PROCESSOR_ARCHITECTURE)' (need AMD64 or ARM64)" }
}

# --- resolve version ---------------------------------------------------------
if ($env:RC_VERSION) {
  $tag = if ($env:RC_VERSION.StartsWith("v")) { $env:RC_VERSION } else { "v$($env:RC_VERSION)" }
} else {
  Write-Host "==> resolving latest release" -ForegroundColor Cyan
  $tag = (Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest").tag_name
  if (-not $tag) { throw "could not resolve the latest release tag from the GitHub API" }
}
$version = $tag.TrimStart("v")

$asset = "rc_${version}_windows_${arch}.zip"
$url   = "https://github.com/$repo/releases/download/$tag/$asset"

# --- install dir -------------------------------------------------------------
$bindir = if ($env:RC_INSTALL_DIR) { $env:RC_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "Programs\rc" }
New-Item -ItemType Directory -Force -Path $bindir | Out-Null

# --- download + extract ------------------------------------------------------
$tmp = Join-Path $env:TEMP ("rc-" + [guid]::NewGuid())
New-Item -ItemType Directory -Force -Path $tmp | Out-Null
try {
  Write-Host "==> downloading $asset ($tag)" -ForegroundColor Cyan
  $zip = Join-Path $tmp "rc.zip"
  Invoke-WebRequest -Uri $url -OutFile $zip
  Expand-Archive -Path $zip -DestinationPath $tmp -Force
  Copy-Item -Path (Join-Path $tmp "rc.exe") -Destination (Join-Path $bindir "rc.exe") -Force
} finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

Write-Host "==> installed rc $version -> $bindir\rc.exe" -ForegroundColor Cyan

# --- add to user PATH --------------------------------------------------------
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if (($userPath -split ";") -notcontains $bindir) {
  [Environment]::SetEnvironmentVariable("Path", "$userPath;$bindir", "User")
  Write-Host "==> added $bindir to your user PATH — open a new terminal to pick it up" -ForegroundColor Yellow
}
$env:Path = "$env:Path;$bindir"
& (Join-Path $bindir "rc.exe") --version
