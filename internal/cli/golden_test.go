package cli

import (
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

// TestNoAPIKey asserts a clear error when no key resolves (env unset, no config).
func TestNoAPIKey(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	e2 := &env{profile: "default", output: "table", baseURLOvr: srv.URL, out: e.out, err: e.err}
	t.Setenv("ROOTCAUSE_API_KEY", "")
	err := run(t, e2, "status")
	if err == nil || !strings.Contains(err.Error(), "no API key") {
		t.Fatalf("expected no-API-key error, got %v", err)
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
