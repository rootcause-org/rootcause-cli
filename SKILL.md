---
name: rootcause-cli
description: "The `rc` CLI — a scriptable Go client that lets a project consume its OWN rootcause data and change its own config over rootcause's public JSON /api/v1, authed with an OAuth access token (sign in via `rc auth login`; the CLI refreshes it). Use when working in this repo: adding/changing a command, the HTTP client, OAuth/token-store/config resolution, or the table/JSON render layer; or when wiring a new endpoint the API already serves. Fat client, thin server: endpoints return raw token-scoped data, the CLI may digest/cluster/render it locally, and `-o json` always exposes the raw rows."
---

# rootcause-cli (`rc`) — a project's window into its own rootcause data

The command tree has nine stable, offline roots. Start work with `status`, `ask`, or the complete
`run` lifecycle; project configuration stays under `project`; specialist development tools stay under
`dev`; fleet operations and box administration stay under `fleet` and `admin`; local credentials and
installation plumbing stay under `auth` and `self`. Cobra help is grouped as Start, Manage, Develop,
Operate, and Local. `rc help` remains callable but framework help/completion commands do not appear as
extra roots; completion lives at `rc self completion`.

`rc` is a **fat client over a thin server**: rootcause endpoints stay simple and return **raw,
token-scoped data**; `rc` may compute views on top of it locally (digests, clustering, health roll-ups,
diagnosis) — and every such command still exposes the raw rows via `-o json`, so a consumer can ignore
our rendering and slice the data themselves. It holds **no DB access** — data comes only through the
public `/api/v1`, with an **OAuth access token** minted by `rc auth login` (the token resolves the caller's
project + principal server-side: a pinned token scopes to one project, an all-projects admin token reads
cross-project).
`--profile` picks *which stored token* to use; the token's project scope is baked in at consent time.
`--project <id-or-name>` is a different lever — a **server-side scope**, not a token selector: it keeps
the active token and names one project on supported endpoints (`?project=`), letting an **all-projects
admin token** review a single project (`rc fleet runs --project momentum-tools`) or trigger one (`rc ask
--project momentum-tools "…"`). A pinned token accepts only its own id/name and rejects a conflicting
selector, so a supplied scope can never be silently ignored. The bet: a dev pulls this in to slice their data the way
they prefer (`| jq`, scripts, a quick `rc run show <id>`) and, before authoring an action/skill, runs
`rc run list` → `rc run events <id>` to **verify against real runs** — the author→verify loop taught in
[rootcause-brain-skills/docs/rc-cli.md](../rootcause-brain-skills/docs/rc-cli.md).

## The ladder (progressive disclosure — index → one run → detail)

The base rungs are one endpoint each (index → one run → detail). Higher-level commands (digests,
patterns, health, thread trace) may fan out over several raw endpoints and compute the view locally —
but they keep the raw rows reachable via `-o json`.

Every executable command has a fail-closed scope contract (`internal/cli/scope.go`). Persistent
`--project` / `--tenant` selectors are either applied or rejected before any request; `--all` is mutually
exclusive with both. Tenant-record profile/settings commands take the slug positionally, never from
ambient brain/login context. Tenant-capable collections use the canonical project/tenant route tree;
outside a brain, a project-pinned login resolves its project through `/whoami`. Human tenant-scoped
output starts with `Scope: <project> / <tenant>`; JSON remains the raw server body.

