package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootcause-org/rootcause-cli/internal/token"
)

// TestProjectScopeRidesAsQueryParam pins the core of the --project rework: the flag is a SERVER-SIDE
// scope, threaded onto each read request as ?project=<id-or-name> with the SAME (default) token — not a
// profile/token selector. It drives the canonical fleet, run, status, health, and thread commands
// against a stub that records the project query param per endpoint.
func TestProjectScopeRidesAsQueryParam(t *testing.T) {
	got := map[string]string{} // endpoint label → observed ?project=
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"projects":[{"id":"11111111-1111-1111-1111-111111111111","name":"momentum-tools"}]}`))
	})
	mux.HandleFunc("GET /api/v1/runs", func(w http.ResponseWriter, r *http.Request) {
		got["runs"] = r.URL.Query().Get("project")
		_, _ = w.Write([]byte(`{"runs":[],"summary":{}}`))
	})
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		got["health"] = r.URL.Query().Get("project")
		_, _ = w.Write([]byte(`{"rows":[]}`))
	})
	mux.HandleFunc("GET /api/v1/threads/{id}/trace", func(w http.ResponseWriter, r *http.Request) {
		got["thread"] = r.URL.Query().Get("project")
		_, _ = w.Write([]byte(`{"id":"t1","resolved_by":"none","runs":[]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	const scope = "momentum-tools"
	cases := []struct {
		label string
		args  []string
	}{
		{"runs", []string{"fleet", "runs", "--project", scope}},
		{"runs", []string{"run", "list", "--project", scope}},
		{"runs", []string{"status", "--project", scope}},
		{"health", []string{"fleet", "health", "--project", scope}},
		{"thread", []string{"run", "thread", "t1", "--project", scope}},
	}
	for _, tc := range cases {
		e, _, _ := newTestEnv(t, srv, "json")
		if err := run(t, e, tc.args...); err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if got[tc.label] != scope {
			t.Errorf("%v: server saw project=%q, want %q", tc.args, got[tc.label], scope)
		}
	}
}

