# rootcause-cli (`rc`)

A scriptable client that lets a project **consume its own rootcause data** and **change its own
config** — over rootcause's public JSON `/api/v1`, authed with an **OAuth access token** (you sign in
once with `rc login`; the CLI refreshes the token for you). **Fat client, thin server:** endpoints
return raw, token-scoped data; the CLI may digest/cluster/render it for you, and every such command also
emits the raw rows as **JSON when piped** so `| jq` always works and you can slice it your own way.

```console
$ rc status
Health: healthy

Sources:
  SOURCE      TOTAL  ERRORS
  Prompt API  12     0

$ rc ask "Do I still have open invoices?"   # simulate a support email, wait, print draft/note
$ rc ask --scenario raw "How many active subscriptions are past due?"
$ rc ask --effort pro "Retry this with a stronger model tier"
$ rc runs --kind prompt --limit 5 | jq '.runs[].run_id'
$ rc run <id> --events        # full per-iteration trace (NDJSON when piped)
$ rc run <id> --full          # the whole bundle (header + trace; JSONL when piped)
$ rc config set max_run_usd=5 default_tier=pro
$ rc env keys                  # key NAMES of the production grounding env (no values)
$ rc env pull                  # sync that env to a local 0600 ./.env (for brain-dev --live)
```

## Install

You do **not** need Go installed — grab a prebuilt binary with the one-liner for your platform.

**macOS — Homebrew:**

```bash
brew install rootcause-org/tap/rc      # then: brew upgrade rc
```

> This is a **cask** (a prebuilt binary), not a source formula — it sidesteps the Homebrew sandbox/PTY
> install that fails on some macOS setups with `can't get Master/Slave device`. Quarantine is stripped
> automatically, so `rc` runs without a Gatekeeper prompt.

**Linux / WSL — install script** (no Homebrew or Go required):

```bash
curl -fsSL https://raw.githubusercontent.com/rootcause-org/rootcause-cli/main/scripts/install.sh | sh
```

Detects your arch, installs `rc` to `/usr/local/bin` (or `~/.local/bin`), and is idempotent — re-run to
upgrade. Knobs: `RC_VERSION=v0.5.1` to pin, `RC_INSTALL_DIR=…` to choose where. Works on macOS too.

**Windows (native PowerShell):**

```powershell
irm https://raw.githubusercontent.com/rootcause-org/rootcause-cli/main/scripts/install.ps1 | iex
```

Installs `rc.exe` under `%LOCALAPPDATA%\Programs\rc` and adds it to your user PATH. (On **WSL**, use the
Linux one-liner above — WSL is Linux.)

**From source (Go devs, any OS):**

```bash
go install github.com/rootcause-org/rootcause-cli/cmd/rc@latest
```

