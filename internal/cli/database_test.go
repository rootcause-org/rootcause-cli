package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// previewReportJSON is a canned scope-preview response the stub server returns.
const previewReportJSON = `{
  "project": "kampadmin",
  "dsn_env": "PREVIEW_DSN",
  "tenant": "lbv",
  "tenant_predicate": true,
  "scope_value": "lbv",
  "principal": {"kind": "kampadmin_person", "external_id": "p-1"},
  "claims": {"person_id": "p-1"},
  "tables": [
    {"name": "people", "count": 1, "predicate": "person_id = 'p-1'::uuid", "rows": [{"id": 1, "person_id": "p-1"}]},
    {"name": "subscriptions", "count": 2, "predicate": "parent_id = 'p-1'::uuid", "rows": []}
  ]
}`

// previewMux serves the fleet list (for --project name resolution) + the scope-preview endpoint, capturing
// the preview request's path + body.
func previewMux(t *testing.T, gotPath *string, gotBody *map[string]any) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"projects":[{"id":"11111111-1111-1111-1111-111111111111","name":"kampadmin"}]}`))
	})
	mux.HandleFunc("POST /api/v1/databases/{dsn}/scope-preview", func(w http.ResponseWriter, r *http.Request) {
		if gotPath != nil {
			*gotPath = r.URL.Path + "?" + r.URL.RawQuery
		}
		if gotBody != nil {
			_ = json.NewDecoder(r.Body).Decode(gotBody)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(previewReportJSON))
	})
	return mux
}

// TestDatabasePreviewForwardsTenantAndPrincipal: the tenant + principal ride the BODY (the scoped identity),
// project rides the query, and -o json returns the report verbatim.
func TestDatabasePreviewForwardsTenantAndPrincipal(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(previewMux(t, &gotPath, &gotBody))
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "--project", "kampadmin", "project", "database", "preview", "PREVIEW_DSN",
		"--tenant", "lbv", "--principal-kind", "kampadmin_person", "--principal-id", "p-1"); err != nil {
		t.Fatalf("database preview: %v", err)
	}
	if !strings.HasPrefix(gotPath, "/api/v1/databases/PREVIEW_DSN/scope-preview") {
		t.Errorf("wrong path: %s", gotPath)
	}
	if !strings.Contains(gotPath, "project=kampadmin") {
		t.Errorf("project not on query: %s", gotPath)
	}
	if gotBody["tenant"] != "lbv" {
		t.Errorf("tenant not in body: %v", gotBody)
	}
	p, ok := gotBody["principal"].(map[string]any)
	if !ok || p["kind"] != "kampadmin_person" || p["external_id"] != "p-1" {
		t.Errorf("principal not in body: %v", gotBody["principal"])
	}
	if !strings.Contains(out.String(), `"scope_value": "lbv"`) && !strings.Contains(out.String(), `"scope_value":"lbv"`) {
		t.Errorf("json report not returned verbatim:\n%s", out.String())
	}
}

// TestDatabasePreviewTableRender: the table view shows the resolved scope header + per-table counts +
// compiled predicates.
func TestDatabasePreviewTableRender(t *testing.T) {
	srv := httptest.NewServer(previewMux(t, nil, nil))
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "--project", "kampadmin", "project", "database", "preview", "PREVIEW_DSN", "--tenant", "lbv",
		"--principal-kind", "kampadmin_person", "--principal-id", "p-1"); err != nil {
		t.Fatalf("database preview table: %v", err)
	}
	got := out.String()
	for _, want := range []string{"kampadmin_person=p-1", "people — 1 row", "person_id = 'p-1'::uuid", "subscriptions — 2 row"} {
		if !strings.Contains(got, want) {
			t.Errorf("table output missing %q:\n%s", want, got)
		}
	}
}

// TestDatabasePreviewPrincipalPairValidated: a lone --principal-kind (no id) is a client-side error, never
// a silent under-scoped preview.
func TestDatabasePreviewPrincipalPairValidated(t *testing.T) {
	srv := httptest.NewServer(previewMux(t, nil, nil))
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "json")
	err := run(t, e, "--project", "kampadmin", "project", "database", "preview", "PREVIEW_DSN", "--principal-kind", "kampadmin_person")
	if err == nil || !strings.Contains(err.Error(), "must be provided together") {
		t.Fatalf("lone principal-kind = %v, want a pair error", err)
	}
}

// TestConsoleDBQueryWriteForwardsFlag: `dev console database query --write` carries write:true to the
// console endpoint (the flag is the consent, no prompt), and the table view leads with the commit count.
func TestConsoleDBQueryWriteForwardsFlag(t *testing.T) {
	var gotBody map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/console/db/{db}/query", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"project":"pro-backup","db":"backups","run_id":"run-1","columns":["id"],"rows":[{"id":7}],"row_count":1,"rows_affected":1,"write":true,"duration_ms":12}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "dev", "console", "database", "query", "backups", "update backups set x=1 where id=7 returning id", "--write"); err != nil {
		t.Fatalf("console db query --write: %v", err)
	}
	if gotBody["write"] != true {
		t.Errorf("write flag not forwarded in body: %v", gotBody)
	}
	if got := out.String(); !strings.Contains(got, "rows affected: 1") {
		t.Errorf("render missing rows-affected line:\n%s", got)
	}
}
