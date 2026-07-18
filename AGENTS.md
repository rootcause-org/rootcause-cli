# AGENTS.md — rootcause-cli (`rc`)

Start here, then open the doc the task needs. This file is a router, not a manual — the detail lives in
the two docs below and in the code.

## What this is (one line)
`rc` is a **scriptable Go client**: it talks to rootcause's public `/api/v1` authed with an **OAuth
access token** (`rc auth login`; refreshed transparently), rendered as a table on a TTY or **JSON when piped**.
**Fat client, thin server:** endpoints stay simple and return **raw, token-scoped data**; presentation
and analysis logic (digests, clustering, health roll-ups, diagnosis) is allowed to live **in the CLI**.
Every such command MUST still expose the raw rows via `-o json`, so a consumer can skip our rendering and
do their own thing. No DB access in the CLI — data comes only through `/api/v1`.

## Where to read (pick by task)
- **Running/scripting `rc`, install, a command's flags, releasing** → **[README.md](README.md)** (the user manual; command inventory is generated from Cobra).
- **Changing code** — a command, the HTTP client, OAuth/token/config resolution, render, or output spill → **[SKILL.md](SKILL.md)** first (architecture & intent: the API ladder, the four thin layers, config/auth precedence, the full "adding a command" recipe).
- **Cutting a release** → run `scripts/release.sh`, or read **[.claude/skills/release/SKILL.md](.claude/skills/release/SKILL.md)** for the runbook.
- **A feature's design** → **[docs/specs/](docs/specs/)** (`brain-test-runs.md`, `progressive-output-disclosure.md`).

## Code map (detail in SKILL.md)
- `cmd/rc/main.go` — entrypoint → `cli.Execute(version)`.
- `internal/cli/` — `surface.go` owns the nine roots; command files implement their grouped endpoint adapters. `tokensource.go` is the live token source; `errors.go` surfaces API errors verbatim.
- `internal/client/` — the one HTTP wrapper (`client.go`, refresh-on-401) + `TokenSource` (`auth.go`) + wire contract (`types.go`, field names match the server exactly) + `APIError`.
- `internal/oauth/` — OAuth protocol client: PKCE loopback + device grant + refresh/revoke (first-party client `rcocl_cli`).
- `internal/token/` — token store `~/.config/rootcause/tokens.json` (0600), per-profile.
- `internal/config/` — env-or-production URL resolution + brain-aware profile/project/tenant context (`.rootcause.toml` + `.rootcause/local.toml`).
- `internal/debugdump/` — the `rc run debug <id>` decomposer (JSONL + thin markdown index).
- `internal/outputspill/` — progressive output disclosure: large stdout/JSON/JSONL spills to `.rootcause/output/` (`--out-dir`/`RC_OUTPUT_DIR`), stdout gets a preview/manifest; `--no-preview`/`--raw-output` tune it.
- `internal/render/` — TTY-detect + JSON passthrough (`render.go`) + per-view table renderers (`table.go`).
- `internal/dnsdetect/` + `internal/idutil/` — local, offline helpers behind `rc dev tools provider|id`.

## Working on it
- **Toolchain:** Go 1.25 via `mise` (pinned in `mise.toml`); `cobra`+`pflag`, `BurntSushi/toml`. Run from the repo dir so mise selects go 1.25.
- **Before finishing any change:** `go build ./... && go vet ./... && go test ./...`, and `gofmt -w`.
- **Golden + generated docs:** fixtures `internal/cli/testdata/*.json` → `*.golden`; the README command inventory and `docs/cli-help.txt` are test-guarded. After changing any command/flag/short, regenerate all of it with `go test ./internal/cli -update`. Fixtures use canned timestamps — never `time.Now`.
- **Greenfield release bias:** verified + low regression risk ⇒ release immediately, then update local `rc`.
- **Adding a command / the four thin layers / config precedence** → see [SKILL.md](SKILL.md).

## Scope guards (push back if asked to cross them)
No MCP in v1, no direct DB access (data comes via `/api/v1`), no interactive TUI. Client-side
analysis/rendering is fine; keep the server endpoints **thin and raw** (raw rows, not server-computed
views) with `-o json` always exposing them. Auth is **OAuth only** against the server's existing
`/oauth/*` + the first-party client `rcocl_cli` — the CLI invents no auth of its own. Writes go **only**
through endpoints the public `/api/v1` already serves (config/resource CRUD, run triggers/feedback,
brain promote/publish/edit, the guarded `dev console` write plane, `admin`); the CLI adds no new server
endpoint. `rc project env pull` writes a local `./.env` only (a GET) and never prints secret values.
