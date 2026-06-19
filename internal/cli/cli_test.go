package cli

import (
	"bytes"
	"encoding/json"
	"flag"
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
		_, _ = w.Write(fixture(t, "runs.json"))
	})
	mux.HandleFunc("GET /api/v1/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		if r.PathValue("id") == "bad" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"UNKNOWN_RUN","message":"unknown run"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "run.json"))
	})
	mux.HandleFunc("GET /api/v1/runs/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "events.json"))
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

	return httptest.NewServer(mux)
}

// newTestEnv builds an env wired to the stub server with a fixed output mode, capturing stdout/stderr.
// It sets a dummy API key via env so newClient resolves auth without a config file.
func newTestEnv(t *testing.T, srv *httptest.Server, output string) (*env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	t.Setenv("ROOTCAUSE_API_KEY", "test-key")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // isolate from any real ~/.config
	var out, errb bytes.Buffer
	e := &env{
		profile:    "default",
		output:     output,
		baseURLOvr: srv.URL,
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
