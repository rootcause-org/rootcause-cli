# Audit round 2 — composable rc primitives (Fable, 2026-08-27)

Verified r2 (rc 1.19.3, server 4eca3e0a, runtime v0.3.30): 15/17 findings FIXED incl. file-get integrity
(Content-Length verify, MITM-tested → exit 5, no partial file), rc_client allow_truncated/bash/query_to_csv
streaming (48 MB RSS on 52k rows), lint/vet/tests green, --all 52k rows in 4.5 s. Good work.

## Still open — must fix (r3)
1. **`--all` is still lossy.** Offset paging, one tx per page, no snapshot, ORDER BY totality only *documented*.
   Live: `… order by date --all` → 52,344 rows emitted, 8–23 duplicated + same number missing, exit 0
   (3 runs). `where random()<0.5 order by id` shows page contents shifting. Guard bypass: `hasTopLevelOrderBy`
   tokenizes ASCII letters only → `select id::text as order_by from organizations --all` accepted with no ORDER BY.
   **Change the design, don't patch the guard**: make `--all` a *single* HTTP request whose response streams
   rows (ndjson/csv, chunked transfer) from ONE transaction using a server-side `DECLARE … CURSOR` /
   `FETCH 5000` loop under REPEATABLE READ. No offsets, no ORDER BY requirement, no cross-page drift; memory
   stays one batch. Keep `statement_timeout` 5 min for that tx; keep `next_cursor` API only if something
   else needs it (else delete). Client: stream body → `--out` with the existing atomic-rename + byte-count
   check (Content-Length unknown → use a trailer/final `{"meta":…,"row_count":N}` frame and verify row count).
   Fix `hasTopLevelOrderBy` word boundaries anyway if it survives; add a test with a non-unique ORDER BY
   against a table with >1 page that asserts zero dup/missing.
2. **kampadmin `scripts/yuki/status_2025_2026.py:55-60` dropped `--all`** → every >500 query now raises.
   `admin_activity_2025_2026.py` untouched: `#!/usr/bin/env python3`/`mise exec -- python3` ⇒ `ImportError lib.rc_client`,
   stale comment `:49`, GROUP BY query >500 with no ORDER BY. Port both to PEP-723 `uv run --script` + `all=True`.
   `ka_extract.py` lost its `--limit 500` + warning; `Q["settings"]` can exceed 500 → needs `all=True`.
3. pro-backup `revenue_per_platform.py:113-118` never passes `all=True` but `SKILL.md:45` claims `--all`;
   `churn_weekly/SKILL.md:106`, `_rcdb.py:2-3`, `scripts/unsubscribe/README.md:277` stale (`--out -` vs temp file).
   Reconcile code and docs; decide per query whether all=True.

## Should fix
4. `client.Download` refresh-failure returns raw err → exit 1; `do()`/`attemptRawWithRefresh` → exit 2. Align.
5. kampkompas + pro-backup `.claude/skills/rootcause/db.md` still lack `--param @name` / exit-code / `--all` section
   (kampadmin's has it — copy the shape).
6. Pins: consumers + `rc-script-wrapper/SKILL.md` say v0.3.29, released v0.3.30 → bump to the tag that ships r3.
7. `rc_client._DEFAULT_BASH_TIMEOUT_S=120` hardcodes server default; read it from `rc dev console capabilities`
   or leave as is but document. brain-kampadmin `terms_sync`: NULL `terms_url` now `None` not `""` — verify
   `kampadmin_terms_rows()` downstream. PEP8 blank lines `rc_client.py:84-86`. Residual `rc db query` in 4
   kampadmin historical docs — leave (provenance).

Report back: `t3-ping-thread --thread 2ac7596e-e879-41dd-a1fa-584a85ae1ed2 --from "Sol rc-primitives r3" -- "<shas, versions, how --all is now snapshot-safe, unverified>"`
