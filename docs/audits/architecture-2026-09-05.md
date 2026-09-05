# Architecture audit — rootcause-cli — 2026-09-05

Intent sources: `SKILL.md` (four thin layers, one method per endpoint, JSON = verbatim server body, scope model), `AGENTS.md` (fat client / raw rows via `-o json`, no `time.Now` in fixtures), `docs/specs/composable-cli-primitives.md` (+ audit-1/2/3), `docs/specs/progressive-output-disclosure.md`, `docs/specs/cloud-mirror-self-update.md`, `internal/debugdump/dump.go:1-10`.
Scope: all of `internal/**` + `cmd/rc` + `scripts/` + `.github/` (146 Go files, 23,002 src LOC / 11,908 test LOC by `loc.py`; file sizes below are `wc -l`) at `85b5d73` (tree clean, fast-forwarded, up to date with origin). 44 commits since the 2026-08-25 audit (`15f6a0d`), ~+3,000 LOC: composable console primitives, embedded chat + action doctors, S3 release mirror + hosted cloud-setup, `run guards`, brain boot check.
Not audited: nothing skipped — suite is fully offline (wall 11.6 s cold, see Speed).

Method: `loc.py`/`deps.py`/`excess.py`/`hotspots.py`/`test_stats.py`/`lint_rules.py` as pointers → four Opus sub-agents (cli · client/render/debugdump · test economy · infra/guardrails), every finding below re-opened in source by the orchestrator. Previous report used as a hypothesis; each old finding re-verified. Audit only: no product code or tests changed.

**Headline.** Graph still clean (0 cycles, 0 layer violations, `client` and `render` still leaves modulo F10). But **none of the 2026-08-25 proposals landed** (0/8 lint rules, 0/6 simplifications, 0/2 timing seams, F1 only partially), and the ~3,000 new LOC re-introduced the same three patterns: the `Raw`-vs-typed double path (+3 sites, plus a new generic `ChatRaw(method, suffix)`), view/diagnosis logic in `internal/cli` (two 100–145 LOC doctor closures), and `types.go` growth (+253 lines). The one structural regression is a **third transport path** in `internal/client` (chat SSE bypasses refresh/backoff). The one correctness bug is F1 below.

## Boundary map

| package | intent (source) | status |
|---|---|---|
| `cmd/rc` | `cli.Execute(version)` (AGENTS.md) | ok |
| `internal/cli` | flags → client call(s) → render; thin adapters; JSON verbatim (SKILL.md "four thin layers") | **F1 F2 F3 F4 F6 F7 F8 F13 F14** |
| `internal/client` | the ONE http wrapper + wire contract, leaf (SKILL.md) | **F5 (3 transports) F9 (types.go) F11 (projection analysis)**; `os.Getenv` regression (L7) |
| `internal/render` | pure functions of rows (SKILL.md "Output") | **F10 (table.go) F12 (time.Now) F15 (outputspill import) F8 (formatter sprawl)** |
| `internal/debugdump` | decorate (dump.go) + emit JSONL + index (emit.go) | S1 (emit.go 4 concerns), S2 (dump.go renders Markdown) |
| `internal/oauth` `token` `config` `outputspill` | protocol client · 0600 store · URL/profile ladder · spill | S3 dead code, S4 write-only fields, S5 `token→config` edge |
| `dnsdetect` `idutil` `contextexport` | offline helpers | ok (`Resolver` port justified by canned fake) |
| `scripts/` + `.github/` | release transaction, hosted cloud-setup, CI | F16 mirror constants ×4, F17 `latest` unverified, G1 no lint gate |

`deps.py --layers entry>cli>{render,debugdump}>infra`: 0 violations. No `func init()`, no `t.Parallel`, no package-level mutable state (grepped).

## Findings (ranked)

### F1. `chat doctor` exit code depends on output mode — piped/`-o json` always exits 0 (correctness)
Evidence `internal/cli/chat.go:433-452`: `if bundle || e.jsonOut() { …; return render.JSON(…) }` returns **before** the `failed` fold at `:437-442`, so `CHAT_DOCTOR_FAILED` (`:452`) can only fire on a TTY. Pipe-first default means any script (`rc project chat doctor | jq`) gets exit 0 on broken chat wiring. `action_dx.go:147,214-226` does it right (`failed` accumulated in the step loop, checked after the JSON branch). No test covers the JSON+failed case (`chat_test.go:124` asserts the bundle only).
Why it hurts: violates `composable-cli-primitives.md` typed exit codes; exactly the "false clean bill of health" class SKILL.md warns about. Seam: hoist the fold above the mode branch (mirror action_dx). Blast radius: ~10 LOC + 1 test.

