# rc: unpinned "always latest" in Claude cloud sandboxes via S3 mirror + self-update

Owner PJ · plan Fable 2026-08-28 · implementer Sol (T3).

## Why
Claude cloud sandbox constraints (docs 2026-08-28): GitHub API + release-asset downloads 403 unless the repo is
attached to the session; the environment setup script runs ONCE per environment (filesystem snapshot reused), so
even "download latest" there freezes. `rc self update` (`internal/cli/upgrade.go`) uses api.github.com +
github.com/releases/download → unusable in the sandbox. PJ wants: no rc version pins anywhere in the cloud
bootstrap; every session runs the newest published rc.

## Design
1. **Release workflow publishes a mirror.** `.github/workflows/release.yml` (after GoReleaser): sync the linux
   amd64/arm64 (+ darwin, cheap) archives and `checksums.txt` to
   `s3://kampkompas-eu-central-1/cloud-bootstrap/rc/<tag>/` and write `cloud-bootstrap/rc/latest` containing the
   tag (plain text, `Cache-Control: no-cache`). Auth: GitHub OIDC → IAM role in the kampadmin AWS account (profile
   `aws-pj-kampadmin`) with PutObject/ListBucket scoped to `cloud-bootstrap/rc/*` only; trust policy limited to
   repo `rootcause-org/rootcause-cli`, ref tags. Create the role + policy with the AWS CLI (PJ authorized), record
   ARN in the workflow. Backfill: run the sync once for the current latest tag (v1.19.5) so `latest` exists.
   Bucket already has public-read GetObject; verify anonymous GET of `latest` works.
2. **`rc self update` mirror support.** Env `RC_RELEASE_MIRROR=<base url>` (default empty → GitHub as today).
   When set: latest tag = GET `<mirror>/latest`, asset + checksums.txt from `<mirror>/<tag>/`, same sha256
   verification as today (checksums.txt from the mirror; HTTPS to our own bucket is the trust root — document).
   Also auto-fallback: if GitHub returns 403/404 and the built-in default mirror URL is set at build time
   (ldflags `-X ...defaultMirror=https://kampkompas-eu-central-1.s3.eu-central-1.amazonaws.com/cloud-bootstrap/rc`),
   use it — so the sandbox needs no env at all. `rc self update --check` prints source (github|mirror).
   Tests with httptest for both paths. Release as v1.20.0 (this feature) via the same workflow → proves step 1.
3. **Cloud bootstrap (both repos, mirrored byte-identical: `kampadmin/scripts/cloud/*` and
   `kampkompas/scripts/cloud/*`, keep `check_mirror.sh` green):**
   - `setup.sh`: rc section becomes: read `<mirror>/latest`, download `<mirror>/<tag>/rc_<ver>_linux_<arch>.tar.gz`
     + `checksums.txt`, verify sha256 from checksums.txt, install. **No RC_VERSION / RC_ASSET_SHA256 constants.**
     uv + pnpm stay pinned (stable tooling; pins documented). Remove the uv warm-up block (setup runs before any
     clone → never effective); instead do the warm-up in the SessionStart hook (below), best-effort, `uv sync
     --script` over `$CLAUDE_PROJECT_DIR/.claude/skills/*/scripts/*.py` with a 120 s cap.
   - `rc_bootstrap.sh` (SessionStart hook, both repos): if rc missing → same mirror install; else
     `rc self update` (mirror, quiet, tolerate failure with a one-line warning — never block the session), then
     token seeding as today, then `rc auth status`. Print `rc --version` in the output.
   - READMEs: replace "bump the pin + refresh SHAs" with "nothing to do; mirror is fed by the release workflow";
     keep the pasted-setup-script instruction; update the sha256 lines for the mirrored files.
4. **Simplify**: delete the old rc GitHub fallback from setup.sh (mirror is authoritative; GitHub 403s anyway);
   drop `cloud-bootstrap/rc/v1.19.5` manual upload docs. Keep scripts POSIX-ish bash, `set -euo pipefail`.

## Verification
- Workflow run for v1.20.0 shows the S3 sync; `curl <mirror>/latest` → `v1.20.0`; anonymous GET of the archive OK.
- Clean `ubuntu:24.04` container (colima) with github.com blocked via /etc/hosts: run setup.sh → rc = latest;
  then run rc_bootstrap.sh with `CLAUDE_CODE_REMOTE=true` and a fake old rc binary on PATH → it self-updates.
- `rc self update --check` locally: github path still works; with `RC_RELEASE_MIRROR` set: mirror path.
- `check_mirror.sh` ok in both repos; commit per repo, push (kampadmin branch kampadmin_v3, kampkompas main,
  rootcause-cli main + tag).

## Report back
`~/.claude/skills/t3-spawn-thread/scripts/t3-ping-thread --thread 2ac7596e-e879-41dd-a1fa-584a85ae1ed2 --from "Sol rc-mirror" -- "<shas, rc version, mirror URL, unverified>"`
