# Audit round 1 — composable rc primitives (Fable, 2026-08-27)

Live E2E: all 6 CLI deliverables PASS on v1.19.1 (csv/ndjson/tsv fidelity, --all 52,344 rows no dup/miss,
exit codes, params proven bound, file get 222 KB sha-equal + traversal/symlink refused, RC_PROJECT,
kampkompas + pro-backup scripts). Publish state verified. Below: what to fix, ranked. Ship as one
follow-up round (server + CLI patch release + runtime bump + doc touch-ups), then ping back.

## Must fix
1. **`--all` correctness**: offset paging, each page a separate tx, no snapshot, no enforced total order
   (`rootcause/internal/api/console.go:1418` pageQuerySQL, `:479`). Concurrent writes or a non-unique/absent
   ORDER BY ⇒ silent dup/missing rows, exit 0. Fix (pick): hold one REPEATABLE READ snapshot/cursor server-side
   across pages, or keyset with injected tiebreaker; at minimum reject `--all` without ORDER BY and document
   "ORDER BY must be total". Also: `--all` default page = 500 ⇒ 105 round trips, each re-running the full
   query under 30 s statement_timeout (`:1314-1322`, `:1466`) — default to consoleMaxPageRows and plumb a
   longer timeout for --all. `--all --limit 100000` silently clamped to 5000 → signal it.
2. **`file get` can return a truncated file with exit 0**: 200 + headers written before `cat` runs
   (`api/console.go:781-791`, `workspace/docker.go:583-596`); on mid-stream failure client `io.Copy`s partial,
   `console_io.go:finish` renames into place with success manifest. Fix: send Content-Length (from stat) and
   have the client verify bytes received == length (or trailer checksum); abort+exit 5 on mismatch; enforce
   max_bytes during copy, not just at stat.
3. **`rc_client.query(allow_truncated=True)` broken** — never appends `--allow-truncated`
   (`rootcause-brain-skills/runtime/lib/rc_client.py:186-188`); test `runtime/tests/test_rc_client.py:45-49`
   false-green (mock returncode=0). Fix + test with returncode=3 + runtime bump + workspace pin.
4. **`rc_client.bash()` can never return non-zero**: CLI exit 4 ⇒ `_run` raises `RemoteCommandError` and drops
   stdout/stderr; `_decode_error` gets a success-shaped payload and dumps the JSON blob as message
   (`rc_client.py:161,213-227`). Fix: parse payload on exit 4, return BashResult(exit_code, stdout, stderr,
   timed_out) — or attach to the exception with `check=` semantics; local subprocess timeout = remote + 30 s.
5. **`rc_client.query_to_csv` buffers whole result in RAM** (`rc_client.py:206-211`); use `--format csv --out <path>`
   streaming. Also boolean/NULL rendering differs Go (`true`/"") vs Python (`True`/"") — make Python delegate.
6. **Auth-refresh failure on `file get` misclassified** as exit 5 (reads closed body,
   `rootcause-cli/internal/client/client.go:483-513`) → should be exit 2 with server message.
7. **Dead `--all` guard + new lint regression**: `cli/console.go:181,193,209` `truncated` never used (SA4006);
   server sets `next_cursor` only when truncated (`:509-512`). Make the early-stop guard real, lint clean.

## Should fix
8. Error envelope only under explicit `-o json` (`cli/root.go:74`) while success uses auto-detect → piped
   callers get JSON on success, text on failure. Use `e.jsonOut()`.
9. `--allow-truncated` manifest omits `truncated:true` — include it.
10. `--all` rejects leading-paren SQL like `(select…) union all (select…) order by 1` (`pageableConsoleSQL`, `:1421`).
11. Encoder nits: `interval` leaks pgx struct `{Days,Microseconds,…}` → text; `date` gets `T00:00:00Z` → plain
    `YYYY-MM-DD`; document bytea=base64.
12. Undocumented breaks: old fixed spill path `.rootcause/output/console-db-query/response.json` gone (add
    `latest` symlink or a release note); >500 rows now exit 3 (release note).
13. `--param` help never says `@name` syntax (`cli-help`, kampkompas/pro-backup/kampadmin `db.md`). Note the
    brain-side `lib.db.query` uses `%s` — document the asymmetry (or unify later).
14. Symlink TOCTOU in file get (readlink then stat then cat as separate execs, `docker.go:604`) — do
    readlink+stat+cat in one exec; dedupe `consoleFileRoots` vs `docker.go:48 homeDir`.

## Adoption / docs
15. `rc_client` has zero consumers. Migrate at least: kampkompas `org-value-stats/scripts/orgvalue.py`,
    pro-backup `_rcdb.py`, `churn_weekly/collect.py`, `revenue_per_platform.py`, brain-kampadmin `terms_sync.py`,
    kampadmin `yuki/status_2025_2026.py`. Skill `rc-script-wrapper/SKILL.md` must show how to depend on it
    (pinned `uv run --with "rootcause-runtime @ git+…@vX#subdirectory=runtime"` + PEP-723 inline example) and
    link `docs/migration-rootcause.md`.
16. Stale/broken docs: pro-backup `.claude/commands/s3_purge_old_backup_ids.md:57-62` (still prescribes
    string_agg — destructive flow); kampkompas `.claude/skills/rootcause/db.md:23-25` uuid-int-array caveat
    (now false); `rc db query` invocations: kampadmin `docs/facturatie-klanten/prijsmodel-analyse-2024-2025/scripts/ka_extract.py:18`,
    `.claude/commands/ka_translate-form-fields.md:16`, kampkompas `.claude/skills/avo-internal-dashboard/SKILL.md:312`;
    kampadmin `.claude/skills/rootcause/db.md` lacks the 500/--all/--format/exit-code section;
    `fetch_org_daily_stats.py:51-56` f-string dates → `--param`; `test_rc_client.py:24` uses `%s`.
17. kampkompas `fetch_org_daily_stats.py` cache: upsert-only, so rows deleted upstream (95 org-days removed by
    the 2026-08-27 recompute) linger (52,439 local vs 52,344 prod). Delete-then-insert per fetched date range.

## KampAdmin promotion
Correctly withheld (deploy guard, Tobias commits in delta); 85a17a1 is operator scripts only — leave for PJ's
explicit `/ka_deploy_production`.

Report back: `t3-ping-thread --thread 2ac7596e-e879-41dd-a1fa-584a85ae1ed2 --from "Sol rc-primitives r2" -- "<shas, versions, unverified>"`
