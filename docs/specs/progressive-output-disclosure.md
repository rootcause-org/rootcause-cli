# Spec - CLI progressive output disclosure

**Repo:** `rootcause-cli`.

`rc` stays a fat client over a thin server: API endpoints keep returning the full token-scoped payload.
The CLI decides how much to print, when to spill large payloads to local files, and how to guide the
caller to inspect those files without flooding a terminal, pipe, or LLM context.

## Problem

Hosted agent bash already has good progressive disclosure: large stdout/stderr is written to
`/tmp/rc-bash/<seq>.<stream>.txt`, while the model receives a head/tail preview, a path, and concrete
`sed`/`rg`/`jq` hints.

`rc bash run` and other CLI commands do not have an equivalent client-side layer:

- table output can print large blobs inline;
- `-o json` pretty-prints whatever the server returned;
- truncated server-side console fields only tell us truncation happened, without a local drill-down
  file;
- callers often need to manually rerun commands with `jq`, `rg`, `sed`, or output redirection.

## Goals

- Keep server APIs raw and complete. No new server summarization requirement.
- Make `rc` safe to use from an LLM/coding-agent loop without dumping huge blobs into context.
- Apply to table, JSON, NDJSON, and plain/text-ish outputs, not only `rc bash run`.
- Preserve full data locally whenever stdout is suppressed or previewed.
- Print concise, copyable drill-down hints.
- Keep pipe-first scripting usable.

## Non-goals

- No server-side output rewriting.
- No database or direct infrastructure access in the CLI.
- No TUI.
- No attempt to parse every platform's domain-specific JSON.

## Output Policy

Add a shared CLI output-spill package used by renderers and JSON passthrough paths.

Default policy:

| Output shape | Small output | Large output |
|---|---|---|
| TTY table/text | print inline | print head/tail preview + spill path + hints |
| `-o json` / piped JSON object | print compact manifest JSON with spill path(s), preview, size, hints | write full JSON to disk |
| NDJSON streams | stream small lines inline | write full stream to disk; stdout prints a manifest JSON object unless `--stream` is explicitly requested |
| Explicit raw mode | print exactly as today | never spill |

The server response is still fetched fully. The CLI decides whether stdout is a preview/manifest or the
raw payload.

## Local Spill Location

Default local directory:

```text
.rootcause/output/
```

Rationale: existing `rc run --debug` already writes local artifacts under `.rootcause/debug/`, and brain
repos wholesale-ignore `.rootcause/`. This keeps sensitive run/customer data out of git.

Flags/env:

- `--out-dir <dir>`: override for commands that can spill.
- `RC_OUTPUT_DIR=<dir>`: default spill dir override.
- `--raw-output`: disable spill and print current raw behavior.
- `--no-preview`: write files and print only paths/metadata.

File naming:

```text
.rootcause/output/<timestamp>-<command>-<short-id>/
  response.json
  stdout.txt
  stderr.txt
  events.jsonl
  INDEX.md
```

Use command-specific names when known:

- `bash-run-<run8>-seq-<seq>/stdout.txt`
- `run-<run8>/trace.jsonl`
- `env-keys/response.json`

## Manifest Contract

When `-o json` spills, stdout should be valid JSON, but not the huge payload:

```json
{
  "spilled": true,
  "path": ".rootcause/output/bash-run-22222222-seq-3/stdout.txt",
  "format": "text",
  "bytes": 82144,
  "lines": 1290,
  "preview": {
    "head": "first lines...",
    "tail": "last lines..."
  },
  "hints": [
    "sed -n '1,120p' .rootcause/output/bash-run-22222222-seq-3/stdout.txt",
    "rg 'PATTERN' .rootcause/output/bash-run-22222222-seq-3/stdout.txt",
    "jq '.' .rootcause/output/bash-run-22222222-seq-3/stdout.txt"
  ],
  "raw_mode_hint": "rerun with --raw-output to print the full payload to stdout"
}
```

For multi-part output:

```json
{
  "spilled": true,
  "artifacts": {
    "response": {"path": ".../response.json", "format": "json", "bytes": 120044},
    "stdout": {"path": ".../stdout.txt", "format": "text", "bytes": 65536},
    "stderr": {"path": ".../stderr.txt", "format": "text", "bytes": 812}
  },
  "hints": ["jq '.stdout' .../response.json", "sed -n '1,120p' .../stdout.txt"]
}
```

## Thresholds

Use byte thresholds, not token estimates:

- `RC_OUTPUT_SPILL_THRESHOLD`, default `6000` bytes per field/stream.
- `RC_OUTPUT_INLINE_MAX`, default `20000` bytes for whole JSON response before replacing stdout with a
  manifest.
- Keep previews close to hosted bash behavior: head `2000` bytes, tail `1000` bytes.

Detection:

- Spill individual fields named `stdout`, `stderr`, `body`, `draft`, `notes`, `system_prompt`,
  `events`, `response`, or any string over threshold.
- Spill whole JSON responses when compact JSON exceeds `RC_OUTPUT_INLINE_MAX`.
- For binary/non-UTF8 payloads, write bytes and mark `format: "binary"`.

## Hints

Generate hints from detected format:

| Format | Hints |
|---|---|
| JSON object/array | `jq '.' FILE`, `jq 'keys' FILE`, `jq -r '.. \| strings' FILE \| rg PATTERN` |
| JSONL/NDJSON | `jq -r 'select(...)' FILE`, `jq -r '.stdout? // empty' FILE` |
| Text | `sed -n 'A,Bp' FILE`, `rg PATTERN FILE`, `tail -n 120 FILE` |
| CSV/TSV | `xsv headers FILE` when available, else `sed -n '1,20p' FILE` |

Every manifest should include at least three useful commands: preview range, search, structured query
when applicable.

## Command Coverage

Phase 1:

- `rc bash run`
- raw JSON passthrough in `internal/cli/console.go`
- `rc run --events`
- `rc run --full`
- `rc run --debug` alignment: keep current files, but emit the same manifest shape when `-o json`

Phase 2:

- `rc fleet`, `rc patterns`, `rc health` when `--all` produces huge merged JSON
- `rc export download` when stdout target is omitted and body is large
- `rc routes` / `rc openapi`
- collection commands with large values

## Bash-Specific Behavior

For `rc bash run`:

- server still returns its current response body;
- CLI writes the response JSON to `response.json`;
- if `stdout` or `stderr` is large, write each stream to `stdout.txt` / `stderr.txt`;
- table mode shows:

```text
stdout: [output too large: 82144 bytes, 1290 lines - full output saved to .../stdout.txt]
<head>
...[middle omitted]...
<tail>

Hints:
  sed -n '1,120p' .../stdout.txt
  rg PATTERN .../stdout.txt
  jq '.' .../stdout.txt
```

- JSON mode shows the manifest rather than the full response, unless `--raw-output` is passed.

If the server response indicates `stdout_truncated=true`, the CLI cannot recover bytes the server did
not return. It should still spill the captured stream to disk and mark:

```json
"server_truncated": true
```

That is a separate concern from client-side progressive disclosure. A later server/console change may
increase or remove the capture cap; this spec does not require it.

## Raw Compatibility

Because `rc` is pipe-first, callers need an escape hatch:

```bash
rc bash run '...' -o json --raw-output
rc run <id> --full -o json --raw-output
```

`--raw-output` means exactly current behavior: write the complete response to stdout and no spill
manifest. It is intentionally loud in docs because it can flood LLM context.

## Implementation Plan

1. Add `internal/outputspill`:
   - threshold config from env + flags;
   - `MaybeSpillBytes`, `MaybeSpillJSON`, `Manifest`;
   - path builder under `.rootcause/output/`;
   - preview and hint generation.

2. Add root-level flags shared by commands:
   - `--raw-output`
   - `--out-dir`
   - `--no-preview`

3. Wire `rc bash run` first:
   - typed response path and raw JSON path both call outputspill;
   - table renderer receives artifact metadata;
   - JSON mode writes manifest.

4. Wire run trace paths:
   - `--events`, `--full`, `--debug`;
   - normalize debug's current path printing into the manifest shape in JSON mode.

5. Extend high-volume commands.

## Tests

Golden/unit tests:

- small table output unchanged;
- large table output contains preview, path, hints, no full middle;
- large `-o json` writes full file and prints manifest JSON;
- `--raw-output` preserves exact previous JSON fixture;
- server-truncated response marks `server_truncated`;
- JSON hints use `jq`, text hints use `sed`/`rg`;
- output files are created under `.rootcause/output/` or `--out-dir`;
- no secrets are copied into filenames.

Use existing `internal/cli/golden_test.go` style. For file paths, golden file contents rather than full
temp paths where possible, like `rc run --debug` tests already do.

## Release / Docs

- Update `README.md` command docs for `--raw-output`, `--out-dir`, and spill manifests.
- Update `SKILL.md` output section: JSON remains raw-source-backed, but may be returned by local path
  when large.
- Update brain skills docs once released so agents learn to read manifests first, then `jq` local files.

## Open Questions

- Should spill be default for piped stdout, or only when stdout is a terminal/LLM context? Proposed:
  default everywhere except `--raw-output`, because Codex often captures piped output too.
- Should `-o json` ever write NDJSON directly for large streams? Proposed: no; emit manifest JSON so
  the caller has one stable shape.
- Should spilled files be automatically cleaned? Proposed: no automatic deletion; `.rootcause/` is
  local scratch and useful during the session.
