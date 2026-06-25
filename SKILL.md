---
name: rootcause-cli
description: The `rc` CLI — a scriptable Go client that lets a project consume its OWN rootcause data and change its own config over rootcause's public JSON /api/v1, authed with an OAuth access token (sign in via `rc login`; the CLI refreshes it). Use when working in this repo: adding/changing a command, the HTTP client, OAuth/token-store/config resolution, or the table/JSON render layer; or when wiring a new endpoint the API already serves. Fat client, thin server: endpoints return raw token-scoped data, the CLI may digest/cluster/render it locally, and `-o json` always exposes the raw rows.
---

# rootcause-cli (`rc`) — a project's window into its own rootcause data

`rc` is a **fat client over a thin server**: rootcause endpoints stay simple and return **raw,
token-scoped data**; `rc` may compute views on top of it locally (digests, clustering, health roll-ups,
diagnosis) — and every such command still exposes the raw rows via `-o json`, so a consumer can ignore
our rendering and slice the data themselves. It holds **no DB access** — data comes only through the
public `/api/v1`, with an **OAuth access token** minted by `rc login` (the token resolves the caller's
project + principal server-side: a pinned token scopes to one project, an all-projects admin token reads
cross-project).
`--profile` picks *which stored token* to use; the token's project scope is baked in at consent time.
`--project <id-or-name>` is a different lever — a **server-side scope**, not a token selector: it keeps
the active token and names one project on supported endpoints (`?project=`), letting an **all-projects
admin token** review a single project (`rc fleet --project momentum-tools`) or trigger one (`rc ask
--project momentum-tools "…"`). A pinned token disregards it (it can't widen its own scope). The bet: a dev pulls this in to slice their data the way
they prefer (`| jq`, scripts, a quick `rc run <id>`) and, before authoring an action/skill, runs
`rc runs` → `rc run <id> --events` to **verify against real runs** — the author→verify loop taught in
[rootcause-brain-skills/docs/rc-cli.md](../rootcause-brain-skills/docs/rc-cli.md).

## The ladder (progressive disclosure — index → one run → detail)

The base rungs are one endpoint each (index → one run → detail). Higher-level commands (digests,
patterns, health, thread trace) may fan out over several raw endpoints and compute the view locally —
but they keep the raw rows reachable via `-o json`.

| Command | Endpoint | What |
|---|---|---|
| `rc ask "<q>"` | `POST /api/v1/runs` | trigger a run from a question, then poll to the answer (the ONE server-write trigger; supports `--scenario email|raw`, `--project` for all-projects admin tokens, and `--effort default|pro|max`; see below) |
| `rc projects` | `GET /api/v1/projects` | list the fleet handles (name + id) the token can see — every project for an all-projects admin token, just its own for a pinned token; the seed for the `--all` fan-out |
| `rc status` / `rc runs` | `GET /api/v1/runs` | index: recent runs + health summary (the [runs-index-api](../rootcause/.agents/skills/features/runs-index-api.md)) |
| `rc run <id>` | `GET /api/v1/runs/{id}` | one run, high level |
| `rc run <id> --events` | `GET /api/v1/runs/{id}/events` | full per-event trace (NDJSON in JSON mode) |
| `rc run <id> --full` | `GET /api/v1/runs/{id}/full` | the whole bundle (header + per-event trace + cost); JSONL in JSON mode |
| `rc run <id> --debug` | `GET /api/v1/runs/{id}/full` | decompose to a jq-able JSONL + thin markdown index on disk (see below) |
| `rc config get` / `set k=v` | `GET` / `PATCH /api/v1/settings` | read / change the self-service settings whitelist |
| `rc env keys` / `pull` / `diff` | `GET /api/v1/env` | sync the project's PRODUCTION grounding `.env` to a local 0600 `./.env` — the self-serve, OAuth-authed twin of operator `scripts/rc_env.py --pull/--keys/--verify` |
| `rc login` / `logout` / `whoami` | `/oauth/*` (+ local) | OAuth sign-in / revoke / local status (see Auth below) |

`rc status` and `rc runs` are the **same endpoint** — status is the no-filter view (leads with the
health summary), `runs` leads with the filterable table (`--limit`/`--kind`/`--category`/`--before`).

`rc ask` ([ask.go](internal/cli/ask.go)) is the one **trigger**: it `POST`s the prompt to `/api/v1/runs`,
then by default polls `/runs/{id}` to a terminal status and renders by scenario (`--no-wait` prints the
`run_id` and returns; JSON echoes the verbatim 202 body so `jq -r .run_id` works). It stays thin —
submit + poll + render; all run logic is server-side. The CLI always sends an explicit `scenario`:
`email` by default, or `raw` (`mcp` accepted as a raw alias). `email` wraps the prompt as a synthetic
inbound support message with `sender` from `--from` (default `rc-ask@example.test`) and `subject` from
`--subject` or a compact first line, then table-renders draft, notes, actions, PR, and run metadata
(using `/runs/{id}/full` when available). `raw` omits default email fields and table-renders one direct
answer plus actions, PR, and run metadata. `--project <id-or-name>` rides as `?project=` on submit,
letting an all-projects admin token trigger a selected project while a pinned token keeps its own
server-side scope. `--session <id>` carries a **client-chosen** `session_id` (the multi-turn join key —
*not* `run_id`); the server keys continuity on `(project, session_id, kind=prompt)` and warm-starts each
follow-up off the prior turns' command trail (see
[multi_turn_warm_start.md](../rootcause/.agents/skills/features/multi_turn_warm_start.md) — the prior
*answer* is not yet replayed for prompt/mcp). `--brain-ref dev/<branch>` runs against a non-main brain
ref (a test run); `--effort pro|max` sends `reasoning_effort` to force a stronger rootcause model tier
for this run (omitted/default keeps normal tier selection); `--tenant <slug>` binds a tenant.

