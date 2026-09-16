package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

func TestDBQueryAllStreamsCSVWithParamsAndStdin(t *testing.T) {
	var requests []clientQueryRequest
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/console/db/{db}/query", func(w http.ResponseWriter, r *http.Request) {
		var req clientQueryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, req)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte("{\"type\":\"header\",\"project\":\"kampkompas\",\"db\":\"prod\",\"run_id\":\"11111111-aaaa\",\"columns\":[\"id\",\"id\",\"amount\"],\"batch_size\":5000}\n" +
			"{\"type\":\"row\",\"row\":[\"a\",\"shadow\",\"10.00\"]}\n" +
			"{\"type\":\"row\",\"row\":[\"b\",\"shadow2\",\"20.00\"]}\n" +
			"{\"type\":\"row\",\"row\":[\"c\",\"shadow3\",\"30.00\"]}\n" +
			"{\"type\":\"meta\",\"row_count\":3,\"duration_ms\":12,\"truncated\":false}\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	e.in = strings.NewReader("select id, parent_id as id, amount from values\n")
	if err := run(t, e, "dev", "console", "database", "query", "prod", "-", "--all", "--format", "csv", "--out", "-", "--param", "tenant=acme"); err != nil {
		t.Fatalf("query --all: %v", err)
	}
	want := "id,id,amount\na,shadow,10.00\nb,shadow2,20.00\nc,shadow3,30.00\n"
	if out.String() != want {
		t.Fatalf("CSV = %q, want %q", out.String(), want)
	}
	if len(requests) != 1 || !requests[0].All || requests[0].Params["tenant"] != "acme" {
		t.Fatalf("requests = %+v", requests)
	}
	if requests[0].SQL != "select id, parent_id as id, amount from values\n" {
		t.Fatalf("stdin SQL = %q", requests[0].SQL)
	}
}

func TestDBQueryCSVPreservesJSONNumbers(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/console/db/{db}/query", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"project":"alpha","db":"prod","run_id":"11111111-aaaa","columns":["amount"],"rows":[[12345678901234567890.1234]],"row_count":1,"truncated":false}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "dev", "console", "database", "query", "prod", "select amount", "--format", "csv", "--out", "-"); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "amount\n12345678901234567890.1234\n"; got != want {
		t.Fatalf("CSV = %q, want %q", got, want)
	}
}

type clientQueryRequest struct {
	SQL    string            `json:"sql"`
	Params map[string]string `json:"params"`
	All    bool              `json:"all"`
}

func TestDBQueryTruncationExitAndAllow(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/console/db/{db}/query", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"project":"alpha","db":"prod","run_id":"aaaaaaaa-bbbb","columns":["id"],"rows":[["1"]],"row_count":1,"truncated":true}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	err := run(t, e, "dev", "console", "database", "query", "prod", "select 1")
	if exitCodeFor(err) != exitTruncated || out.Len() != 0 {
		t.Fatalf("truncated exit/output = %d/%q (%v)", exitCodeFor(err), out.String(), err)
	}
	e, out, _ = newTestEnv(t, srv, "json")
	if err := run(t, e, "dev", "console", "database", "query", "prod", "select 1", "--allow-truncated"); err != nil {
		t.Fatalf("allow truncated: %v", err)
	}
	if !strings.Contains(out.String(), `"truncated": true`) {
		t.Fatalf("allowed output = %s", out.String())
	}
	e, out, _ = newTestEnv(t, srv, "json")
	path := filepath.Join(t.TempDir(), "partial.csv")
	if err := run(t, e, "dev", "console", "database", "query", "prod", "select 1", "--allow-truncated", "--format", "csv", "--out", path); err != nil {
		t.Fatalf("allow truncated to file: %v", err)
	}
	if !strings.Contains(out.String(), `"truncated": true`) {
		t.Fatalf("truncated manifest = %s", out.String())
	}
}

func TestDBQueryRejectsAllLimitAboveServerMaximum(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "json")
	err := run(t, e, "dev", "console", "database", "query", "prod", "select 1 order by 1", "--all", "--limit", "5001")
	if err == nil || !strings.Contains(err.Error(), "cannot exceed 5000") {
		t.Fatalf("limit error = %v", err)
	}
}

