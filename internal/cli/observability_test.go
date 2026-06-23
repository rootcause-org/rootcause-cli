package cli

import (
	"encoding/json"
	"errors"
	"testing"
)

// decodeJSON unmarshals body into v, failing the test on a decode error.
func decodeJSON(t *testing.T, body []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("decode json: %v\nbody: %s", err, body)
	}
}

// The observability commands (fleet / patterns / health) — golden tests for the human render + a
// JSON-passthrough test per command + the health non-zero-exit contract. The stub server pages the
// runs index + events feed so the paging loop is exercised end to end.

// TestFleetTable pins the runs_digest port: the per-run flag line (incl. the client-computed $! cost
// spike), the aggregate, and the worst-offender shortlists. The --kind fleet param routes the stub to
// the operator-tier (health-bearing) paged fixtures.
func TestFleetTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "fleet", "--kind", "fleet"); err != nil {
		t.Fatalf("fleet: %v", err)
	}
	assertGolden(t, "fleet.golden", out.String())
}

// TestFleetAgentTable pins the token-lean agent index (full ids + ranked "look here first" + all runs).
func TestFleetAgentTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "fleet", "--kind", "fleet", "--format", "agent"); err != nil {
		t.Fatalf("fleet --format agent: %v", err)
	}
	assertGolden(t, "fleet_agent.golden", out.String())
}

// TestFleetBadFormat: an unknown --format is a clear client-side error.
func TestFleetBadFormat(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "fleet", "--format", "bogus"); err == nil {
		t.Fatal("expected an error for --format bogus")
	}
}

// TestPatternsTable pins the run_patterns port: the bash-failure clusters (twin orders_2024/2025 collapse
// to one signature across 2 runs via masking) + the blocked-egress host cluster, each with a suggested-fix
// stub.
func TestPatternsTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "patterns"); err != nil {
		t.Fatalf("patterns: %v", err)
	}
	assertGolden(t, "patterns.golden", out.String())
}

// TestHealthTable pins the health roll-up (stale/failing mirror + dead-lettered run → UNHEALTHY) AND the
// non-zero exit contract: an unhealthy fleet returns an error so CI/cron sees a failure.
func TestHealthTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "health")
	if err == nil {
		t.Fatal("expected a non-zero exit (error) for an unhealthy fleet")
	}
	assertGolden(t, "health.golden", out.String())
}

// TestHealthCleanExitsZero: a clean fleet renders healthy AND returns nil (zero exit).
func TestHealthCleanExitsZero(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "health", "--hours", "999"); err != nil {
		t.Fatalf("clean health should exit zero, got %v", err)
	}
	assertGolden(t, "health_clean.golden", out.String())
}

// --- JSON passthrough: -o json must round-trip the server rows (paged ones reassembled), no rendering ---

func TestFleetJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "fleet", "--kind", "fleet"); err != nil {
		t.Fatalf("fleet -o json: %v", err)
	}
	// The accumulated runs across both pages, under {runs:[…]}.
	var got struct {
		Runs []map[string]any `json:"runs"`
	}
	decodeJSON(t, out.Bytes(), &got)
	if len(got.Runs) != 4 {
		t.Fatalf("fleet json runs = %d, want 4 (both pages); body=%s", len(got.Runs), out.String())
	}
	// No client-side digest leaked into JSON mode: the rows are the wire struct (run_id + health present).
	if got.Runs[0]["run_id"] == nil || got.Runs[0]["health"] == nil {
		t.Errorf("json rows reshaped — want verbatim wire rows, got %+v", got.Runs[0])
	}
}

func TestPatternsJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "patterns"); err != nil {
		t.Fatalf("patterns -o json: %v", err)
	}
	var got struct {
		Events []map[string]any `json:"events"`
		Egress []map[string]any `json:"egress"`
	}
	decodeJSON(t, out.Bytes(), &got)
	// All 4 events ride through verbatim (the ok `ls /brain` too — passthrough does NOT filter; clustering
	// is a render-only concern).
	if len(got.Events) != 4 || len(got.Egress) != 2 {
		t.Fatalf("patterns json = %d events / %d egress, want 4/2; body=%s", len(got.Events), len(got.Egress), out.String())
	}
}

func TestHealthJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	// An unhealthy fleet still exits non-zero in JSON mode, but the body is the verbatim server rows.
	if err := run(t, e, "health"); !errors.Is(err, errUnhealthy) {
		t.Fatalf("health -o json on unhealthy fleet: err = %v, want errUnhealthy", err)
	}
	assertJSONEqual(t, fixture(t, "health.json"), out.Bytes())
}
