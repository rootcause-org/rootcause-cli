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
	if err := run(t, e, "run", "list", "--limit", "10"); err != nil {
		t.Fatalf("runs: %v", err)
	}
	assertGolden(t, "runs.golden", out.String())
}

func TestRunDetailTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "run", "show", "11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertGolden(t, "run.golden", out.String())
}

func TestRunEventsTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "run", "events", "11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatalf("run events: %v", err)
	}
	assertGolden(t, "events.golden", out.String())
}

func TestRunFullTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "run", "trace", "11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatalf("run trace: %v", err)
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
	if err := run(t, e, "run", "show", "declined"); err != nil {
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
	if err := run(t, e, "run", "events", "declined"); err != nil {
		t.Fatalf("run events declined: %v", err)
	}
	assertGolden(t, "events_declined.golden", out.String())
}

// TestRunDeclinedFullTable pins the full header's debug rows + the untruncated decline_reason block.
func TestRunDeclinedFullTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "run", "trace", "declined"); err != nil {
		t.Fatalf("run trace declined: %v", err)
	}
	assertGolden(t, "full_declined.golden", out.String())
}

// TestRunDeclinedJSONPassthrough confirms the new debug fields ride through -o json verbatim (the CLI
// reshapes nothing): the raw server body round-trips unchanged.
func TestRunDeclinedJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "run", "show", "declined"); err != nil {
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
	if err := run(t, e, "run", "brain-diff", "11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatalf("run brain-diff: %v", err)
	}
	assertGolden(t, "brain_diff.golden", out.String())
}

// TestRunBrainDiffNotFoundTable pins the explicit-empty case: a run that wrote no journal commit shows
// the "No brain changes from this run." line, not an error.
func TestRunBrainDiffNotFoundTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "run", "brain-diff", "no-brain"); err != nil {
		t.Fatalf("run brain-diff (not found): %v", err)
	}
	assertGolden(t, "brain_diff_none.golden", out.String())
}

// TestRunBrainDiffJSONPassthrough confirms -o json rides the server body through verbatim (the CLI
// reshapes nothing).
func TestRunBrainDiffJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "run", "brain-diff", "11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatalf("run brain-diff -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "brain_diff.json"), out.Bytes())
}

func TestBashRunTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "dev", "console", "bash", "run", "--timeout", "45", "printf hello && >&2 echo warn && exit 7"); err != nil {
		t.Fatalf("dev console bash run: %v", err)
	}
	assertGolden(t, "bash_run.golden", out.String())
}

func TestBashRunJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "dev", "console", "bash", "run", "--timeout", "45", "printf hello && >&2 echo warn && exit 7"); err != nil {
		t.Fatalf("dev console bash run -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "bash_run.json"), out.Bytes())
}

func TestBashRunLargeTableSpills(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	outDir := t.TempDir()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "--out-dir", outDir, "dev", "console", "bash", "run", "large-output"); err != nil {
		t.Fatalf("dev console bash run large: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "[output too large:") || !strings.Contains(got, "full output saved to") || !strings.Contains(got, "Hints:") {
		t.Fatalf("large table output missing spill block:\n%s", got)
	}
	if strings.Contains(got, "MIDDLE-SENTINEL") {
		t.Fatalf("large table output printed omitted middle:\n%s", got)
	}
	stdoutPath := filepath.Join(outDir, "bash-run-33333333-seq-9", "stdout.txt")
	b, err := os.ReadFile(stdoutPath)
	if err != nil {
		t.Fatalf("read spilled stdout: %v", err)
	}
	if !strings.Contains(string(b), "MIDDLE-SENTINEL") {
		t.Fatalf("spilled stdout missing full content")
	}
	if _, err := os.Stat(filepath.Join(outDir, "bash-run-33333333-seq-9", "response.json")); err != nil {
		t.Fatalf("response.json missing: %v", err)
	}
}

func TestBashRunSmallServerTruncatedSpills(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	outDir := t.TempDir()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "--out-dir", outDir, "dev", "console", "bash", "run", "small-truncated"); err != nil {
		t.Fatalf("dev console bash run small truncated: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "full output saved to") || !strings.Contains(got, "stderr truncated") {
		t.Fatalf("small truncated table did not spill:\n%s", got)
	}
	stderrPath := filepath.Join(outDir, "bash-run-44444444-seq-4", "stderr.txt")
	b, err := os.ReadFile(stderrPath)
	if err != nil {
		t.Fatalf("read spilled stderr: %v", err)
	}
	if string(b) != "warn\n" {
		t.Fatalf("spilled stderr = %q", b)
	}

	eJSON, outJSON, _ := newTestEnv(t, srv, "json")
	if err := run(t, eJSON, "--out-dir", outDir, "--no-preview", "dev", "console", "bash", "run", "small-truncated"); err != nil {
		t.Fatalf("dev console bash run small truncated -o json: %v", err)
	}
	var manifest struct {
		Artifacts map[string]struct {
			ServerTruncated bool `json:"server_truncated"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(outJSON.Bytes(), &manifest); err != nil {
		t.Fatalf("manifest not JSON: %v\n%s", err, outJSON.String())
	}
	if !manifest.Artifacts["stderr"].ServerTruncated {
		t.Fatalf("stderr artifact did not mark server_truncated: %s", outJSON.String())
	}
}

func TestBashRunLargeJSONManifestAndRawOutput(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()

	outDir := t.TempDir()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "--out-dir", outDir, "--no-preview", "dev", "console", "bash", "run", "large-output"); err != nil {
		t.Fatalf("dev console bash run large -o json: %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(out.Bytes(), &manifest); err != nil {
		t.Fatalf("manifest not JSON: %v\n%s", err, out.String())
	}
	if manifest["spilled"] != true || strings.Contains(out.String(), "MIDDLE-SENTINEL") {
		t.Fatalf("bad manifest output:\n%s", out.String())
	}
	artifacts, ok := manifest["artifacts"].(map[string]any)
	if !ok || artifacts["stdout"] == nil || artifacts["response"] == nil {
		t.Fatalf("manifest artifacts missing: %#v", manifest["artifacts"])
	}

	rawDir := t.TempDir()
	eRaw, rawOut, _ := newTestEnv(t, srv, "json")
	if err := run(t, eRaw, "--out-dir", rawDir, "--raw-output", "dev", "console", "bash", "run", "large-output"); err != nil {
		t.Fatalf("dev console bash run large --raw-output: %v", err)
	}
	if !strings.Contains(rawOut.String(), "MIDDLE-SENTINEL") || strings.Contains(rawOut.String(), `"spilled": true`) {
		t.Fatalf("raw output not preserved:\n%s", rawOut.String())
	}
	if entries, err := os.ReadDir(rawDir); err != nil {
		t.Fatalf("read raw output dir: %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("raw output wrote artifacts: %v", entries)
	}
}

func TestBashListTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "dev", "console", "bash", "list"); err != nil {
		t.Fatalf("dev console bash list: %v", err)
	}
	assertGolden(t, "bash_list.golden", out.String())
}

// TestRunFullJSONL locks the cross-repo seam: `rc run trace <id> -o json` must emit a `type:run`
// header line followed by one `type:event` line per event (JSONL), each carrying its fields verbatim.
func TestRunFullJSONL(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "run", "trace", "11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatalf("run trace -o json: %v", err)
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

// TestRunDebug locks `rc run debug`'s two output files against goldens: a thin markdown index
// and the jq-able JSONL (type:run header + type:event lines keyed by disp). The printed PATHS are
// non-deterministic (a temp out-dir), so we golden the FILE CONTENTS, not stdout.
func TestRunDebug(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	outDir := t.TempDir()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "run", "debug", "11111111-1111-1111-1111-111111111111", "--out-dir", outDir); err != nil {
		t.Fatalf("run debug: %v", err)
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

func TestRunDebugJSONManifest(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	outDir := filepath.Join(t.TempDir(), "debug out")
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "run", "debug", "11111111-1111-1111-1111-111111111111", "--out-dir", outDir); err != nil {
		t.Fatalf("run debug -o json: %v", err)
	}
	var manifest struct {
		Spilled   bool `json:"spilled"`
		Artifacts map[string]struct {
			Path string `json:"path"`
		} `json:"artifacts"`
		Hints []string `json:"hints"`
	}
	if err := json.Unmarshal(out.Bytes(), &manifest); err != nil {
		t.Fatalf("manifest not JSON: %v\n%s", err, out.String())
	}
	if !manifest.Spilled || manifest.Artifacts["index"].Path == "" || manifest.Artifacts["trace"].Path == "" {
		t.Fatalf("bad debug manifest: %s", out.String())
	}
	if len(manifest.Hints) == 0 || !strings.Contains(manifest.Hints[0], "'") {
		t.Fatalf("debug hints not shell-quoted for spaced path: %#v", manifest.Hints)
	}
	for name, art := range manifest.Artifacts {
		info, err := os.Stat(art.Path)
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("%s perms = %o, want 600", name, got)
		}
	}
}

// TestThreadTraceTable pins the thread trace: how the id resolved, the newest-first runs table with
// health flags + placement, and the "Likely:" hint on the newest (egress-blocked) run.
func TestThreadTraceTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "run", "thread", "thread-abc123"); err != nil {
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
	if err := run(t, e, "run", "thread", "session-fallback"); err != nil {
		t.Fatalf("run thread session: %v", err)
	}
	assertGolden(t, "thread_trace_session.golden", out.String())
}

// TestThreadTraceUnknownTable pins the explicit-empty case: an unknown id is a clean "no runs" answer
// (resolved_by:"none"), not an error.
func TestThreadTraceUnknownTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "run", "thread", "unknown"); err != nil {
		t.Fatalf("run thread unknown: %v", err)
	}
	assertGolden(t, "thread_trace_none.golden", out.String())
}

// TestThreadTraceJSONPassthrough confirms -o json emits the server body verbatim — the CLI reshapes
// nothing.
func TestThreadTraceJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "run", "thread", "thread-abc123"); err != nil {
		t.Fatalf("run thread -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "thread_trace.json"), out.Bytes())
}

func TestConfigGetTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "settings", "runtime", "get"); err != nil {
		t.Fatalf("project settings runtime get: %v", err)
	}
	assertGolden(t, "config_get.golden", out.String())
}

func TestConfigSetTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "settings", "runtime", "set", "default_tier=pro", "max_run_usd=5"); err != nil {
		t.Fatalf("project settings runtime set: %v", err)
	}
	assertGolden(t, "config_set.golden", out.String())
}

// TestConfigSetListValue locks the list-coercion contract: `project settings runtime set pr.triggers=inbound,mcp` sends a
// JSON ARRAY (asserted in the PATCH handler), not a comma string. The handler fatals if it's not an array.
func TestConfigSetListValue(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "settings", "runtime", "set", "pr.triggers=inbound,mcp"); err != nil {
		t.Fatalf("project settings runtime set pr.triggers: %v", err)
	}
	assertGolden(t, "config_set.golden", out.String())
}

// TestConfigSetListClear locks the empty-list "clear" gesture: `project settings runtime set egress.allowlist=` sends an
// empty JSON array (asserted server-side), not null or "".
func TestConfigSetListClear(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "settings", "runtime", "set", "egress.allowlist="); err != nil {
		t.Fatalf("project settings runtime set egress.allowlist= : %v", err)
	}
}

// TestKBGetTable pins `rc project knowledge sync get` — the generic bag command over a non-settings
// bag renders the same {key:value/effective/default/source} table as project runtime settings.
func TestKBGetTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "knowledge", "sync", "get"); err != nil {
		t.Fatalf("project knowledge sync get: %v", err)
	}
	assertGolden(t, "kb_get.golden", out.String())
}

// TestKBSetTable pins `rc project knowledge sync set provider=intercom` round-tripping through PATCH /api/v1/kb.
func TestKBSetTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "knowledge", "sync", "set", "provider=intercom", "base_url=https://acme.intercom.io"); err != nil {
		t.Fatalf("project knowledge sync set: %v", err)
	}
	assertGolden(t, "kb_get.golden", out.String())
}

// TestActionConfigSetBoolCoercion locks the bool-coercion contract: `rc project action-settings set
// actions_enabled=true` must send a JSON boolean, not the string "true" (asserted in the PATCH handler).
func TestActionConfigSetBoolCoercion(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "action-settings", "set", "actions_enabled=true"); err != nil {
		t.Fatalf("project action-settings set actions_enabled=true: %v", err)
	}
}

func TestSchemaTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "settings", "schema"); err != nil {
		t.Fatalf("schema: %v", err)
	}
	assertGolden(t, "schema.golden", out.String())
}

func TestSchemaJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "project", "settings", "schema"); err != nil {
		t.Fatalf("schema -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "meta_schema.json"), out.Bytes())
}

func TestExplainTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "settings", "describe", "default_tier"); err != nil {
		t.Fatalf("explain: %v", err)
	}
	assertGolden(t, "explain_default_tier.golden", out.String())
}