| Command | Endpoint | What |
|---|---|---|
| `rc ask "<q>"` | `POST /api/v1/runs` | trigger a run from a question, then poll to the answer (the ONE server-write trigger; supports `--scenario email|raw`, repeatable `--attach`, `--project` for all-projects admin tokens, and `--effort default|pro|max`; see below) |
| `rc project list` | `GET /api/v1/projects` | list the fleet handles (name + id) the token can see — every project for an all-projects admin token, just its own for a pinned token; the seed for the `--all` fan-out |
| `rc project rename <new-name>` | `PATCH /api/v1/projects/{project}/rename` | rename the active project slug + brain repo; `--project` supplies `{project}`, otherwise the CLI requires exactly one visible project |
| `rc status` / `rc run list` | `GET /api/v1/runs` | index: recent runs + health summary (the [runs-index-api](../rootcause/.agents/skills/features/runs-index-api.md)) |
| `rc run show <id>` | `GET /api/v1/runs/{id}` | one run, high level |
| `rc run events <id>` | `GET /api/v1/runs/{id}/events` | full per-event trace (NDJSON in JSON mode) |
| `rc run trace <id>` | `GET /api/v1/runs/{id}/trace` | the whole bundle (header + per-event trace + cost); JSONL in JSON mode |
| `rc run debug <id>` | `GET /api/v1/runs/{id}/trace` | decompose to a jq-able JSONL + thin markdown index on disk (see below) |
| `rc run process-thread <thread-id>` | `POST /api/v1/projects/{project}[/tenants/{slug}]/inbox/threads/{id}/process` | resume a triage-skipped or security-blocked mail thread; requires an explicit or brain-derived project |
| `rc dev learning evidence` | `GET /api/v1/dream/evidence` | feedback + sent-edit + triage evidence for local dream-cycle passes; JSON-only because planes are heterogeneous; `--plane`, body detail opt-in |
| `rc dev console database query <db> <sql>` | `POST /api/v1/console/db/{db}/query` | read through the scoped/masked plane by default; `--write` uses the project-level unmasked write plane and commits, while `--write --dry-run` executes with identical authorization and rolls the transaction back after reporting `rows affected` + explicit `RETURNING` rows |
| `rc project settings runtime get` / `set k=v` | `GET` / `PATCH /api/v1/settings` | read / change the self-service settings whitelist (list keys like `pr.triggers=inbound,mcp` comma-split to a JSON array — see below) |
| `rc project knowledge content list` / `search` / `export` | `POST /api/v1/console/bash/run` | first-class KB article discovery over the mounted `/kb/<provider>` snapshot: stdout stays compact; search/export write timestamped local artifacts under `.rootcause/tmp/kb-searches/...` with `manifest.json`, `hits.md`, and matched article markdown files. `rc project knowledge sync get/set` still owns KB sync config over `/api/v1/kb` |
| `rc project settings behavior get/set` | `GET/PATCH /api/v1/projects/{project}/settings?resolved=true` | read/change nested project hierarchy settings (`persona.*`, `channel.*`) |
| `rc project triage policy get/set`, `rc project triage rules ls/add/set/rm` | `/api/v1/triage/*` | read/change mail draft/no-draft guidance and deterministic skip/force-process rules |
| `rc project tenant settings get/set <slug>` | `GET/PATCH /api/v1/projects/{project}/tenants/{slug}/settings?resolved=true` | read/change tenant hierarchy overrides; null clears the scope-local override |
| `rc project tenant profile get/set <slug>` | `GET/PATCH /api/v1/tenants/{slug}/profile` | read/change the tenant projection/profile values record |
| `rc project mailbox settings get/set <id>` | `GET/PATCH /api/v1/projects/{project}/mailboxes/{id}/settings?resolved=true` | read/change mailbox hierarchy overrides |
| `rc project mailbox harvest <id> [--wait]` | `POST /api/v1/mailboxes/{id}/harvest` | start a local-synthesis harvest of a mailbox → a queued export; `--wait` polls the export to a terminal status. `--clean` (default true), `--max-threads N` |
| `rc project mailbox connect-imap --email … --imap-host …` | `POST /api/v1/mailboxes/imap/connect` | connect a generic IMAP/SMTP mailbox (server live-probes IMAP+SMTP before saving). Password via `$RC_MAILBOX_PASSWORD` or stdin prompt — never argv. Defaults mirror the server (`--username`→`--email`, `--smtp-host`→`--imap-host`, `993/implicit`+`587/starttls`) |
| `rc project corpus ls` / `get <id>` | `GET /api/v1/exports[/{id}]` | list/read the harvest/survey corpus exports (newest-first) |
| `rc project corpus download <id> [--out f] [--split dir]` | `GET /api/v1/exports/{id}/download` | download the raw Markdown corpus (stdout/`--out`), or `--split` it into an `INDEX.md` + per-thread tree the local dream-cycle greps (default `.rootcause/exports/<id>/`, auto-gitignored). Marks the export consumed |
| `rc dev api routes` / `rc dev api openapi` | `GET /api/v1/meta/routes` / `GET /api/v1/meta/openapi.json` | canonical route manifest + generated OpenAPI |
| `rc dev brain status` / `sync` | `GET` / `POST /api/v1/brain/{status,sync}` | inspect/refresh the deployed on-box brain cache; status includes stable/edge resolved, origin, and main SHAs; sync fetches origin/main, fast-forwards when safe, and expires warm bash workspaces |
| `rc dev brain promote --channel stable|edge --sha <full-SHA>` | `POST /api/v1/projects/{project}/brain/promote` | move one managed project-brain channel to the exact tested commit; project-admin authority + `brain:promote` when explicitly scope-limited, never tenant scope |
| `rc project repo ls/add/set/rm` | `GET/POST/PATCH/DELETE /api/v1/repos` | source repos (mirrors + per-repo PR config); id = repo name |
| `rc project tenant ls/add/get/set` | `GET/POST/GET/PATCH /api/v1/tenants[/{slug}]` | manage project tenant rows; archive with `set <slug> status=archived` |
| `rc project connection ls/add/reveal/rotate/rm` | `/api/v1/connections` (+ `/{id}/reveal\|rotate\|revoke`) | outbound integration connections; `reveal` prints the secret to stdout ONCE; `rm` = revoke then DELETE |
| `rc project connection probe <capability>` | `POST /api/v1/connections/probe` | developer write-plane diagnostic: check the OAuth/capability grant independently from action-plane enablement; optional provider-specific write probes such as `notion.write --write --notion-page <id> --cleanup` |
| `rc project member ls/add/rm` | `GET/POST/DELETE /api/v1/members` | project members (no read/update server-side → 405) |
| `rc project token ls/mint/revoke` | `GET/POST/DELETE /api/v1/tokens` | API tokens; `mint` prints the `refresh_token` ONCE |
| `rc project env keys` / `pull` / `diff` | `GET /api/v1/env` | sync the project's PRODUCTION grounding `.env` to a local 0600 `./.env` — values never print |
| `rc project env set` / `rm` / `reveal` | `/api/v1/env_grounding` or `/api/v1/env_action` | add/rotate/delete/reveal one sealed env key. `grounding` is the normal run-injected read-only plane; `action` is the operator-only write-plane (`.env.action`) |
| `rc auth login` / `logout` / `status` | `/oauth/*` (+ local) | OAuth sign-in / revoke / local status (see Auth below) |