**Manual** — grab a tarball/zip from the [latest release](https://github.com/rootcause-org/rootcause-cli/releases/latest)
(`darwin`/`linux`/`windows` × `amd64`/`arm64`), extract `rc`, and put it on your PATH. On macOS, an
unsigned binary may be quarantined: `xattr -d com.apple.quarantine $(which rc)`.

### Upgrading

```bash
rc upgrade            # self-update to the latest release (Linux / WSL / Windows)
rc upgrade --check    # just say whether a newer version exists
brew upgrade rc       # macOS (Homebrew) — rc upgrade detects this and points you here
```

`rc upgrade` replaces its own binary with the latest release for your OS/arch (verifying the published
checksum first). On a Homebrew install it defers to `brew upgrade rc` so it doesn't fight brew. (`go
install …@latest` re-installs the latest for Go users.)

## Sign in

`rc` authenticates with **OAuth**. Sign in once with `rc login`; it stores an access + refresh token in
`~/.config/rootcause/tokens.json` (0600) and refreshes the short-lived access token transparently on
every later command. The token's **project scope is chosen on the browser consent screen** (a single
project, or — for a global admin — all projects); there is no key to paste and no `--project` is needed
to *prove* scope, it's baked into the token.

```bash
rc login            # opens your browser (PKCE loopback), catches the redirect, stores the token
rc login --device   # headless/SSH: prints a short code you approve in a browser on any device
rc logout           # revoke the token server-side and clear it locally
```

**Let the brain checkout select the profile.** A brain repo (`rootcause-brain-<project>`) commits a
`.rootcause.toml` binding it to one project + base URL, so `rc` run anywhere inside it first looks for
a local token profile with the same name. If that profile exists, it uses it; otherwise it uses the
`default` profile and sends the brain project as the server-side `--project` scope where supported:

```bash
cd rootcause-brain-acme   # committed .rootcause.toml: project = "acme", base_url = "…"
rc login                  # stores a token under the "acme" profile
rc whoami                 # profile: acme · project: acme · auth: logged in
rc ask "…"                # just works
```

That gives two workflows: project developers can keep one token per project profile, while a global
admin can keep one all-projects token in `default` and still have each brain checkout auto-scope to its
own project. Main intent: the checkout chooses the project context; the profile only chooses which
local token to use.

**Base URL** comes from `ROOTCAUSE_BASE_URL`, the brain marker's `base_url`, a config profile, or the
built-in production default (`https://rootcause.probackup.io`). A stored token also remembers the issuer it was minted
against, so commands hit the same server you logged in to.

```bash
export ROOTCAUSE_BASE_URL=https://your-rootcause-host   # default: https://rootcause.probackup.io
```

Optional `~/.config/rootcause/config.toml` holds **base-URL-only** profiles (no secrets — tokens live
in the token store):

```toml
[default]
base_url = "https://your-rootcause-host"

[profiles.staging]
base_url = "https://staging.your-rootcause-host"
```

**Profiles** are the token-store keys. The profile is resolved as: explicit `--profile <name>` >
the brain marker's project if that token exists > `"default"`. `--profile` picks *which stored token*
a command uses.
`--project <id-or-name>` is **not** a token selector — it's a **server-side scope**: it keeps the active
token and names one project on supported endpoints (`?project=`), so an **all-projects admin token** can
review a single project (`rc fleet --project momentum-tools`) or trigger one (`rc ask --project
momentum-tools "…"`) without minting a per-project profile; a project-pinned token disregards it.
`--tenant <slug>` scopes a request within the token's project where the endpoint accepts it.

For a whole-fleet review with an all-projects token, `rc fleet`/`patterns`/`health` take **`--all`**:
the CLI lists the fleet (`rc projects`) and fans out per project — `fleet --all` groups the digest by
project with a fleet total, `health --all` exits non-zero if ANY project is unhealthy, `patterns --all`
clusters per project. `-o json` emits the merged `{projects:[…]}` shape. `--all` against a project-scoped
token is a friendly error (it needs an all-projects token), not a silent single-project run.

`rc fleet` also carries the aggregates operators used to drop to raw SQL for: **`--by-model`** (per
answered model — runs, total/avg cost, and how many were **fallbacks**; the highest-value view, it
surfaces "one model is N% of spend purely as a fallback") and **`--timeline`** (per-day
runs/errors/cost). Both off by default to keep the digest scannable; the per-run `is_fallback` /
`planned_model` always ride in `-o json` so any breakdown is re-derivable. Stuck runs (`running` past
a 30m clock with no finish) and a `FB` model-fallback flag are surfaced inline; every worst-offender
line carries the full triage tail (cost · secs · turns · bash_err · ctx · FB).

**`.rootcause.toml`** (committed, per brain) names the project + endpoint — no secret, safe to commit,
ships the binding with a clone. There is no longer any `.rootcause.secret.toml` — credentials live only
in the OAuth token store.

## Commands

Global flags: `--profile <name>` picks the stored token; `--project <id-or-name>` scopes supported
requests to one project server-side (useful for all-projects tokens outside a brain checkout or as an
override; inside a brain checkout the `.rootcause.toml` project is used automatically when falling back
to `default`);
`--tenant <slug>` scopes a request to a tenant; `-o json|table` forces output.

| Command | Does |
|---|---|
| `rc login [--device]` | OAuth sign-in: PKCE loopback (browser) by default, `--device` for headless/SSH. Stores the token under the resolved profile |
| `rc logout` | revoke the profile's token server-side and clear it from the local store |
| `rc whoami` | the resolved profile/project/tenant + sign-in status (local only — no server call) |
| `rc projects` | list the fleet handles (name + id) the token can see — every project for an all-projects admin token, just its own for a pinned token |
| `rc status` | recent runs + health summary (the no-filter index view) |
| `rc ask "<q>" [--scenario email\|raw] [--from addr] [--subject s] [--session <id>] [--brain-ref <ref>] [--effort default\|pro\|max] [--no-wait] [--timeout 5m]` | trigger a run; waits by default (`--no-wait` prints the run_id). Default `--scenario email` simulates a support email and renders draft/note/actions/PR/run metadata; `--scenario raw` renders one direct answer plus actions/PR/run metadata (`mcp` is accepted as a raw alias). Inside a brain checkout, an all-projects `default` token auto-scopes to that brain; outside one, add global `--project <id-or-name>`. `--from` defaults to `rc-ask@example.test`; `--subject` defaults to a compact first line. `--session` threads the run onto a multi-turn session (see below). `--effort pro|max` forces a stronger rootcause model tier for this run; omitted/default keeps normal tier selection |
| `rc runs [--limit N] [--kind email\|prompt\|mcp\|analysis] [--category …] [--before <id>]` | filterable run list, keyset-paged |
| `rc run <id>` | one run, high level |
| `rc run <id> --events` | full per-event trace (NDJSON in JSON mode) |
| `rc run <id> --full` | the whole bundle: header (full draft/notes, system prompt, egress, trace) + per-event trace with cost/tokens. JSON mode is JSONL (`type:run` header line, then `type:event` per line) |
| `rc run <id> --debug [--out-dir <dir>]` | decompose the run into a jq-able JSONL event log + a thin markdown index on disk; prints both paths (the cross-project debug path for an all-projects admin token) |
| `rc config get` | effective settings + box defaults |
| `rc config set k=v [k=v…]` | change settings (validated server-side) |
| `rc env keys` | key NAMES of the project's production grounding env (log-safe, no values) |
| `rc env pull` | fetch that env and write a local **0600 `./.env`** (prints NAMES + count, never values) |
| `rc env diff` | compare local `./.env` to the server — NAMES-only drift, **nonzero exit on drift** |
| `rc upgrade [--check]` | self-update to the latest release (Linux/WSL/Windows); on a Homebrew install, defers to `brew upgrade rc` |
| `rc --version` · `rc help` | |

`rc ask --brain-ref dev/<branch>` runs the question against a **non-main brain ref** — the project
dev's "test without pushing main" loop. Push a `dev/*` branch to your brain first (`git push origin
dev/<branch>`); the server runs the real loop against it and flags any actions/PRs as test.

`rc ask` defaults to `--scenario email`: it sends an explicit `scenario=email` to the Prompt API and
wraps the prompt as one synthetic inbound message from `--from` with `--subject` (or a compact prompt
first line). Use this for high-fidelity brain-dev checks: tone, notes, actions, PR proposals, and
declines are rendered like a reviewable support result. Use `--scenario raw` for direct investigations;
the CLI sends `scenario=raw` and prints one Markdown answer. `--scenario mcp` is accepted as a
compatibility alias for raw, but `raw` is the documented name.

`rc ask --effort pro|max` is a per-run escalation knob. It maps to rootcause's model tiers, not raw
provider effort values; use it when you explicitly want a stronger retry. Omit it, or pass
`--effort default`, for normal behavior.

#### `--session` — multi-turn threading

`--session <id>` threads a run onto a session so follow-up turns build on the earlier ones. The id is
**client-chosen** (any stable string — `session_id` is the join key, not `run_id`); reuse the same id
across turns and the server warm-starts each follow-up off the prior turns on that session:

```bash
rc ask --session homer-42 "Hello, my name is Homer. When can I have a dental appointment?"
rc ask --session homer-42 "Okay, let's go for Thursday."   # same session → warm-started
```

The server keys continuity on `(project, session_id, kind=prompt)` and injects a digest of **what the
earlier turns ran and how each ended** (the command trail) — *not* the prior conversation or answer
text. So a follow-up knows which queries already dead-ended, but does **not** yet replay the prior
turn's answer; phrase follow-ups to stand on their own rather than referring back anaphorically ("the
second option"). `--no-wait` prints just the `run_id`; the `session_id` is the one you passed in.

Output auto-detects: **TTY → table, piped → JSON**. Force with `-o json` / `-o table`. API errors are
surfaced verbatim (`CODE: message`) with a non-zero exit.

### `rc env` — self-serve grounding-env sync

A project's grounding scripts read their credentials (PG DSN, Stripe key, …) from a gitignored `.env`
in the brain clone. `rc env` lets a **developer** sync that **production** env to their laptop over the
same OAuth token — the self-serve equivalent of the operator-only `scripts/rc_env.py --pull` (which
needs AWS/SSM access). Run it **from inside the brain clone** (it reads/writes `./.env`):

```bash
rc env keys                 # what keys exist (names only — safe to paste/log)
rc env pull                 # write ./.env at 0600 — then `brain-dev --live` can run grounding locally
rc env diff                 # has my local ./.env drifted from production? (names-only; exit≠0 on drift)
rc env pull --tenant <slug> # a tenant-enabled project: the project ∪ tenant env (tenant authoritative)
```

> **Secret hygiene:** no `rc env` subcommand ever prints a secret **value** — `pull` writes values only
> to the 0600 file and reports names + count; `keys`/`diff` are names-only in both table and JSON modes.
> The pulled `.env` holds **real production secrets** on your laptop — treat it like a password file
> (it's gitignored in every brain repo). A tenant-enabled project (e.g. dentai) requires `--tenant`.

## Releasing

Use the script — it does the whole cycle reliably and verifies each part:

```bash
scripts/release.sh patch     # 0.1.0 -> 0.1.1  (also: minor | major | vX.Y.Z | --dry-run)
```

A release is **three things that must land together**, which is why a bare `git tag` isn't enough:

1. the **git tag** `vX.Y.Z` on `main`;
2. the **GitHub Release** + prebuilt binaries — the [release workflow](.github/workflows/release.yml)
   builds every OS/arch via [GoReleaser](https://goreleaser.com) and attaches archives + checksums;
3. the **Go module proxy** ingesting the tag, so consumers' `go get …@latest` resolves the new version
   instead of a stale pseudo-version (the step that's easy to forget by hand).

The script gates on `go build/vet/test`, refuses a dirty/behind checkout, tags + pushes, waits for the
binaries, then warms the proxy. See [`.claude/skills/release/SKILL.md`](.claude/skills/release/SKILL.md)
for the full runbook and manual fallback.

**Homebrew** is wired up: each release, GoReleaser commits an updated `Casks/rc.rb` **cask** to the
public [`rootcause-org/homebrew-tap`](https://github.com/rootcause-org/homebrew-tap) repo (the
`homebrew_casks:` block in [`.goreleaser.yaml`](.goreleaser.yaml)). It authenticates with the
`HOMEBREW_TAP_GITHUB_TOKEN` repo secret — a token with `contents:write` on the tap repo, since the
default `GITHUB_TOKEN` is scoped to this repo only and can't push to a second one.

It's a **cask** (prebuilt binary), not a source **formula**, on purpose: a non-bottled formula installs
through a Homebrew sandbox + Ruby PTY that fails on some macOS setups (`can't get Master/Slave device`);
a cask just links the binary. A cask is macOS-only and can't share the name `rc` with a formula (bare
`brew install …/rc` would pick the formula — the broken path), so the old `Formula/rc.rb` was deleted
from the tap and a `tap_migrations.json` (`{"rc":"rc"}`) added there so existing installs migrate
formula→cask on `brew upgrade`. Linux/WSL/Windows install via [`scripts/install.sh`](scripts/install.sh)
and [`scripts/install.ps1`](scripts/install.ps1), which need no Homebrew.

See [`SKILL.md`](SKILL.md) for the architecture and how to add a command.
