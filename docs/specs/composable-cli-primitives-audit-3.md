# Audit round 3 — composable rc primitives (Fable, 2026-08-27) — SHIP ✅ + small r4

r3 verified (rc 1.19.4, server 9d3b7971, runtime v0.3.31): `--all` = one NDJSON response from one REPEATABLE READ
READ ONLY tx via DECLARE/FETCH; offset API deleted; client verifies final row_count, atomic install after.
Live: 52,344/52,344 no ORDER BY in 1.4 s; `order by date` lossless; `random()<0.5 order by id` ×3 distinct==total;
SIGINT on a 2.3 M-row export left destination untouched. All audit-2 residuals fixed. Builds/lint/tests green,
releases + Homebrew + workspace pin + pro-backup staging green.

## r4 (small, then done)
1. **kampkompas `fetch_org_daily_stats.py:45`** org catalogue query lacks `all=True` (338 orgs today, cap 500;
   we bulk-onboard municipalities). Add it.
2. **kampadmin `scripts/yuki/financial_estimate_2025_2026.py`** not migrated: imports `status_2025_2026` →
   `lib.rc_client` under plain python3 ⇒ ModuleNotFoundError. Port to PEP-723 like its siblings.
3. **rc SIGINT/SIGTERM handler**: killing `--all --out <path>` orphans hidden `.rc-output-*` temp in the destination
   dir (GBs on big exports). `console_io.go openOutputTarget` → signal.NotifyContext + `target.abort()`.
4. **Server streaming write deadline**: no `WriteTimeout`; a client that stops reading pins goroutine + pgx conn +
   scoped role until dbproxy 900 s idle kill. `http.NewResponseController(w).SetWriteDeadline` per batch; also use a
   detached ctx for `h.audit`/`finishConsoleRun` so abandoned runs don't stay `running`.
5. **rootcause-brain-skills runtime deps**: `pypdf==6.14.2` / `requests==2.32.3` exact pins now break every
   pro-backup Dependabot cycle. Loosen to ranges (`>=x,<next-major`).
6. Low/optional: tests for mid-stream `{"type":"error"}` frame + row_count mismatch; `--all` failures incl. 5-min
   timeout map to a server/timeout code not 400; tautological server-side row-count guard in `dbQueryAll`;
   stale comments `ka_extract.py:165`, `admin_activity_2025_2026.py:53`; db.md "never consume a partial destination"
   is untrue for `--out -`.

Report back: `t3-ping-thread --thread 2ac7596e-e879-41dd-a1fa-584a85ae1ed2 --from "Sol rc-primitives r4" -- "<shas/versions>"`
