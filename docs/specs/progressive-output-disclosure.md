# Spec — CLI progressive output disclosure

**Status: shipped.** Package [`internal/outputspill`](../../internal/outputspill/outputspill.go) +
per-command wiring in [`internal/cli/outputspill.go`](../../internal/cli/outputspill.go)
(`env.renderJSON` / `env.renderBytes`). [`SKILL.md`](../../SKILL.md) has the one-paragraph summary and
the flag/env knobs; this file keeps the durable contract (policy, manifest shape, thresholds, detection,
hints) that the code and its golden tests hold to.

## Intent

`rc` is a fat client over a thin server: endpoints keep returning the full token-scoped payload. The CLI
decides how much to print, spills large payloads to local files, and guides the caller to inspect them
without flooding a terminal, pipe, or LLM context. This mirrors hosted agent bash, which already writes
large stdout/stderr to disk and hands the model a head/tail preview + path + `sed`/`rg`/`jq` hints.

Server APIs stay raw and complete (no server-side summarization); no DB/infra access; no TUI.

## Output policy

The response is still fetched fully. The CLI decides whether stdout is a preview/manifest or the raw
payload:

| Output shape | Small output | Large output |
|---|---|---|
| TTY table/text | print inline | head/tail preview + spill path + hints |
| `-o json` / piped JSON | print inline | write full JSON to disk; stdout gets a manifest JSON (path(s), preview, size, hints) |
| NDJSON streams | stream inline | write full stream to disk; stdout gets a manifest unless `--stream` |
| `--raw-output` | print exactly as today | never spill |

## Local spill location

Default `.rootcause/output/` (brain repos wholesale-ignore `.rootcause/`, keeping run/customer data out
of git). Per-artifact subfolders use command-specific names when known, e.g.
`bash-run-<run8>-seq-<seq>/stdout.txt`, `run-<run8>/trace.jsonl`, `env-keys/response.json`. Files:
`response.json`, `stdout.txt`, `stderr.txt`, `events.jsonl`, `INDEX.md` as applicable.

Flags/env: `--out-dir <dir>` / `RC_OUTPUT_DIR` (spill dir), `--raw-output` (disable spill, raw stdout),
`--no-preview` (write files, print only paths/metadata).

## Manifest contract

When `-o json` spills, stdout is valid JSON but not the huge payload (`Manifest` in
[`outputspill.go`](../../internal/outputspill/outputspill.go)):

```json
{
  "spilled": true,
  "path": ".rootcause/output/bash-run-22222222-seq-3/stdout.txt",
  "format": "text",
  "bytes": 82144,
  "lines": 1290,
  "preview": {"head": "first lines...", "tail": "last lines..."},
  "hints": [
    "sed -n '1,120p' .rootcause/output/bash-run-22222222-seq-3/stdout.txt",
    "rg 'PATTERN' .rootcause/output/bash-run-22222222-seq-3/stdout.txt",
    "jq '.' .rootcause/output/bash-run-22222222-seq-3/stdout.txt"
  ],
  "raw_mode_hint": "rerun with --raw-output to print the full payload to stdout"
}
```

Multi-part output uses `"artifacts": {"response": {...}, "stdout": {...}, "stderr": {...}}` instead of a
single `path`. A stream the server itself truncated is still spilled and marked `"server_truncated": true`
(a separate concern — the CLI cannot recover bytes the server did not return).

## Thresholds & detection

Byte thresholds, not token estimates:

- `RC_OUTPUT_SPILL_THRESHOLD` (default `6000`) — per field/stream.
- `RC_OUTPUT_INLINE_MAX` (default `20000`) — whole compact JSON before stdout becomes a manifest.
- Preview: head `2000` bytes, tail `1000` bytes.

Spill individual fields named `stdout`, `stderr`, `body`, `draft`, `notes`, `system_prompt`, `events`,
`response`, or any string over threshold; spill whole responses over `RC_OUTPUT_INLINE_MAX`;
binary/non-UTF8 payloads are written and marked `format: "binary"`.

## Hints

Generated from detected format — every manifest carries ≥3 useful commands (preview range, search,
structured query when applicable):

| Format | Hints |
|---|---|
| JSON object/array | `jq '.' FILE`, `jq 'keys' FILE`, `jq -r '.. \| strings' FILE \| rg PATTERN` |
| JSONL/NDJSON | `jq -r 'select(...)' FILE`, `jq -r '.stdout? // empty' FILE` |
| Text | `sed -n 'A,Bp' FILE`, `rg PATTERN FILE`, `tail -n 120 FILE` |

## Coverage

Wired through `env.renderJSON` / `env.renderBytes`: `rc dev console bash run` + console JSON passthrough,
`rc run events|trace|debug`, `rc fleet runs|patterns|health` (incl. `--all`), `rc dev api routes|openapi`,
collection CRUD with large values, and `rc project corpus download` stdout. Intentional one-time secret
reveals (`rc project connection reveal`, `rc project token mint`) stay raw so capture/copy is unchanged.