// TestNoProjectScopeOmitsQueryParam: without --project the read request carries no project param (a
// pinned token reads its own project; the server would disregard the param anyway).
func TestNoProjectScopeOmitsQueryParam(t *testing.T) {
	var sawProject string
	hit := false
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/runs", func(w http.ResponseWriter, r *http.Request) {
		hit = true
		sawProject = r.URL.Query().Get("project")
		_, _ = w.Write([]byte(`{"runs":[],"summary":{}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	e, _, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "fleet", "runs"); err != nil {
		t.Fatalf("fleet: %v", err)
	}
	if !hit {
		t.Fatal("fleet never hit /api/v1/runs")
	}
	if sawProject != "" {
		t.Errorf("no --project, but server saw project=%q", sawProject)
	}
}

func TestFleetDaysRidesAsQueryParam(t *testing.T) {
	var sawDays []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/runs", func(w http.ResponseWriter, r *http.Request) {
		sawDays = append(sawDays, r.URL.Query().Get("days"))
		_, _ = w.Write([]byte(`{"runs":[],"summary":{}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	e, _, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "fleet", "runs", "--days", "3"); err != nil {
		t.Fatalf("fleet --days: %v", err)
	}
	if len(sawDays) != 1 || sawDays[0] != "3" {
		t.Fatalf("server saw days=%v, want [3]", sawDays)
	}
}

func TestRunLearningFiltersRideAsQueryParams(t *testing.T) {
	var queries []map[string]string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/runs", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		queries = append(queries, map[string]string{
			"outcome":  q.Get("outcome"),
			"learning": q.Get("learning"),
			"days":     q.Get("days"),
		})
		_, _ = w.Write([]byte(`{"runs":[],"summary":{}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	e, _, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "run", "list", "--outcome", "failed", "--learning=feedback"); err != nil {
		t.Fatalf("run list learning filters: %v", err)
	}
	e, _, _ = newTestEnv(t, srv, "json")
	if err := run(t, e, "fleet", "runs", "--learning"); err != nil {
		t.Fatalf("fleet runs bare --learning: %v", err)
	}

	if len(queries) != 2 {
		t.Fatalf("run filter requests = %d, want 2", len(queries))
	}
	if got := queries[0]; got["outcome"] != "failed" || got["learning"] != "feedback" || got["days"] != "" {
		t.Fatalf("run list query = %#v", got)
	}
	if got := queries[1]; got["outcome"] != "" || got["learning"] != "any" || got["days"] != "7" {
		t.Fatalf("fleet runs query = %#v", got)
	}
}

func TestFleetLearningFilterLabelsDigestPopulation(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/runs", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("learning"); got != "any" {
			t.Fatalf("fleet learning query = %q, want any", got)
		}
		_, _ = w.Write([]byte(`{"runs":[],"summary":{}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "fleet", "runs", "--learning"); err != nil {
		t.Fatalf("fleet runs bare --learning: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "learning=any") {
		t.Fatalf("filtered fleet digest did not label population:\n%s", got)
	}
}

func TestRunLearningFiltersRejectUnknownValues(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "outcome", args: []string{"run", "list", "--outcome", "ok"}, want: `invalid --outcome "ok"`},
		{name: "run learning", args: []string{"run", "list", "--learning=journal"}, want: `invalid --learning "journal"`},
		{name: "fleet learning", args: []string{"fleet", "runs", "--learning=journal"}, want: `invalid --learning "journal"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _, _ := newTestEnv(t, srv, "json")
			err := run(t, e, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestBrainDefaultProfileFallbackAddsProjectScope(t *testing.T) {
	isolatedConfig(t)
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, ".rootcause.toml"),
		[]byte("project = \"pro-backup\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var sawProject string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		_, _ = w.Write([]byte(`{"projects":[{"id":"11111111-1111-1111-1111-111111111111","name":"pro-backup"}]}`))
	})
	mux.HandleFunc("GET /api/v1/runs", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		sawProject = r.URL.Query().Get("project")
		_, _ = w.Write([]byte(`{"runs":[],"summary":{}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	t.Setenv("ROOTCAUSE_BASE_URL", srv.URL)
	seedToken(t, "default", token.Token{
		AccessToken: "test-key", RefreshToken: "rcor_x",
		ExpiresAt: time.Now().Add(time.Hour), BaseURL: srv.URL,
	})

	var out, errb bytes.Buffer
	e := &env{output: "json", out: &out, err: &errb}
	if err := run(t, e, "status"); err != nil {
		t.Fatalf("status: %v", err)
	}
	if sawProject != "pro-backup" {
		t.Errorf("server saw project=%q, want pro-backup", sawProject)
	}
}

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
	if err := run(t, e, "fleet", "runs", "--kind", "fleet"); err != nil {
		t.Fatalf("fleet: %v", err)
	}
	assertGolden(t, "fleet.golden", out.String())
}

// TestFleetByModel pins the model×cost×fallback breakdown — per answered model: runs, total/avg cost,
// and the fallback count (the opus run is a fallback from sonnet in the fixtures). It's the highest-value
// view: which model burned the spend, and how much was a fallback.
func TestFleetByModel(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "fleet", "runs", "--kind", "fleet", "--by-model"); err != nil {
		t.Fatalf("fleet --by-model: %v", err)
	}
	assertGolden(t, "fleet_by_model.golden", out.String())
}

// TestFleetTimeline pins the per-day runs/errors/cost histogram (the "what changed today" anchor).
func TestFleetTimeline(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "fleet", "runs", "--kind", "fleet", "--timeline"); err != nil {
		t.Fatalf("fleet --timeline: %v", err)
	}
	assertGolden(t, "fleet_timeline.golden", out.String())
}

// TestFleetAgentTable pins the token-lean agent index (full ids + ranked "look here first" + all runs).
func TestFleetAgentTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "fleet", "runs", "--kind", "fleet", "--format", "agent"); err != nil {
		t.Fatalf("fleet --format agent: %v", err)
	}
	assertGolden(t, "fleet_agent.golden", out.String())
}

// TestFleetBadFormat: an unknown --format is a clear client-side error (fleet runs + patterns).
func TestFleetBadFormat(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	for _, args := range [][]string{
		{"fleet", "runs", "--format", "bogus"},
		{"fleet", "patterns", "--format", "bogus"},
	} {
		e, _, _ := newTestEnv(t, srv, "table")
		if err := run(t, e, args...); err == nil {
			t.Fatalf("%v: expected an error for --format bogus", args)
		}
	}
}