func TestDBQueryRejectsServerLimitClamp(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/console/db/{db}/query", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{\"type\":\"header\",\"project\":\"alpha\",\"db\":\"prod\",\"run_id\":\"aaaaaaaa-bbbb\",\"columns\":[\"id\"],\"batch_size\":5000,\"limit_clamped\":true}\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	err := run(t, e, "dev", "console", "database", "query", "prod", "select 1 order by 1", "--all", "--limit", "5000")
	if err == nil || !strings.Contains(err.Error(), "clamped by the server to 5000") || out.Len() != 0 {
		t.Fatalf("clamp error/output = %v/%q", err, out.String())
	}
}

func TestDBQueryAllMissingMetaKeepsDestinationAtomic(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/console/db/{db}/query", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte("{\"type\":\"header\",\"project\":\"alpha\",\"db\":\"prod\",\"run_id\":\"aaaaaaaa-bbbb\",\"columns\":[\"id\"],\"batch_size\":5000}\n" +
			"{\"type\":\"row\",\"row\":[\"partial\"]}\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "json")
	path := filepath.Join(t.TempDir(), "rows.csv")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := run(t, e, "dev", "console", "database", "query", "prod", "select id from rows", "--all", "--format", "csv", "--out", path)
	if exitCodeFor(err) != exitServer {
		t.Fatalf("exit = %d err=%v, want server/transport failure", exitCodeFor(err), err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || string(got) != "old\n" {
		t.Fatalf("destination = %q err=%v, want original file", got, readErr)
	}
}

func TestDBQueryAllStreamFailureKeepsDestinationAtomic(t *testing.T) {
	tests := []struct {
		name    string
		trailer string
	}{
		{name: "error frame", trailer: `{"type":"error","error":{"code":"QUERY_FAILED","message":"stream failed","status":500}}`},
		{name: "row count mismatch", trailer: `{"type":"meta","row_count":2,"duration_ms":12,"truncated":false}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("POST /api/v1/console/db/{db}/query", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/x-ndjson")
				_, _ = w.Write([]byte("{\"type\":\"header\",\"project\":\"alpha\",\"db\":\"prod\",\"run_id\":\"aaaaaaaa-bbbb\",\"columns\":[\"id\"],\"batch_size\":5000}\n" +
					"{\"type\":\"row\",\"row\":[\"partial\"]}\n" + tt.trailer + "\n"))
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()
			e, _, _ := newTestEnv(t, srv, "json")
			dir := t.TempDir()
			path := filepath.Join(dir, "rows.csv")
			err := run(t, e, "dev", "console", "database", "query", "prod", "select id from rows", "--all", "--format", "csv", "--out", path)
			if exitCodeFor(err) != exitServer {
				t.Fatalf("exit = %d err=%v", exitCodeFor(err), err)
			}
			if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
				t.Fatalf("partial destination installed: %v", statErr)
			}
			matches, globErr := filepath.Glob(filepath.Join(dir, ".rc-output-*"))
			if globErr != nil || len(matches) != 0 {
				t.Fatalf("orphaned temp outputs = %v, err=%v", matches, globErr)
			}
		})
	}
}

func TestDBQueryAllCancellationRemovesTemporaryOutput(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/console/db/{db}/query", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte("{\"type\":\"header\",\"project\":\"alpha\",\"db\":\"prod\",\"run_id\":\"aaaaaaaa-bbbb\",\"columns\":[\"id\"],\"batch_size\":5000}\n" +
			"{\"type\":\"row\",\"row\":[\"partial\"]}\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "json")
	ctx, cancel := context.WithCancel(context.Background())
	e.requestCtx = ctx
	dir := t.TempDir()
	path := filepath.Join(dir, "rows.csv")
	time.AfterFunc(50*time.Millisecond, cancel)
	err := run(t, e, "dev", "console", "database", "query", "prod", "select id from rows", "--all", "--format", "csv", "--out", path)
	if exitCodeFor(err) != exitServer {
		t.Fatalf("exit = %d err=%v", exitCodeFor(err), err)
	}
	matches, globErr := filepath.Glob(filepath.Join(dir, ".rc-output-*"))
	if globErr != nil || len(matches) != 0 {
		t.Fatalf("orphaned temp outputs = %v, err=%v", matches, globErr)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("partial destination installed: %v", statErr)
	}
}

func TestDBQueryAutoOutputUsesRunID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/console/db/{db}/query", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"project":"alpha","db":"prod","run_id":"abcdef12-3456","columns":["id"],"rows":[["1"]],"row_count":1,"truncated":false}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	e.outDir = t.TempDir()
	if err := run(t, e, "--out-dir", e.outDir, "dev", "console", "database", "query", "prod", "select 1", "--format", "ndjson", "--out", "auto"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.outDir, "console-db-query-abcdef12.ndjson")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("auto output: %v; manifest=%s", err, out.String())
	}
	if !strings.Contains(out.String(), path) {
		t.Fatalf("manifest missing path: %s", out.String())
	}
}

func TestConsoleFileGetStreamsToAtomicOutput(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/console/file", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("path"); got != "/tmp/export.csv" {
			t.Fatalf("remote path = %q", got)
		}
		_, _ = w.Write([]byte("id,name\n1,Ada\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	path := filepath.Join(t.TempDir(), "export.csv")
	if err := run(t, e, "dev", "console", "file", "get", "/tmp/export.csv", "--out", path); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(b, []byte("id,name\n1,Ada\n")) {
		t.Fatalf("file = %q, err=%v", b, err)
	}
	if !strings.Contains(out.String(), path) {
		t.Fatalf("manifest = %s", out.String())
	}
}

func TestConsoleFileGetRejectsIncompleteBodyAndKeepsDestinationAtomic(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/console/file", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "20")
		_, _ = w.Write([]byte("partial"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "json")
	path := filepath.Join(t.TempDir(), "must-not-exist.csv")
	err := run(t, e, "dev", "console", "file", "get", "/tmp/export.csv", "--out", path)
	if exitCodeFor(err) != exitServer {
		t.Fatalf("incomplete file exit = %d, err=%v", exitCodeFor(err), err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("partial destination installed: %v", statErr)
	}
}

func TestConsoleFileGetUnauthorizedPreservesServerError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/console/file", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"TOKEN_EXPIRED","message":"log in again"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "json")
	err := run(t, e, "dev", "console", "file", "get", "/tmp/export.csv", "--out", filepath.Join(t.TempDir(), "out"))
	if exitCodeFor(err) != exitAuth || !strings.Contains(err.Error(), "log in again") {
		t.Fatalf("auth exit/error = %d/%v", exitCodeFor(err), err)
	}
}

func TestAutoOutputUsesJSONErrorEnvelopeWhenPiped(t *testing.T) {
	var out, errOut bytes.Buffer
	e := &env{out: &out, err: &errOut}
	if got := reportCommandError(e, truncationError("too many rows")); got != exitTruncated {
		t.Fatalf("exit = %d", got)
	}
	// Byte-exact: the machine envelope's shape is the contract, not just its code field.
	if out.String() != "{\"error\":{\"code\":\"TRUNCATED\",\"message\":\"too many rows\",\"status\":0,\"fields\":[]}}\n" || errOut.Len() != 0 {
		t.Fatalf("stdout/stderr = %q/%q", out.String(), errOut.String())
	}
}

func TestStableExitClassification(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{authenticationError("login"), exitAuth},
		{&client.APIError{Status: http.StatusBadRequest, Code: "BAD_BODY"}, exitUsage},
		{&client.APIError{Status: http.StatusForbidden, Code: "FORBIDDEN"}, exitAuth},
		{&client.APIError{Status: http.StatusInternalServerError, Code: "BROKEN"}, exitServer},
		{remoteExitError(), exitRemote},
	}
	for _, tc := range cases {
		if got := exitCodeFor(tc.err); got != tc.want {
			t.Errorf("exitCodeFor(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
}

// TestConsolePrincipalWireContract pins both halves of the principal binding: the request must carry
// {kind, external_id} when the pair is given and must OMIT the key entirely when it is not (an older
// host rejects an unknown body field, so a dormant `principal: null` would break every unbound call),
// and the echoed principal + hidden_tables must survive into `-o json`.
func TestConsolePrincipalWireContract(t *testing.T) {
	var bashBodies, queryBodies []string
	var schemaQueries []string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/console/bash/run", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bashBodies = append(bashBodies, string(body))
		_, _ = w.Write([]byte(`{"project":"alpha","run_id":"abcdef12-3456","exit_code":0,"stdout":"ok\n",` +
			`"principal":{"kind":"kampadmin_admin","external_id":"usr-1"},"hidden_tables":["public.payouts"]}`))
	})
	mux.HandleFunc("POST /api/v1/console/db/{db}/query", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		queryBodies = append(queryBodies, string(body))
		_, _ = w.Write([]byte(`{"project":"alpha","db":"prod","run_id":"abcdef12-3456","columns":["id"],"rows":[["1"]],` +
			`"row_count":1,"truncated":false,"principal":{"kind":"kampadmin_admin","external_id":"usr-1"},` +
			`"hidden_tables":["public.payouts"]}`))
	})
	mux.HandleFunc("GET /api/v1/console/db/{db}/schema", func(w http.ResponseWriter, r *http.Request) {
		schemaQueries = append(schemaQueries, r.URL.RawQuery)
		_, _ = w.Write([]byte(`{"project":"alpha","db":"prod","tables":[],"principal":{"kind":"kampadmin_admin","external_id":"usr-1"},"hidden_tables":["public.payouts"]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "dev", "console", "bash", "run", "echo hi", "--principal-kind", "kampadmin_admin", "--principal-id", "usr-1"); err != nil {
		t.Fatalf("bash run bound: %v", err)
	}
	for _, want := range []string{`"kampadmin_admin"`, `"usr-1"`, `"hidden_tables"`, `"public.payouts"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("bash run -o json missing %s:\n%s", want, out.String())
		}
	}
	e, _, _ = newTestEnv(t, srv, "json")
	if err := run(t, e, "dev", "console", "bash", "run", "echo hi"); err != nil {
		t.Fatalf("bash run unbound: %v", err)
	}

	e, out, _ = newTestEnv(t, srv, "json")
	if err := run(t, e, "dev", "console", "database", "query", "prod", "select 1", "--principal-kind", "kampadmin_admin", "--principal-id", "usr-1"); err != nil {
		t.Fatalf("query bound: %v", err)
	}
	for _, want := range []string{`"kampadmin_admin"`, `"hidden_tables"`, `"public.payouts"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("query -o json missing %s:\n%s", want, out.String())
		}
	}
	e, _, _ = newTestEnv(t, srv, "json")
	if err := run(t, e, "dev", "console", "database", "query", "prod", "select 1"); err != nil {
		t.Fatalf("query unbound: %v", err)
	}

	e, out, _ = newTestEnv(t, srv, "json")
	if err := run(t, e, "dev", "console", "database", "schema", "prod", "--principal-kind", "kampadmin_admin", "--principal-id", "usr-1"); err != nil {
		t.Fatalf("schema bound: %v", err)
	}
	if !strings.Contains(out.String(), `"hidden_tables"`) || !strings.Contains(out.String(), `"public.payouts"`) {
		t.Errorf("schema -o json dropped hidden_tables:\n%s", out.String())
	}
	e, _, _ = newTestEnv(t, srv, "json")
	if err := run(t, e, "dev", "console", "database", "schema", "prod"); err != nil {
		t.Fatalf("schema unbound: %v", err)
	}

	if len(bashBodies) != 2 || len(queryBodies) != 2 || len(schemaQueries) != 2 {
		t.Fatalf("captured %d bash, %d query, %d schema calls", len(bashBodies), len(queryBodies), len(schemaQueries))
	}
	for _, c := range []struct {
		name  string
		bound string
		plain string
	}{
		{"bash run", bashBodies[0], bashBodies[1]},
		{"db query", queryBodies[0], queryBodies[1]},
	} {
		if !strings.Contains(c.bound, `"principal":{"kind":"kampadmin_admin","external_id":"usr-1"}`) {
			t.Errorf("%s bound body = %s", c.name, c.bound)
		}
		if strings.Contains(c.plain, "principal") {
			t.Errorf("%s unbound body must omit principal entirely, got %s", c.name, c.plain)
		}
	}
	if !strings.Contains(schemaQueries[0], "principal_kind=kampadmin_admin") || !strings.Contains(schemaQueries[0], "principal_id=usr-1") {
		t.Errorf("schema bound query = %q", schemaQueries[0])
	}
	if strings.Contains(schemaQueries[1], "principal") {
		t.Errorf("schema unbound query must omit principal params, got %q", schemaQueries[1])
	}
}
