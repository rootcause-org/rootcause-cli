# Architecture audit — rootcause-cli — 2026-08-25

Intent sources: `SKILL.md` (four thin layers, scope model, output contract), `AGENTS.md`, `docs/specs/progressive-output-disclosure.md`, `docs/specs/brain-test-runs.md`, `internal/debugdump/dump.go:1-10` (package doc)
Scope: all of `internal/**` + `cmd/rc` (136 files, 19,986 src LOC, 10,415 test LOC) at commit `f743e67` (tree clean, fast-forwarded, up to date with origin)
Not audited: nothing skipped — the suite has no infra lane; `go test ./...` runs fully offline (6.0s wall).

Method: `loc.py`/`deps.py`/`excess.py`/`hotspots.py`/`test_stats.py` as pointers → four Opus sub-agents (one per package group + one test-economy pass) → every finding below re-opened in source by the orchestrator. Audit only; no product code or tests changed.

## Boundary map (intent per package, one line)

| package | job | intent source | status |
|---|---|---|---|
| `cmd/rc` | `cli.Execute(version)` | AGENTS.md code map | ok (9 LOC) |
| `internal/cli` | parse flags → client call(s) → render; scope contract; verbatim errors | SKILL.md "Architecture — four thin layers" | **deviations: F1, F2, F3, F5, F6** |
| `internal/client` | the ONE http wrapper + wire contract (`types.go` matches server verbatim) | SKILL.md same section | **F4 (types.go god file), F7 (analysis in client)** |
| `internal/render` | TTY-detect + JSON passthrough + per-view pure renderers | SKILL.md "Output: pipe-first" | **F8 (table.go god file), F9 (time.Now), F10 (imports outputspill)** |
| `internal/oauth` | PKCE loopback + device grant + refresh/revoke | SKILL.md "Auth" | small dead code (S3) |
| `internal/token` | dumb 0600 per-profile store | `store.go:5-7` | one-function dep on config (S5) |
| `internal/config` | env-or-production URL + brain marker → profile/project/tenant | `profiles.go:26-28` | write-only fields (S4) |
| `internal/debugdump` | decorate + emit JSONL + render index | `dump.go:1-10` | `emit.go` mixes 4 concerns (S1) |
| `internal/outputspill` | spill big stdout to disk + manifest | `docs/specs/progressive-output-disclosure.md` | dead `WithRaw` (S3) |
| `internal/dnsdetect`, `idutil`, `contextexport` | offline single-command helpers | AGENTS.md code map | ok — `Resolver` 6-method port is justified by `detect_test.go:10-35` fake |

Graph (`deps.py --depth 2`): 0 cycles, 0 hubs, 0 layer violations against `entry > cli > {render,debugdump} > infra`. Layering is honoured; the findings are about *what lives inside* the layers, not who imports whom. No `func init()`, no `t.Parallel`, no package-level mutated state anywhere in the repo (grepped).

## Findings (ranked)

### 1. `rc dev console` write bodies are encoded twice — `-o json` and table mode can send different SQL requests
Evidence `internal/cli/console.go:125-137` builds `map[string]any{"sql": …, "limit", "write", "dry_run"}` and POSTs it via `c.Raw` on the JSON path; `console.go:143` builds `client.DBQueryRequest{SQL, Limit, Write, DryRun}` for the table path. Same shape at `console.go:194/196/200` (`BashRunRequest` vs map) and `console.go:314/317/323` (`ActionExecRequest` vs map).
Why it hurts: this is the sealed DB **write plane**. A field added to one encoding but not the other silently changes what a COMMIT-ing statement does depending on whether stdout is a pipe. `types.go` is not the wire contract for these three endpoints.
Proposed seam: one typed request; the client method returns `(typed, json.RawMessage, error)` — the pattern `client.GetTenantSettings` (`client.go:394`) and `client.Export` (`mailbox.go:152`) already use. Blast radius: `console.go` + 3 client methods, ~60 LOC. Deviates from SKILL.md "types.go field names MUST match the server verbatim" and "JSON mode is a verbatim pretty-print of the server body".