`rc status` and `rc run list` are the **same endpoint** — status requests a fixed five-row at-a-glance
page and leads with the health summary; `run list` owns the filterable table
(`--limit`/`--kind`/`--category`/`--outcome`/`--learning[=signal]`/`--before`). Outcome and learning
filters are server-side so cursor pagination remains correct. Bare `--learning` means `any`; explicit
values are `feedback`, `sent_delta`, `triage_skipped`, and `triage_corrected`. The table exposes the
safe learning booleans; JSON remains verbatim.

### Database writes: rehearse, then commit

Always rehearse production SQL on the real write plane before committing it. Include the key and every
changed column in `RETURNING`; use `RETURNING *` for DELETE so the disappearing row can be archived.
Treat an unexpected `rows affected` count as a hard stop.

```bash
# 1. Rehearse — see exactly which rows change and their new values
rc dev console database query backups \
  "UPDATE users SET plan = 'pro' WHERE email = 'x@y.com' RETURNING id, email, plan" \
  --write --dry-run
# → DRY RUN — rolled back, nothing committed
# → rows affected: 1   (expected 1? if 500, your WHERE is wrong — nothing happened)

# 2. Commit — same statement, drop --dry-run
rc dev console database query backups "UPDATE … RETURNING id, email, plan" --write
```

Dry-run has the same write-grade authorization and unmasked project-level DSN as a commit: it is a
safety net, not a weaker privilege. Rollback undoes row changes and transactional side effects such as
`NOTIFY`, but sequence increments remain and volatile functions still execute. Locks remain held until
rollback, bounded by the server's 30-second statement timeout. `UPDATE ... RETURNING` exposes new values;
capture old and new values with a self-join when needed. Rehearsal and commit are two executions, so
production may change between them—confirm the commit run's row count still matches.

`rc dev learning evidence` stays JSON-only: feedback, sent deltas, and triage corrections are
heterogeneous evidence records, so a single table would discard useful plane-specific fields.
`--plane feedback|deltas|triage` narrows the response; proposed/sent bodies remain excluded unless
`--include-bodies` sets the API's `include_bodies=true`. There is no `--since` until the endpoint owns
that filter.

