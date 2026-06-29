package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// Table-mode golden tests: each pins one renderer's human output against testdata/*.golden.

func TestStatusTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "status"); err != nil {
		t.Fatalf("status: %v", err)
	}
	assertGolden(t, "status.golden", out.String())
}

func TestRunsTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "runs", "--limit", "10"); err != nil {
		t.Fatalf("runs: %v", err)
	}
	assertGolden(t, "runs.golden", out.String())
}

func TestRunDetailTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "run", "11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertGolden(t, "run.golden", out.String())
}

func TestRunEventsTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "run", "11111111-1111-1111-1111-111111111111", "--events"); err != nil {
		t.Fatalf("run --events: %v", err)
	}
	assertGolden(t, "events.golden", out.String())
}

func TestRunFullTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "run", "11111111-1111-1111-1111-111111111111", "--full"); err != nil {
		t.Fatalf("run --full: %v", err)
	}
	assertGolden(t, "full.golden", out.String())
}

// TestRunDeclinedTable pins the index "why" one-liner for a run that declined (the motivating case:
// the CLI previously showed `declined` with no WHY). It must surface the truncated decline_reason plus
// the guardrail/forced/fallback flags on a single Why: row.
func TestRunDeclinedTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "run", "declined"); err != nil {
		t.Fatalf("run declined: %v", err)
	}
	assertGolden(t, "run_declined.golden", out.String())
}

// TestRunDeclinedEventsTable pins the trace's terminal-decline rendering: the reply event shows the
// decline_reason instead of a draft/note line.
func TestRunDeclinedEventsTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "run", "declined", "--events"); err != nil {
		t.Fatalf("run declined --events: %v", err)
	}
	assertGolden(t, "events_declined.golden", out.String())
}

// TestRunDeclinedFullTable pins the full header's debug rows + the untruncated decline_reason block.
func TestRunDeclinedFullTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "run", "declined", "--full"); err != nil {
		t.Fatalf("run declined --full: %v", err)
	}
	assertGolden(t, "full_declined.golden", out.String())
}

// TestRunDeclinedJSONPassthrough confirms the new debug fields ride through -o json verbatim (the CLI
// reshapes nothing): the raw server body round-trips unchanged.
func TestRunDeclinedJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "run", "declined"); err != nil {
		t.Fatalf("run declined -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "run_declined.json"), out.Bytes())
}

// TestRunBrainDiffTable pins the brain-diff renderer: the commit header (short sha, author, time,
// subject), the touched files with churn, and the unified diff.
func TestRunBrainDiffTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "run", "11111111-1111-1111-1111-111111111111", "--brain-diff"); err != nil {
		t.Fatalf("run --brain-diff: %v", err)
	}
	assertGolden(t, "brain_diff.golden", out.String())
}

// TestRunBrainDiffNotFoundTable pins the explicit-empty case: a run that wrote no journal commit shows
// the "No brain changes from this run." line, not an error.
func TestRunBrainDiffNotFoundTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "run", "no-brain", "--brain-diff"); err != nil {
		t.Fatalf("run --brain-diff (not found): %v", err)
	}
	assertGolden(t, "brain_diff_none.golden", out.String())
}

// TestRunBrainDiffJSONPassthrough confirms -o json rides the server body through verbatim (the CLI
// reshapes nothing).
func TestRunBrainDiffJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "run", "11111111-1111-1111-1111-111111111111", "--brain-diff"); err != nil {
		t.Fatalf("run --brain-diff -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "brain_diff.json"), out.Bytes())
}

// TestRunBrainDiffMutualExclusion: --brain-diff can't combine with --events/--full/--debug.
func TestRunBrainDiffMutualExclusion(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "run", "11111111-1111-1111-1111-111111111111", "--brain-diff", "--full"); err == nil {
		t.Fatal("expected an error combining --brain-diff with --full")
	}
}

func TestBashRunTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "bash", "run", "--timeout", "45", "printf hello && >&2 echo warn && exit 7"); err != nil {
		t.Fatalf("bash run: %v", err)
	}
	assertGolden(t, "bash_run.golden", out.String())
}

func TestBashRunJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "bash", "run", "--timeout", "45", "printf hello && >&2 echo warn && exit 7"); err != nil {
		t.Fatalf("bash run -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "bash_run.json"), out.Bytes())
}

// TestRunFullJSONL locks the cross-repo seam: `rc run <id> --full -o json` must emit a `type:run`
// header line followed by one `type:event` line per event (JSONL), each carrying its fields verbatim.
func TestRunFullJSONL(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "run", "11111111-1111-1111-1111-111111111111", "--full"); err != nil {
		t.Fatalf("run --full -o json: %v", err)
	}
	assertGolden(t, "full.jsonl.golden", out.String())

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 4 { // 1 run header + 3 events
		t.Fatalf("expected 4 JSONL lines, got %d:\n%s", len(lines), out.String())
	}
	var head map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &head); err != nil {
		t.Fatalf("header line not JSON: %v", err)
	}
	if head["type"] != "run" {
		t.Errorf("first line type = %v, want run", head["type"])
	}
	// Run-header fields must ride through verbatim (full draft body, not a boolean).
	if head["draft"] != "You have 2 open invoices totalling $480." {
		t.Errorf("draft body not carried through: %v", head["draft"])
	}
	if head["brain_resolved"] != "dev/refund-rework @ abc123def456" || head["tenant"] != "de-kies" {
		t.Errorf("projection metadata not carried through: brain_resolved=%v tenant=%v", head["brain_resolved"], head["tenant"])
	}
	if ts, ok := head["tenant_settings"].(string); !ok || !strings.Contains(ts, "sha256:tenantabc") {
		t.Errorf("tenant_settings raw snapshot not carried through: %T %v", head["tenant_settings"], head["tenant_settings"])
	}
	if ts, ok := head["tenant_settings_current"].(string); !ok || !strings.Contains(ts, "sha256:tenantdef") {
		t.Errorf("tenant_settings_current raw snapshot not carried through: %T %v", head["tenant_settings_current"], head["tenant_settings_current"])
	}
	if count, ok := head["grounding_source_drift_count"].(float64); !ok || count != 1 {
		t.Errorf("grounding_source_drift_count = %T %v, want 1", head["grounding_source_drift_count"], head["grounding_source_drift_count"])
	}
	gs, ok := head["grounding_sources"].(map[string]any)
	if !ok || gs["captured"] != true {
		t.Fatalf("grounding_sources missing from full JSONL header: %T %v", head["grounding_sources"], head["grounding_sources"])
	}
	sources, ok := gs["sources"].([]any)
	if !ok || len(sources) != 2 {
		t.Fatalf("grounding sources = %T %v, want 2", gs["sources"], gs["sources"])
	}
	for i, ln := range lines[1:] {
		var ev map[string]any
		if err := json.Unmarshal([]byte(ln), &ev); err != nil {
			t.Fatalf("event line %d not JSON: %v", i, err)
		}
		if ev["type"] != "event" {
			t.Errorf("event line %d type = %v, want event", i, ev["type"])
		}
	}
}