// TestFleetAgentDigestRendersWhenPiped pins the fleet-review fix: an EXPLICIT --format agent must emit
// the computed digest (shortlist + aggregate + by-model + timeline + offenders, full UUIDs) even when
// stdout is a pipe — auto mode used to fall through to the raw JSON passthrough and dump every row.
// The env's "" output mode leaves -o unset; a test buffer is a non-TTY, i.e. exactly a pipe.
func TestFleetAgentDigestRendersWhenPiped(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "")
	if err := run(t, e, "fleet", "runs", "--kind", "fleet", "--format", "agent"); err != nil {
		t.Fatalf("piped fleet --format agent: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"look here first:",
		"Aggregate:",
		"By model (cost · fallbacks):",
		"Daily timeline:",
		"Worst offenders (full ids",
		"aaaaaaaa-0000-0000-0000-000000000001", // full UUID, one paste from rc run debug
	} {
		if !strings.Contains(got, want) {
			t.Errorf("piped agent digest missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `"run_id"`) {
		t.Errorf("piped agent digest leaked raw JSON rows:\n%s", got)
	}
}

// TestFleetAllAgentDigestRendersWhenPiped: the --all fan-out honors an explicit --format over a pipe
// too — per-project sections plus the fleet total with the per-project rollup table.
func TestFleetAllAgentDigestRendersWhenPiped(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "")
	if err := run(t, e, "fleet", "runs", "--all", "--kind", "fleet", "--format", "agent"); err != nil {
		t.Fatalf("piped fleet --all --format agent: %v", err)
	}
	got := out.String()
	for _, want := range []string{"════ FLEET TOTAL ════", "PROJECT", "BASH_ERR", "look here first:"} {
		if !strings.Contains(got, want) {
			t.Errorf("piped --all agent digest missing %q:\n%s", want, got)
		}
	}
}

// TestFleetAutoPipeWithoutFormatStaysJSON: without an explicit --format, auto mode on a pipe keeps the
// raw-rows JSON default — the load-bearing `| jq` contract must not regress.
func TestFleetAutoPipeWithoutFormatStaysJSON(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "")
	if err := run(t, e, "fleet", "runs", "--kind", "fleet"); err != nil {
		t.Fatalf("piped fleet (auto): %v", err)
	}
	var got struct {
		Runs []map[string]any `json:"runs"`
	}
	decodeJSON(t, out.Bytes(), &got)
	if len(got.Runs) != 4 {
		t.Fatalf("auto-piped fleet json runs = %d, want 4; body=%s", len(got.Runs), out.String())
	}
}

// TestFleetExplicitJSONWinsOverFormat: -o json is the explicit raw spill and takes precedence over
// --format agent.
func TestFleetExplicitJSONWinsOverFormat(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "fleet", "runs", "--kind", "fleet", "--format", "agent"); err != nil {
		t.Fatalf("fleet -o json --format agent: %v", err)
	}
	var got struct {
		Runs []map[string]any `json:"runs"`
	}
	decodeJSON(t, out.Bytes(), &got)
	if len(got.Runs) != 4 {
		t.Fatalf("-o json --format agent runs = %d, want 4 raw rows; body=%s", len(got.Runs), out.String())
	}
}

// TestPatternsAgentRendersWhenPiped: `rc fleet patterns --format agent` emits the clustered view over a
// pipe (it used to dump the raw high-volume feeds — the "table only" caveat this fix removes).
func TestPatternsAgentRendersWhenPiped(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "")
	if err := run(t, e, "fleet", "patterns", "--format", "agent"); err != nil {
		t.Fatalf("piped patterns --format agent: %v", err)
	}
	got := out.String()
	for _, want := range []string{"# Run patterns", "## Bash failure clusters", "suggested fix:"} {
		if !strings.Contains(got, want) {
			t.Errorf("piped patterns agent view missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `"events"`) {
		t.Errorf("piped patterns agent view leaked raw JSON feeds:\n%s", got)
	}
}

// TestPatternsTable pins the run_patterns port: the bash-failure clusters (twin orders_2024/2025 collapse
// to one signature across 2 runs via masking) + the blocked-egress host cluster, each with a suggested-fix
// stub.
func TestPatternsTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "fleet", "patterns"); err != nil {
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
	err := run(t, e, "fleet", "health")
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
	if err := run(t, e, "fleet", "health", "--hours", "999"); err != nil {
		t.Fatalf("clean health should exit zero, got %v", err)
	}
	assertGolden(t, "health_clean.golden", out.String())
}