`rc ask` ([ask.go](internal/cli/ask.go)) is the one **trigger**: it `POST`s the prompt to `/api/v1/runs`,
then by default polls `/runs/{id}` to a terminal status and renders by scenario (`--no-wait` prints the
`run_id` and returns; JSON echoes the verbatim 202 body so `jq -r .run_id` works). It stays thin —
submit + poll + render; all run logic is server-side. The CLI first sends the rich contract: explicit
`scenario` (`email` by default, or `raw`), `sender`/`subject` for email,
and any run-control fields. `--attach <path>` is repeatable:
the CLI resolves relative/absolute local paths, reads bytes, detects a MIME type, and sends
`attachments[]` `{filename,mime_type,size_bytes,content_base64}` so the server can mint real inbound
attachment IDs for action params. If a deployed server rejects that body as schema-malformed, and no
run-control field (`session_id`, `brain_ref`, `reasoning_effort`) would be lost — **nor a `principal` or
attachments** would be dropped
(a dropped principal is a silent data under-scope, so the guard is security, not parity) — the client
retries the legacy body `{prompt, tenant?}` so older Prompt API deployments still accept plain `rc ask`.
`email`
wraps the prompt as a synthetic inbound support message with `sender` from `--from` (default
`rc-ask@example.test`) and `subject` from `--subject` or a compact first line, then table-renders draft,
notes, actions, PR, and run metadata (using `/runs/{id}/trace` when available). `raw` omits default email
fields and table-renders one direct answer plus actions, PR, and run metadata. `--project <id-or-name>`
rides as `?project=` on submit,
letting an all-projects admin token trigger a selected project while a pinned token keeps its own
server-side scope. `--session <id>` carries a **client-chosen** `session_id` (the multi-turn join key —
*not* `run_id`); the server keys continuity on `(project, session_id, kind=prompt)` and warm-starts each
follow-up off the prior turns' command trail (see
[multi_turn_warm_start.md](../rootcause/.agents/skills/features/multi_turn_warm_start.md) — the prior
*answer* is not yet replayed for prompt/mcp). `--brain-ref dev/<branch>` runs against a non-main brain
ref (a test run); `--effort pro|max` sends `reasoning_effort` to force a stronger rootcause model tier
for this run (omitted/default keeps normal tier selection). `--principal-kind`+`--principal-id` (a
required pair; optional `--asserted-by`/`--assurance` need that pair) send a nested `principal` object
that scopes the run's data access to that identity — dormant unless the project declares `scope_claims`;
tenant binding stays the `--tenant` slug, never `tenant_hint`. On tenant-enabled projects, `rc auth login` may
be tenant-pinned or project-pinned. A tenant-pinned login works with plain `rc ask`; a project-pinned
login must pass `--tenant <slug>` on each workspace-producing command.

`rc project env` deliberately treats secret values differently from ordinary JSON. Bulk `GET /api/v1/env`
returns live grounding secret VALUES, so `env.go` reshapes to NAMES only for `keys`/`diff`, and `pull`
writes values solely to the 0600 `./.env` (never stdout). Per-key `set` reads the value from STDIN by
default and calls the collection create/upsert; `rm` deletes one key; `reveal` is the one command that
prints a value, with a stderr warning, for intentional copy/pipe use. `--plane grounding` targets the
normal read-only run env (`env_grounding`); `--plane action` targets `.env.action` (`env_action`), an
operator-only write-plane collection that is never mounted into normal runs.

`rc dev brain status`, `sync`, and `promote` are the public project-brain publishing loop. Push the
tested commit, `sync` origin/main into the on-box cache, promote its **exact full SHA** to `stable` or
`edge`, then use `status` to verify the channel's on-box `resolved_sha` plus its fetched
`origin_sha`/`main_sha`, comparison state, and ref provenance. Never infer a live channel from
main=`current`: a managed channel can still be stale. Promotion retries are idempotent. The canonical
route is project-only; tenant overlays use main and have no channels, and tenant-scoped principals must
be denied rather than redirected to a tenant brain route. If local main is ahead/diverged/dirty, sync
refuses to reconcile and returns the current/deployed SHAs for explicit handling. Sync refreshes warm
Developer Console bash workspaces so the next bash run remounts `/brain`; console surfaces also echo
brain status/resolution so a pushed brain commit cannot fail silently behind an old catalog.

`rc run trace` and `rc run debug` treat historical snapshots as authoritative: `brain_resolved`,
`tenant_settings`, and `grounding_sources` come from `/trace`; current tenant/source state is only a drift
annotation. Debug JSONL preserves raw `grounding_sources` and adds `grounding_source_drift_count`; table
and markdown render missing/drifted mirrors or KB first.

