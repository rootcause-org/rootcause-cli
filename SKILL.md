---
name: rootcause-cli
description: "The `rc` CLI — a scriptable Go client that lets a project consume its OWN rootcause data and change its own config over rootcause's public JSON /api/v1, authed with an OAuth access token (sign in via `rc auth login`; the CLI refreshes it). Read before changing code: adding/changing a command, the HTTP client, OAuth/token-store/config resolution, or the table/JSON render layer. Fat client, thin server: endpoints return raw token-scoped data, the CLI may digest/cluster/render it locally, and `-o json` always exposes the raw rows."
---

# rootcause-cli (`rc`) — architecture & intent

`rc` is a **fat client over a thin server**. rootcause endpoints stay simple and return **raw,
token-scoped data**; `rc` may compute views on top locally (digests, clustering, health roll-ups,
diagnosis) — and every such command still exposes the raw rows via **`-o json`**, so a consumer can
ignore our rendering and slice the data themselves. `rc` holds **no DB access** — data comes only through
`/api/v1`, authed with an **OAuth access token** (`rc auth login`; refreshed transparently).

The full command reference lives in **[README.md](README.md)** (generated inventory + per-command help).
This doc is the map, the invariants, and the workflow for changing code. **[AGENTS.md](AGENTS.md)** is the
router.

## Command surface

Nine stable, offline roots, assembled in [`internal/cli/surface.go`](internal/cli/surface.go) and grouped
in Cobra help as **Start** (`status`, `ask`, `run`), **Manage** (`project`), **Develop** (`dev`),
**Operate** (`fleet`, `admin`), **Local** (`auth`, `self`). Framework help/completion commands do not
appear as extra roots; completion lives at `rc self completion`.

## The ladder (progressive disclosure: index → one run → detail)

The spine is one endpoint per rung, so an agent verifies against real runs before authoring a skill:

| Rung | Command | Endpoint |
|---|---|---|
| index | `rc status` / `rc run list` | `GET /api/v1/runs` (same endpoint; `status` is a fixed 5-row health-led page, `run list` the filterable table) |
| one run | `rc run show <id>` | `GET /api/v1/runs/{id}` |
| detail | `rc run events <id>` | `GET /api/v1/runs/{id}/events` (**NDJSON** in `-o json`) |
| bundle | `rc run trace <id>` | `GET /api/v1/runs/{id}/trace` (header + per-event trace + cost; JSONL in `-o json`) |
| decompose | `rc run debug <id>` | `/trace` → local jq-able JSONL + thin markdown index ([see below](#the-rc-run-debug-decomposer)) |

`run list` filters (`--limit`/`--kind`/`--category`/`--outcome`/`--learning[=signal]`/`--before`) are
server-side so cursor pagination stays correct. Bare `--learning` means `any`; explicit values are
`feedback`, `sent_delta`, `triage_skipped`, `triage_corrected`.

**Higher-level commands fan out** over several raw endpoints and compute the view in the CLI, but always
keep the raw rows reachable via `-o json`:
- `rc fleet runs|patterns|health` — observability digests ([`internal/render/fleet.go`](internal/render/fleet.go),
  `patterns.go`, `health.go`); pure functions of the `/api/v1/{runs,health}` rows.
- `rc run thread <id>` / `rc run process-thread <id>` — thread/session trace and set-aside-thread resume.
- `rc dev learning evidence` — heterogeneous dream-cycle evidence, **JSON-only** (`--plane`, `--include-bodies`).
- `rc project knowledge content search|export` — progressive KB discovery over the guarded bash workspace
  (ranked metadata to stdout, full articles spilled to a local artifact folder).

### Server writes
The invariant is not a fixed list of write commands — it is that **every write goes through an endpoint the
public `/api/v1` already serves**. The CLI adds no server endpoint and no auth of its own; a write command
is just a typed adapter over an existing `POST`/`PATCH`/`DELETE`. So the surface grows freely (config,
tenant/connection/token/member/repo/branding writes, `rc ask`, brain promote/publish/edit, the guarded DB
write plane, mailbox connect/harvest, run feedback/retry/process-thread, admin, …) without changing the
architecture. Notable ones detailed below: [`rc ask`](#rc-ask--the-one-trigger) is the one *run* trigger
(`POST /api/v1/runs`); [brain publishing](#project-brain-publishing); [database writes](#database-writes).
`rc project env pull` writes a LOCAL 0600 `./.env` only (still a GET).

## Scope model (fail-closed)

Four independent levers, resolved before any request. The scope contract in
[`internal/cli/scope.go`](internal/cli/scope.go) stamps **every executable node** with which selectors it
accepts (`commandScope`) and **rejects** any selector a command did not opt into — a new command is
locked down by default until it appears in `commandScope`.

- **`--profile <name>`** picks *which stored token* to use (the token-store key). The token's project +
  principal scope is baked in at consent time.
- **`--project <id-or-name>`** is a **server-side scope**, not a token selector: it keeps the active token
  and rides as `?project=` on supported endpoints, letting an **all-projects admin token** review or
  trigger one project (`rc fleet runs --project X`, `rc ask --project X "…"`). A pinned token accepts only
  its own id/name and rejects a conflicting selector, so a supplied scope is never silently ignored.
  Before using a non-empty scope the CLI validates it against `GET /api/v1/projects` and returns
  `UNKNOWN_PROJECT` if not visible (`env.scopeProject` / `env.validateProjectScope` in
  [`root.go`](internal/cli/root.go)).
- **`--tenant <slug>`** overrides the login tenant where an endpoint accepts it. Tenant-record
  profile/settings commands take the slug **positionally** (the target), never from ambient brain/login
  context.
- **`--scope project|tenant`** forces request *routing* (not authorization): `project` clears any resolved
  tenant so a tenant-capable command hits the project route; `tenant` requires a resolvable tenant and
  fails closed otherwise. With a tenant-pinned token, `--scope project` routes to the project route and the
  server returns 403 — the flag controls routing, not authority.

`--all` (`rc fleet runs|patterns|health`) is the **fat-client fan-out**: list the fleet via
`rc project list`, call the per-project endpoint once per project with `?project=`, and merge
(`fanOutProjects` / `runFleetAll` / `runPatternsAll` / `runHealthAll` in `internal/cli`). It is mutually
exclusive with `--project`/`--tenant` and needs an all-projects token (against a scoped token the fleet
list returns one project and `--all` errors, never silently runs just that one). `-o json` emits the
merged `{projects:[…]}`.

Human tenant-scoped output is prefixed `Scope: <project> / <tenant>` (`installScopeHeader`); JSON stays
the byte-faithful server body.

## Architecture — four thin layers, no logic

```
cmd/rc/main.go        → cli.Execute(version)
internal/cli/         surface.go owns the nine-root tree; command files own thin endpoint adapters.
                      A command = parse flags → client call(s) → render. root.go owns the env + global
                      flags + scope resolution; scope.go the fail-closed scope contract; errors.go
                      surfaces {code,message,details} VERBATIM (exit 1); tokensource.go is the live
                      client.TokenSource (store + refresh policy); outputspill.go wires spill into render.
internal/client/      the ONE http wrapper (client.go: refresh-on-401 retry) + TokenSource interface
                      (auth.go) + wire contract (types.go) + APIError (errors.go). One method per
                      endpoint; types.go field names MUST match the server verbatim.
internal/oauth/       the OAuth protocol client: PKCE loopback (loopback.go) + device grant (device.go) +
                      refresh/revoke/exchange (oauth.go) + browser opener (browser.go).
internal/token/       the token store: ~/.config/rootcause/tokens.json (0600), per-profile.
internal/config/      resolution: env-or-production base URL + brain marker (.rootcause.toml) + local
                      overlay (.rootcause/local.toml) → profile + project + tenant.
internal/debugdump/   the rc-run-debug decomposer: decorate (dump.go) + emit JSONL + render index (emit.go).
internal/outputspill/ progressive disclosure for large stdout/JSON/JSONL: full bytes to disk + manifest.
internal/render/      render.go (TTY-detect + JSON passthrough) + per-view table renderers.
```

### Output: pipe-first, TTY-aware
`render.IsJSON(mode, w)` — `-o json`/`-o table` wins; else **JSON unless stdout is a terminal** (so
`rc run list | jq …` always works). JSON mode is a **verbatim pretty-print of the server body** (re-indent
only) — the CLI cannot invent or drop a field. `rc run events <id> -o json` emits **NDJSON** (one event
per line), not an array.

Large payloads spill to disk ([`internal/outputspill`](internal/outputspill/outputspill.go), wired via
`env.renderJSON` / `env.renderBytes` in [`outputspill.go`](internal/cli/outputspill.go)): the response is
still fetched fully, but full artifacts land under `.rootcause/output/` and stdout gets a small preview or
manifest with copyable `sed`/`rg`/`jq` hints. Global knobs: `--out-dir` / `RC_OUTPUT_DIR`,
`RC_OUTPUT_SPILL_THRESHOLD` (per field/stream, 6000 B), `RC_OUTPUT_INLINE_MAX` (whole JSON/JSONL, 20000 B),
`--no-preview`, `--raw-output` (exact full stdout, no spill). Intentional one-time secret reveals stay
raw. Contract detail: [docs/specs/progressive-output-disclosure.md](docs/specs/progressive-output-disclosure.md).

### Auth (OAuth) — login, token store, transparent refresh
OAuth is the **only** bearer credential (no legacy API key or env token). The flow in
[`internal/cli/auth.go`](internal/cli/auth.go) runs `internal/oauth` against the static first-party client
`rcocl_cli`:
- **`rc auth login`** — **PKCE loopback** by default (bind a localhost port, open the browser at
  `/oauth/authorize`, catch `127.0.0.1:<port>/callback`, exchange the code — port-insensitive server-side
  per RFC 8252). The authorize URL is printed before the OS opener is tried, and opener failure is
  non-fatal so an agent can hand the URL to a human. **`--device`** is the headless/SSH path (RFC 8628).
  **Project/tenant scope is chosen on the browser consent screen**, not the CLI.
- **Token store** ([`internal/token`](internal/token/store.go)): `~/.config/rootcause/tokens.json` (0600),
  keyed by profile, `{access_token, refresh_token, expires_at, base_url}`. `base_url` is diagnostic/refresh
  metadata only; it never overrides command transport. `rc auth logout` revokes server-side + clears it.
- **Transparent refresh**: `client.Client` takes a `TokenSource`; `tokensource.go`'s `liveSource` refreshes
  pre-emptively within 60s of expiry (and once on a 401), **persists the rotated pair**, and surfaces a dead
  refresh (`invalid_grant`) as a "run `rc auth login`" prompt. All refresh policy lives in `liveSource` —
  the client stays OAuth-oblivious. Tests inject `client.StaticToken` to bypass the store.

### Config & profile precedence
[`internal/config/profiles.go`](internal/config/profiles.go) `Load(profile)` picks a **profile name**
(the token-store key), a **base URL**, and an optional local tenant override — no secret. `--project` is
**not** an input here; it's a server-side scope the command layer threads onto each read.
- **explicit `--profile <name>`** → that profile, no brain binding (the override escape hatch);
- **inside a brain** (`.rootcause.toml` marker) → the marker's project as the profile; if no token exists
  for it, the command layer falls back to `"default"` and carries the marker's project as `?project=`
  (`root.go`, `autoProject`);
- **outside any brain** → `"default"`.

Base URL is exactly `ROOTCAUSE_BASE_URL` > built-in production (`https://app.replypen.com`). The env var
is the deliberate staging/dev escape hatch; persisted `base_url` values in markers or token records never
override transport. The legacy host `https://rootcause.probackup.io` is canonicalized to
`https://app.replypen.com` (`CanonicalBaseURL`). The `.rootcause/local.toml` overlay supports only
`tenant`. `XDG_CONFIG_HOME` is honored for token storage. `rc auth status` calls `/api/v1/whoami` to show
the login-bound project/tenant and whether the URL came from built-in production or `ROOTCAUSE_BASE_URL`.

## Selected command internals

### `rc ask` — the one trigger
[`ask.go`](internal/cli/ask.go) `POST`s the prompt to `/api/v1/runs`, then by default polls `/runs/{id}`
to a terminal status and renders by scenario (`--no-wait` prints the `run_id` and returns; JSON echoes the
verbatim 202 body so `jq -r .run_id` works). It stays thin — submit + poll + render; all run logic is
server-side. Flags are validated **before** authentication. The rich contract carries explicit `scenario`
(`email` default / `raw`), `sender`/`subject` for email, repeatable `--attach` (bytes + MIME → real
inbound attachment IDs), `--session` (a client-chosen `session_id` multi-turn join key — *not* `run_id`),
`--brain-ref dev/<branch>` (test run), `--effort pro|max` (`reasoning_effort`), `--project` (`?project=`),
and `--principal-kind`+`--principal-id` (a required pair scoping data access). If a deployed server rejects
the rich body as schema-malformed, `Client.Submit` retries the legacy `{prompt, tenant?}` body — but only
when no run-control field, principal, or attachment would be dropped (a dropped principal is a silent
under-scope, so the guard is security, not parity). Test-run intent: [docs/specs/brain-test-runs.md](docs/specs/brain-test-runs.md).

### `rc project env` — self-serve secret sync
[`env.go`](internal/cli/env.go) treats secret values specially. Bulk `GET /api/v1/env` returns live
grounding VALUES, so `keys`/`diff` reshape to NAMES only and `pull` writes values solely to the 0600
`./.env` (never stdout). `set` reads the value from STDIN; `rm` deletes one key; `reveal` is the one
command that prints a value (stderr warning) for intentional copy/pipe. `--plane grounding` targets the
normal read-only run env (`env_grounding`); `--plane action` targets the operator-only write-plane
`.env.action` (`env_action`), never mounted into normal runs.

### Project-brain publishing
`rc dev brain {status,sync,promote,publish}` ([`brain.go`](internal/cli/brain.go)) is the public
project-brain loop: push the tested commit, `sync` origin/main into the on-box cache, **promote its exact
full SHA** to `stable`/`edge`, then `status`-verify the channel's on-box `resolved_sha` plus fetched
`origin_sha`/`main_sha`. `publish` chains sync → promote → status-verify with gating, forcing sync/status
to project scope so an ambient tenant can never split them onto the overlay. The canonical route is
project-only (`commandScope` → `projectOnly`); tenant overlays use main, have no channels, and a
tenant-scoped principal is denied, not redirected. Promotion is idempotent; if local main is
ahead/diverged/dirty, sync refuses and returns the current/deployed SHAs. `brain edit`/`consolidate` queue
out-of-band brain work. Never infer a live channel from main=`current`.

### Database writes
`rc dev console database query <db> <sql>` ([`console.go`](internal/cli/console.go)) reads through the
scoped/masked plane by default. `--write` executes against the project's sealed write-plane DSN
(`<X>_WRITE_DSN` in `.env.action`, scope `console:db:write`) and COMMITs; `--write --dry-run` runs with
**identical authorization**, reports `rows affected` + `RETURNING` rows, then ROLLs BACK (a safety net, not
a weaker privilege). `--dry-run` requires `--write`. Rollback undoes row changes and transactional side
effects (e.g. `NOTIFY`) but sequence increments and volatile-function effects remain; locks are held to
rollback, bounded by the server's 30s statement timeout. Rehearsal and commit are two executions, so
confirm the commit's row count still matches. (User-facing rehearse-then-commit playbook: README.)

### The `rc run debug` decomposer
[`internal/debugdump`](internal/debugdump/emit.go) ports rootcause's `rc_agent_debug.py` to Go: it pulls
`/trace` (cross-project for an all-projects admin token) and writes a **jq-able JSONL** event log + a
**thin markdown index** to `--out-dir` (default `.rootcause/debug/`), then prints both paths. It does NOT
summarize into stdout — the calling agent reads the index, then drills into the JSONL. The JSONL contract
stays compatible with the Python/shared renderer: line 1 `{"type":"run"…}` header, later lines
`{"type":"event"…}` keyed by `disp` (grounding pre-steps `P1,P2,…`; main loop `1,2,…`), so jq recipes
like `select(.disp=="23").command` keep working. `decorate` (dump.go) derives disp/label/command; `emit.go`
writes both files. Historical `/trace` snapshots are authoritative (`brain_resolved`, `tenant_settings`,
`grounding_sources`); current state is only a drift annotation (`grounding_source_drift_count`,
`tenant_settings_drift`).

### Errors
Any non-2xx → the client decodes `{"error":{code,message,details?}}` into a typed `APIError` and the CLI
prints `CODE: message` to stderr (exit 1); `INVALID_SETTINGS` field errors print one indented line each. A
non-decodable body falls back to `error: HTTP <status>` — clean non-zero exit, never a panic.

## Working on it

- **Toolchain:** Go 1.25 via `mise` (`mise.toml` pins it); `cobra`+`pflag`, `BurntSushi/toml`. Run from the
  repo dir so mise selects go 1.25.
- **Before finishing any change:** `go build ./...`, `go vet ./...`, `go test ./...`, `gofmt -w`.
- **Tests** (`internal/cli/`): golden-file tests per table renderer + JSON-passthrough round-trip, driven
  by an `httptest` stub (`testdata/*.json` → `*.golden`); plus NDJSON shape, the `rc run debug` decomposer,
  the verbatim API-error path, the not-logged-in error, and the typed-error contract. Auth is exercised
  end-to-end against a stub OAuth server (device login, refresh + rotation + dead-token re-login, logout;
  PKCE loopback in `internal/oauth`). Regenerate goldens with `go test ./internal/cli -update`; fixtures use
  **canned** timestamps, never `time.Now`.
- **Adding a command for a new endpoint:** wire struct in [`types.go`](internal/client/types.go) (match
  server JSON exactly) → client method → render function (+ golden fixture/test) → cobra command wired in
  `surface.go`, with a `commandScope` entry if it accepts any selector. Simple rungs stay 1:1 with one
  endpoint; a higher-level command may call several raw endpoints and compute its view — keep the endpoints
  thin and always expose the raw rows via `-o json`. No DB access in the CLI.
- **Collection nouns (`repo`/`connection`/`member`/`token`):** all four ride one generic path —
  [`internal/client/collections.go`](internal/client/collections.go) (list/create/patch/verb/delete over
  `/api/v1/<resource>[/{id}][/{verb}]`, items as flat `map[string]json.RawMessage`) +
  [`internal/render/collections.go`](internal/render/collections.go) (one list table + one item block,
  `id` pinned first then sorted) + [`internal/cli/collections.go`](internal/cli/collections.go) (shared
  `ls/add/set/rm` + verb helpers). The CLI holds **no per-resource field knowledge** — a new server field
  appears with no CLI change. `connection reveal` / `token mint` print the secret/refresh-token to stdout
  with a stderr "shown once" warning; `connection rm` issues `/revoke` then `DELETE`.
- **`rc project settings runtime set` coercion:** [`config.go`](internal/cli/config.go) fetches
  `/meta/schema` ONCE and coerces each `k=v` by the field's declared type (a `list`/`array` comma-splits to
  a JSON array, empty → `[]`; a numeric type → a JSON number). On a schema miss it falls back to a static
  known-key set. The server is always the final validator.

## Local installation plumbing: `rc self doctor` / `rc self update`

[`doctor.go`](internal/cli/doctor.go) inventories the running executable and every PATH candidate, reads
embedded build metadata without executing shadowed candidates, and reports install/version/PATH findings.
[`buildversion.go`](internal/cli/buildversion.go) makes Go-installed binaries report their embedded module
version when release ldflags are absent. macOS has one canonical install: the `rootcause-org/tap/rc`
Homebrew cask.

[`upgrade.go`](internal/cli/upgrade.go) is the deliberate exception to "every command is one `/api/v1`
call": it talks to the **GitHub releases** API (no bearer key), then self-replaces its own binary with the
latest OS/arch archive (sha256-verified against `checksums.txt`, atomic same-dir rename); on macOS it runs
Homebrew's updater instead. `--migrate` idempotently canonicalizes a mixed setup: verify the updated cask,
remove only Go binaries whose embedded metadata identifies rootcause-cli ([`install.go`](internal/cli/install.go)),
`mise reshim`, then require PATH to resolve solely to Homebrew. Unknown binaries fail closed. Keep this the
*only* command that reaches outside `/api/v1`.

## Scope guards (push back if asked)

No MCP in v1 (a future layer over the same endpoints). Client-side analysis/rendering is fine, but **no
direct DB access** — data comes only through `/api/v1`, and the endpoints behind it stay thin (raw rows,
not server-computed views), with `-o json` always exposing those rows. Every write goes through an endpoint
the public `/api/v1` already serves (see [Server writes](#server-writes)) — the CLI adds **no new server
endpoint**. Auth is **OAuth only** against the server's existing `/oauth/*` + the static first-party CLI
client — the CLI invents no auth of its own. No interactive TUI/dashboard — scriptable, pipe-first, headless.