// TestExplainUnknownKey asserts an unknown key is a clear client-side error (not a silent miss).
func TestExplainUnknownKey(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "project", "settings", "describe", "nope")
	if err == nil || !strings.Contains(err.Error(), "unknown config key") {
		t.Fatalf("want unknown-key error, got %v", err)
	}
}

func TestAccessTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "auth", "access"); err != nil {
		t.Fatalf("access: %v", err)
	}
	assertGolden(t, "access.golden", out.String())
}

func TestAccessJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "auth", "access"); err != nil {
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
	if err := run(t, e, "run", "show", "11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatalf("run -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "run.json"), out.Bytes())
}

func TestConfigGetJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "project", "settings", "runtime", "get"); err != nil {
		t.Fatalf("project settings runtime get -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "settings.json"), out.Bytes())
}

// TestEventsNDJSON asserts -o json on `rc run events` emits one event object per line (NDJSON), not a
// wrapping array — the streamable, jq-friendly contract.
func TestEventsNDJSON(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "run", "events", "11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatalf("run events -o json: %v", err)
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

func TestEventsLargeNDJSONSpills(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	outDir := t.TempDir()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "--out-dir", outDir, "run", "events", "large-events"); err != nil {
		t.Fatalf("run events large-events: %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(out.Bytes(), &manifest); err != nil {
		t.Fatalf("manifest not JSON: %v\n%s", err, out.String())
	}
	if manifest["spilled"] != true || manifest["format"] != "jsonl" {
		t.Fatalf("bad events manifest: %#v", manifest)
	}
	path, _ := manifest["path"].(string)
	if path == "" {
		t.Fatalf("manifest path missing: %#v", manifest)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read spilled events: %v", err)
	}
	if lines := strings.Split(strings.TrimSpace(string(b)), "\n"); len(lines) != 260 {
		t.Fatalf("spilled events lines = %d, want 260", len(lines))
	}

	streamDir := t.TempDir()
	eStream, streamOut, _ := newTestEnv(t, srv, "json")
	if err := run(t, eStream, "--out-dir", streamDir, "run", "events", "large-events", "--stream"); err != nil {
		t.Fatalf("run events large-events --stream: %v", err)
	}
	if strings.Contains(streamOut.String(), `"spilled": true`) {
		t.Fatalf("--stream emitted manifest:\n%s", streamOut.String())
	}
	if lines := strings.Split(strings.TrimSpace(streamOut.String()), "\n"); len(lines) != 260 {
		t.Fatalf("--stream lines = %d, want 260", len(lines))
	}
	if entries, err := os.ReadDir(streamDir); err != nil {
		t.Fatalf("read stream dir: %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("--stream wrote artifacts: %v", entries)
	}

	rawDir := t.TempDir()
	eRaw, rawOut, _ := newTestEnv(t, srv, "json")
	if err := run(t, eRaw, "--out-dir", rawDir, "--raw-output", "run", "events", "large-events"); err != nil {
		t.Fatalf("run events large-events --raw-output: %v", err)
	}
	if strings.Contains(rawOut.String(), `"spilled": true`) {
		t.Fatalf("--raw-output emitted manifest:\n%s", rawOut.String())
	}
	if lines := strings.Split(strings.TrimSpace(rawOut.String()), "\n"); len(lines) != 260 {
		t.Fatalf("--raw-output lines = %d, want 260", len(lines))
	}
}

// TestAPIErrorPath asserts an API error code/message is surfaced verbatim to stderr, the field lines
// are printed, and Execute returns non-nil (→ exit 1).
func TestAPIErrorPath(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, errb := newTestEnv(t, srv, "table")
	err := run(t, e, "project", "settings", "runtime", "set", "max_run_usd=oops")
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

// TestNotLoggedIn asserts a clear "run `rc auth login`" error when no token resolves (no token store).
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
	err := run(t, e, "run", "show", "405")
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
	if err := run(t, e, "run", "events", "sentinel"); err != nil {
		t.Fatalf("run events: %v", err)
	}
	table := out.String()
	if !strings.Contains(table, "#1  bash") || !strings.Contains(table, "#2  bash") {
		t.Errorf("expected renumbered #1/#2, got:\n%s", table)
	}
	if strings.Contains(table, "#-") {
		t.Errorf("table leaked a negative sentinel seq:\n%s", table)
	}

	eJSON, outJSON, _ := newTestEnv(t, srv, "json")
	if err := run(t, eJSON, "run", "events", "sentinel"); err != nil {
		t.Fatalf("run events -o json: %v", err)
	}
	if !strings.Contains(outJSON.String(), `"seq":-1000000`) {
		t.Errorf("NDJSON must keep the raw seq, got:\n%s", outJSON.String())
	}
}

// TestSpamListTable pins `rc project senders ls`: both lists rendered as one VERDICT/PATTERN/TYPE/SOURCE/CREATED
// table (allows first, then blocks), in server order.
func TestSpamListTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "senders", "ls"); err != nil {
		t.Fatalf("project senders ls: %v", err)
	}
	assertGolden(t, "spam_ls.golden", out.String())
}

// TestSpamListJSON confirms -o json carries both raw list bodies through verbatim under an
// {allows,blocks} envelope — the CLI reshapes nothing inside each.
func TestSpamListJSON(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "project", "senders", "ls"); err != nil {
		t.Fatalf("project senders ls -o json: %v", err)
	}
	var got struct {
		Allows []client.SpamRule `json:"allows"`
		Blocks []client.SpamRule `json:"blocks"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode project senders ls json: %v\n%s", err, out.String())
	}
	if len(got.Allows) != 2 || len(got.Blocks) != 2 {
		t.Fatalf("want 2 allows + 2 blocks, got %d + %d", len(got.Allows), len(got.Blocks))
	}
	if got.Allows[0].Pattern != "@acme.com" || got.Blocks[0].Verdict != "block" {
		t.Errorf("raw list bodies not carried through: %+v / %+v", got.Allows[0], got.Blocks[0])
	}
}

// TestSpamAllowTable pins `rc project senders allow <pattern> --reason …`: the echoed rule with the
// server-inferred match_type renders as a one-row table.
func TestSpamAllowTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "senders", "allow", "@partner.example", "--reason", "trusted"); err != nil {
		t.Fatalf("project senders allow: %v", err)
	}
	assertGolden(t, "spam_allow.golden", out.String())
}

// TestSpamAllowMailboxTable pins `rc project senders allow <pattern> --mailbox <uuid>`: the mailbox_id rides in the
// POST body (asserted server-side by echoing it back as "mailbox"), and the echoed rule renders with the
// MAILBOX column populated.
func TestSpamAllowMailboxTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "senders", "allow", "@partner.example", "--mailbox", "mbx11111-0000-0000-0000-000000000009"); err != nil {
		t.Fatalf("project senders allow --mailbox: %v", err)
	}
	assertGolden(t, "spam_allow_mailbox.golden", out.String())
}

// TestSpamListMailboxFilter pins `rc project senders ls --mailbox <uuid>`: the client-side filter narrows the table
// to the two rules (one allow, one block) scoped to that mailbox, dropping the project-scoped rows.
func TestSpamListMailboxFilter(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "senders", "ls", "--mailbox", "mbx11111-0000-0000-0000-000000000001"); err != nil {
		t.Fatalf("project senders ls --mailbox: %v", err)
	}
	assertGolden(t, "spam_ls_mailbox.golden", out.String())
}

// TestSpamBlockTable pins `rc project senders block <pattern>` (no reason).
func TestSpamBlockTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "senders", "block", "junk@spammy.example"); err != nil {
		t.Fatalf("project senders block: %v", err)
	}
	assertGolden(t, "spam_block.golden", out.String())
}

// TestSpamRmTryBoth locks the `rm <id>` UX: an id absent from the block list (404) falls through to the
// allow list, which deletes it — a clean "deleted" with no verdict flag from the caller.
func TestSpamRmTryBoth(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "senders", "rm", "allow-only"); err != nil {
		t.Fatalf("project senders rm: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "deleted spam rule allow-only") {
		t.Errorf("want deleted confirmation, got %q", got)
	}
}

// TestErrorIsTyped confirms the client returns a typed *APIError carrying code+message+fields, the
// load-bearing contract for verbatim surfacing.
func TestErrorIsTyped(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "run", "show", "bad") // → 404 UNKNOWN_RUN
	var apiErr *client.APIError
	if !asAPIError(err, &apiErr) {
		t.Fatalf("expected *client.APIError, got %T: %v", err, err)
	}
	if apiErr.Code != "UNKNOWN_RUN" {
		t.Errorf("code = %q, want UNKNOWN_RUN", apiErr.Code)
	}
}
