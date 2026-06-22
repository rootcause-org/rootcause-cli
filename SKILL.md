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
| `rc ask "<q>"` | `POST /api/v1/runs` | trigger a run from a question, then poll to the answer (the ONE server-write trigger; see below) |
| `rc status` / `rc runs` | `GET /api/v1/runs` | index: recent runs + health summary (the [runs-index-api](../rootcause/.agents/skills/features/runs-index-api.md)) |
| `rc run <id>` | `GET /api/v1/runs/{id}` | one run, high level |
| `rc run <id> --events` | `GET /api/v1/runs/{id}/events` | full per-event trace (NDJSON in JSON mode) |
| `rc config get` / `set k=v` | `GET` / `PATCH /api/v1/settings` | read / change the self-service settings whitelist |
| `rc env keys` / `pull` / `diff` | `GET /api/v1/env` | sync the project's PRODUCTION grounding `.env` to a local 0600 `./.env` — the self-serve, key-authed twin of operator `scripts/rc_env.py --pull/--keys/--verify` |

`rc status` and `rc runs` are the **same endpoint** — status is the no-filter view (leads with the
health summary), `runs` leads with the filterable table (`--limit`/`--kind`/`--category`/`--before`).

`rc ask` ([ask.go](internal/cli/ask.go)) is the one **trigger**: it `POST`s the prompt to `/api/v1/runs`,
then by default polls `/runs/{id}` to a terminal status and renders the answer like `rc run <id>`
(`--no-wait` prints the `run_id` and returns; JSON echoes the verbatim 202 body so `jq -r .run_id`
works). It stays thin — submit + poll + render; all run logic is server-side. `--session <id>` carries a
**client-chosen** `session_id` (the multi-turn join key — *not* `run_id`); the server keys continuity on
`(project, session_id, kind=prompt)` and warm-starts each follow-up off the prior turns' command trail
(see [multi_turn_warm_start.md](../rootcause/.agents/skills/features/multi_turn_warm_start.md) — the
prior *answer* is not yet replayed for prompt/mcp). `--brain-ref dev/<branch>` runs against a non-main
brain ref (a test run); `--tenant <slug>` binds a tenant.

`rc env` is the one place the CLI deliberately **does not** pass the server body through: `GET
/api/v1/env` returns live secret VALUES, so `env.go` reshapes to NAMES only for `keys`/`diff`, and
`pull` writes the values solely to the 0600 `./.env` (never stdout). It also writes a local file — the
only filesystem write in the CLI — but performs **no server write** (it's a GET), so the read-only-API
scope guard holds.

## Architecture — four thin layers, no logic

```
cmd/rc/main.go            → cli.Execute(version)
internal/cli/             cobra commands; one file per command (root/status/runs/run/config/env).
                          A command = parse flags → one client call → render. errors.go surfaces
                          the API's {code,message,fields} VERBATIM to stderr, exit 1.
internal/client/          the ONE http wrapper (client.go) + the wire contract (types.go) + APIError
                          (errors.go). One method per endpoint; types.go field names MUST match the
                          server verbatim — the CLI never reshapes data.
internal/config/          resolution: brain marker (.rootcause.toml) + secret + env + config.toml.
internal/render/          render.go (TTY-detect + JSON passthrough) + table.go (one renderer per view).
```

### Output: pipe-first, TTY-aware
`render.IsJSON(mode, w)` — `-o json`/`-o table` wins; else **JSON unless stdout is a terminal**. So a
TTY gets a table; a pipe/redirect gets JSON (`rc runs | jq …` always works). JSON mode is a **verbatim
pretty-print of the server body** (re-indent only), so jq sees the true response shape — the CLI can't
invent or drop a field. `rc run --events -o json` emits **NDJSON** (one event per line), not an array.

### Config & auth precedence
In `internal/config` (`profiles.go`), resolution is **brain-aware**: `Load("")` walks up from cwd for a
committed `.rootcause.toml` marker (`project` + `base_url`) and binds to that project. Per field, an env
var (`ROOTCAUSE_API_KEY` / `ROOTCAUSE_BASE_URL`) always wins; then:

- **explicit `--profile <name>`** → that profile only, no brain binding (the override escape hatch);
- **inside a brain** → env > `.rootcause.secret.toml` (`api_key`, gitignored, written by `rc login`) >
  `[profiles.<project>]` > **a hard error naming the project** — it must NOT silently fall back to
  `[default]` (the footgun: running `rc` in one brain quietly hitting another project);
- **outside any brain** → env > `[default]` > built-in default (`base_url` → `http://localhost:8080`;
  `api_key` has no default → hard error).

`Resolved` carries `Project`/`Brain`/`KeySource` so `root.go` can craft the loud error and `rc whoami`
can explain the binding. A typo'd named profile errors (never silently the wrong server). Honors
`XDG_CONFIG_HOME`. The committed marker is non-secret; keys live in env / `.rootcause.secret.toml` /
config **by name, never committed**. `rc login`/`rc whoami` live in `auth.go`.

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
no DB access. The only **server** write surfaces are `config set` (the settings whitelist IS the
boundary) and `rc ask` (triggers a run via `POST /api/v1/runs` — the CLI still holds no run logic; the
server owns the loop, and `ask` never sends actions/mail itself). `rc env pull` writes a LOCAL `./.env`
only — still a GET against the API. No new auth mechanism. No interactive TUI/dashboard — scriptable,
pipe-first, headless.