### F2. The `if jsonMode { Raw } else { Typed }` double-path grew to 29 sites / 17 files; new code added a worse variant
Evidence sites: `thread.go:39` `tenant.go:150` `action_dx.go:316` `run.go:85,116,195` `bag.go:43,77` `console.go:28,58,91,290,324,436,464,506` `ask.go:116` `health.go:140` `dream.go:77` `triage.go:167` `deploystate.go:45` `chat.go:351` `runs.go:45` `projects.go:31` `routes.go:35,68` `schema.go:33,97` `status.go:27`. ~20 are genuine double-fetch (two different HTTP requests for one user command); `dream.go:77`, `action_dx.go:316`, `triage.go:167`, `chat.go:351` are single-path and fine.
New variant: `internal/client/chat.go:29 ChatRaw(ctx, method, project, suffix string, body map[string]any)` + `PrincipalsRaw:55`/`PrincipalResolveRaw:59` — the **path, verb, query and body shape** of 8 chat endpoints now live in `internal/cli` (`cli/chat.go:64 "/secret/"+action`, `:100 "/token"`, `:361 "/rejects?limit=100"`). `client/chat.go` declares zero wire types.
Client method census (117 methods): 58 typed-only · 37 typed+raw · 22 raw-only — the intended `(typed, json.RawMessage, error)` seam is a 32 % minority and drifting down.
Why it hurts: SKILL.md "one method per endpoint; types.go is the wire contract" is false for ~30 endpoints; F1-class bugs (dual encoding) hide here. Seam: finish the `(typed, raw, error)` return on every method, one named method per chat endpoint, delete `Raw/RawScoped/ChatRaw` from the command layer; then lint L9 becomes enforceable. Blast radius: ~20 cli sites + ~25 client methods, net −120 LOC.

### F3. Dual-encoded write body on the guarded action-exec plane (old F1, narrowed)
Evidence `internal/cli/console.go:503` `body := map[string]any{"params": p}` (JSON path, `:506 c.Raw(POST)`) vs `:512 client.ActionExecRequest{Params: p}` (table path). Fixed since 08-25: DB query (`console.go:148`, one `client.DBQueryRequest`), bash run (`:320-324`, one `Raw` call — but it hand-builds `{"command","timeout_s"}` while `kb_content.go:288` uses the typed `client.BashRun`; `client/console.go:187` exists and is bypassed).
Why it hurts: the two encodings agree today only because `ActionExecRequest` (`types.go:413`) has one field. Seam: `ActionRun/ActionPreflight` return `(typed, raw, error)`; `console.go:324` calls `c.BashRun`. Blast radius: ~15 LOC + 3 client methods. Lint L9 pins it (2 true hits today).

### F4. `-o json` for `run guards|egress|actions` re-marshals a closed typed struct, not the server body
Evidence `internal/cli/run.go:161` `raw, err := json.Marshal(resp.Run.Guards)`; same at `:135` (egress) and `:180` (actions). `client.GuardsView` (`types.go:949-1013`) is closed (typed sub-objects, no catch-all). Contrast `run.go:85,116` (trace, brain-diff) which use `Raw` "so no server field is dropped on the cross-repo seam".
Why it hurts: SKILL.md "the CLI cannot invent or drop a field" — a new server guard field disappears from `-o json` until the CLI is rebuilt; `client/types_test.go:9` (unknown fields survive) does not cover this projection. Seam: guards ride on `/trace`; fetch raw once and project `.run.guards` as `json.RawMessage`, or add a raw carrier. Blast radius: `run.go` ~30 LOC, 1 golden.

### F5. Three transport paths in `internal/client`; chat SSE bypasses refresh and backoff
Evidence `client.go:626 do` (buffered; refresh-on-401 + backoff), `client.go:491 openStream` (re-implements the refresh loop `:509-527` and retry loop `:529-541` inline, ~50 LOC parallel to `do`), `client/chat.go:68,121,180` raw `c.http.Do(req)` with no refresh, no 429/5xx backoff, no shared error path; the repo's only `errcheck` hits are here (`chat.go:72,125,184`). `console.go:97 DBQueryStream` correctly reuses `openStream`.
Why it hurts: SKILL.md promises "the ONE http wrapper (refresh-on-401 retry)"; policy now lives in two places and is absent in a third; `retry_test.go` covers neither stream. The embed-JWT credential justifies skipping *OAuth refresh*, not skipping backoff/headers/error decoding. Seam: one `send(ctx, req, opts{buffer bool, cred})` loop; `do` = `openStream` + read-all; chat passes an `embedTokenSource`. Blast radius: `client.go` ~120 LOC restructured, `chat.go` ~60 LOC, tests in `retry_test.go`.

### F6. Two doctor commands put diagnosis logic in `internal/cli` closures and invent their `-o json`
Evidence `chat.go:312-461 chatDoctorCmd` (143 LOC in one `RunE`: 5 fetches, staleness fold `:369-373`, dominant-reject pick `:607-626`, findings builder `:380-398`, human table `:437-450`, `time.Now()` at `:325`); `action_dx.go:126-232 newActionDoctorCmd` (104 LOC, same anatomy, `time.Now()` `:144`). `-o json` is a CLI-invented envelope (`chat.go:290-310 doctorBundle`, `action_dx.go:112-124`) and the only JSON output; `doctorReject` drops server reject fields (`ip_prefix`, `session_id` per `chat_test.go:140`) silently. `--bundle` is a redundant alias for `-o json` on both, described as "redacted" though no redaction step exists (`chat.go:458`, `action_dx.go:230`).
Why it hurts: AGENTS.md "every such command MUST still expose the raw rows via `-o json`"; precedent for exactly this shape is `render/health.go`+`fleet.go` (pure, golden-covered). No golden possible while `now` is baked in. Same open question as `kbCommandSummary` (F13), now three commands deep. Seam: pure `buildChatDoctor(inputs, now) doctorBundle` + `render.ChatDoctor(w, b)`; decide once (SKILL.md) whether doctor-family JSON is a documented carve-out or must carry raw rows. Blast radius: 2 files, ~250 LOC moved, unlocks 2 goldens.

