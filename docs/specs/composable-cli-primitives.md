# Composable `rc` primitives for wrapping scripts

Author: Fable (KampKompas thread), 2026-08-27. Owner: PJ. Implementer: Sol (T3 thread).

## Why
Deterministic Python scripts wrap `rc` in ≥4 repos (kampkompas `org-value-stats`, pro-backup
`scripts/unsubscribe/_rcdb.py`, `churn_weekly/collect.py`, kampadmin terms_sync, brain-skills
prepass). Each re-implements: the 500-row cap workaround (`string_agg` packing / LIMIT-OFFSET
paging / hard-exit), spill-manifest vs `--raw-output` handling, argv quoting hacks
(`ChR(44)`, base64), `auth status` preflight, error parsing. Silent truncation was nearly
destructive once (pro-backup orphan-S3 cleanup). Audit facts below are from rc 1.18.1
(`rootcause-cli@83d0ad0`) + server `rootcause`.

## Current facts (verify before changing)
- Output: stdout or spill under `.rootcause/output/<label>/response.json`; db-query label is
  hardcoded `console-db-query` (`internal/cli/console.go:141`) → concurrent queries clobber.
  Only `--out-dir`/`RC_OUTPUT_DIR` (`internal/cli/root.go:106`); no `--out <file>` on console cmds.
- Formats: `-o json|table` only (`internal/render/render.go`). No csv/tsv/ndjson.
- Encoder (`rootcause/internal/api/console.go:1208-1223`): uuid → 16-int array, numeric → float64,
  duplicate column names collapse (rows are maps).
- Row cap server-side `consoleMaxRows = 500` (`api/console.go:43`); `--limit >500` silently
  clamped (`:422-425`); `truncated:true` (`:1176`) → exit 0. No cursor/stream/`--all`.
- Exit codes 0/1 only (`internal/cli/root.go:64-72`). `bash run` ignores remote `exit_code`/`timed_out`
  (`console.go:189-222`). Errors → stderr plain text even under `-o json` (`root.go:385-415`).
- Input: `query <db> <sql>` and `bash run <cmd>` are ExactArgs — no stdin/`@file`; wire request
  `{sql,limit,write,dry_run}` (`internal/client/types.go:294`) — no params. Brain runtime
  `rootcause-brain-skills/runtime/lib/db.py` `query(sql, params)` DOES support `%s` params, no cap.
- No remote→local file channel. `workspace.ReadFile` exists (`internal/workspace/workspace.go:147`)
  but unrouted (`internal/app/routes_api.go:390-399`). base64-over-stdout capped at 64 KB
  (`BASH_OUTPUT_CAP`), already used ad hoc in `internal/cli/kb_content.go:360-373`.
- HTTP client: no timeout (`client.go:44`), no 429/5xx retry, single 401 retry.
- Brain-side spill: `rootcause-brain-skills/runtime/lib/_output.py` `emit_rows` writes
  `$TMPDIR/rootcause-out/<label>-XXXX.{csv,json}` above 5 KB — unreachable from local today.

## Deliverables (all six)
1. `--out <path>` on `dev console database query` and `dev console bash run`. `--out -` = stdout,
   `--out auto` = `.rootcause/output/<cmd>-<runid8>.<ext>`. Data → file, manifest (JSON) → stdout.
   Unique per-invocation path always (also when unset: include runid8 in the label; keep a
   `latest` symlink if you want the old path to keep working).
2. `--format json|ndjson|csv|tsv` for query results. Fix encoder: uuid/numeric/timestamptz/bytea
   as text (or lossless), preserve column order + duplicate columns (emit rows as arrays + `columns`).
3. `--all`: one chunked HTTP response backed by a server-side cursor inside one read-only
   `REPEATABLE READ` transaction. The server fetches at most 5000 rows per batch and ends the NDJSON
   stream with an exact row-count frame; the client accepts file output atomically only after that
   frame verifies. No `ORDER BY` is required for completeness. Inline default stays 500.
   `truncated:true` without `--allow-truncated` → exit 3. `--limit >500` without `--all` → error.