`rc env` is the one place the CLI deliberately **does not** pass the server body through: `GET
/api/v1/env` returns live secret VALUES, so `env.go` reshapes to NAMES only for `keys`/`diff`, and
`pull` writes the values solely to the 0600 `./.env` (never stdout). It also writes a local file — the
only filesystem write in the CLI — but performs **no server write** (it's a GET), so the read-only-API
scope guard holds.

## Architecture — four thin layers, no logic

```
cmd/rc/main.go            → cli.Execute(version)
internal/cli/             cobra commands; one file per command (root/status/runs/run/config/env/auth).
                          A command = parse flags → one client call → render. errors.go surfaces
                          the API's {code,message,fields} VERBATIM to stderr, exit 1. tokensource.go is
                          the live client.TokenSource (store + refresh policy).
internal/client/          the ONE http wrapper (client.go: refresh-on-401 retry) + the TokenSource
                          interface (auth.go) + the wire contract (types.go) + APIError (errors.go).
                          One method per endpoint; types.go field names MUST match the server verbatim.
internal/oauth/           the OAuth protocol client: PKCE loopback (loopback.go) + device grant
                          (device.go) + refresh/revoke/token exchange (oauth.go) + browser opener.
internal/token/           the token store: ~/.config/rootcause/tokens.json (0600), per-profile.
internal/config/          resolution: brain marker (.rootcause.toml) + env + config.toml → profile + base URL.
internal/debugdump/       the rc-agent-debug decomposer: decorate + emit JSONL + render thin index.
internal/render/          render.go (TTY-detect + JSON passthrough) + table.go (one renderer per view).
```

### Output: pipe-first, TTY-aware
`render.IsJSON(mode, w)` — `-o json`/`-o table` wins; else **JSON unless stdout is a terminal**. So a
TTY gets a table; a pipe/redirect gets JSON (`rc runs | jq …` always works). JSON mode is a **verbatim
pretty-print of the server body** (re-indent only), so jq sees the true response shape — the CLI can't
invent or drop a field. `rc run --events -o json` emits **NDJSON** (one event per line), not an array.

### Auth (OAuth) — login, token store, transparent refresh
OAuth is the **only** bearer credential (the legacy `rcl_` key, `ROOTCAUSE_API_KEY`, and
`.rootcause.secret.toml` are gone). The shape:

- **`rc login`** ([auth.go](internal/cli/auth.go)) runs a flow in `internal/oauth` against the static
  first-party client `rcocl_cli`: **PKCE loopback** by default (bind a localhost port, open the browser
  at `/oauth/authorize`, catch `http://127.0.0.1:<port>/callback`, exchange the code — the loopback
  redirect is port-insensitive server-side per RFC 8252), or **`--device`** (RFC 8628: print a code,
  poll `/oauth/token`). The **project scope is chosen on the browser consent screen**, not the CLI.
- **Token store** (`internal/token`): `~/.config/rootcause/tokens.json` (0600), keyed by profile —
  `{access_token, refresh_token, expires_at, base_url}`. `rc logout` revokes server-side + clears it.
- **Transparent refresh**: `client.Client` takes a `TokenSource`; `tokensource.go`'s `liveSource` reads
  the profile's token, refreshes pre-emptively within 60s of expiry (and on a 401, the client retries
  once after a forced refresh), and **persists the rotated pair**. A dead refresh (`invalid_grant`)
  surfaces as a "run `rc login`" prompt. All refresh policy lives in `liveSource` — the client stays
  OAuth-oblivious. Tests inject `client.StaticToken` to bypass the store.

