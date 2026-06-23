# rootcause-cli (`rc`)

A thin, scriptable client that lets a project **consume its own rootcause data** and **change its own
config** — over rootcause's public JSON `/api/v1`, authed with the project's existing **Prompt API
bearer key**. No business logic of its own: every command is one API call, rendered as a table on a
terminal or **JSON when piped** so `| jq` always works.

```console
$ rc status
Health: healthy

Sources:
  SOURCE      TOTAL  ERRORS
  Prompt API  12     0

$ rc ask "Do I still have open invoices?"   # trigger a run, wait, print the answer
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

## Configure

`rc` needs the project's **Prompt API bearer key** (the key resolves the project server-side — there's
no `--project` flag) and the API base URL.

**Recommended — let the brain checkout select the project.** A brain repo (`rootcause-brain-<project>`)
commits a `.rootcause.toml` binding it to one project, so `rc` run anywhere inside it auto-targets that
project — no `--profile`, no env export:

```bash
cd rootcause-brain-acme   # contains a committed .rootcause.toml: project = "acme", base_url = "…"
rc login                  # paste the key once → gitignored .rootcause.secret.toml (0600)
rc whoami                 # project: acme · key source: brain-secret · server says: acme ✓
rc ask "…"                # just works
```

Inside a brain with no key, `rc` fails **loudly** naming the project — it never silently falls back to
a different project's `[default]` key. See `.rootcause.toml` below.

**Or set it globally** (env / config profiles — also the override when inside a brain):

```bash
export ROOTCAUSE_API_KEY=rcl_…
export ROOTCAUSE_BASE_URL=https://your-rootcause-host   # default: http://localhost:8080
```

Or use `~/.config/rootcause/config.toml` with named profiles (`--profile <name>`):

```toml
[default]
api_key  = "rcl_…"
base_url = "https://your-rootcause-host"

[profiles.staging]
api_key  = "rcl_…"
base_url = "https://staging.your-rootcause-host"
```

**`.rootcause.toml`** (committed, per brain) names the project and endpoint — `project` + `base_url`,
no secret. **`.rootcause.secret.toml`** (gitignored, written by `rc login`) holds the `api_key`. Keep
the secret file out of git; `.rootcause.toml` is safe to commit and is what ships the binding with a
clone.

Precedence per field (an env var always wins as a one-off override):

```
explicit --profile <name>        → that profile only (no brain binding)
otherwise, inside a brain:         env > .rootcause.secret.toml > [profiles.<project>] > LOUD ERROR
otherwise, outside any brain:      env > [default] > built-in default
```

A typo'd `--profile` errors rather than silently falling through to env. Keys live in env / the secret
file / config **by name** — never commit them.

## Commands

| Command | Does |
|---|---|
| `rc login [--api-key <k>] [--no-verify]` | store this brain's key in a gitignored `.rootcause.secret.toml` (verifies it matches `.rootcause.toml`) |
| `rc whoami [--no-verify]` | which project `rc` targets from here: brain binding, base URL, key source (+ server confirm) |
| `rc status` | recent runs + health summary (the no-filter index view) |
| `rc ask "<q>" [--session <id>] [--brain-ref <ref>] [--tenant <slug>] [--no-wait] [--timeout 5m]` | trigger a run; waits for the answer by default (`--no-wait` prints the run_id). `--session` threads the run onto a multi-turn session (see below) |
| `rc runs [--limit N] [--kind email\|prompt\|mcp\|analysis] [--category …] [--before <id>]` | filterable run list, keyset-paged |
| `rc run <id>` | one run, high level |
| `rc run <id> --events` | full per-event trace (NDJSON in JSON mode) |
| `rc run <id> --full` | the whole bundle: header (full draft/notes, system prompt, egress, trace) + per-event trace with cost/tokens. JSON mode is JSONL (`type:run` header line, then `type:event` per line) |
| `rc config get` | effective settings + box defaults |
| `rc config set k=v [k=v…]` | change settings (validated server-side) |
| `rc env keys [--tenant <slug>]` | key NAMES of the project's production grounding env (log-safe, no values) |
| `rc env pull [--tenant <slug>]` | fetch that env and write a local **0600 `./.env`** (prints NAMES + count, never values) |
| `rc env diff [--tenant <slug>]` | compare local `./.env` to the server — NAMES-only drift, **nonzero exit on drift** |
| `rc upgrade [--check]` | self-update to the latest release (Linux/WSL/Windows); on a Homebrew install, defers to `brew upgrade rc` |
| `rc --version` · `rc help` | |

`rc ask --brain-ref dev/<branch>` runs the question against a **non-main brain ref** — the project
dev's "test without pushing main" loop. Push a `dev/*` branch to your brain first (`git push origin
dev/<branch>`); the server runs the real loop against it and flags any actions/PRs as test.

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
same Prompt API key — the self-serve equivalent of the operator-only `scripts/rc_env.py --pull` (which
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