### 2. The `if jsonMode { c.Raw(path) } else { c.Typed() }` double-path is repeated at ~28 call sites in 10 files
Evidence `run.go:82,112,169`; `console.go:28,58,91,137,170,247,275,317`; `schema.go:33,97`; `bag.go:43,77`; `thread.go:31`; `status.go:27`; `runs.go:44`; `projects.go:31`; `health.go:140`; `deploystate.go:45`; `ask.go:116`; `dream.go:49`; `routes.go:35,68`; `tenant.go:150`; `triage.go:167`. Generic escape hatch: `client.Raw` at `internal/client/client.go:439`.
Why it hurts: each site restates the URL twice (once as `client.XPath(...)` for `Raw`, once inside the typed method) and the two branches issue *different HTTP requests* for one user command; F1 is the worst instance. It defeats "one method per endpoint".
Proposed seam: make typed client methods uniformly return `(typed, json.RawMessage, error)` (already done for ~25 methods — see "Confirmed OK" below) and remove `Raw`/`RawScoped` from the command layer. Blast radius: ~25 client methods (~150 LOC) + ~28 CLI sites (~120 LOC removed net).

### 3. `kb_content.go` ships ~200 lines of Python as Go string literals, executed on the server box
Evidence `internal/cli/kb_content.go:748` (`kbPython` = `"python3 - <<'PY'\n…"`), programs at `:544` (list), `:579` (search — 84 LOC incl. regex scoring, front-matter parsing, ranking), `:666`, `:708`, `:730`; sent through `c.BashRun` at `:285`. `-o json` emits `kbCommandSummary` (`:82`), a CLI-invented envelope.
Why it hurts: the KB search *ranking algorithm* is versioned in the CLI binary, unreachable by `go vet`/tests, re-shipped on every `rc self update`, and `-o json` here is not a server body. It is a de-facto private endpoint implemented client-side.
Proposed seam: a real `/api/v1/kb/{list,search,article}` family (server side, out of this repo's scope); until then `//go:embed *.py` so the scripts lint/diff as Python. Blast radius: `kb_content.go` (754 LOC). Deviates from SKILL.md "keep the endpoints thin and always expose the raw rows via `-o json`".

### 4. `internal/client/types.go` is a god module and the #1 churn hotspot
Evidence 1,540 lines, **124 exported types**, ~14 unrelated families (`ConsoleDBInfo:12`, `BrainStatus:80`, `RunSummary:348`, `WatchedMailbox:930`, exports `:982`, egress `:1105`, health `:1292`, deploy `:1339`, tenant/settings `:1429-1537`); 64 commits / 2,298 churn lines in 12 months (next file: 28 commits). Siblings already own their own types: `connection_probe.go:9-60`, `watchedmailbox.go:46,61`, `spam.go:14`, `projection.go:12-27`, `observability.go:14,133` — so one-file is residue, not policy.
Why it hurts: parallel agents on `main` collide on this file constantly; a reviewer cannot see which endpoint a type change belongs to.
Proposed seam: move each family next to its method file (`console_types.go`, `brain_types.go`, …), keep `types.go` for shared primitives. Blast radius: zero import churn (same package), `go build` verifies. SKILL.md says "wire contract (types.go)" — nothing says one file.

### 5. Eleven `render*` view functions live in `internal/cli`, including the only `tabwriter` outside `internal/render`
Evidence `doctor.go:452` (`renderDoctorHuman`, `tabwriter.NewWriter` at `:453`), `tenant.go:416` (`renderTenantSettings`: schema grouping, x-order sort, orphan-key spill — real view computation), `hierarchy_settings.go:257,295,304`, `env.go:307`, `upgrade.go:116`, `export.go:257` (+ `escapeTableCell` `:309`), `kb_content.go:375,392,418`.
Why it hurts: they take `*env` not `io.Writer`, so are testable only through a full Cobra invocation and none is golden-covered by the render fixture harness.
Proposed seam: `func(w io.Writer, typed…)` in `internal/render/{doctor,tenant,hierarchy,env,kb}.go`. Blast radius: 8 files, ~350 LOC moved, ~15 call sites. Deviates from SKILL.md "command files own thin endpoint adapters".

### 6. `newRunViewCmd` is a 7-way enum dispatch over unrelated endpoints
Evidence `internal/cli/run.go:55-193` (139 LOC) switches on `runView` (`:18-30`) across `debug :76`, `trace :82/91`, `events :100`, `brain-diff :112/123`, `egress :131`, `actions :148`, fallthrough `show :169`; `--stream` re-gated by view at `:189`. Shared body is 6 lines.
Why it hurts: a new run view edits an enum, a switch and a flag gate; the branches share only scaffolding.
Proposed seam: seven small `newRunXCmd(e)` + one `withRunID(e, fn)` helper. Blast radius: `run.go` ~140 LOC; command paths/flags unchanged so `docs/cli-help.txt` stays byte-identical.

### 7. Analysis/presentation logic inside `internal/client`
Evidence `internal/client/projection.go:148` `SortGroundingSources` ("returns a copy ordered for human triage"), `:120,133` drift/attention counters, `:50` `BranchSelectorValues` with a hard-coded heuristic key list, `:77` `TenantSettingsDrift`. Callers are all presentation: `render/table.go:637,740,743,762,891`, `debugdump/emit.go:58,82,322,485-493`, `cli/run.go:535`; zero client-internal callers.
Proposed seam: move `projection.go` to `internal/render` (or `internal/digest`); client keeps only `ParsePromptSections`/`ParseManifestBlocks`/`ContextCaptured` (decoders of its own wire fields). Blast radius: 3 packages, ~15 call sites, no behaviour change. Deviates from SKILL.md "four thin layers, **no logic**" / AGENTS.md "analysis logic lives in the CLI".

### 8. `internal/render/table.go` is a god file — 15 views, 1,210 lines
Evidence `Projects:23`, `ProjectRename:37`, `Status:43`, `Runs:94`, `Run:135`, `AskEmail:181`, `AskRaw:209-429`, `BrainDiff:429-562`, `Events:562`, `Full:602-902`, `Settings:902`, `Schema:915`, `ExplainField:945`, `Access:990`, `SpamRules:1014`. Every other family already has its own file (`fleet.go`, `console.go`, `egress.go`, `mailbox.go`, …).
Proposed seam: split by family; shared formatters (`duration:1111`, `truncate:551`, `num:1157`) → `format.go`. Blast radius: package-internal; 79 goldens pin output byte-for-byte, so `go test ./internal/cli` verifies the split completely.

### 9. Two `time.Now()` calls make renderers non-deterministic — and block goldens
Evidence `internal/render/fleet.go:440` (`fleetStuck`) and `internal/render/health.go:176` (`expiredTime`). `fleet.go:229 isStuck(r, now)` already takes `now` — the seam is closed one frame too late. This is why there is no `fleet_stuck.golden` (AGENTS.md: fixtures never use `time.Now`).
Proposed seam: `now` on `FleetOptions`/`HealthOptions`, defaulted at the `internal/cli` call site. Blast radius: 2 render files + 2 cli sites; unlocks 2 goldens. Lint rule C below enforces it afterwards.

### 10. `render → outputspill` is a narrow layer inversion
Evidence `internal/render/console.go:12` imports `outputspill`; used at `:162` (`BashRun(w, r, artifacts map[string]outputspill.Artifact)`) and `:196`. The renderer only reads `Path/Bytes/Lines/Preview`, but `outputspill` does I/O + env (`outputspill.go:64,105,109,128,443`), so render transitively depends on the filesystem layer for a struct shape — and `BashRun` is one of the few uncovered renderers because of it.
Proposed seam: a local `spillArtifact` struct in render, mapped in `internal/cli/outputspill.go` (which SKILL.md already names as the wiring point). Blast radius: 1 render file, 1 cli call site.

### 11. Duplicated adaptation across cli/render (small, but goldens lock the divergence in)
Evidence prefix clippers: `render/fleet.go:738 short8`, `render/thread.go:17 shortID` (`""→""`), `cli/run.go:517 shortID` (`""→"unknown"`), `render/deploystate.go:108 short12`, `render/console.go:524 shortGit`, `render/table.go:470 shortSHA`. Dash helpers: `cli/auth.go:416 emptyDash`, `render/console.go:459 dash`, `render/deploystate.go:118 orDash`. Durations: `table.go:1111 duration`, `actions.go:100 nullableDuration`, `actions.go:82`, `egress.go:74`, inline `%d ms` at `console.go:81,97,193,443,487`. Also two parallel "inventory rc installs on PATH" implementations: `internal/cli/install.go:128 classifyInstallPath` (used by `upgrade.go:62,201`) vs `internal/cli/doctor.go:215 classifyInstall` (used by `doctor.go:121`).
Proposed seam: `render/format.go` (`clipID`, `clip`, `duration`) and one install inventory used by both `doctor` and `self update`. Blast radius: ~8 files, ~80 LOC removed, some golden churn (some `%dms` vs `%d ms` differences are intentional — consolidate, don't unify output).

### Confirmed OK — do not "clean up"
- The `(typed, json.RawMessage, error)` dual return on ~25 client methods (`config_surface.go:87-306`, `client.go:112,125,207,394,411`, `exports.go`, `watchedmailbox.go`, `spam.go`) **is** the intended byte-faithful seam (SKILL.md "the CLI cannot invent or drop a field"). F2 asks to finish that pattern, not remove it.
- ~57 "unused" exported client types are nested wire structs that must be exported for `encoding/json`.
- `dnsdetect.Resolver` (6 methods, 1 impl): every method is used by `Detect` and a canned fake drives 7 offline tests — textbook port.
- `outputspill` env knobs are read exactly once each inside `NewConfig` (`outputspill.go:54-64`), single caller `internal/cli/outputspill.go:11-13`.
- `machine_token_env.go:34-40` vs `:65-73` look like copy-paste but differ deliberately (stored-token escape hatch at `:66`) — worth a comment, not a refactor.

## Simplification proposals

### S1. Split `internal/debugdump/emit.go` (932 lines) into its documented seams
Intent "decorate (dump.go) + emit JSONL + render index (emit.go)" (`dump.go:1-10`, SKILL.md `internal/debugdump/` line) · Exists: `EmitJSONL 18-123` + JSON shapers `801-862`; `RenderIndex 124-302` + sub-renderers `303-676`; **run analysis** `flags 677-756`, `benignGrepMiss 760`, `filesRead 771-800`, `median 921` — the anomaly policy (repeated-command flailing, 4× median spike, EGRESS_BLOCKED, >20 KB stdout) is buried between two renderers and is undocumented because it has no file.
Simpler design: `jsonl.go` / `index.go` / `analysis.go`, pure moves · Est. removed 0 LOC (a move; 930 → 3 files ≤550) · Risk if wrong: none — same package, `emit_test.go` (7 tests) + goldens guard.

### S2. `newRunViewCmd` → per-view constructors (F6) · ~140 LOC touched, ~30 removed · Risk: help text drift, caught by `TestRecursiveHelpAndREADMEInventoryFresh`.

### S3. Dead code, verified by grep (no callers anywhere)
- `internal/oauth/oauth.go:81` `now func() time.Time` — never assigned (only decl + read at `:100-101`); `clock()` `:99-103` is `time.Now()` behind a dead nil check. **~9 LOC.**
- `internal/oauth/oauth.go:92-97` `httpClient()` nil fallback to `http.DefaultClient` — `&Client{}` is built only in `NewClient` (`:86`), which always sets `HTTP`. The fallback silently drops the 30 s timeout if ever hit. **~6 LOC.**
- `internal/outputspill/outputspill.go:80` `WithRaw` — definition only; raw is plumbed via `NewConfig(dir, noPreview, raw)` (`internal/cli/outputspill.go:12`). **4 LOC.**
- `internal/debugdump/dump.go:303` `func max(a, b int)` shadows the Go 1.21+ builtin (module is Go 1.25). **6 LOC.**
Risk if wrong: none; `go build` catches every site.

### S4. Write-only config fields
- `internal/config/profiles.go:70` `Resolved.BaseURLFromDefault` — written at `:148,:153` and `internal/cli/root.go:173`; read only by `profiles_test.go:58,74,88`. `BaseURLSource` (read at `cli/auth.go:211,248`, `doctor.go:491`) carries the same information. **~10 LOC.**
- `profiles.go:86` `Brain.BaseURL` — its own comment (`:81`) says "legacy decoded field, ignored by resolution"; no reader. BurntSushi/toml ignores unknown keys, so old markers keep parsing. **~4 LOC.** Intent: `profiles.go:26-28` precedence ladder does not include a marker URL.
Risk if wrong: a future "reject markers carrying base_url" check would need it back — nothing does today.

### S5. `token → config` edge is one function wide and points the wrong way
`internal/token/store.go:65` calls `config.ConfigDir()` (`profiles.go:273-282`, sole caller; comment at `:271` literally says "exported for internal/token"). Move `ConfigDir` into `internal/token`; `token.Path()` is already the accessor everyone uses (106 refs). **0 net LOC, −1 package edge.** Risk: none; `store_test.go:15` already isolates via `XDG_CONFIG_HOME`.

### S6. Test-seam in `newClient` skips the production pre-flight
`internal/cli/root.go:177-188`: with `e.tokenOvr != ""` the function returns before `loadResolvedToken :190`, brain→default fallback `:195-206`, machine-token `/whoami` check `:211-219`, `resolveProjectForTenant :221`. Every golden/CLI test runs through `tokenOvr`, so no CLI test exercises the real ordering of tenant resolution vs scope enforcement. Simpler: inject a `tokenSourceFactory` so the override replaces only the credential and there is one control-flow path. ~40 LOC. Risk: tests that relied on skipping `/whoami` need a stub route. Intent: SKILL.md "Tests inject `client.StaticToken` to bypass the store" — the store, not the scope pipeline.

## Test economy

Note on tooling: `test_stats.py` reports 27 clusters / 255 tests (a 174-test cluster keyed on `config_surface_test.go:774`). That is an artifact of the shared harness in `internal/cli/cli_test.go` (1,508 lines, **zero `func Test`** — `stubServer`/`newTestEnv`/`run`/`assertGolden`/`assertJSONEqual`): every command test is the same 9-line shape over a different command + fixture. Re-clustering on bodies and opening every member gives **8 real clusters / 50 tests**, and all but two members pin a distinct renderer or endpoint. 450 tests, avg 2.96 asserts/test, 0 mock-only, 0 assertion-free, 0 orphan fixtures (79 `.golden` + 64 `.json`).

| cut | reason | covered by (survivor) |
|---|---|---|
| `internal/cli/ask_test.go:597 TestAskBadScenario` | strict subset: asserts only `Contains(err, "invalid --scenario")`; survivor asserts the full message on the same path | `internal/cli/ask_test.go:149 TestAskRejectsUnknownScenario` |
| `internal/client/actions_test.go:46 TestAllActionsPagesAndPreservesRawRows` | layered duplicate: 2-page cursor walk + unknown-field + params preservation, proven end-to-end by the survivor (which also covers repeatable `--action/--status`). Loses the only explicit `capped == false` assert; `capped == true` stays at `internal/client/actions_test.go:88` | `internal/cli/actions_test.go:12 TestFleetActionsPagesFiltersAndPreservesRawRows` |
| **merge** 24 golden tests → one table `TestTableGolden` in `golden_test.go` | identical body, only `args`+`golden` vary: `golden_test.go:16,26,36,46,544,644,685`; `collections_test.go:13,148,168,188,265,287,329`; `config_surface_test.go:21,173,183,193,304,326,346,638`; `export_test.go:18,38` | same tests as subtests (per-renderer failure granularity kept) |
| **merge** 13 JSON-passthrough tests → one table `TestJSONPassthrough` | identical body, only `args`+`fixture` vary: `golden_test.go:140,534,654,708,718`; `collections_test.go:23,198,297`; `config_surface_test.go:31,336`; `deploystate_test.go:132`; `export_test.go:28,48` | same tests as subtests |

Examined and **kept** (distinct behaviour): `config_surface_test.go:429/448/607` (three commands' validation matrices), `collections_test.go:240` vs `config_surface_test.go:156`, `auth_test.go:166` vs `:194`, `scope_test.go:238` vs `:259`, `ask_test.go:105` vs `:561`, `tenant_test.go:27` vs `:143`, `actions_test.go:135` vs `observability_test.go:476` (each command must call `rawRowsJSON` itself — the per-command guard *is* the contract), `install_test.go:103` vs `doctor_test.go:158` (different prod functions — see F11; consolidate prod first).

Totals: 2 hard cuts (~56 LOC) + 37 → 2 table merges (~348 LOC); ~404 of 11,765 test LOC (3.4%), 450 → 413 tests, zero coverage change.

Keep-list (critical path → owner today): nine roots + retired roots dead `surface_test.go:17` · every command declares scope `scope_test.go:16` · fail-closed selectors before request `scope_test.go:73,93,114` · scope as query param / absent when unscoped `observability_test.go:22,67` · JSON verbatim passthrough (the 13 above; anchor `golden_test.go:534`) · unknown server fields survive typed structs `client/types_test.go:9` · PKCE login/state mismatch/browser failure `oauth/oauth_test.go:21,75,120,159` · device grant `cli/auth_test.go:106` · refresh-on-401 + rotation persisted `auth_test.go:334`, concurrent rotation `:366`, dead refresh → re-login `:400` · logout revokes `:144` · store 0600 `token/store_test.go:54`, cross-process profiles `:91` · machine-token env fail-closed `machine_token_env_test.go:16-180` · config precedence `config/profiles_test.go:44-302` · output spill + secrets stay raw `outputspill_test.go:11,40,61`, `collections_test.go:43`, `export_test.go:221`, `golden_test.go:170,198,763` · secret file 0600 + `.gitignore` `export_test.go:117` · API errors verbatim `ask_test.go:642` · run poll/timeout `ask_test.go:662,692` · cursor paging + cap `cli/actions_test.go:12`, `client/actions_test.go:88` · decomposer `debugdump/emit_test.go` · release publishes main before tag `releasetest/release_script_test.go:14` · generated docs fresh `surface_test.go:110` (0.02 s; one `go test ./internal/cli -update` regenerates goldens, README inventory and `docs/cli-help.txt`).

Render coverage is **not** a gap despite test/src 0.06: `go test ./internal/cli -coverpkg=./internal/render/...` → 78.4% statements (avg 82% over 208 funcs) via the goldens. The only 0% renderers are the `rc dev console` action/db-introspection plane: `render/console.go:15 Capabilities`, `:39 DBList`, `:52 DBSchema`, `:374 ActionList`, `:389 ActionShow`, `:486 ActionExec` (+ `client/console.go:23-91` same set) and `table.go:350 renderNoteActions`. Cheapest real win: 6 fixtures + 6 goldens.

## Test suite speed
Total **6.0 s wall** (`/usr/bin/time mise exec -- go test ./... -count=1`; all lanes offline, nothing "not timed"). Per package: cli 5.31 s · releasetest 3.93 s · token 1.93 · contextexport 1.87 · config 1.72 · render 1.70 · outputspill 1.55 · debugdump 1.40 · oauth 1.09 · client 0.96 · idutil 0.72 · dnsdetect 0.58 (≈0.5 s each is compile/link).

Time sinks (real wall-clock sleeps, 3.0 s of the cli lane's 5.3 s):
- `TestLoginDeviceStoresToken` `internal/cli/auth_test.go:106` — **2.00 s**. `internal/oauth/device.go:45` `interval := da.Interval * time.Second`; stub sends `"interval":1` (`auth_test.go:36`, the RFC 8628 minimum) and `device.go:53` waits *before* the first poll; stub needs 2 polls (`:41`) ⇒ 2 × 1 s. Fix: `Client.PollWait func(time.Duration) <-chan time.Time` (default `time.After`) next to the clock field at `oauth.go:80`, used at `device.go:53`, overridden from `internal/cli/auth.go:71` — same precedent as `env.openBrowser` (`root.go:43`). Saves ~2.0 s.
- `TestMailboxHarvestWait` `internal/cli/export_test.go:104` — **1.00 s**. `internal/cli/mailbox.go:153` `const interval = time.Second`, timer at `:175`; the comment at `:150` claims "no fixed sleep in tests" — false. Sibling `waitForRun` (`ask.go:315`) honours the server's `poll_after_ms`, which is why all 33 ask tests are ≤0.03 s. Fix: package `var exportPollInterval` (or a server poll hint) + fix the stale comment. Saves ~1.0 s.
- `TestReleasePublishesMainBeforeTag` `internal/releasetest/release_script_test.go:14` — 2.69 s, **no sleep**: ~15 `git` subprocesses + `bash scripts/release.sh` (sleeps at lines 173/191/207/217 only on retry, never hit). Leave; it is the only proof of publish-before-tag and becomes the critical path after the two fixes.
- `TestLocalUnpromoted*` `deploystate_test.go:47,71` — 0.7 s of `gitRepo(t)` (7 git subprocesses each); optional per-package fixture reuse. `upgrade_test.go:197` 0.33 s of real `/bin/sh` shims — leave.

Proposed: 2 seam fixes, ~3.0 s saved → cli ≈2.3 s, wall ≈4.0 s (bounded by releasetest).

## Proposed custom lint rules (each run against the repo via `lint_rules.py`; every hit a true violation)

| rule | vehicle | hits today | all true? |
|---|---|---|---|
| `internal/cli` must not use `text/tabwriter` | depguard (pkg deny) | 2 (`internal/cli/doctor.go:13,453`) | yes (F5) |
| no `^func render[A-Z]…(` outside `internal/render` | forbidigo, path-scoped | 11 (`doctor.go:452`, `env.go:307`, `export.go:257`, `hierarchy_settings.go:257,295,304`, `kb_content.go:375,392,418`, `tenant.go:416`, `upgrade.go:116`) | yes (F5) |
| no interpreter heredoc (`python3 - <<`, `bash - <<`, …) in Go source | forbidigo | 1 (`internal/cli/kb_content.go:748`) | yes (F3) |
| `internal/render` (except `render.go`, the TTY detector) must not import `os`, `net`, `net/http`, `os/exec` | depguard | 0 | ratchet |
| `internal/render` must not call `time.Now()` | forbidigo, path-scoped | 2 (`render/fleet.go:440`, `render/health.go:176`) | yes (F9) — enable after the `now` threading, or land with 2 `//nolint` as a debt ratchet |
| `internal/client` imports no sibling `internal/*` package | depguard | 0 | ratchet — pins client as the leaf |
| no `os.Getenv` in `internal/render`, `internal/client` | forbidigo | 0 | ratchet |
| no package-local `func max/min/clear/any(` | forbidigo | 1 (`internal/debugdump/dump.go:303`) | yes (S3) |

Dropped (false positives): "dual-encoded write body" (F1) — the loose form `Raw(…, POST/PATCH/PUT …)` also flags `bag.go:77`, which legitimately passes the same `patch` var down both branches; the true rule is "no `Raw` in `internal/cli`", enforceable only after F2. "`os.Getenv` in `internal/cli`" — 15 hits, 8 are legitimate install inspection (`PATH`/`GOBIN`/`GOPATH`/`MISE_DATA_DIR` in `upgrade.go`, `doctor.go`); narrowing to `RC_|ROOTCAUSE_|CLAUDE_` leaves 7 config-shaped reads (`machine_token_env.go:28,47,58,93`, `mailbox.go:416,425`, `contextexport.go:35`) that are arguably fine where they are — not shipped.

Prototype (validated with `lint_rules.py`, output above):

```yaml
- id: cli-no-tabwriter
  paths: internal/cli/**
  exclude: internal/cli/*_test.go
  forbid: '"text/tabwriter"|tabwriter\.NewWriter'
- id: cli-no-render-funcs
  paths: internal/cli/**
  exclude: internal/cli/*_test.go
  forbid: '^func render[A-Z][A-Za-z]*\('
- id: cli-no-embedded-interpreter
  paths: internal/**
  exclude: internal/**/*_test.go
  forbid: '(python3|python|bash|sh) - ?<<'
- id: render-no-os-net
  paths: internal/render/**
  exclude: internal/render/render.go, internal/render/*_test.go
  forbid: '^\s+"(os|net|net/http|os/exec)"$'
- id: render-no-time-now
  paths: internal/render/**
  exclude: internal/render/*_test.go
  forbid: 'time\.Now\(\)'
- id: client-leaf
  paths: internal/client/**
  forbid: 'rootcause-cli/internal/(render|cli|outputspill|debugdump|config|token|oauth)'
- id: no-getenv-in-client-render
  paths: internal/render/**, internal/client/**
  forbid: 'os\.Getenv'
- id: no-builtin-shadow
  paths: internal/**, cmd/**
  forbid: '^func (max|min|clear|any)\('
```

Native port — the repo has **no `.golangci.yml`** today (AGENTS.md's global rule says run `golangci-lint run` after `.go` edits, so adding one is the natural vehicle):

```yaml
# .golangci.yml (proposed, NOT wired)
linters:
  enable: [depguard, forbidigo]
linters-settings:
  depguard:
    rules:
      cli-no-tabwriter:
        files: ["**/internal/cli/**", "!**/*_test.go"]
        deny:
          - pkg: text/tabwriter
            desc: table formatting belongs in internal/render
      render-pure:
        files: ["**/internal/render/**", "!**/internal/render/render.go", "!**/*_test.go"]
        deny:
          - {pkg: os, desc: renderers are pure functions of rows}
          - {pkg: net/http, desc: renderers are pure functions of rows}
          - {pkg: os/exec, desc: renderers are pure functions of rows}
          - {pkg: github.com/rootcause-org/rootcause-cli/internal/outputspill, desc: map the artifact into a render-owned type (audit F10)}
      client-leaf:
        files: ["**/internal/client/**"]
        deny:
          - pkg: github.com/rootcause-org/rootcause-cli/internal
            desc: internal/client imports no sibling package
  forbidigo:
    analyze-types: true
    forbid:
      - p: '^time\.Now$'
        pkg: '^github.com/rootcause-org/rootcause-cli/internal/render$'
        msg: thread `now` in from internal/cli
      - p: '^os\.Getenv$'
        pkg: '^github.com/rootcause-org/rootcause-cli/internal/(render|client)$'
```
(`render-no-time-now` currently has 2 true hits — land the F9 fix first or add `//nolint:forbidigo` at both sites. The `render*`-outside-render and heredoc rules have no clean forbidigo form; keep them in `lint_rules.py` or a `scripts/lint-contracts.sh` grep.)

## Proposed dependency contract (NOT wired into CI)
The depguard block above is the contract: `cli → {render, debugdump, client, config, token, oauth, outputspill, dnsdetect, idutil, contextexport}`, `render → client` only (after F10), `debugdump → client`, `token` and `client` leaves (after S5). Would have caught: F10 (`render → outputspill`), S5 (`token → config`), and pins F7's direction (client never imports render). The current `deps.py --layers` run (`entry > cli > view > infra`) reports 0 violations, so the contract is a ratchet, not a repair.

## Deliberately not done
- No code or test changes (audit only). All simplifications (S1–S6) and the two timing seams are deferred; each is ≤ ~150 LOC and golden-guarded.
- F3 (KB search as a server endpoint) needs the rootcause server repo — out of scope here; the `go:embed` half-step is local.
- `.golangci.yml` not added: adding it would fail today on F5/F9/S3 hits and the repo's global "run golangci-lint" rule would start blocking other agents. Wire it after F5/F9 land, or with the noted `//nolint` ratchets.
- `test_stats.py` clustering is misleading on this harness shape (see Test economy); its 27-cluster figure is not used.
- Open question: whether `-o json` for `rc project knowledge content search` should stay a CLI-invented `kbCommandSummary` envelope (F3) or be documented in SKILL.md as the one sanctioned exception, alongside the `--format` carve-out already documented for fleet views.
