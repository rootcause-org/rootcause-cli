---
name: rootcause-cli
description: "Architecture & intent of the `rc` CLI — a scriptable Go client over rootcause's public JSON /api/v1. Read before changing code: adding or reshaping a command, the HTTP client, OAuth/token-store/config resolution, the scope contract, or the table/JSON render + output-spill layer. Command reference and usage live in README.md; AGENTS.md is the router."
---

# rootcause-cli (`rc`) — architecture & intent

Command reference: **[README.md](README.md)** (inventory generated from Cobra + `docs/cli-help.txt`).
Code map + toolchain: **[AGENTS.md](AGENTS.md)**. This doc is the *why* and the recipes for changing code.

## Fat client, thin server

rootcause endpoints stay simple and return **raw, token-scoped rows**; `rc` may compute views on top
locally (digests, clustering, health roll-ups, diagnosis). Two constraints keep that honest:

- **`-o json` always reaches the raw rows** — our rendering is never the only way to the data.
- **No DB access, no CLI-invented endpoint or auth.** Data and writes both go through `/api/v1` +
  `/oauth/*`. (Hard rule; full scope guards in [AGENTS.md](AGENTS.md#scope-guards-push-back-if-asked-to-cross-them).)

Consequence for writes: the invariant is *not* a fixed list of write commands. A write command is just a
typed adapter over an existing `POST`/`PATCH`/`DELETE`, so the surface grows freely without changing the
architecture.

Nine offline roots (`status ask run project dev fleet admin auth self`), assembled with their help groups
in [`internal/cli/surface.go`](internal/cli/surface.go).

## The ladder — index → one run → detail

Progressive disclosure over runs, one endpoint per rung, so an agent verifies against real runs before
authoring a skill:

| Rung | Command | Endpoint |
|---|---|---|
| index | `rc status` / `rc run list` | `GET /api/v1/runs` (`status` = fixed health-led page; `run list` = filterable) |
| one run | `rc run show <id>` | `GET /api/v1/runs/{id}` |
| detail | `rc run events <id>` | `GET /api/v1/runs/{id}/events` (NDJSON in `-o json`) |
| bundle | `rc run trace <id>` | `GET /api/v1/runs/{id}/trace` (JSONL in `-o json`) |
| decompose | `rc run debug <id>` | `/trace` → local jq-able JSONL + thin markdown index |

`run list` filters are **server-side** so cursor pagination stays correct — never re-implement one
client-side. Two lanes that must stay distinct: `--learning` (training signal, held-out threads excluded)
vs `--reviewed` (human audit, held-out threads included). Merging them leaks eval data into training.

Fan-out commands (`rc fleet runs|patterns|health`, `rc fleet actions`, `rc run thread`,
`rc dev learning evidence`, `rc project knowledge content …`) call several raw endpoints and compute the
view in `internal/render`; the raw rows stay reachable through `-o json`.

## Scope model (fail-closed)

Four independent levers, resolved before any request; the contract is
[`internal/cli/scope.go`](internal/cli/scope.go), which stamps every executable node with the selectors it
accepts. **A new command rejects every selector until it is listed in `commandScope`** — this is the
security default, don't work around it by reading flags directly.

- **`--profile`** picks *which stored token* to use. Project/principal scope was baked in at consent time.
- **`--project`** is a *server-side* scope (`?project=`), not a token selector: it lets an all-projects
  token act on one project. A pinned token rejects a conflicting value rather than silently ignoring it,
  and a non-empty scope is validated against `GET /api/v1/projects` first (`env.validateProjectScope`).
- **`--tenant`** overrides the login tenant where an endpoint accepts it. Tenant-record commands take the
  slug **positionally** — never from ambient brain/login context.
- **`--scope project|tenant`** forces request *routing*, not authorization. Routing a tenant-pinned token
  at a project route still 403s server-side.

`--all` is the fat-client fan-out (`fanOutProjects` + `run{Fleet,Patterns,Health}All`): list projects, call
the per-project endpoint once each, merge. Against a scoped token it errors instead of quietly running one
project.

## Four thin layers, no logic

A command is `parse flags → client call(s) → render`. The split exists so each concern has exactly one
home and nothing leaks sideways — see [AGENTS.md](AGENTS.md#code-map-detail-in-skillmd) for the per-package
map. The rules that make the layering real:

- `internal/client` is the **one** HTTP wrapper — every request goes through the single send loop in
  `transport.go` (buffered `do`/`fetch` or streamed `openStream`, one credential source per call).
  Wire-contract field names must match the server verbatim (`<endpoint>_types.go` per endpoint family,
  shared primitives in `types.go`).
  The client is OAuth-oblivious — it takes a `TokenSource`, all refresh policy lives in
  `internal/cli/tokensource.go`.
- `internal/render` holds no transport and no auth; renderers are pure functions of server rows, which is
  what makes them golden-testable.
- Business logic never lives in `internal/cli` command bodies beyond flag validation + orchestration.

### Output: pipe-first, TTY-aware

`render.IsJSON` — `-o json`/`-o table` wins, else **JSON unless stdout is a terminal**, so
`rc … | jq` always works. JSON mode is a **verbatim pretty-print of the server body**: the CLI cannot
invent or drop a field.

Two deliberate exceptions, both documented at their code site:

- an **explicitly passed** `--format` pins a computed fleet digest even over a pipe (`rawRowsJSON`,
  [`observability.go`](internal/cli/observability.go)); explicit `-o json` still wins.
- console query output is a normalized `columns` + positional `rows` shape streamed as
  `json|ndjson|csv|tsv`, with a terminal row-count frame so a cut-off stream fails instead of installing a
  partial file.

Large payloads spill to `.rootcause/output/` with a preview/manifest on stdout
([`internal/outputspill`](internal/outputspill/outputspill.go), wired in
[`outputspill.go`](internal/cli/outputspill.go)). Contract + knobs:
[docs/specs/progressive-output-disclosure.md](docs/specs/progressive-output-disclosure.md).

### Auth (OAuth only)

No API key, no access-token env var. [`auth.go`](internal/cli/auth.go) drives `internal/oauth` against the
static first-party client `rcocl_cli`: PKCE loopback by default, `--device` for headless/SSH. **Scope is
chosen on the browser consent screen, not by CLI flags.** The authorize URL is printed *before* the OS
opener runs and opener failure is non-fatal, so an agent can hand the URL to a human.

Tokens: `~/.config/rootcause/tokens.json`, 0600, per profile ([`internal/token`](internal/token/store.go)).
Its stored `base_url` is diagnostic metadata only and never overrides transport.
`liveSource` refreshes pre-emptively and once on a 401, persists the rotated pair, and turns a dead
refresh into a "run `rc auth login`" prompt. Tests bypass the store with `client.StaticToken`.

**Headless machine token:** a committed brain marker may *name* (never contain) a secret env var via
`machine_token_env`. `rc` seeds that profile, then requires `/whoami` to match the marker project before
any command endpoint, and refuses to send the token to a non-production base URL
([`machine_token_env.go`](internal/cli/machine_token_env.go)). Under `CLAUDE_CODE_REMOTE=true` its absence
fails rather than falling back to a broader `default` token.

### Config precedence

[`internal/config/profiles.go`](internal/config/profiles.go) `Load(profile)` resolves a **profile name**
(token-store key), a **base URL**, and an optional tenant override — never a secret. `--project` is not an
input here; it is a server-side scope threaded on by the command layer.

- explicit `--profile` → that profile, no brain binding (the escape hatch);
- inside a brain (`.rootcause.toml`) → the marker's project as profile; without a project profile, fall
  back to `default` and carry the marker project as `?project=` (`autoProject` in `root.go`);
- otherwise → `default`.

Base URL is exactly `ROOTCAUSE_BASE_URL` > built-in production. Rationale: one env var is the only
staging/dev escape hatch, so a stale persisted `base_url` in a marker or token record can never silently
redirect traffic. `.rootcause/local.toml` overlays `tenant` only.

## Invariants worth knowing before you touch these commands

- **`rc ask`** ([ask.go](internal/cli/ask.go)) is the one *run* trigger (`POST /api/v1/runs`): submit,
  poll, render — all run logic is server-side. Its legacy-body retry is allowed only when no run-control
  field, principal, or attachment would be dropped: a dropped principal is a silent **under-scope**, so
  that guard is security, not compatibility. Test-run intent:
  [docs/specs/brain-test-runs.md](docs/specs/brain-test-runs.md).
- **Redacted run detail** — the server serves run detail only to project admins, and a non-admin read
  still returns `200` with `detail_redacted: true`. The failure mode is a *false clean bill of health*, so
  every surface that can receive one prints "withheld" instead of an empty section
  ([`internal/render/redaction.go`](internal/render/redaction.go), `render.Patterns`, the `run debug`
  index). Absent field = older server = full detail.
- **`rc run debug`** ([`internal/debugdump`](internal/debugdump/emit.go)) writes files and deliberately
  does **not** summarize into stdout; the agent reads the index, then drills the JSONL. Its JSONL shape
  (header line + `disp`-keyed events) is a contract shared with rootcause's Python renderer — jq recipes
  in the field depend on it. Historical `/trace` snapshots are authoritative; current state appears only
  as drift annotations.
- **`rc dev brain`** ([brain.go](internal/cli/brain.go)) is project-scoped by design: tenant overlays use
  main, have no channels, and a tenant-scoped principal is denied, never redirected. `preflight` is
  promote's dry run over the same server-side canary — it exits non-zero on a refusing verdict, but the
  server enforces the check on `promote` too, so preflight is for *seeing* the answer early, never what
  makes promotion safe. `consumers` (not `checked`) is the authoritative channel-use count.
- **`rc dev console database query --write --dry-run`** runs with **identical authorization** to a commit
  and rolls back — a safety net, not a lesser privilege. Rollback doesn't undo sequence bumps or volatile
  side effects, and rehearsal + commit are two executions, so re-check the row count.
- **`rc project env`** ([env.go](internal/cli/env.go)) is secret-shaped: `keys`/`diff` are names-only,
  `pull` writes values only into a 0600 `./.env`, `set` reads STDIN, `reveal` is the single deliberate
  print. `--plane action` targets the operator-only write plane that never enters normal runs. Operator
  playbook: [README](README.md#rc-project-env--self-serve-grounding-env-sync).
- **`rc self update`** ([upgrade.go](internal/cli/upgrade.go)) is the one command that reaches outside
  `/api/v1` (GitHub Releases or `RC_RELEASE_MIRROR`, sha256-verified, Homebrew on macOS). Keep it the only
  one. `--migrate` fails closed on binaries it cannot identify as ours.

## Adding a command

1. Wire struct in the endpoint's `internal/client/<endpoint>_types.go` — field names match the server JSON exactly.
2. Client method in `internal/client` (one method per endpoint).
3. Render function + golden fixture/test in `internal/render`.
4. Cobra command in `surface.go`, plus a `commandScope` entry if it accepts any selector (without one it
   rejects all of them).
5. `go test ./internal/cli -update` to regenerate goldens, the README inventory and `docs/cli-help.txt`.

Simple rungs stay 1:1 with one endpoint; a higher-level command may fan out — but keep the endpoints thin
and `-o json` on the raw rows.

**Reuse the generic paths instead of adding per-resource code:**

- **Collection nouns** (`repo`/`connection`/`member`/`token`) ride one generic path —
  `collections.go` in [client](internal/client/collections.go) / [render](internal/render/collections.go) /
  [cli](internal/cli/collections.go), items as flat `map[string]json.RawMessage`. The CLI holds **no
  per-resource field knowledge**, so a new server field appears with no CLI change.
- **Settings coercion** ([config.go](internal/cli/config.go),
  [hierarchy_settings.go](internal/cli/hierarchy_settings.go)) fetches `/meta/schema` once and coerces
  `k=v` by the *declared* type. There is no hardcoded key list — a knob the server gains is settable
  without a CLI release, and the server stays the final validator.
- **Capability/format lists the server owns** (e.g. harvest-corpus format versions on `/meta/capabilities`)
  are read at runtime, never re-pinned in CLI source.

Errors: any non-2xx decodes into a typed `APIError` and is surfaced **verbatim**
([errors.go](internal/cli/errors.go)); a non-decodable body degrades to `error: HTTP <status>`, never a
panic. Exit codes are a scripting contract — the list lives in
[README](README.md#composable-console-primitives).