### Config & profile precedence
In `internal/config` (`profiles.go`), resolution is **brain-aware** and picks a **profile name** (the
token-store key) + a **base URL** — no secret. `Load(profile)` (note: `--project` is **not** an input —
it's a server-side scope the command layer threads onto each read request, never a token selector):

- **explicit `--profile <name>`** → that profile, no brain binding (the override escape hatch);
- **inside a brain** → first try the brain marker's project as the profile; if no token exists for it,
  fall back to `"default"` and carry the marker's project as `?project=` on supported endpoints;
- **outside any brain** → `"default"`.

`--project <id-or-name>` rides as `?project=` on supported per-project endpoints (run index, feeds,
health, thread-trace, prompt submit, env, and settings); an all-projects admin token uses it to scope
one project, a pinned token disregards it server-side. The brain→default fallback sets this scope
implicitly from `.rootcause.toml`. See `env.scopeProject` in `internal/cli/root.go`.

**Fleet-wide `--all`** (`rc fleet`/`patterns`/`health`) is the FAT-CLIENT fan-out that complements
`--project`: it lists the fleet via `rc projects`, then calls the per-project read endpoint once per
project with `?project=<id>`, and merges the results — grouped-per-project with a fleet total (`fleet`),
a clustered section per project (`patterns`), or a per-project verdict whose worst case sets the exit
code (`health`). `-o json` emits the merged `{projects:[…]}` structure. `--all` needs an all-projects
token: against a project-scoped token (the fleet list returns one project) it fails with a friendly,
named error rather than silently running just that one. The per-project endpoints stay thin and raw —
the fan-out + grouping live entirely in the CLI (`runFleetAll`/`runPatternsAll`/`runHealthAll`,
`fanOutProjects` in `internal/cli`).

**`rc fleet` aggregates** (all computed fat-client in `internal/render/fleet.go`, pure functions of
the `/api/v1/runs` rows): the default human digest is the per-run flag table + rates + worst
offenders; **`--by-model`** adds the model×cost×**fallback** breakdown (the highest-value view — which
model burned the spend and how much was a fallback), **`--timeline`** adds the per-day
runs/errors/cost histogram. Stuck/running runs and a `FB` model-fallback flag are inline; offender
lines carry the full triage tail. The fallback signal is the server's clean `run_health.is_fallback`
boolean (it bakes around the `runs.model_fallback_from` empty-string-vs-NULL trap — see the `support`
skill's `db-reference.md`); each row's `is_fallback`/`planned_model` ride raw in `-o json`.

Base URL per field: `ROOTCAUSE_BASE_URL` > marker `base_url` > `[profiles.<name>] base_url` > built-in
production default (`https://rootcause.probackup.io`). A stored token also pins the issuer it was minted against, so commands
hit the same server. `Resolved` carries `Profile`/`Project`/`Brain` so `root.go` crafts the loud error
and `rc whoami` explains the binding (locally — there is no server identity endpoint yet). Honors
`XDG_CONFIG_HOME`. The committed marker is non-secret; tokens live only in the 0600 token store.

### The `--debug` decomposer (`internal/debugdump`)
`rc run <id> --debug` ports rootcause's `rc_agent_debug.py` to Go: it pulls `/full` (cross-project for an
all-projects admin token) and writes two files to `--out-dir` (default `.rootcause/debug/`) — a **jq-able JSONL**
event log and a **thin markdown index** — then prints both paths. It does NOT summarize the run into
stdout: the calling agent reads the index, then drills into the JSONL with its own bash/jq. The JSONL
contract is kept compatible with the Python/shared-runtime renderer: line 1 is a `{"type":"run"…}`
header, every later line a `{"type":"event"…}` keyed by `disp` (grounding pre-steps `P1,P2,…`; the main
loop `1,2,…`), so existing jq recipes (`select(.disp=="23").command`) keep working. `decorate` derives
disp/grounding/label/command/gist; `emit.go` writes the JSONL + the index (timeline, flags, files read,
egress, example jq calls). One shape note vs the operator dump: the JSONL `egress` carries the API's
aggregated rollup (`{host, count, blocked}`), not the operator dump's per-row `{decision, port, …}` —
the per-event drill-down keys are identical, only egress differs.

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
  NDJSON shape, the `--debug` decomposer (golden index + JSONL), the API-error path (verbatim + exit),
  the not-logged-in error, and the typed-error contract. Auth is exercised end-to-end against a stub
  OAuth server: device-flow login, transparent refresh (incl. rotation + a dead-token re-login prompt),
  logout/revoke, and (in `internal/oauth`) the PKCE loopback flow. The token store + `internal/token`
  have their own tests. Tests bypass the store via `client.StaticToken`/`tokenOvr`, or seed the store
  directly. Regenerate goldens with `go test ./internal/cli -update`; fixtures use **canned** timestamps,
  never `time.Now`.
- **Adding a command for a new endpoint:** add the wire struct to `internal/client/types.go` (match the
  server JSON exactly), a client method, a render function (+ golden fixture/test), and a cobra command.
  Simple rungs stay 1:1 with one endpoint; a higher-level command may call several raw endpoints and
  compute its view locally — keep the endpoints themselves thin, and always expose the raw rows via
  `-o json`. DB access stays out of the CLI; data comes only through `/api/v1`.

## The one non-API command: `rc upgrade`

[`internal/cli/upgrade.go`](internal/cli/upgrade.go) is the deliberate exception to "every command is
one API call": it talks to the **GitHub releases** API (not the rootcause API, no bearer key), then
self-replaces its own binary with the latest archive for the running OS/arch (sha256-verified against
the release's `checksums.txt`, atomic same-dir rename). It's CLI plumbing, not business logic. On a
Homebrew install (`isHomebrewManaged` — path under `/Caskroom/` or `/Cellar/`) it refuses and defers to
`brew upgrade rc`, so it never desyncs the cask manifest. The pure helpers (version compare, asset name,
checksum parse, brew-path detection) are unit-tested in `upgrade_test.go`; the network/replace path is
verified by hand against a real release. Keep this the *only* command that reaches outside `/api/v1`.

## Scope guards (push back if asked)

No MCP in v1 (a future layer over the same endpoints). Client-side analysis/rendering is fine, but **no
direct DB access** — data comes only through `/api/v1`, and the endpoints behind it stay thin (raw rows,
not server-computed views), with `-o json` always exposing those rows. The only **server** write surfaces are `config set` (the settings whitelist IS the
boundary) and `rc ask` (triggers a run via `POST /api/v1/runs` — the CLI still holds no run logic; the
server owns the loop, and `ask` never sends actions/mail itself). `rc env pull` writes a LOCAL `./.env`
only — still a GET against the API. Auth is **OAuth only**, against the server's existing `/oauth/*`
endpoints + the static first-party CLI client — the CLI invents no auth of its own (no new grant types,
no token minting beyond the standard flows). No interactive TUI/dashboard — scriptable, pipe-first,
headless.