### F7. `newRunViewCmd` is now an 8-way enum dispatch (old F6, worse)
Evidence `internal/cli/run.go:18-30` enum (`runViewGuards` added `:29`), dispatch `:57-213` = 145 LOC of `if view == …` blocks (`:73 :81 :99 :111 :130 :149 :172`, fallthrough `:193`), flag re-gate `:209`. Shared body ~6 lines. Seam: 8 × `newRunXCmd(e)` + `withRunID(e, fn)`; paths/flags unchanged ⇒ `docs/cli-help.txt` byte-identical. Blast radius: `run.go` ~145 LOC.

### F8. Formatter sprawl, now cross-package (old F11, grown)
Evidence ID clippers: `render/fleet.go:785 short8` ≡ `render/thread.go:17 shortID`; `render/table.go:476 shortSHA` ≡ `render/console.go:555 shortGit`; `render/deploystate.go:108 short12`; `cli/run.go:542 shortID` (`""→"unknown"`); **new** `cli/console_io.go:161 shortRunID` (dash-strip) vs `cli/console.go:566 bashRunLabel` (inline, no dash-strip → `bash run`'s spill label and `--out auto` filename clip the same ID differently). Empty sentinels: `egress.go:210 blank`, `deploystate.go:118 orDash`, `actions.go:97 blankDash`, `console.go:490 dash` (em-dash), `cli/auth.go:416`, `cli/brain.go:405`. Durations ×5 (`table.go:1117,1136,502`, `actions.go:112,126`). New `guards.go:150 joinNonEmpty`/`:160 kv`/`:188 boolMark` twin `debugdump/emit.go:617` (args reversed!), `:603`, `:416`/`table.go:509 yesNo`. Two doc-URL constants byte-identical: `chat.go:22 errorDocsBase`, `action_dx.go:20 embassyErrorsBase`. Two settings-bag decoders: `chat.go:581-601 bagBool/bagStrings/bagString` (untyped, unmarshal error discarded at `:326`) vs `action_dx.go:417-452` via typed `c.GetBag`.
Seam: `render/format.go` (`clipID`, `orDash`, `duration`, `clip`), `bashRunLabel→shortRunID`, chat doctor uses `c.GetBag`. Blast radius: ~10 files, ~110 LOC removed, some golden churn (keep intentional output differences as explicit args).

### F9. `internal/client/types.go` — god module, #1 hotspot, still growing (old F4)
Evidence 1,793 lines / 145 type decls (08-25: 1,540 / 124); +287/−34 over 14 commits since; 78 commits/12 mo (`hotspots.py` score 2023 vs next file 603). Families: console `:12-33,287-450`, brain `:80-275`, run/trace `:452-1145`, guards `:949-1038`, mailbox `:1147`, exports `:1200`, project `:1228`, feeds `:1276-1518`, health `:1520`, deploy `:1578`, settings/tenant `:1657-1800`. Siblings already own their types (`connection_probe.go`, `projection.go`, `spam.go`, `actions.go`, `console.go`, …) — one-file is residue, not policy. Seam: `<endpoint>_types.go` next to each method file; zero import churn. Blast radius: ~1,500 LOC moved, `go build` verifies.

### F10. `internal/render/table.go` — 1,216 lines, 15 views + the shared formatters everyone needs (old F8)
Evidence `Projects:23 … SpamRules:1020`; trapped helpers `truncate:557 duration:1117 runDuration:1136 yesNo:509 shortSHA:476 num:1163 joinOrDash:1107`. Seam: split by family + `format.go`; 81 goldens verify byte-for-byte. Package-internal.

### F11. Analysis in `internal/client/projection.go` (old F7, unchanged)
Evidence `:148 SortGroundingSources` ("ordered for human triage"), `:50 BranchSelectorValues` (hard-coded key heuristics), `:77 TenantSettingsDrift`, `:120/:133` counters, `:195 displaySettingValue` ("(unset)"). Callers 100 % presentation: `render/table.go:643,746,749,768,880,897`, `debugdump/emit.go:58-499` (11 sites), `cli/run.go:560`. Seam: `internal/digest` importable by render+debugdump; client keeps `ParsePromptSections`/`ParseManifestBlocks`/`ContextCaptured`/`ParseTenantSettingsSnapshot`. Blast radius: 3 packages, 18 call sites, `projection_test.go` moves.

### F12. `time.Now()` in two renderers blocks goldens (old F9, unchanged)
Evidence `render/fleet.go:487` (`fleetStuck`; `isStuck(r, now)` at `:229` already takes `now` — seam one frame late), `render/health.go:210` (`expiredTime`). Seam: `now` on `FleetOptions`/`HealthOptions`, defaulted in cli. Unlocks 2 goldens; lint L5 then enforces.

### F13. `kb_content.go` ships ~200 lines of Python as Go strings (old F3, untouched)
Evidence `kb_content.go:748 kbPython` heredoc, programs `:544 :579 :666 :708 :730`, `-o json` = `kbCommandSummary :82`. Seam unchanged: server `/api/v1/kb/*` (other repo) or `//go:embed *.py` half-step. 710 LOC.

### F14. Eleven `render*` functions + the only `tabwriter` outside `internal/render` (old F5, unchanged count)
Evidence `doctor.go:471` (+`tabwriter` `:13,:472`; also `interface{ Write([]byte) (int, error) }` instead of `io.Writer`), `tenant.go:416`, `hierarchy_settings.go:352,390,399`, `env.go:315`, `export.go:257`, `kb_content.go:375,392,418`, `upgrade.go:129`. New code did not add `render*` funcs — it writes bare `fmt.Fprintf(e.out, …)` inside `RunE` instead (F6), which is worse. Seam: `func(w io.Writer, typed…)` in `internal/render`. ~350 LOC, 8 files.

### F15. `render → outputspill` layer inversion (old F10, unchanged)
Evidence `render/console.go:12` import; `:164 BashRun(…, artifacts map[string]outputspill.Artifact)`, `:198 renderBashStream`. Only non-`client` internal import in render. Seam: render-owned `spillArtifact`, mapped in `internal/cli/outputspill.go`. 1 render file + 1 cli site.

### F16. Release-mirror URL hard-coded in four places, asset name pattern in five
Evidence `.goreleaser.yaml:18` (ldflag `defaultMirror`), `scripts/cloud-setup.sh:28`, `scripts/release.sh:35`, `.github/workflows/release.yml:49` (`s3://` dialect); asset name `upgrade.go:494`, `.goreleaser.yaml:30`, `cloud-setup.sh:126`, `install.sh:79`, `release.yml:60-70`. Intent `cloud-mirror-self-update.md` §1-2 (one mirror, one knob). Why it hurts: the fallback path used only when GitHub is down needs 4–5 coordinated edits. Seam: at minimum a test asserting the three defaults are equal (~10 LOC); better, one generated source. Blast radius: 4 files, 0 behaviour.

### F17. `release.sh` verifies the mirror's `cloud-setup.sh` but not `latest` or archives
Evidence `scripts/release.sh:193` loop covers `$VERSION/cloud-setup.sh` + `cloud-setup.sh` only; `release.yml:89-93` writes `latest` last. `cloud-setup.sh:99` and `upgrade.go:416` both read `<mirror>/latest`. Intent: spec "Verification" list (`curl <mirror>/latest → tag`). Why it hurts: green release, every cloud sandbox silently installs the previous rc. Seam: extend the loop (`latest == $VERSION`, HEAD `checksums.txt`); the `file://` sandbox in `release_script_test.go:44-49` already supports it. ~6 LOC.

### F18. Two forked PATH inventories in the self-update/doctor plane (old F11b, deepened)
Evidence `install.go:44 inspectRCInstallations`/`:128 classifyInstallPath`/`:141 isMiseShim` vs `doctor.go:272 scanPathBinaries`/`:234 classifyInstall`/`:344 isMiseDispatcher`/`:352 isMiseInstalledRC`; commit `e005f19` added `markCurrentActive :206` + `physicalBinaryKey :421` to doctor's copy instead of consolidating. `upgrade.go:96` sends users to `rc self doctor` when update refuses — the two can disagree. Seam: `rcInstallInventory` is the one producer; doctor maps it to its JSON shape. ~200 LOC, 2 files + 2 test files.

### Confirmed OK — do not "clean up"
- `(typed, json.RawMessage, error)` dual return on 37 methods **is** the intended seam; F2 asks to finish it.
- `client/console.go:87 DBQueryStream` reuses `openStream`; owns only the NDJSON frame state machine (documented contract). `Download`/`openStream` split from `do` is right for large bodies; F5 asks to share the loop, not to buffer.
- Exit-code mapping has one owner (`errors.go:51 exitCodeFor` ← `root.go:82,89`); SIGINT one owner (`root.go:69`); `console_io.go:126 abort()` is spec'd behaviour. Console query normalized JSON is the documented exception.
- `hierarchy_settings.go:186-190` fetching `/meta/schema` before every `behavior set` and hard-failing: correct (CLI holds no key list). `config.go:121-124` literal `null` for every kind: server owns nullability.
- `chat.go:349-355` not aborting on unreachable branding; `recentPrincipalRejects` deterministic tie-break (`chat_test.go:296`).
- `render/guards.go`, `health.go` boot check, `fleet.go` shadow learning: pure, golden-covered; client-side severity scoring is sanctioned fat-client work.
- `dnsdetect.Resolver` 6-method port (canned fake drives 7 tests); ~57 "unused" exported client types are nested wire structs; the 31 "tiny wrappers" from `excess.py` are the one-method-per-endpoint contract; `asAPIError` has 5 callers.
- `upgrade.go:331 httpGet` own `http.Client`: GitHub/S3 downloads, not `/api/v1` — correct.
- `machine_token_env.go:34-40` vs `:65-73` deliberate (stored-token escape hatch).

## Simplification proposals

### S1. Split `debugdump/emit.go` (938 lines) into its documented seams
Intent `dump.go:1-10` + SKILL.md decomposer section · Exists: JSONL `EmitJSONL:18-123` + shapers `:807-853`; index `RenderIndex:124-308` + sub-renderers `:309-682`; **anomaly policy** `flags:683-762` (flailing `:721-738`, 4× median `:740-756`, `EGRESS_BLOCKED :713`, >20 KB stdout `:716`), `benignGrepMiss:766`, `filesRead:783`, `median:927`; formatters `:869-926`. Simpler: `jsonl.go`/`index.go`/`analysis.go`, pure moves · 0 net LOC · Risk none (`emit_test.go` 7 tests + goldens).

### S2. `dump.go` is no longer "decorate only"
`dump.go:56 linkSummary`, `:100 attachmentSummary` build rendered Markdown (`:89,:92`) consumed by the index. ~100 LOC → `index.go` half of S1; decoders stay. Guarded by `emit_test.go` link/attachment tests.

### S3. Dead / redundant code, verified by grep (~44 LOC, `go build` catches every site)
- `oauth/oauth.go:81 now func() time.Time` never assigned (0 `now:` hits); `clock() :99-103` always falls to `time.Now()`. ~9 LOC.
- `oauth/oauth.go:92-97 httpClient()` nil fallback unreachable (`NewClient :86` always sets `HTTP`); would silently drop the 30 s timeout. ~6 LOC.
- `outputspill/outputspill.go:81 WithRaw` — definition only. 4 LOC.
- `cli/upgrade.go:439 fetchReleaseBinary` — unused since `3896833` added `…From` (`upgrade.go:118`, `upgrade_test.go:290`); golangci `unused` agrees. 5 LOC.
- `debugdump/dump.go:400 func max` — **correction to 08-25: not dead** (caller `dump.go:321`); defect is shadowing the Go 1.21 builtin on a go 1.25 module. 6 LOC.
- `config/profiles.go:70 BaseURLFromDefault` — written `:148,:153`, `cli/root.go:197`; read only by `profiles_test.go:58,74,88`; `BaseURLSource` carries the same info. ~10 LOC.
- `config/profiles.go:86 Brain.BaseURL` — comment `:81` "ignored by resolution"; zero readers; toml tolerates unknown keys. 4 LOC.
- `outputspill.go:247 ShellQuote` one-line alias of `shellQuote` (3 callers `cli/run.go:440-442`). 3 LOC.
- `action_dx.go:372` staticcheck S1016: `resultError`/`actionDiagnostic` field-identical → conversion.

### S4. `token → config` edge is one function wide (old S5, unchanged)
`token/store.go:65` is the sole caller of `config.ConfigDir()` (`profiles.go:271-282`, "exported for internal/token"). Move it; −1 package edge, 0 net LOC; `store_test.go:15` already isolates via `XDG_CONFIG_HOME`.

### S5. `tokenOvr` test seam skips the credential ladder (old S6, unchanged)
`cli/root.go:203-212` returns before `loadResolvedToken :214`, brain→default fallback `:220-232`, `/whoami` machine-token check `:238`. Scope enforcement **is** now run on this branch (`:205-211`), so the untested part is credential resolution only. Seam: `tokenSourceFactory`. ~40 LOC + stub routes.

### S6. `upgrade.go` (718 lines) repeats brew discovery
`migrateToHomebrew :181` re-runs `exec.LookPath("brew")`, `brew --prefix`, `canonicalLink` that `verifyHomebrewLatest :224` computed at `:184`. Return them. ~15 LOC.

### S7. `--bundle` flag on both doctors
Redundant alias for `-o json` (`chat.go:433`, `action_dx.go:214`); help claims "redacted" with no redaction step. Drop it, or redefine as "JSON even on a TTY" and say so. ~6 LOC + help regen.

## Test economy
512 top-level tests (689 with subtests), 13,393 test LOC, avg 2.8 asserts/test, 0 mock-only, 0 assertion-free, 0 orphan fixtures (81 `.golden` + 67 `.json`). `test_stats.py`'s 177-member cluster is the shared-harness artifact noted last time; re-clustered on bodies below.

| cut | reason | covered by (survivor) |
|---|---|---|
| `cli/ask_test.go:597 TestAskBadScenario` | strict subset (`Contains "invalid --scenario"`) | `cli/ask_test.go:149 TestAskRejectsUnknownScenario` (full message) |
| `client/actions_test.go:46 TestAllActionsPagesAndPreservesRawRows` | layered dup of 2-page walk + unknown-field survival; `capped==true` stays at `client/actions_test.go:88` | `cli/actions_test.go:12 TestFleetActionsPagesFiltersAndPreservesRawRows` (+ exact params `:82`) |
| `cli/console_primitives_test.go:317 TestJSONErrorEnvelopeAndExitClassification` | exit + envelope duplicated; move the one byte-exact shape line into the survivor | `console_primitives_test.go:328` + `:339 TestStableExitClassification` |
| `cli/console_primitives_test.go:301 TestBashRunJSONPreservesUnknownServerFields` (conditional) | only unique claim is unknown-field survival — add one `server_extra` key to `testdata/bash_run.json` and it's covered | `cli/golden_test.go:228 TestBashRunJSONPassthrough` + `client/types_test.go:9` |
| **merge** 68 golden-table tests → `TestTableGolden` | identical body, only args+golden vary (`golden_test.go:19…1067`, `collections_test.go:13…329`, `config_surface_test.go:23…1034`, `export_test.go:18,38`, `ask_test.go:32,42`, `deploystate_test.go:121`, `observability_test.go:406,563,608`, `tenant_test.go:17`) | same cases as `t.Run` subtests |
| **merge** 29 JSON-passthrough tests → `TestJSONPassthrough` | identical body (`collections_test.go:23…339`, `config_surface_test.go:33…676`, `golden_test.go:172…817`, `tenant_test.go:27,143,178,204`, `export_test.go:28,48`, `deploystate_test.go:132`) | subtests |
| **merge** 10 `Contains` table tests → `TestTableContains`; 7 `RejectsBeforeRequest` tests | `collections_test.go:136,275,317,375`, `config_surface_test.go:145,246,894,907,961,973` · `config_surface_test.go:417,939,950`, `golden_test.go:689,764`, `kb_content_test.go:93`, `tenant_test.go:37` | subtests |

Totals: 4 hard cuts (~82 LOC) + 114 → 4 table merges (~945 LOC); ~1,027 of 13,393 test LOC (7.7 %), 512 → 402 top-level tests, zero coverage change.
Kept on purpose: `chat_test.go:18` (only test of the `[ReplyPen] CODE: hint — docs` branch `root.go:423`), `install_test.go:103` vs `doctor_test.go:185` (consolidate prod F18 first), `upgrade_test.go:221` vs `:248` (different branches), `auth_test.go:166/194`, `scope_test.go:238/259`.

Render coverage via goldens: `go test ./internal/cli -coverpkg=./internal/render/...` → **81.5 %** statements. Still-0 % renderers = the `rc dev console` capabilities/db/action table plane: `render/console.go:15 Capabilities`, `:39 DBList`, `:52 DBSchema`, `:405 ActionList`, `:420 ActionShow`, `:498/:506` cells, `:517 ActionExec`, `table.go:356 renderNoteActions`, `thread.go:238 containsAny`. The new `console_primitives_test.go` covers primitives (CSV, `--out`, exit codes), never these tables. Cheapest win: 6 fixtures + 6 goldens as rows in `TestTableGolden`.

Keep-list (critical path → anchor, all verified today): nine roots + retired roots dead `cli/surface_test.go:17` · generated docs fresh `surface_test.go:110` · every command declares scope `scope_test.go:16` · fail-closed selectors `scope_test.go:73,93,114` · scope as query param `observability_test.go:22,75` · unknown fields survive `client/types_test.go:9` · PKCE/state/browser `oauth/oauth_test.go:21,75,120` · device grant `cli/auth_test.go:106` · refresh-on-401 `auth_test.go:334`, concurrent rotation `:366`, dead refresh `:400`, logout revokes `:144` · store 0600 `token/store_test.go:54`, profiles `:91` · machine-token fail-closed `machine_token_env_test.go:16-180` · config precedence `config/profiles_test.go:44-302` · spill + secrets raw `outputspill_test.go:11,40,61`, `export_test.go:233`, `golden_test.go:172` · secret file 0600 `export_test.go:129` · API errors verbatim `golden_test.go:911` · run poll/timeout `ask_test.go:662,692` · paging + cap `cli/actions_test.go:12`, `client/actions_test.go:88` · decomposer `debugdump/emit_test.go:12-260` · publish-before-tag `releasetest/release_script_test.go:14`.
New since 08-25 (keep): exit ladder `console_primitives_test.go:339`, truncation=3 `:328`, remote=4 `golden_test.go:218` · `--out` atomicity `console_primitives_test.go:129,153,242,266`, rescue `export_test.go:288,308,319` · SIGINT cleanup `:189` · `--all` single snapshot `:18`, server clamps `:105,115` · chat doctor bundle/staleness/dominant/SSE `chat_test.go:124,312,296,249,18` · action doctor `action_dx_test.go:69,106,11,36` · doctor active `doctor_test.go:93` · mirror fallback + checksum `upgrade_test.go:248,274` · safe-retry `client/retry_test.go:20,41` · boot check `render/fleet_test.go:144`.

## Test suite speed
Total **11.6 s wall** cold (`/usr/bin/time mise exec -- go test ./... -count=1`; per-package in-suite: cli 12.6 · releasetest 9.4 · oauth 2.4 · client 2.3 · token 2.0 · others ≤1.9). Isolated re-runs show most per-package numbers are build/link + 12-way CPU contention: `TestLocalUnpromoted*` (`deploystate_test.go:47,71`) = 0.68 s isolated, identical to 08-25 — **no regression**. Real sinks:
- `TestLoginDeviceStoresToken` `cli/auth_test.go:106` — **2.0 s**. `oauth/device.go:45` `interval*time.Second`, waits before first poll `:52`, stub needs 2 polls. Still unfixed (no `PollWait`). Fix: `Client.PollWait func(time.Duration) <-chan time.Time` beside the `clock` field, overridden from `cli/auth.go` (precedent `env.openBrowser`, `root.go:43`). −2.0 s.
- `TestMailboxHarvestWait` `cli/export_test.go:116` — **1.0 s**. `cli/mailbox.go:197 const interval = time.Second`; comment `:192` "no fixed sleep in tests" is false. Fix: package `var exportPollInterval` or honour a server poll hint. −1.0 s.
- `TestReleasePublishesMainBeforeTag` `releasetest/release_script_test.go:14` — 7.9 s in-suite / 5.8 s isolated. **Not recursion**: the `go` shim (`:70`) no-ops build/vet/test. Real work: `shellcheck scripts/cloud-setup.sh` runs for real (`release.sh:137`, 0.3–1.8 s, duplicating `ci.yml:20`), `golangci-lint run` runs for real via the real PATH (`:106`; advisory, lints a checkout with zero Go files → proves nothing, 0.5–1.0 s; absent on CI so laptop and CI take different branches), ~15 git subprocesses, `gh auth status` 0.7 s cold. `25bc90b` added ≈0.7 s (gates became genuine; before, the missing `cloud-setup.sh` failed instantly and was masked by `cmd && ok`). Fix: shim `shellcheck`+`golangci-lint` in `fakeBin` (assert invoked, don't re-run) → −1–2.4 s, removes the undeclared host dependency (`release.sh:107` dies without shellcheck ⇒ `go test ./...` fails on machines without it); and/or `testing.Short()` gate + `go test -short ./...` in the agent loop (release.sh itself runs the full suite at `:133`).
Proposed: 2 seam fixes + shims ≈ −5 s; with `-short`, agent-loop wall ≈ 4.5 s (bounded by cli).

## Guardrails — adoption status: 0 of 8
No `.golangci.yml` exists. `ci.yml` runs build/vet/test/shellcheck/gofmt, no lint. `release.sh:139-143` runs `golangci-lint` **advisory**, justified by a comment ("known, intentional errcheck findings in the render layer") that is **stale** — today's default-config run reports 5 issues, none in render: `client/chat.go:72,125,184` errcheck, `action_dx.go:372` S1016, `upgrade.go:439` unused. Commit `6246ebd "keep CLI lint clean"` shows an agent ran it by hand; the habit is one agent's memory, not a repo property. Existing test-shaped guards remain strong: `surface_test.go:17,110`, `scope_test.go` (17 tests), `releasetest`, `ci.yml:20-21`.

## Proposed custom lint rules (each run via `lint_rules.py` against `85b5d73`; every hit a true violation)
| id | rule | vehicle | hits today (08-25) | all true? |
|---|---|---|---|---|
| L1 | `internal/cli` must not use `text/tabwriter` | depguard | 2 `doctor.go:13,472` (2) | yes (F14) |
| L2 | no `^func render[A-Z]` outside `internal/render` | grep script | 11 (11) — list in F14 | yes |
| L3 | no interpreter heredoc in Go source | grep script | 1 `kb_content.go:748` (1) | yes (F13) |
| L4 | `internal/render` (except `render.go`) imports no `os`/`net`/`net/http`/`os/exec` | depguard | 0 (0) | ratchet |
| L5 | no `time.Now()` in `internal/render` | forbidigo | 2 `fleet.go:487`, `health.go:210` (2) | yes (F12) — land with 2 `//nolint` or after fix |
| L6 | `internal/client` imports no sibling `internal/*` | depguard | 0 (0) | ratchet |
| L7 | no `os.Getenv` in `internal/render` | forbidigo | 0 | ratchet. **Note:** the 08-25 form also covered `internal/client`; that now hits `client/client.go:54 os.Getenv("RC_HTTP_TIMEOUT")` (new since `a70c712`). Judged a soft violation (config resolution in the leaf; untestable without env mutation) — recorded as a finding, rule narrowed to render for zero-FP. Proper fix: `client.New(..., WithTimeout(d))` resolved in cli/config. |
| L8 | no package-local `func max/min/clear/any(` | forbidigo | 1 `debugdump/dump.go:400` (1) | yes (S3) |
| L9 | **new** no `Raw`/`RawScoped` with `http.MethodPost|Patch|Put|Delete` literal in `internal/cli` | grep script | 2 `console.go:324,506` | yes (F3). Narrow on purpose: `bag.go:77` passes `"PATCH"` as a string and the same `patch` var down both branches — legit until F2 lands. |
| — | dropped: `no http.NewRequest in internal/cli` | | 1 `upgrade.go:331` | false positive (GitHub/S3 download) — excluding `upgrade.go` leaves 0; not worth a rule |

```yaml
# /tmp/rules.yaml — validated with lint_rules.py (21 hits, all listed above)
- {id: cli-no-tabwriter,   paths: internal/cli/**, exclude: internal/cli/*_test.go, forbid: '"text/tabwriter"|tabwriter\.NewWriter'}
- {id: cli-no-render-funcs, paths: internal/cli/**, exclude: internal/cli/*_test.go, forbid: '^func render[A-Z][A-Za-z]*\('}
- {id: cli-no-embedded-interpreter, paths: internal/**, exclude: internal/**/*_test.go, forbid: '(python3|python|bash|sh) - ?<<'}
- {id: render-no-os-net,   paths: internal/render/**, exclude: "internal/render/render.go, internal/render/*_test.go", forbid: '^\s+"(os|net|net/http|os/exec)"$'}
- {id: render-no-time-now, paths: internal/render/**, exclude: internal/render/*_test.go, forbid: 'time\.Now\(\)'}
- {id: client-leaf,        paths: internal/client/**, forbid: 'rootcause-cli/internal/(render|cli|outputspill|debugdump|config|token|oauth)'}
- {id: no-getenv-in-render, paths: internal/render/**, forbid: 'os\.Getenv'}
- {id: no-builtin-shadow,  paths: "internal/**, cmd/**", forbid: '^func (max|min|clear|any)\('}
- {id: cli-no-raw-write,   paths: internal/cli/**, exclude: internal/cli/*_test.go, forbid: '\.(Raw|RawScoped)\(e\.ctx\(\), http\.Method(Post|Patch|Put|Delete)'}
```

Native port (proposed, NOT wired):
```yaml
# .golangci.yml
version: "2"
linters:
  default: standard          # errcheck, staticcheck, unused, govet, ineffassign — 5 hits today, fix S3 first
  enable: [depguard, forbidigo]
  settings:
    depguard:
      rules:
        cli-no-tabwriter:
          files: ["**/internal/cli/**", "!**/*_test.go"]
          deny: [{pkg: text/tabwriter, desc: table formatting belongs in internal/render (F14)}]
        render-pure:
          files: ["**/internal/render/**", "!**/internal/render/render.go", "!**/*_test.go"]
          deny:
            - {pkg: os, desc: renderers are pure functions of rows}
            - {pkg: net/http, desc: renderers are pure functions of rows}
            - {pkg: os/exec, desc: renderers are pure functions of rows}
            - {pkg: github.com/rootcause-org/rootcause-cli/internal/outputspill, desc: map the artifact into a render-owned type (F15)}
        client-leaf:
          files: ["**/internal/client/**"]
          deny: [{pkg: github.com/rootcause-org/rootcause-cli/internal, desc: internal/client imports no sibling}]
    forbidigo:
      analyze-types: true
      forbid:
        - {p: '^time\.Now$', pkg: '^github.com/rootcause-org/rootcause-cli/internal/render$', msg: thread `now` in from internal/cli (F12)}
        - {p: '^os\.Getenv$', pkg: '^github.com/rootcause-org/rootcause-cli/internal/render$'}
```
L2/L3/L9 have no clean forbidigo form → `scripts/lint-contracts.sh` (grep) run from `ci.yml`. Wire `golangci-lint run` into `ci.yml` and make `release.sh:141` blocking once S3 lands (today's 5 hits are all trivially fixable). `render-pure`'s `outputspill` deny fires once today (F15) — land with `//nolint` or after the fix.

## Proposed dependency contract (NOT wired)
`cli → {render, debugdump, client, config, token, oauth, outputspill, dnsdetect, idutil, contextexport}` · `render → client` only (after F15) · `debugdump → client` (+ `digest` after F11) · `client`, `token` leaves (after S4). `deps.py --layers` reports 0 violations today, so this is a ratchet; it would have caught F15, S4, and pins F11's direction.

## Resolved since 2026-08-25
- **F1 (DB write plane dual encoding)** — `console.go:148` now one `client.DBQueryRequest`; `--all` streams from one server snapshot (`a80b38c`). Bash run is single-path (`:320-324`). Only action-exec remains (now F3).
- Console `-o json` error envelope is stable and has one owner (`errors.go`, `168885d`); exit-code ladder 1–5 tested (`console_primitives_test.go:339`).
- `TestLocalUnpromoted*` "slowdown" — measurement artifact, not a regression (0.68 s isolated, same as 08-25).
- Nothing else from the 08-25 list landed: F2–F11, S1–S6, both timing seams, all 8 lint rules, the depguard contract, and the 6 zero-coverage console renderers are all still open (renumbered above).

## Deliberately not done
- No code/test changes (audit only). Every item above is ≤ ~250 LOC and golden- or build-guarded.
- F13 server-side KB endpoint needs the rootcause server repo.
- `.golangci.yml` not added: it would fail today on S3's 5 default-linter hits + L1/L2/L5/L8 — land S3 first (one small commit), then wire with `//nolint` ratchets on F12/F15.
- Open decision for PJ (blocks F6/F13 direction): is a CLI-invented `-o json` envelope for the *doctor* family (`chat doctor`, `action doctor`, `knowledge content search`) a sanctioned carve-out to document in SKILL.md next to `--format`, or must each carry the raw rows? Three commands now depend on the answer.