// --- JSON passthrough: -o json must round-trip the server rows (paged ones reassembled), no rendering ---

func TestFleetJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "fleet", "runs", "--kind", "fleet"); err != nil {
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
	learning, ok := got.Runs[0]["learning"].(map[string]any)
	if !ok || learning["feedback"] != true || learning["triage_corrected"] != true {
		t.Errorf("json learning fields lost — got %+v", got.Runs[0]["learning"])
	}
}

func TestPatternsJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "fleet", "patterns"); err != nil {
		t.Fatalf("fleet patterns -o json: %v", err)
	}
	var got struct {
		Events []map[string]any `json:"events"`
		Egress []map[string]any `json:"egress"`
		HTTP   []map[string]any `json:"http"`
	}
	decodeJSON(t, out.Bytes(), &got)
	// All 4 events ride through verbatim (the ok `ls /brain` too — passthrough does NOT filter; clustering
	// is a render-only concern).
	if len(got.Events) != 4 || len(got.Egress) != 2 || len(got.HTTP) != 2 {
		t.Fatalf("patterns json = %d events / %d egress / %d HTTP, want 4/2/2; body=%s", len(got.Events), len(got.Egress), len(got.HTTP), out.String())
	}
}

func TestEgressInspectionCommands(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "run egress", args: []string{"run", "egress", "run-1"}, want: "HTTP attempts"},
		{name: "run actions", args: []string{"run", "actions", "run-1"}, want: "create_order"},
		{name: "project egress", args: []string{"project", "egress"}, want: "Unattributed gateway connections"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, out, _ := newTestEnv(t, srv, "table")
			if err := run(t, e, tc.args...); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Fatalf("output missing %q:\n%s", tc.want, out.String())
			}
		})
	}
}

func TestHealthJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	// An unhealthy fleet still exits non-zero in JSON mode, but the body is the verbatim server rows.
	if err := run(t, e, "fleet", "health"); !errors.Is(err, errUnhealthy) {
		t.Fatalf("fleet health -o json on unhealthy fleet: err = %v, want errUnhealthy", err)
	}
	assertJSONEqual(t, fixture(t, "health.json"), out.Bytes())
}

func TestFleetPatternsHealthAllLargeJSONSpills(t *testing.T) {
	t.Setenv("RC_OUTPUT_INLINE_MAX", "200")
	srv := stubServer(t)
	defer srv.Close()

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "fleet", args: []string{"fleet", "runs", "--all", "--kind", "fleet"}, want: `"total_runs"`},
		{name: "patterns", args: []string{"fleet", "patterns", "--all"}, want: `"egress"`},
		{name: "health", args: []string{"fleet", "health", "--all", "--hours", "999"}, want: `"health"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outDir := t.TempDir()
			e, out, _ := newTestEnv(t, srv, "json")
			args := append([]string{"--out-dir", outDir}, tc.args...)
			if err := run(t, e, args...); err != nil {
				t.Fatalf("%s spill: %v", tc.name, err)
			}
			m := requireSpillManifest(t, out.Bytes())
			art := m.Artifacts["response"]
			if art.Path == "" {
				t.Fatalf("%s manifest missing response artifact: %s", tc.name, out.String())
			}
			b, err := os.ReadFile(art.Path)
			if err != nil {
				t.Fatalf("read %s spill: %v", tc.name, err)
			}
			if !bytes.Contains(b, []byte(tc.want)) {
				t.Fatalf("%s spill missing %s:\n%s", tc.name, tc.want, b)
			}

			rawDir := t.TempDir()
			eRaw, rawOut, _ := newTestEnv(t, srv, "json")
			rawArgs := append([]string{"--out-dir", rawDir, "--raw-output"}, tc.args...)
			if err := run(t, eRaw, rawArgs...); err != nil {
				t.Fatalf("%s --raw-output: %v", tc.name, err)
			}
			if strings.Contains(rawOut.String(), `"spilled": true`) || !strings.Contains(rawOut.String(), tc.want) {
				t.Fatalf("%s raw output not preserved:\n%s", tc.name, rawOut.String())
			}
			if entries, err := os.ReadDir(rawDir); err != nil {
				t.Fatalf("read %s raw dir: %v", tc.name, err)
			} else if len(entries) != 0 {
				t.Fatalf("%s --raw-output wrote artifacts: %v", tc.name, entries)
			}
		})
	}
}