`rc project knowledge content search` is progressive-disclosure glue over the guarded bash workspace, not a giant markdown
dump: it searches `/kb/<provider>` remotely, prints only ranked metadata/snippets, then fetches each
matched article individually and writes a fresh local artifact folder. Agents should treat the printed
artifact path as the handle, then use local `rg`/`sed`/scripts over `articles/`. JSON mode exposes the
same `artifact_dir`, counts, truncation flag, and ranked article metadata. `rc project knowledge content export` uses the same
writer for `--query`, `--article`, or `--all`; every output directory must be new to avoid stale
cross-search contamination.

## Architecture — four thin layers, no logic

```
cmd/rc/main.go            → cli.Execute(version)
internal/cli/             `surface.go` owns the nine-root information architecture; command files own
                          their thin endpoint adapters (status/run/config/env/auth/etc.).
                          A command = parse flags → one client call → render. errors.go surfaces
                          the API's {code,message,details} VERBATIM to stderr, exit 1. tokensource.go is
                          the live client.TokenSource (store + refresh policy).
internal/client/          the ONE http wrapper (client.go: refresh-on-401 retry) + the TokenSource
                          interface (auth.go) + the wire contract (types.go) + APIError (errors.go).
                          One method per endpoint; types.go field names MUST match the server verbatim.
internal/oauth/           the OAuth protocol client: PKCE loopback (loopback.go) + device grant
                          (device.go) + refresh/revoke/token exchange (oauth.go) + browser opener.
internal/token/           the token store: ~/.config/rootcause/tokens.json (0600), per-profile.
internal/config/          resolution: env-or-production base URL + brain marker (.rootcause.toml) + local overlay (.rootcause/local.toml) → profile + project + tenant.
internal/debugdump/       the rc-agent-debug decomposer: decorate + emit JSONL + render thin index.
internal/outputspill/     progressive disclosure for large stdout/JSON/JSONL: full bytes to `.rootcause/output/`
                          (or --out-dir / RC_OUTPUT_DIR), stdout gets a preview/manifest + sed/rg/jq hints.
internal/render/          render.go (TTY-detect + JSON passthrough) + table.go (one renderer per view).
```

### Output: pipe-first, TTY-aware
`render.IsJSON(mode, w)` — `-o json`/`-o table` wins; else **JSON unless stdout is a terminal**. So a
TTY gets a table; a pipe/redirect gets JSON (`rc run list | jq …` always works). JSON mode is a **verbatim
pretty-print of the server body** (re-indent only), so jq sees the true response shape — the CLI can't
invent or drop a field. `rc run events <id> -o json` emits **NDJSON** (one event per line), not an array.

Progressive disclosure lives in `internal/outputspill`: large payloads are still fetched fully, but the
CLI writes full artifacts under `.rootcause/output/` by default and prints a small table preview or JSON
manifest with copyable `sed`/`rg`/`jq` hints. Global knobs: `--out-dir`, `RC_OUTPUT_DIR`,
`RC_OUTPUT_SPILL_THRESHOLD` (per field/stream, default 6000 bytes), `RC_OUTPUT_INLINE_MAX` (whole JSONL
or JSON response, default 20000 bytes), `--no-preview`, and `--raw-output` for exact full stdout.
Phase-1 wiring covers `internal/cli/console.go` JSON passthrough, `rc dev console bash run` stdout/stderr, and
`rc run events|trace|debug`; large NDJSON manifests unless `--stream` or `--raw-output` is passed.
Phase-2 wiring extends the same `env.renderJSON` / `env.renderBytes` path to high-volume API metadata
(`rc dev api routes`, `rc dev api openapi`), observability fan-outs (`fleet runs` / `fleet patterns` /
`fleet health`, including `--all`),
collection CRUD JSON responses with large values, and `rc project corpus download` stdout bodies. Intentional
one-time secret reveal surfaces (`connection reveal`, `token mint`) stay raw so capture/copy behavior is
unchanged.

### Auth (OAuth) — login, token store, transparent refresh
OAuth is the **only** bearer credential (the legacy `rcl_` key, `ROOTCAUSE_API_KEY`, and
`.rootcause.secret.toml` are gone). The shape:

- **`rc auth login`** ([auth.go](internal/cli/auth.go)) runs a flow in `internal/oauth` against the static
  first-party client `rcocl_cli`: **PKCE loopback** by default (bind a localhost port, open the browser
  at `/oauth/authorize`, catch `http://127.0.0.1:<port>/callback`, exchange the code — the loopback
  redirect is port-insensitive server-side per RFC 8252). It prints the full authorize URL before trying
  the OS browser opener, and opener failure is non-fatal so agents can hand the URL to a human. Use
  **`--device`** for true headless/SSH sessions where a browser cannot reach localhost (RFC 8628: print
  a code, poll `/oauth/token`). The **project/tenant scope is chosen on the browser consent screen**, not
  the CLI.
- **Token store** (`internal/token`): `~/.config/rootcause/tokens.json` (0600), keyed by profile —
  `{access_token, refresh_token, expires_at, base_url}`. `base_url` is diagnostic/refresh metadata from
  login or the latest refresh; it does not override normal command transport. `rc auth logout` revokes
  server-side + clears it.
- **Transparent refresh**: `client.Client` takes a `TokenSource`; `tokensource.go`'s `liveSource` reads
  the profile's token, refreshes pre-emptively within 60s of expiry (and on a 401, the client retries
  once after a forced refresh), and **persists the rotated pair**. A dead refresh (`invalid_grant`)
  surfaces as a "run `rc auth login`" prompt. All refresh policy lives in `liveSource` — the client stays
  OAuth-oblivious. Tests inject `client.StaticToken` to bypass the store.