// TestRunDebug locks the --debug decomposer's two output files against goldens: a thin markdown index
// and the jq-able JSONL (type:run header + type:event lines keyed by disp). The printed PATHS are
// non-deterministic (a temp out-dir), so we golden the FILE CONTENTS, not stdout.
func TestRunDebug(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	outDir := t.TempDir()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "run", "11111111-1111-1111-1111-111111111111", "--debug", "--out-dir", outDir); err != nil {
		t.Fatalf("run --debug: %v", err)
	}
	// stdout carries the two written paths (index first, then jsonl).
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 printed paths, got %d: %q", len(lines), out.String())
	}

	base := "11111111-coca-cola"
	idx, err := os.ReadFile(filepath.Join(outDir, base+".md"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	assertGolden(t, "debug.md.golden", string(idx))

	jsonl, err := os.ReadFile(filepath.Join(outDir, base+".jsonl"))
	if err != nil {
		t.Fatalf("read jsonl: %v", err)
	}
	assertGolden(t, "debug.jsonl.golden", string(jsonl))

	// Contract checks on the JSONL: a type:run header then type:event lines keyed by disp.
	jl := strings.Split(strings.TrimRight(string(jsonl), "\n"), "\n")
	if len(jl) != 4 { // 1 header + 3 events
		t.Fatalf("expected 4 JSONL lines, got %d", len(jl))
	}
	var head map[string]any
	if err := json.Unmarshal([]byte(jl[0]), &head); err != nil || head["type"] != "run" {
		t.Fatalf("first line not a run header: %v (%v)", head["type"], err)
	}
	if head["brain_resolved"] != "dev/refund-rework @ abc123def456" || head["tenant"] != "de-kies" {
		t.Errorf("debug header projection metadata missing: brain_resolved=%v tenant=%v", head["brain_resolved"], head["tenant"])
	}
	if count, ok := head["grounding_source_drift_count"].(float64); !ok || count != 1 {
		t.Errorf("debug grounding_source_drift_count = %T %v, want 1", head["grounding_source_drift_count"], head["grounding_source_drift_count"])
	}
	if gs, ok := head["grounding_sources"].(map[string]any); !ok || gs["captured"] != true {
		t.Fatalf("debug header grounding_sources missing: %T %v", head["grounding_sources"], head["grounding_sources"])
	}
	ts, ok := head["tenant_settings"].(map[string]any)
	if !ok || ts["source"] != "cli" || ts["version"] != "sha256:tenantabc" {
		t.Errorf("debug header tenant_settings not parsed: %T %v", head["tenant_settings"], head["tenant_settings"])
	}
	current, ok := head["tenant_settings_current"].(map[string]any)
	if !ok || current["version"] != "sha256:tenantdef" {
		t.Errorf("debug header tenant_settings_current not parsed: %T %v", head["tenant_settings_current"], head["tenant_settings_current"])
	}
	drift, ok := head["tenant_settings_drift"].([]any)
	if !ok || len(drift) != 1 {
		t.Fatalf("debug header tenant_settings_drift missing: %T %v", head["tenant_settings_drift"], head["tenant_settings_drift"])
	}
	item, ok := drift[0].(map[string]any)
	if !ok || item["key"] != "practice_name" || item["then"] != "De Kies" || item["now"] != "De Nieuwe Kies" {
		t.Errorf("debug drift item = %#v", drift[0])
	}
	actions, ok := head["proposed_actions"].([]any)
	if !ok || len(actions) != 1 {
		t.Fatalf("debug header proposed_actions missing: %T %v", head["proposed_actions"], head["proposed_actions"])
	}
	action, ok := actions[0].(map[string]any)
	if !ok || action["slug"] != "create_appointment" {
		t.Errorf("debug header proposed action slug = %v, want create_appointment", action["slug"])
	}
	disps := map[string]bool{}
	for _, ln := range jl[1:] {
		var ev map[string]any
		if err := json.Unmarshal([]byte(ln), &ev); err != nil {
			t.Fatalf("event line not JSON: %v", err)
		}
		if ev["type"] != "event" {
			t.Errorf("line type = %v, want event", ev["type"])
		}
		disps[ev["disp"].(string)] = true
	}
	// Grounding pre-step → P1; the two main steps → 1, 2.
	for _, d := range []string{"P1", "1", "2"} {
		if !disps[d] {
			t.Errorf("missing disp %q in JSONL", d)
		}
	}
}

// TestThreadTraceTable pins the thread trace: how the id resolved, the newest-first runs table with
// health flags + placement, and the "Likely:" hint on the newest (egress-blocked) run.
func TestThreadTraceTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "thread", "thread-abc123"); err != nil {
		t.Fatalf("thread: %v", err)
	}
	assertGolden(t, "thread_trace.golden", out.String())
}

// TestThreadTraceSessionTable pins the session-fallback path (resolved_by:"session") and the declined-run
// hint (the agent's own words on a declined run).
func TestThreadTraceSessionTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "thread", "session-fallback"); err != nil {
		t.Fatalf("thread session: %v", err)
	}
	assertGolden(t, "thread_trace_session.golden", out.String())
}

// TestThreadTraceUnknownTable pins the explicit-empty case: an unknown id is a clean "no runs" answer
// (resolved_by:"none"), not an error.
func TestThreadTraceUnknownTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "thread", "unknown"); err != nil {
		t.Fatalf("thread unknown: %v", err)
	}
	assertGolden(t, "thread_trace_none.golden", out.String())
}

