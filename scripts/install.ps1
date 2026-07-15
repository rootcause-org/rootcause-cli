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
$target = Join-Path $bindir "rc.exe"
$selectedBefore = (Get-Command rc.exe -ErrorAction SilentlyContinue).Source
if ($selectedBefore -and ((-not (Test-Path $target)) -or ((Resolve-Path $selectedBefore).Path -ne (Resolve-Path $target).Path))) {
  throw "PATH already selects $selectedBefore; refusing to create a second rc at $target (run 'rc self doctor')"
}

# --- download + extract ------------------------------------------------------
$tmp = Join-Path $env:TEMP ("rc-" + [guid]::NewGuid())
New-Item -ItemType Directory -Force -Path $tmp | Out-Null
try {
  Write-Host "==> downloading $asset ($tag)" -ForegroundColor Cyan
  $zip = Join-Path $tmp "rc.zip"
  Invoke-WebRequest -Uri $url -OutFile $zip
  $checksums = Invoke-WebRequest -Uri "https://github.com/$repo/releases/download/$tag/checksums.txt"
  $line = ($checksums.Content -split "`n" | Where-Object { $_ -match "\s+$([regex]::Escape($asset))\s*$" } | Select-Object -First 1)
  if (-not $line) { throw "checksums.txt has no entry for $asset" }
  $want = ($line.Trim() -split "\s+")[0].ToLowerInvariant()
  $got = (Get-FileHash -Algorithm SHA256 -Path $zip).Hash.ToLowerInvariant()
  if ($got -ne $want) { throw "checksum mismatch for $asset — refusing to install" }
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
$selected = (Get-Command rc.exe -ErrorAction SilentlyContinue).Source
$installed = (Resolve-Path (Join-Path $bindir "rc.exe")).Path
if ($selected -and ((Resolve-Path $selected).Path -ne $installed)) {
  throw "installed $installed, but PATH still selects $selected; run '$installed self doctor' and remove the shadowing install"
}
