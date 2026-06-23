# AGENTS.md — rootcause-cli (`rc`)

Start here, then open the doc the task needs. This file is a router, not a manual — the detail lives in
the two docs below and in the code.

## What this is (one line)
`rc` is a **thin, pure Go client**: every command is one call to rootcause's public `/api/v1`, authed
with an **OAuth access token** (`rc login`; refreshed transparently), rendered as a table on a TTY or
**JSON when piped**. No business logic, no DB — if a command needs logic, it belongs in rootcause first.

## Where to read
- **[README.md](README.md)** — user-facing: install, configure (brain binding + profiles), every command, releasing.
- **[SKILL.md](SKILL.md)** — architecture & intent: the API ladder, the four thin layers, config/auth precedence, scope guards. **Read before changing code.**
- **[.claude/skills/release/SKILL.md](.claude/skills/release/SKILL.md)** — the release runbook (or just run `scripts/release.sh`).
- **[docs/specs/](docs/specs/)** — feature specs (e.g. brain test runs).

## Code map (detail in SKILL.md)
- `cmd/rc/main.go` — entrypoint → `cli.Execute(version)`.
- `internal/cli/` — one cobra file per command (`status`/`runs`/`run`/`ask`/`config`/`env`/`tenant`/`auth`); `tokensource.go` is the live token source; `errors.go` surfaces API errors verbatim.
- `internal/client/` — the one HTTP wrapper (`client.go`, refresh-on-401) + `TokenSource` (`auth.go`) + wire contract (`types.go`, field names match the server exactly) + `APIError`.
- `internal/oauth/` — OAuth protocol client: PKCE loopback + device grant + refresh/revoke (first-party client `rcocl_cli`).
- `internal/token/` — token store `~/.config/rootcause/tokens.json` (0600), per-profile.
- `internal/config/` — brain-aware resolution (`.rootcause.toml` marker + env + `config.toml`) → profile + base URL.
- `internal/debugdump/` — the `rc run <id> --debug` decomposer (JSONL + thin markdown index).
- `internal/render/` — TTY-detect + JSON passthrough (`render.go`) + per-view table renderers (`table.go`).

## Working on it
- **Toolchain:** Go 1.25 via `mise` (pinned in `mise.toml`); `cobra`+`pflag`, `BurntSushi/toml`. Run from the repo dir so mise selects go 1.25.
- **Before finishing any change:** `go build ./... && go vet ./... && go test ./...`, and `gofmt -w`.
- **Golden tests** live in `internal/cli/` (fixtures `testdata/*.json` → `*.golden`); regenerate with `go test ./internal/cli -update`. Fixtures use canned timestamps — never `time.Now`.
- **Adding a command for a new endpoint:** wire struct in `internal/client/types.go` (match server JSON) → client method → render fn (+ golden) → thin cobra command. Keep it 1:1 with the endpoint.

## Scope guards (push back if asked to cross them)
No MCP in v1, no business logic / DB access, no interactive TUI. Auth is **OAuth only** against the
server's existing `/oauth/*` (the CLI invents no auth of its own). The only **server** writes are `config
set` (the settings whitelist is the boundary) and `rc ask` (`POST /api/v1/runs`); `rc env pull` writes a
local `./.env` only and never prints secret values.