// TestThreadTraceJSONPassthrough confirms -o json emits the server body verbatim — the CLI reshapes
// nothing.
func TestThreadTraceJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "thread", "thread-abc123"); err != nil {
		t.Fatalf("thread -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "thread_trace.json"), out.Bytes())
}

func TestConfigGetTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "config", "get"); err != nil {
		t.Fatalf("config get: %v", err)
	}
	assertGolden(t, "config_get.golden", out.String())
}

func TestConfigSetTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "config", "set", "default_tier=pro", "max_run_usd=5"); err != nil {
		t.Fatalf("config set: %v", err)
	}
	assertGolden(t, "config_set.golden", out.String())
}

// TestConfigSetListValue locks the list-coercion contract: `config set pr.triggers=inbound,mcp` sends a
// JSON ARRAY (asserted in the PATCH handler), not a comma string. The handler fatals if it's not an array.
func TestConfigSetListValue(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "config", "set", "pr.triggers=inbound,mcp"); err != nil {
		t.Fatalf("config set pr.triggers: %v", err)
	}
	assertGolden(t, "config_set.golden", out.String())
}

// TestConfigSetListClear locks the empty-list "clear" gesture: `config set egress.allowlist=` sends an
// empty JSON array (asserted server-side), not null or "".
func TestConfigSetListClear(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "config", "set", "egress.allowlist="); err != nil {
		t.Fatalf("config set egress.allowlist= : %v", err)
	}
}

// TestKBGetTable pins `rc kb get` — the generic bag command over a non-settings bag renders the same
// {key:value/effective/default/source} table as `config get`.
func TestKBGetTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "kb", "get"); err != nil {
		t.Fatalf("kb get: %v", err)
	}
	assertGolden(t, "kb_get.golden", out.String())
}

// TestKBSetTable pins `rc kb set provider=intercom` round-tripping through PATCH /api/v1/kb.
func TestKBSetTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "kb", "set", "provider=intercom", "base_url=https://acme.intercom.io"); err != nil {
		t.Fatalf("kb set: %v", err)
	}
	assertGolden(t, "kb_get.golden", out.String())
}

// TestActionConfigSetBoolCoercion locks the bool-coercion contract: `rc action config set
// actions_enabled=true` must send a JSON boolean, not the string "true" (asserted in the PATCH handler).
func TestActionConfigSetBoolCoercion(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "action", "config", "set", "actions_enabled=true"); err != nil {
		t.Fatalf("action config set actions_enabled=true: %v", err)
	}
}

func TestSchemaTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "schema"); err != nil {
		t.Fatalf("schema: %v", err)
	}
	assertGolden(t, "schema.golden", out.String())
}

func TestSchemaJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "schema"); err != nil {
		t.Fatalf("schema -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "meta_schema.json"), out.Bytes())
}

func TestExplainTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "explain", "default_tier"); err != nil {
		t.Fatalf("explain: %v", err)
	}
	assertGolden(t, "explain_default_tier.golden", out.String())
}

// TestExplainUnknownKey asserts an unknown key is a clear client-side error (not a silent miss).
func TestExplainUnknownKey(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "explain", "nope")
	if err == nil || !strings.Contains(err.Error(), "unknown config key") {
		t.Fatalf("want unknown-key error, got %v", err)
	}
}

func TestAccessTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "access"); err != nil {
		t.Fatalf("access: %v", err)
	}
	assertGolden(t, "access.golden", out.String())
}

func TestAccessJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "access"); err != nil {
		t.Fatalf("access -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "meta_capabilities.json"), out.Bytes())
}

// JSON-mode passthrough tests: -o json must emit the canned server body verbatim (re-indented only),
// so it round-trips to the same value the server sent.

func TestStatusJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "status"); err != nil {
		t.Fatalf("status -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "runs.json"), out.Bytes())
}

func TestRunDetailJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "run", "11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatalf("run -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "run.json"), out.Bytes())
}

func TestConfigGetJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "config", "get"); err != nil {
		t.Fatalf("config get -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "settings.json"), out.Bytes())
}

// TestEventsNDJSON asserts -o json on `run --events` emits one event object per line (NDJSON), not a
// wrapping array — the streamable, jq-friendly contract.
func TestEventsNDJSON(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "run", "11111111-1111-1111-1111-111111111111", "--events"); err != nil {
		t.Fatalf("run --events -o json: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 NDJSON lines, got %d: %q", len(lines), out.String())
	}
	// Each line must be a standalone JSON object (not an array element / wrapped).
	for i, ln := range lines {
		if !strings.HasPrefix(ln, "{") || !strings.HasSuffix(ln, "}") {
			t.Errorf("line %d is not a bare JSON object: %q", i, ln)
		}
	}
	// First line should carry the bash event's command, verbatim.
	if !strings.Contains(lines[0], `"command":"psql`) {
		t.Errorf("first NDJSON line missing command: %q", lines[0])
	}
}

// TestAPIErrorPath asserts an API error code/message is surfaced verbatim to stderr, the field lines
// are printed, and Execute returns non-nil (→ exit 1).
func TestAPIErrorPath(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, errb := newTestEnv(t, srv, "table")
	err := run(t, e, "config", "set", "max_run_usd=oops")
	if err == nil {
		t.Fatal("expected error from INVALID_SETTINGS, got nil")
	}
	// Execute() returns the error; the binary's Execute() wrapper prints it. Simulate that here.
	printError(errb, err)
	got := errb.String()
	if !strings.Contains(got, "INVALID_SETTINGS: settings rejected") {
		t.Errorf("missing verbatim code/message: %q", got)
	}
	if !strings.Contains(got, "  max_run_usd: must be a number") {
		t.Errorf("missing field error line: %q", got)
	}
}

// TestNotLoggedIn asserts a clear "run `rc login`" error when no token resolves (no token store).
func TestNotLoggedIn(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	// Drop the static-token seam so newClient consults the (empty, isolated) token store.
	e2 := &env{profile: "default", output: "table", baseURLOvr: srv.URL, out: e.out, err: e.err}
	err := run(t, e2, "status")
	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("expected a not-logged-in error, got %v", err)
	}
}

// TestNonEnvelopeHTTPError asserts a plain-text non-2xx (here a 405 from an older server) is rendered
// with method + path + status text + base URL and the 405 hint — not a bare "HTTP 405".
func TestNonEnvelopeHTTPError(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, errb := newTestEnv(t, srv, "table")
	err := run(t, e, "run", "405")
	if err == nil {
		t.Fatal("expected error from 405, got nil")
	}
	printError(errb, err)
	got := errb.String()
	for _, want := range []string{
		"GET /api/v1/runs/405 → HTTP 405 Method Not Allowed",
		"the runs list endpoint isn't deployed",
		"base URL: " + srv.URL,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// TestEventsRenumbered asserts the table view renumbers events 1..N (hiding the server's negative
// sentinel seqs), while NDJSON (-o json) preserves the raw seq.
func TestEventsRenumbered(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()

	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "run", "sentinel", "--events"); err != nil {
		t.Fatalf("run --events: %v", err)
	}
	table := out.String()
	if !strings.Contains(table, "#1  bash") || !strings.Contains(table, "#2  bash") {
		t.Errorf("expected renumbered #1/#2, got:\n%s", table)
	}
	if strings.Contains(table, "#-") {
		t.Errorf("table leaked a negative sentinel seq:\n%s", table)
	}

	eJSON, outJSON, _ := newTestEnv(t, srv, "json")
	if err := run(t, eJSON, "run", "sentinel", "--events"); err != nil {
		t.Fatalf("run --events -o json: %v", err)
	}
	if !strings.Contains(outJSON.String(), `"seq":-1000000`) {
		t.Errorf("NDJSON must keep the raw seq, got:\n%s", outJSON.String())
	}
}

// TestErrorIsTyped confirms the client returns a typed *APIError carrying code+message+fields, the
// load-bearing contract for verbatim surfacing.
func TestErrorIsTyped(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "run", "bad") // → 404 UNKNOWN_RUN
	var apiErr *client.APIError
	if !asAPIError(err, &apiErr) {
		t.Fatalf("expected *client.APIError, got %T: %v", err, err)
	}
	if apiErr.Code != "UNKNOWN_RUN" {
		t.Errorf("code = %q, want UNKNOWN_RUN", apiErr.Code)
	}
}
