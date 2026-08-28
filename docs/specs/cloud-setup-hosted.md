# Hosted cloud setup script: `curl -fsSL https://app.replypen.com/install/cloud.sh | bash`

Owner PJ · plan Fable 2026-08-28 · implementer Sol. Follows docs/specs/cloud-mirror-self-update.md (done, v1.20.0).

## Why
The setup script is currently pasted in full into the claude.ai environment and mirrored byte-identical in
kampadmin + kampkompas (`scripts/cloud/setup.sh` + `check_mirror.sh`). Make it a RootCause building block:
single source in rootcause-cli, published with each release, reachable from a stable branded URL. We are the
only consumer → opinionated, minimal, changeable later. Not over-engineered: no templating, no per-customer config.

## Design
1. **Source of truth**: `rootcause-cli/scripts/cloud-setup.sh` = today's `kampadmin/scripts/cloud/setup.sh`
   (take it verbatim as the starting point), plus:
   - platform detection for logging + defaults only: Claude (`CLAUDE_CODE_REMOTE=true` or the setup-script
     context; treat unknown as generic) vs Codex/ChatGPT cloud (check Codex cloud docs for its env marker, e.g.
     `CODEX_*`; if none documented, `RC_CLOUD_PLATFORM=codex` override). One script, `case` on platform only
     where behaviour must differ (probably nowhere today besides the log line — keep it that way).
   - opt-outs: `RC_CLOUD_SKIP_UV=1`, `RC_CLOUD_SKIP_PNPM=1` (rc always installs). Default = install all.
   - `RC_RELEASE_MIRROR` override honoured (default = the S3 mirror URL).
   - keep: sha256-pinned uv/pnpm, rc from mirror `latest` + checksums.txt, idempotent, PATH persistence,
     final version summary. Header comment: canonical path + how it's published + the one-liner.
2. **Publish**: release workflow uploads `cloud-setup.sh` to `<mirror>/<tag>/cloud-setup.sh` and
   `<mirror>/cloud-setup.sh` (latest, `Cache-Control: no-cache`). Backfill for v1.20.0 (or cut v1.20.1 if the
   workflow change needs a release to run — simplest: release v1.20.1).
3. **Stable URL**: rootcause server (`/Users/pjmuller/code/rootcause-org/rootcause`) adds unauthenticated
   `GET /install/cloud.sh` → `302` to `<mirror>/cloud-setup.sh` (and `GET /install/cloud.sh?v=<tag>` → versioned).
   Also `GET /install` → short plain-text usage. Deploy per rootcause release flow (AGENTS.md). Verify
   `curl -fsSL https://app.replypen.com/install/cloud.sh | head -3` returns the script. Note in docs that
   `app.replypen.com` + `*.amazonaws.com` must be allowlisted (already the case for our env).
4. **Consumers**: kampadmin + kampkompas: delete `scripts/cloud/setup.sh` and `check_mirror.sh`; README →
   "cloud environment setup script = `curl -fsSL https://app.replypen.com/install/cloud.sh | bash`" + env var
   list + opt-outs; keep `rc_bootstrap.sh` (SessionStart hook: self update, token seeding, uv warm-up — project
   specific, stays in repo). Update pointers (`doc/cloud-sessions.md`, rootcause skill in kampkompas, org-value
   SKILL.md line about the setup script). Memory/docs in rootcause-cli README + AGENTS.md: "Cloud setup" section.
5. **Tests**: shellcheck on the script (add to CI if cheap); container test as before (ubuntu:24.04, GitHub
   blocked, run the one-liner against the real URL) → rc latest, uv, pnpm; with `RC_CLOUD_SKIP_PNPM=1` → no pnpm.

## Report back
`~/.claude/skills/t3-spawn-thread/scripts/t3-ping-thread --thread 2ac7596e-e879-41dd-a1fa-584a85ae1ed2 --from "Sol cloud-setup" -- "<shas per repo, rc version, URL verified, unverified>"`
