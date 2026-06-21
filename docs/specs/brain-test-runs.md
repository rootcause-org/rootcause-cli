# Spec — Brain test runs (CLI side: `rc ask` + run dump)

**Repo:** `rootcause-cli` (the `rc` Go CLI). `rc` stays a thin, typed, TTY-aware front-end to the
public `/api/v1`; **no business logic, no rendering of the markdown index** — it triggers runs and
emits clean JSON for the brain-side renderer to consume.

- Server contract (the endpoints below): [`rootcause/docs/specs/brain-test-runs.md`](../../../rootcause/docs/specs/brain-test-runs.md)
- Renderer + playbook that consumes `rc` output: [`rootcause-brain-skills/docs/specs/brain-test-runs.md`](../../../rootcause-brain-skills/docs/specs/brain-test-runs.md)

## What exists today

Commands registered in [`internal/cli/root.go:47`](../internal/cli/root.go): `status`, `runs`, `run`,
`config`. `rc run <id> --events` already calls `GET /api/v1/runs/{id}/events` and emits **NDJSON** in
`-o json` ([`internal/cli/run.go:13`](../internal/cli/run.go), `Client.Events`
[`internal/client/client.go:89`](../internal/client/client.go)). Auth: `ROOTCAUSE_API_KEY` +
`ROOTCAUSE_BASE_URL` (or `~/.config/rootcause/config.toml`). **Read-only — there is no trigger verb.**

## Change 1 — new verb `rc ask`

The missing trigger. Env-authed by the project key (exactly the "based on env vars we have"
requirement). New command file `internal/cli/ask.go`, registered in `root.go`.

```
rc ask "<question>" [--brain-ref <ref>] [--tenant <slug>] [--wait/--no-wait] [--timeout 5m] [-o json|table]

  rc ask "Hi, my account is sophie.coca-cola.com. Do I still have open invoices?"
  rc ask "<q>" --brain-ref dev/refund-rework        # run against a non-main brain ref (test run)
```

Behavior:
1. `POST /api/v1/runs` with `{prompt, session_id?, tenant?, brain_ref?}` → 202 `{run_id, status_url,
   poll_after_ms}`. New `Client.Submit(ctx, SubmitRequest) (*SubmitResponse, error)` in `client.go`;
   add `SubmitRequest`/`SubmitResponse` to [`internal/client/types.go`](../internal/client/types.go).
2. **`--wait` is the DEFAULT** (decided): poll `GET /api/v1/runs/{id}` every `poll_after_ms` until the
   run reaches a terminal status (`done`/`error`/timeout), honoring `--timeout` (default 5m). Print a
   terse live status line on a TTY. `--no-wait` prints `run_id` and returns immediately.
3. On completion, print the run summary (same renderer as `rc run <id>`). In `-o json`, print the
   summary object; `run_id` is always on stdout so it can be captured: `RID=$(rc ask "…" --no-wait -o json | jq -r .run_id)`.
4. `--brain-ref` is forwarded verbatim as `brain_ref`. A `4xx BAD_BRAIN_REF` or "ref not found" is
   surfaced via the existing typed-error path ([`internal/client/errors.go`](../internal/client/errors.go)) —
   tell the user to `git push origin <ref>` to the brain first.

> `rc ask --brain-ref` is the project-dev's full-fidelity "test without pushing main" loop: push a
> `dev/*` branch (main untouched → not live), then ask against it. The server runs the real loop and
> flags any actions/PRs as test. See the server spec.

## Change 2 — `rc run <id> --full` (the bundle, decomposed)

The server returns one bundle (`GET /api/v1/runs/{id}/full`); **`rc` decomposes it for progressive
disclosure** (decided). Add `Client.Full(ctx, id) (*FullResponse, error)` hitting `/full`, with
`FullResponse{Run RunHeader; Events []EventItem}` in `types.go` (superset of today's `EventsResponse`:
`EventItem` gains `cost_usd`, `total_tokens`, `model`, `args`; new `RunHeader` carries full
draft/notes bodies, `system_prompt`, warm inputs, egress, `trace_url`).

Extend `internal/cli/run.go`:

| Invocation | Hits | Output |
|---|---|---|
| `rc run <id>` | `/runs/{id}` | high-level summary (today) |
| `rc run <id> --events` | `/runs/{id}/events` | per-event trace (today; NDJSON in `-o json`) |
| **`rc run <id> --full`** | `/runs/{id}/full` | **the whole bundle.** `-o json`: emit as the brain-renderer's input — header line first, then one NDJSON line per event (the JSONL shape). `-o table`: a compact decomposed view (header block + timeline). |

The `--full -o json` output is **the exact stdin contract** the brain-dev renderer reads to produce
`<run8>-<proj>.{md,jsonl}`. Keep it stable; it is the cross-repo seam. Reuse `emitNDJSON`
([`run.go:65`](../internal/cli/run.go)) for the event lines, prefixed by the run-header line
(`{"type":"run",…}` then `{"type":"event",…}` per line — mirrors the existing JSONL format so the
renderer is source-agnostic).

## Output / JSON conventions (unchanged)

TTY-aware: table on a terminal, JSON when piped or `-o json`. `rc ask`/`rc run --full` must behave
identically to existing commands re: `-o`, exit codes, and the typed error envelope.

## Tests

- `internal/cli/ask_test.go`: submit → poll → terminal, `--no-wait`, `--brain-ref` forwarding, timeout,
  `BAD_BRAIN_REF` surfacing. Mock the HTTP client as in [`internal/cli/cli_test.go`](../internal/cli/cli_test.go).
- Golden test ([`internal/cli/golden_test.go`](../internal/cli/golden_test.go)) for `rc run <id> --full
  -o json` — this is the renderer's input contract; lock it.

## Release

New verbs ship via the normal `scripts/release.sh patch|minor|major` GoReleaser flow (Homebrew tap +
prebuilt binaries). Bump the `rc-cli` version in [`rootcause-brain-skills/docs/rc-cli.md`](../../../rootcause-brain-skills/docs/rc-cli.md)
once published. The `/full` JSON contract version must move in lockstep with the renderer (brain-skills spec).

## Out of scope

- Rendering the markdown index / writing files — that's the brain-dev renderer (Python), fed by
  `rc run <id> --full -o json`. `rc` only emits JSON.
- Any SSM / host access — `rc` is the infra-free public-API client by definition.
