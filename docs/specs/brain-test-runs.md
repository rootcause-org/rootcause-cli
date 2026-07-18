# Spec — Brain test runs (CLI side: `rc ask` + run trace)

**Status: shipped.** Architecture and flag detail now live in [`SKILL.md`](../../SKILL.md) (the ladder
row for `rc ask`, the `rc ask` trigger section, and `rc run trace`/`debug`). This file keeps only the
cross-repo intent and the seam that must stay stable.

- Server contract: [`rootcause/docs/specs/brain-test-runs.md`](../../../rootcause/docs/specs/brain-test-runs.md)
- Renderer + playbook that consumes `rc` output: [`rootcause-brain-skills/docs/specs/brain-test-runs.md`](../../../rootcause-brain-skills/docs/specs/brain-test-runs.md)

## Intent

`rc` stays a thin, typed, TTY-aware front-end to `/api/v1`: it **triggers** runs and **emits clean
JSON** for the brain-side (Python) renderer to consume. It never renders the markdown index or touches
SSM/host access.

`rc ask --brain-ref dev/<branch>` is the project-dev's full-fidelity "test without pushing main" loop:
push a `dev/*` branch (main untouched → not live), then ask against it. The server runs the real loop
and flags any actions/PRs as test. A `BAD_BRAIN_REF` / "ref not found" surfaces via the typed-error path
([`internal/client/errors.go`](../../internal/client/errors.go)) — the fix is `git push origin <ref>`
to the brain first.

## The stable seam

`rc run trace <id> -o json` is the **exact stdin contract** the brain-dev renderer reads to produce
`<run8>-<proj>.{md,jsonl}`. It emits the run-header line first, then one NDJSON line per event
(`{"type":"run",…}` then `{"type":"event",…}` — the JSONL shape shared with `rc run debug`). Keep it
stable; the `/trace` JSON contract version must move in lockstep with the renderer (brain-skills spec).

Implementation: `Client.Submit` / `Client.Full` in [`internal/client/client.go`](../../internal/client/client.go)
(+ `SubmitRequest`/`SubmitResponse`/`FullResponse`/`RunHeader` in
[`types.go`](../../internal/client/types.go)); `rc ask` in [`internal/cli/ask.go`](../../internal/cli/ask.go);
`rc run show|events|trace|debug` in [`internal/cli/run.go`](../../internal/cli/run.go) (event lines via
`emitNDJSON`). Golden test for the `rc run trace <id> -o json` contract lives in
[`internal/cli/golden_test.go`](../../internal/cli/golden_test.go); lock it.
