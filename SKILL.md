---
name: rootcause-cli
description: The `rc` CLI — a thin, scriptable Go client that lets a project consume its OWN rootcause data and change its own config over rootcause's public JSON /api/v1, authed with the project's Prompt API bearer key. Use when working in this repo: adding/changing a command, the HTTP client, the config/profile resolution, or the table/JSON render layer; or when wiring a new endpoint the API already serves. No business logic lives here — every command is one API call rendered for humans or piped as JSON.
---

# rootcause-cli (`rc`) — a project's window into its own rootcause data

`rc` is a **pure client**: every capability is a JSON endpoint that rootcause serves first, and
`rc` just *renders* it. It holds **no business logic, no DB access, no new auth** — it speaks the public
`/api/v1` with the project's existing **Prompt API bearer key** (the key resolves the project
server-side, so there is no `--project` flag). The bet: a dev pulls this in to slice their data the way
they prefer (`| jq`, scripts, a quick `rc run <id>`) and, before authoring an action/skill, runs
`rc runs` → `rc run <id> --events` to **verify against real runs** — the author→verify loop taught in
[rootcause-brain-skills/docs/rc-cli.md](../rootcause-brain-skills/docs/rc-cli.md).

## The ladder (progressive disclosure — index → one run → detail)

Each rung is one endpoint; one command per rung. The CLI mirrors the API ladder exactly.

| Command | Endpoint | What |
|---|---|---|
| `rc status` / `rc runs` | `GET /api/v1/runs` | index: recent runs + health summary (the [runs-index-api](../rootcause/.agents/skills/features/runs-index-api.md)) |
| `rc run <id>` | `GET /api/v1/runs/{id}` | one run, high level |
| `rc run <id> --events` | `GET /api/v1/runs/{id}/events` | full per-event trace (NDJSON in JSON mode) |
| `rc config get` / `set k=v` | `GET` / `PATCH /api/v1/settings` | read / change the self-service settings whitelist |

`rc status` and `rc runs` are the **same endpoint** — status is the no-filter view (leads with the
health summary), `runs` leads with the filterable table (`--limit`/`--kind`/`--category`/`--before`).

## Architecture — four thin layers, no logic

```
cmd/rc/main.go            → cli.Execute(version)
internal/cli/             cobra commands; one file per command (root/status/runs/run/config).
                          A command = parse flags → one client call → render. errors.go surfaces
                          the API's {code,message,fields} VERBATIM to stderr, exit 1.
internal/client/          the ONE http wrapper (client.go) + the wire contract (types.go) + APIError
                          (errors.go). One method per endpoint; types.go field names MUST match the
                          server verbatim — the CLI never reshapes data.
internal/config/          env + ~/.config/rootcause/config.toml profile resolution.
internal/render/          render.go (TTY-detect + JSON passthrough) + table.go (one renderer per view).
```

### Output: pipe-first, TTY-aware
`render.IsJSON(mode, w)` — `-o json`/`-o table` wins; else **JSON unless stdout is a terminal**. So a
TTY gets a table; a pipe/redirect gets JSON (`rc runs | jq …` always works). JSON mode is a **verbatim
pretty-print of the server body** (re-indent only), so jq sees the true response shape — the CLI can't
invent or drop a field. `rc run --events -o json` emits **NDJSON** (one event per line), not an array.

### Config & auth precedence
In `internal/config`: a non-empty value in the selected profile of `config.toml` **overrides** the env
var (`ROOTCAUSE_API_KEY` / `ROOTCAUSE_BASE_URL`), which overrides the built-in default (`base_url` →
`http://localhost:8080`; `api_key` has no default → hard error). `--profile <name>` selects `[default]`
or `[profiles.<name>]`; a typo'd named profile errors (never silently falls through to the wrong
server). Honors `XDG_CONFIG_HOME`. Keys live in env/config **by name, never committed**.

### Errors
Any non-2xx → the client decodes `{"error":{code,message,fields?}}` into a typed `APIError` and the CLI
prints `CODE: message` to stderr (exit 1); `INVALID_SETTINGS` field errors print one indented line each.
A non-decodable body falls back to `error: HTTP <status>` — still a clean non-zero exit, never a panic.

## Working on it

- **Toolchain:** Go 1.25 via `mise` (`mise.toml` pins it). `cobra`+`pflag`, `BurntSushi/toml`. Build/run
  from the repo dir so mise selects go 1.25.
- **Before finishing any change:** `go build ./...`, `go vet ./...`, `go test ./...`, `gofmt -w`.
- **Tests** (`internal/cli/`): golden-file tests for every table renderer + JSON-passthrough round-trip,
  driven by an `httptest` stub returning canned fixtures (`testdata/*.json` → `*.golden`), plus the
  NDJSON shape, the API-error path (verbatim + exit), the no-key error, and the typed-error contract.
  Regenerate goldens with `go test ./internal/cli -update`. Goldens are stable: fixtures use **canned**
  timestamps, never `time.Now`. Tests inject the base URL + force the output mode rather than relying on
  TTY detection.
- **Adding a command for a new endpoint:** add the wire struct to `internal/client/types.go` (match the
  server JSON exactly), a client method, a render function (+ golden fixture/test), and a thin cobra
  command. Keep it 1:1 with the endpoint — anything that needs logic belongs in rootcause first.

## Scope guards (push back if asked)

No MCP in v1 (a future layer over the same endpoints — keep commands mappable 1:1). No business logic /
no DB access. No write surface beyond `config set` (the settings whitelist IS the boundary — the CLI
never triggers runs/actions/mail). No new auth mechanism. No interactive TUI/dashboard — scriptable,
pipe-first, headless.
