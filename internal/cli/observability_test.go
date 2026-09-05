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
	mux.HandleFunc("GET /api/v1/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		got["thread-run"] = r.URL.Query().Get("project")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"UNKNOWN_RUN","message":"unknown run"}}`))
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
	if got["thread-run"] != scope {
		t.Errorf("run thread resolver: server saw project=%q, want %q", got["thread-run"], scope)
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
			"reviewed": q.Get("reviewed"),
			"days":     q.Get("days"),
		})
		_, _ = w.Write([]byte(`{"runs":[],"summary":{}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	e, _, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "run", "list", "--outcome", "failed", "--learning=feedback", "--reviewed"); err != nil {
		t.Fatalf("run list learning filters: %v", err)
	}
	e, _, _ = newTestEnv(t, srv, "json")
	if err := run(t, e, "fleet", "runs", "--learning"); err != nil {
		t.Fatalf("fleet runs bare --learning: %v", err)
	}

	if len(queries) != 2 {
		t.Fatalf("run filter requests = %d, want 2", len(queries))
	}
	if got := queries[0]; got["outcome"] != "failed" || got["learning"] != "feedback" || got["reviewed"] != "true" || got["days"] != "" {
		t.Fatalf("run list query = %#v", got)
	}
	if got := queries[1]; got["outcome"] != "" || got["learning"] != "any" || got["reviewed"] != "" || got["days"] != "7" {
		t.Fatalf("fleet runs query = %#v", got)
	}
}

func TestRunSessionAndFleetLimitRideAsQueryParams(t *testing.T) {
	var queries []map[string]string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/runs", func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, map[string]string{"session": r.URL.Query().Get("session"), "limit": r.URL.Query().Get("limit")})
		_, _ = w.Write([]byte(`{"runs":[],"summary":{}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	e, _, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "run", "list", "--session", "session-42"); err != nil {
		t.Fatal(err)
	}
	e, _, _ = newTestEnv(t, srv, "json")
	if err := run(t, e, "fleet", "runs", "--limit", "7"); err != nil {
		t.Fatal(err)
	}
	if len(queries) != 2 || queries[0]["session"] != "session-42" || queries[1]["limit"] != "7" {
		t.Fatalf("queries = %#v", queries)
	}
}

func TestFleetReviewedFilterCarriesReviewAndLabelsDigest(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/runs", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("reviewed"); got != "true" {
			t.Fatalf("fleet reviewed query = %q, want true", got)
		}
		_, _ = w.Write([]byte(`{"runs":[{"run_id":"11111111-1111-1111-1111-111111111111","kind":"email","source":"Email","status":"done","outcome":"answered","category":"ok","created_at":"2026-08-07T10:00:00Z","has_draft":true,"has_note":true,"learning":{"feedback":false},"review":{"score":1,"comment":"wrong fact"}}],"summary":{}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "fleet", "runs", "--reviewed", "--format", "agent"); err != nil {
		t.Fatalf("fleet runs --reviewed: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "reviewed") || !strings.Contains(got, "REV:1") {
		t.Fatalf("reviewed fleet digest missing scope/score:\n%s", got)
	}

	e, out, _ = newTestEnv(t, srv, "json")
	if err := run(t, e, "fleet", "runs", "--reviewed", "--raw-output"); err != nil {
		t.Fatalf("fleet runs --reviewed JSON: %v", err)
	}
	var raw struct {
		Runs []map[string]any `json:"runs"`
	}
	decodeJSON(t, out.Bytes(), &raw)
	review, ok := raw.Runs[0]["review"].(map[string]any)
	if !ok || review["score"] != float64(1) || review["comment"] != "wrong fact" {
		t.Fatalf("reviewed fleet JSON lost raw score/comment: %+v", raw.Runs)
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

// modelIdentityFixtureStrings are the model names the testdata fixtures still carry — deliberately, as
// a stand-in for a stale or rogue server that has not stopped emitting them. The typed DTOs no longer
// decode those keys and the metadata passthrough filters them, so NO rendered surface may echo one.
var modelIdentityFixtureStrings = []string{
	"anthropic/claude-opus-4", "anthropic/claude-sonnet-4",
	"claude-opus-4-8", "claude-opus-5", "google/gemini-3.5-flash",
}

// TestNoSurfaceRendersModelIdentity pins the invariant that replaced the old --by-model breakdown:
// the SERVING MODEL identity is host-only telemetry, exactly like the provider slug — naming the rung
// that answered is as cost-reverse-engineerable as naming who served it. The server projects none of it
// on ANY tier (operator included), so every CLI surface that composes output — the fleet digests, the
// run/trace/events tables, and the `run debug` markdown + JSONL — must be model-free. Only the content-
// free run_health.is_fallback boolean (THAT a swap happened, never between which models) survives; the
// FB flag below asserts it still renders. Raw `-o json` passthroughs are excluded on purpose — they
// re-emit the server's bytes verbatim (`run show -o json`, and `run trace -o json`, which only injects
// a `type` key per line); `run debug` is the composed dump and IS covered.
func TestNoSurfaceRendersModelIdentity(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()

	for _, tc := range []struct {
		name string
		mode string
		args []string
	}{
		{"fleet human", "table", []string{"fleet", "runs", "--kind", "fleet"}},
		{"fleet agent", "table", []string{"fleet", "runs", "--kind", "fleet", "--format", "agent"}},
		{"fleet timeline", "table", []string{"fleet", "runs", "--kind", "fleet", "--timeline"}},
		{"run show", "table", []string{"run", "show", "declined"}},
		{"run trace", "table", []string{"run", "trace", "11111111-1111-1111-1111-111111111111"}},
		{"run trace declined", "table", []string{"run", "trace", "declined"}},
		{"run events", "table", []string{"run", "events", "declined"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, out, _ := newTestEnv(t, srv, tc.mode)
			if err := run(t, e, tc.args...); err != nil {
				t.Fatalf("%v: %v", tc.args, err)
			}
			assertNoModelIdentity(t, out.String())
		})
	}

	// `rc run debug` writes its two files instead of printing them, so it is driven separately.
	t.Run("run debug files", func(t *testing.T) {
		outDir := t.TempDir()
		e, _, _ := newTestEnv(t, srv, "table")
		if err := run(t, e, "run", "debug", "11111111-1111-1111-1111-111111111111", "--out-dir", outDir); err != nil {
			t.Fatalf("run debug: %v", err)
		}
		for _, name := range []string{"11111111-coca-cola.md", "11111111-coca-cola.jsonl"} {
			b, err := os.ReadFile(filepath.Join(outDir, name))
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			assertNoModelIdentity(t, string(b))
		}
	})

	// The other half of the invariant: is_fallback still reaches the digest as the FB flag, so an
	// operator can see THAT a rung swap happened without learning between which models.
	t.Run("is_fallback survives", func(t *testing.T) {
		e, out, _ := newTestEnv(t, srv, "table")
		if err := run(t, e, "fleet", "runs", "--kind", "fleet"); err != nil {
			t.Fatalf("fleet: %v", err)
		}
		for _, want := range []string{"FB", "Fallback runs ("} {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("fleet digest lost the content-free fallback signal %q:\n%s", want, out.String())
			}
		}
	})
}

func assertNoModelIdentity(t *testing.T, got string) {
	t.Helper()
	for _, m := range modelIdentityFixtureStrings {
		if strings.Contains(got, m) {
			t.Fatalf("surface leaked the serving model identity %q:\n%s", m, got)
		}
	}
	// The label is barred too: a "Model:" row with a blank value would still advertise the concept.
	for _, label := range []string{"Model:", "MODEL\t", "By model"} {
		if strings.Contains(got, label) {
			t.Fatalf("surface still renders a model line (%q):\n%s", label, got)
		}
	}
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
	got := out.String()
	shortlist := strings.SplitN(got, "\nAggregate:", 2)[0]
	for _, want := range []string{
		"aaaaaaaa-0000-0000-0000-000000000005  LRN:sent_delta/missed_content",
		"LRN:sent_delta/divergent_facts",
		"LRN:sent_delta/unjudged",
		"LRN:sent_delta/equivalent",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("shadow fleet digest missing %q:\n%s", want, got)
		}
	}
	for _, positive := range []string{"aaaaaaaa-0000-0000-0000-000000000002", "aaaaaaaa-0000-0000-0000-000000000004"} {
		if strings.Contains(shortlist, positive) {
			t.Errorf("positive/unjudged shadow row %s entered look-here-first shortlist:\n%s", positive, shortlist)
		}
	}
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
// the computed digest (shortlist + aggregate + timeline + offenders, full UUIDs) even when
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
	if len(got.Runs) != 5 {
		t.Fatalf("auto-piped fleet json runs = %d, want 5; body=%s", len(got.Runs), out.String())
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
	if len(got.Runs) != 5 {
		t.Fatalf("-o json --format agent runs = %d, want 5 raw rows; body=%s", len(got.Runs), out.String())
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

// TestHealthAllProjectsTokenFallsBackToFanOut: an all-projects token with no --project gets
// NO_PROJECT_SCOPE from the flat route; the CLI falls back to the --all fan-out (with a stderr note)
// instead of surfacing the server error, and the unhealthy verdict still drives the non-zero exit.
func TestHealthAllProjectsTokenFallsBackToFanOut(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, errOut := newTestEnv(t, srv, "table")
	if err := run(t, e, "fleet", "health", "--hours", "888"); !errors.Is(err, errUnhealthy) {
		t.Fatalf("fallback fan-out on unhealthy fleet: err = %v, want errUnhealthy", err)
	}
	if !strings.Contains(errOut.String(), "as --all") {
		t.Errorf("missing fan-out note on stderr:\n%s", errOut.String())
	}
	got := out.String()
	for _, want := range []string{"════ alpha ════", "════ bravo ════", "════ FLEET ════"} {
		if !strings.Contains(got, want) {
			t.Errorf("fallback fan-out output missing %q:\n%s", want, got)
		}
	}
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
	if len(got.Runs) != 5 {
		t.Fatalf("fleet json runs = %d, want 5 (both pages); body=%s", len(got.Runs), out.String())
	}
	// No client-side digest leaked into JSON mode: the rows are the wire struct (run_id + health present).
	if got.Runs[0]["run_id"] == nil || got.Runs[0]["health"] == nil {
		t.Errorf("json rows reshaped — want verbatim wire rows, got %+v", got.Runs[0])
	}
	if got.Runs[0]["thread_id"] != "thread-fleet-1" || got.Runs[0]["session_id"] != "session-fleet-1" {
		t.Errorf("json run identity lost — got %+v", got.Runs[0])
	}
	learning, ok := got.Runs[0]["learning"].(map[string]any)
	if !ok || learning["feedback"] != true || learning["triage_corrected"] != true {
		t.Errorf("json learning fields lost — got %+v", got.Runs[0]["learning"])
	}
	shadowLearning, ok := got.Runs[2]["learning"].(map[string]any)
	if !ok || shadowLearning["sent_delta_shadow"] != true || shadowLearning["sent_delta_verdict"] != "divergent_facts" {
		t.Errorf("json shadow learning fields lost — got %+v", got.Runs[2]["learning"])
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
		// The whitelisted class column tells an infra failure from a domain one on the customer-safe endpoint.
		{name: "run actions error class", args: []string{"run", "actions", "run-1"}, want: "executor_predispatch"},
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

// TestRunEgressActionsJSONKeepsUnknownFields pins the F4 contract on the two per-run inspection views:
// `-o json` must carry the server's body, not a re-marshal of the CLI's closed structs. The stub adds a
// top-level key the CLI does not model (`future_summary`) and a row-level one (`future_row_field`); both
// have to reach jq.
func TestRunEgressActionsJSONKeepsUnknownFields(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	for _, tc := range []struct{ view, want string }{
		{"egress", "future_summary"},
		{"actions", "future_row_field"},
	} {
		t.Run(tc.view, func(t *testing.T) {
			e, out, _ := newTestEnv(t, srv, "json")
			if err := run(t, e, "run", tc.view, "run-1"); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Fatalf("run %s -o json dropped %q:\n%s", tc.view, tc.want, out.String())
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