### Config & profile precedence
In `internal/config` (`profiles.go`), resolution is **brain-aware** for project/profile context and
deliberately boring for transport. It picks a **profile name** (the token-store key), a **base URL**, and
an optional local tenant override — no secret. `Load(profile)` (note: `--project` is **not** an input —
it's a server-side scope the command layer threads onto each read request, never a token selector):

- **explicit `--profile <name>`** → that profile, no brain binding (the override escape hatch);
- **inside a brain** → first try the brain marker's project as the profile; if no token exists for it,
  fall back to `"default"` and carry the marker's project as `?project=` on supported endpoints;
- **outside any brain** → `"default"`.

`--project <id-or-name>` rides as `?project=` on supported per-project endpoints (run index, feeds,
health, thread-trace, prompt submit, env, and settings); an all-projects admin token uses it to scope
one project, while a pinned token requires the selector to name its own project. The brain→default fallback sets this scope
implicitly from `.rootcause.toml`. Before using a non-empty scope, the CLI checks `GET /api/v1/projects`
and returns `UNKNOWN_PROJECT` with a `rc project list` hint when the id/name is not visible. See
`env.scopeProject` / `env.validateProjectScope` in `internal/cli/root.go`.
On tenant-enabled projects, the active OAuth login may bind one tenant or the whole project. Plain
`rc ask` sends no tenant flag and works only when the token is tenant-pinned; project-pinned logins use
`--tenant <slug>` per command. `rc auth status` calls `/api/v1/whoami` to show the login-bound project and
tenant, when one is pinned.

**Fleet-wide `--all`** (`rc fleet runs` / `rc fleet patterns` / `rc fleet health`) is the FAT-CLIENT fan-out that complements
`--project`: it lists the fleet via `rc project list`, then calls the per-project read endpoint once per
project with `?project=<id>`, and merges the results — grouped-per-project with a fleet total (`fleet`),
a clustered section per project (`patterns`), or a per-project verdict whose worst case sets the exit
code (`health`). `-o json` emits the merged `{projects:[…]}` structure. `--all` needs an all-projects
token: against a project-scoped token (the fleet list returns one project) it fails with a friendly,
named error rather than silently running just that one. The per-project endpoints stay thin and raw —
the fan-out + grouping live entirely in the CLI (`runFleetAll`/`runPatternsAll`/`runHealthAll`,
`fanOutProjects` in `internal/cli`).

`rc fleet health` renders the raw `/api/v1/health` inputs: mirror rows (state/staleness), watched-mailbox
watch facts, and dead-lettered runs in the chosen window. The CLI marks mailbox rows unhealthy only when
they are parked (`error`/`needs_attention`) or active with an expired main/spam subscription; JSON mode
still passes the raw shape through unchanged.

**`rc fleet runs` aggregates** (all computed fat-client in `internal/render/fleet.go`, pure functions of
the `/api/v1/runs` rows): the default human digest is the per-run flag table + rates + worst
offenders; **`--by-model`** adds the model×cost×**fallback** breakdown (the highest-value view — which
model burned the spend and how much was a fallback), **`--timeline`** adds the per-day
runs/errors/cost histogram. Stuck/running runs and a `FB` model-fallback flag are inline; offender
lines carry the full triage tail. The fallback signal is the server's clean `run_health.is_fallback`
boolean (it bakes around the `runs.model_fallback_from` empty-string-vs-NULL trap — see the `support`
skill's `db-reference.md`); each row's `is_fallback`/`planned_model` ride raw in `-o json`.
`--learning` uses the same bare-`any` / explicit signal semantics as `run list` and is passed through on
every page and every `--all` project request. Human and agent renderers add an `LRN:` flag naming the
safe signals; learning does not affect operational severity or worst-offender ranking.

Base URL resolution is exactly `ROOTCAUSE_BASE_URL` > built-in production default
(`https://app.replypen.com`). `ROOTCAUSE_BASE_URL` is the deliberate staging/dev escape hatch; otherwise
normal commands and `rc auth login` hit production. Persisted `base_url` values in config profiles, brain
markers, or token records do not override command transport. The legacy production host
`https://rootcause.probackup.io` is canonicalized to `https://app.replypen.com` when an explicit env or
stored token URL is normalized. `Resolved` carries `Profile`/`Project`/`Brain` and the URL source so
`root.go` crafts the loud error and `rc auth status` can print whether the URL came from built-in production
or `ROOTCAUSE_BASE_URL`. `rc auth status` asks `/api/v1/whoami` for the login-bound project/tenant when a
token is present. Explicit `--tenant` and `.rootcause/local.toml` remain local override/debug paths. The
local overlay only supports `tenant`. Honors `XDG_CONFIG_HOME` for token storage. The committed marker
is non-secret; tokens live only in the 0600 token store.

### The `rc run debug` decomposer (`internal/debugdump`)
`rc run debug <id>` ports rootcause's `rc_agent_debug.py` to Go: it pulls `/trace` (cross-project for an
all-projects admin token) and writes two files to `--out-dir` (default `.rootcause/debug/`) — a **jq-able JSONL**
event log and a **thin markdown index** — then prints both paths. It does NOT summarize the run into
stdout: the calling agent reads the index, then drills into the JSONL with its own bash/jq. The JSONL
contract is kept compatible with the Python/shared-runtime renderer: line 1 is a `{"type":"run"…}`
header, every later line a `{"type":"event"…}` keyed by `disp` (grounding pre-steps `P1,P2,…`; the main
loop `1,2,…`), so existing jq recipes (`select(.disp=="23").command`) keep working. The run header and
markdown outcome must preserve pull-plane `proposed_actions`, because the proposals note body can be
empty while the action proposal is real. `decorate` derives
disp/grounding/label/command/gist; `emit.go` writes the JSONL + the index (timeline, projection inputs,
flags, files read, egress, example jq calls). The run header preserves the production projection
metadata from `/trace`: `brain_resolved`, `tenant`, and the parsed `tenant_settings` snapshot
(source/synced_at/version/settings; branch selector values summarized in the markdown index when
parseable). When `/trace` also returns `tenant_settings_current`, the CLI diffs `settings` locally and
prints a drift warning only when values differ; `tenant_settings_drift` rides in the debug JSONL header.
One shape note vs the operator dump: the JSONL `egress` carries the API's aggregated rollup
(`{host, count, blocked}`), not the operator dump's per-row `{decision, port, …}` — the per-event
drill-down keys are identical, only egress differs.

### Errors
Any non-2xx → the client decodes `{"error":{code,message,details?}}` into a typed `APIError` and the CLI
prints `CODE: message` to stderr (exit 1); `INVALID_SETTINGS` field errors print one indented line each.
A non-decodable body falls back to `error: HTTP <status>` — still a clean non-zero exit, never a panic.

## Working on it

- **Toolchain:** Go 1.25 via `mise` (`mise.toml` pins it). `cobra`+`pflag`, `BurntSushi/toml`. Build/run
  from the repo dir so mise selects go 1.25.
- **Before finishing any change:** `go build ./...`, `go vet ./...`, `go test ./...`, `gofmt -w`.
- **Tests** (`internal/cli/`): golden-file tests for every table renderer + JSON-passthrough round-trip,
  driven by an `httptest` stub returning canned fixtures (`testdata/*.json` → `*.golden`), plus the
  NDJSON shape, the `rc run debug` decomposer (golden index + JSONL), the API-error path (verbatim + exit),
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
- **Collection nouns (`repo`/`connection`/`member`/`token`):** all four ride one generic path —
  [`internal/client/collections.go`](internal/client/collections.go) (list/create/patch/verb/delete over
  `/api/v1/<resource>[/{id}][/{verb}]`, items kept as flat `map[string]json.RawMessage`) +
  [`internal/render/collections.go`](internal/render/collections.go) (one list table + one item block,
  rendering whatever keys came back, `id` pinned first then sorted) +
  [`internal/cli/collections.go`](internal/cli/collections.go) (the shared `ls/add/set/rm` + verb
  helpers). The CLI holds **no per-resource field knowledge** — a new server-side field appears with no
  CLI change, same invariant as the settings bag. `connection reveal` and `token mint` print the
  secret/refresh-token to **stdout** (so a pipe captures just it) with a one-line **stderr** "shown once"
  warning; `connection rm` issues `/revoke` then `DELETE`.
- **`rc project settings runtime set` value coercion:** [`config.go`](internal/cli/config.go) is **schema-aware** — it fetches
  `/meta/schema` ONCE and coerces each `k=v` by the field's declared TYPE: a `list`/`array` type
  comma-splits into a JSON array (`pr.triggers=inbound,mcp` → `["inbound","mcp"]`; an empty value →
  `[]`, the clear gesture), a numeric type → a JSON number. On a schema miss (older server, network blip)
  it falls back to a static known-key set (`egress.allowlist`, `pr.triggers` as lists; `max_run_usd` as a
  number). The server is always the final validator.

## Local installation plumbing: `rc self doctor` / `rc self update`

[`internal/cli/doctor.go`](internal/cli/doctor.go) inventories the running executable and every PATH
candidate in order, reads embedded build metadata without executing shadowed candidates, distinguishes
a mise selector from its dispatched Go binary, and reports install/version/PATH findings alongside
local scope and update state. [`internal/cli/buildversion.go`](internal/cli/buildversion.go) makes
Go-installed binaries report their embedded module version when release ldflags are absent.
[`internal/cli/install.go`](internal/cli/install.go) is the updater's stricter physical inventory and
migration guard. macOS has one canonical install: the `rootcause-org/tap/rc` Homebrew cask.

[`internal/cli/upgrade.go`](internal/cli/upgrade.go) is the deliberate exception to "every command is
one API call": it talks to the **GitHub releases** API (not the rootcause API, no bearer key), then
self-replaces its own binary with the latest archive for the running OS/arch (sha256-verified against
the release's `checksums.txt`, atomic same-dir rename). It's CLI plumbing, not business logic. On macOS
it runs Homebrew's updater rather than overwriting the cask. `--migrate` explicitly and idempotently
canonicalizes a mixed setup: verify the updated cask first, remove only Go binaries whose embedded build
metadata identifies rootcause-cli, run `mise reshim`, then require PATH to resolve solely to Homebrew.
Unknown binaries fail closed and remain untouched. Checks inspect installation state even when the
running version is already latest, so duplicates cannot hide behind an early return. The version parser,
inventory, migration safety/idempotency, asset/checksum helpers, and messaging are unit-tested. Keep
this the *only* command that reaches outside `/api/v1`.

## Scope guards (push back if asked)

No MCP in v1 (a future layer over the same endpoints). Client-side analysis/rendering is fine, but **no
direct DB access** — data comes only through `/api/v1`, and the endpoints behind it stay thin (raw rows,
not server-computed views), with `-o json` always exposing those rows. Server writes are limited to
public config/run/brain surfaces: `rc project settings runtime set` (settings whitelist), `rc project env set/rm` (sealed per-key secret
collections), `rc ask` (triggers a run via `POST /api/v1/runs`; the CLI still holds no run logic), and
the project-only exact-SHA `rc dev brain promote`.
`rc project env pull` writes a LOCAL `./.env` only — still a GET against the API. Auth is **OAuth only**, against the server's existing `/oauth/*`
endpoints + the static first-party CLI client — the CLI invents no auth of its own (no new grant types,
no token minting beyond the standard flows). No interactive TUI/dashboard — scriptable, pipe-first,
headless.
