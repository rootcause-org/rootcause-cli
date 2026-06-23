package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// update regenerates the golden files instead of comparing. Run: go test ./internal/cli -update
var update = flag.Bool("update", false, "update golden files")

// stubServer returns canned JSON per endpoint so the renderers and JSON passthrough can be pinned by
// golden tests without a live API. Each handler asserts the bearer header is present (auth wiring) and
// echoes a fixed fixture; the settings PATCH path returns an INVALID_SETTINGS envelope when asked to,
// driving the error-path test.
func stubServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/runs", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		// `rc fleet` pages this: with a cursor set, return the operator-tier (health-bearing) page that
		// ENDS the window (no next_before) so the paging loop terminates. Without a cursor it's the
		// existing single-page fixture (status/runs view).
		if r.URL.Query().Get("before") != "" {
			_, _ = w.Write(fixture(t, "fleet_runs_p2.json"))
			return
		}
		if r.URL.Query().Get("kind") == "fleet" { // `rc fleet` test path: drive the operator-tier digest fixtures
			_, _ = w.Write(fixture(t, "fleet_runs_p1.json"))
			return
		}
		_, _ = w.Write(fixture(t, "runs.json"))
	})

	// Observability feeds (rc fleet / patterns / health). The events feed is paged: page 1 carries a
	// next_before, page 2 (before set) is the last page — exercising the CLI's paging loop + accumulation.
	mux.HandleFunc("GET /api/v1/runs/events", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("before") == "" {
			_, _ = w.Write(fixture(t, "events_feed_p1.json"))
			return
		}
		_, _ = w.Write(fixture(t, "events_feed_p2.json"))
	})
	mux.HandleFunc("GET /api/v1/runs/egress", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "egress_feed.json"))
	})
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		// hours=999 simulates a clean (healthy) fleet; the default returns the unhealthy fixture.
		if r.URL.Query().Get("hours") == "999" {
			_, _ = w.Write(fixture(t, "health_clean.json"))
			return
		}
		_, _ = w.Write(fixture(t, "health.json"))
	})

	mux.HandleFunc("GET /api/v1/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		if r.PathValue("id") == "bad" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"UNKNOWN_RUN","message":"unknown run"}}`))
			return
		}
		// id "running" never reaches a terminal status — drives the `rc ask` timeout path.
		if r.PathValue("id") == "running" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"run_id":"running","status":"running","kind":"prompt","created_at":"2026-06-19T09:00:00Z","has_draft":false,"has_note":false,"attachments":[]}`))
			return
		}
		// id "405" simulates an older server whose endpoint returns a plain-text (non-JSON) 405 — the
		// no-envelope error path the friendly diagnostics are for.
		if r.PathValue("id") == "405" {
			w.Header().Set("Allow", "POST")
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = w.Write([]byte("Method Not Allowed\n"))
			return
		}
		// id "declined" carries the run-debug bundle (decline_reason + guardrail/forced/fallback flags +
		// recoverable retries) — exercises the index "why" line and the -o json passthrough of the new fields.
		if r.PathValue("id") == "declined" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(fixture(t, "run_declined.json"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "run.json"))
	})
	mux.HandleFunc("GET /api/v1/runs/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		// id "sentinel" returns the negative-sentinel seq block the real server emits, to exercise the
		// table renumbering (and confirm NDJSON keeps the raw seq).
		if r.PathValue("id") == "sentinel" {
			_, _ = w.Write([]byte(`{"run_id":"sentinel","events":[` +
				`{"seq":-1000000,"tool":"bash","status":"error","exit_code":64,"duration_ms":0,"at":"2026-06-19T08:10:06Z"},` +
				`{"seq":-999999,"tool":"bash","status":"ok","exit_code":0,"duration_ms":97,"at":"2026-06-19T08:10:10Z"}]}`))
			return
		}
		// id "declined" ends on a reply event that carries decline_reason (no draft/note) — exercises the
		// trace's terminal-decline rendering.
		if r.PathValue("id") == "declined" {
			_, _ = w.Write(fixture(t, "events_declined.json"))
			return
		}
		_, _ = w.Write(fixture(t, "events.json"))
	})
	mux.HandleFunc("GET /api/v1/runs/{id}/full", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		// id "declined" carries the run.debug bundle, exercising the full header's debug rows + the
		// untruncated decline_reason block.
		if r.PathValue("id") == "declined" {
			_, _ = w.Write(fixture(t, "full_declined.json"))
			return
		}
		_, _ = w.Write(fixture(t, "full.json"))
	})
	mux.HandleFunc("GET /api/v1/runs/{id}/brain-diff", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		// id "no-brain" wrote no journal commit — the explicit empty (found:false) result.
		if r.PathValue("id") == "no-brain" {
			_, _ = w.Write([]byte(`{"run_id":"no-brain","found":false}`))
			return
		}
		_, _ = w.Write(fixture(t, "brain_diff.json"))
	})
	mux.HandleFunc("POST /api/v1/runs", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		// A rejected ref drives the BAD_BRAIN_REF path; seeing it in the body also proves --brain-ref
		// is forwarded verbatim.
		if strings.Contains(body, `"brain_ref":"bad/ref"`) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"BAD_BRAIN_REF","message":"brain ref rejected"}}`))
			return
		}
		// A prompt asking to hang resolves to the never-terminal "running" run (timeout test).
		runID := "11111111-1111-1111-1111-111111111111"
		if strings.Contains(body, "hang-please") {
			runID = "running"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"run_id":"` + runID + `","status":"running","status_url":"/api/v1/runs/` + runID + `","poll_after_ms":1}`))
	})
	mux.HandleFunc("GET /api/v1/env", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		// A tenant query returns a tenant-merged shape (an extra key + a differing FEATURE_FLAG), so the
		// --tenant plumbing and the scope label are exercised.
		if r.URL.Query().Get("tenant") == "acme" {
			_, _ = w.Write([]byte(`{"project":"momentum-tools","tenant":"acme","keys":{"FEATURE_FLAG":"tenant","REGION":"eu","ACME_DSN":"postgres://acme@h/db"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"project":"momentum-tools","keys":{"FEATURE_FLAG":"project","REGION":"eu","STRIPE_KEY":"sk_live_SECRET"}}`))
	})
	mux.HandleFunc("GET /api/v1/settings", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "settings.json"))
	})
	mux.HandleFunc("PATCH /api/v1/settings", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		// "max_run_usd":"oops" (a non-number) drives the INVALID_SETTINGS error path.
		if strings.Contains(body, `"max_run_usd":"oops"`) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"INVALID_SETTINGS","message":"settings rejected","fields":[{"key":"max_run_usd","message":"must be a number"}]}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "settings.json"))
	})

	// Tenant settings editing surface (Wave 3). The schema is served verbatim from the embedded copy;
	// GET returns a canned record; PATCH echoes the merge AND drives the validation_failed path.
	mux.HandleFunc("GET /api/v1/tenants/settings/schema", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "tenant_schema.json"))
	})
	mux.HandleFunc("GET /api/v1/tenants/{slug}/settings", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		// An unknown tenant is a uniform 404 (existence not leaked), like the real server.
		if r.PathValue("slug") == "ghost" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"NOT_FOUND","message":"tenant not found"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "tenant_settings.json"))
	})
	mux.HandleFunc("PATCH /api/v1/tenants/{slug}/settings", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		// A bad enum value drives the 400 validation_failed / field_errors path (server is final word).
		if strings.Contains(body, `"reschedule_method":"nope"`) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"validation_failed","field_errors":{"reschedule_method":"must be one of propose_one_book, propose_options_confirm, unset"}}`))
			return
		}
		// Echo the sent settings back as the merged record (the test asserts the exact body the CLI
		// sent: sparse keys, source:"cli", explicit null for unset). We re-emit the request's settings so
		// the golden/JSON assertions can see what was merged.
		var req struct {
			Settings json.RawMessage `json:"settings"`
		}
		_ = json.Unmarshal([]byte(body), &req)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"tenant_id":"de-kies","version":"sha256:patched","applied_at":"2026-06-22T10:00:00Z","settings":%s}`, req.Settings)
	})

	return httptest.NewServer(mux)
}

// newTestEnv builds an env wired to the stub server with a fixed output mode, capturing stdout/stderr.
// It sets a static bearer (tokenOvr) so newClient resolves auth without the OAuth token store, and
// isolates XDG_CONFIG_HOME so a real ~/.config/rootcause is never read or written.
func newTestEnv(t *testing.T, srv *httptest.Server, output string) (*env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // isolate the token store + config
	t.Setenv("ROOTCAUSE_BASE_URL", "")       // a stray env must not override the test base URL
	var out, errb bytes.Buffer
	e := &env{
		profile:    "default",
		output:     output,
		baseURLOvr: srv.URL,
		tokenOvr:   "test-key",
		out:        &out,
		err:        &errb,
	}
	return e, &out, &errb
}

// run executes a command line against a fresh root built on the test env, returning the captured
// stdout, stderr, and the error from Execute (so the error-path test can assert non-nil).
func run(t *testing.T, e *env, args ...string) error {
	t.Helper()
	// Cobra resets the --output-bound field to its flag default during parsing, so force the mode via
	// an explicit -o arg (mirroring how a user would) rather than presetting e.output.
	if e.output != "" {
		args = append([]string{"-o", e.output}, args...)
	}
	root := newRootCmd(e, "0.1.0-test")
	root.SetArgs(args)
	root.SetOut(e.out)
	root.SetErr(e.err)
	return root.Execute()
}

func requireAuth(t *testing.T, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("missing/wrong auth header: %q", got)
	}
}

func readBody(t *testing.T, r *http.Request) string {
	t.Helper()
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(r.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	return buf.String()
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// assertGolden compares got against testdata/<name>, writing it when -update is set. Goldens are
// stable: fixtures use canned timestamps, never time.Now.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden %s: %v", name, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	if got != string(want) {
		t.Errorf("output mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

// assertJSONEqual checks that two JSON byte slices decode to the same value — the passthrough
// contract: -o json must round-trip the server's body (re-indentation aside), never reshape it.
func assertJSONEqual(t *testing.T, want, got []byte) {
	t.Helper()
	var wv, gv any
	if err := json.Unmarshal(want, &wv); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	if err := json.Unmarshal(got, &gv); err != nil {
		t.Fatalf("unmarshal got: %v\nraw: %s", err, got)
	}
	if !reflect.DeepEqual(wv, gv) {
		t.Errorf("JSON not equal\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