4. Typed exit codes: 0 ok · 1 usage · 2 auth · 3 truncated · 4 remote non-zero/timeout · 5 server/network.
   Under `-o json` errors are a JSON envelope on stdout `{error:{code,message,status,fields}}`.
   `bash run` propagates remote exit code (4) and `timed_out`.
5. Input: stdin (`-`) and `@file` for SQL and bash command; `--param k=v` (repeatable, typed as text)
   wired to server-side parameterized execution (same placeholder mechanism as brain `lib.db.query`).
6. `rc dev console file get <remote-path> --out <local>` (and `file put` if trivial): route
   `workspace.ReadFile`, chunked/streamed, no 64 KB cap, restricted to session workspace + `/tmp`.
   Make brain-side `emit_rows` spill files fetchable; print the remote path in its preview.

Hygiene: HTTP timeout (configurable, default ~10 min for `--all`), 429/5xx backoff (3×),
`RC_PROJECT`/`RC_TENANT` env fallbacks so scripts can drop `--project`.

## Client library + adoption
- Add `rc_client` to `rootcause-brain-skills/runtime` (or wherever `rootcause-runtime` lives):
  `query(sql, params=None, all=False) -> Result(columns, rows, truncated)`, `query_to_csv(path)`,
  `bash(cmd, timeout) -> (exit_code, stdout, stderr)`, `file_get(remote, local)`. One canonical
  truncation/error policy: raise on truncated unless allowed; typed exceptions per exit code.
- Migrate callers to the library / new flags, delete workarounds:
  - `kampkompas/.claude/skills/org-value-stats/scripts/orgvalue.py` (rc_query_chunked → `--all --format csv`),
    `fetch_org_daily_stats.py`; update `.claude/skills/rootcause/{SKILL,db}.md`.
  - `pro-backup/pro-backup-backend`: `scripts/unsubscribe/_rcdb.py`, `.claude/skills/scheduled/churn_weekly/scripts/collect.py`,
    `.claude/skills/global/revenue_per_platform/scripts/revenue_per_platform.py`,
    `.claude/skills/scheduled/job-creator/SKILL.md` template, `.claude/skills/rootcause/db.md:75-87` prose loop.
  - `rootcause-brain-kampadmin/.agents/skills/kampadmin-terms-sync/scripts/terms_sync.py`, `_internal/audit_prepass/prepass.py`.
  - Stale `rc db query prod` callers in `kampadmin/scripts/yuki/*.py`, `scripts/dev/monologue_dict_sync.py` → fix or delete.
- `rootcause-brain-skills`: add/refresh a best-practices skill "wrapping rc from scripts"
  (exit codes, `--all`, `--out`, params, file get, library usage, when to spill vs stream).
  Update `rootcause/AGENTS.md`, `rootcause-cli/AGENTS.md`, `docs/cli-help.txt`.

## Ship
Server (`rootcause`) + CLI (`rootcause-cli`, incl. homebrew tap if that's the release path) →
promote + publish per each repo's documented release flow (read their AGENTS.md; do not invent).
Then `rc dev brain sync`/publish for the brain-skills changes. Verify end-to-end from kampkompas:
`./fetch_org_daily_stats.py` returns 52k+ rows via `--all` with no chunking code, and pro-backup
`_rcdb.py` reads pass. Commit per repo, one commit per high-level task, on `main`.

## Non-goals
Multi-statement transactions, cross-DB joins, server job queue.

## Report back
When done (or blocked): `t3-ping-thread --thread 2ac7596e-e879-41dd-a1fa-584a85ae1ed2 --from "Sol rc-primitives" -- "<terse status: shas per repo, published versions, what's unverified>"`.
Fable audits and pings back with fixes if needed.
