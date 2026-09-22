package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const sessionsBody = `{"sessions":[
 {"session_id":"11111111-1111-1111-1111-111111111111","tenant_slug":"yes-events","surface":"embed",
  "created_at":"2026-09-20T10:00:00Z","turns":5,"first_question":"waar vind ik mijn factuur",
  "principal":{"kind":"embed_user","id":"ext-7"},"feedback":{"count":2,"min_score":1,"max_score":5,"has_comment":true},"shared":true},
 {"session_id":"22222222-2222-2222-2222-222222222222","surface":"dashboard","title":"Weekly numbers",
  "created_at":"2026-09-19T09:00:00Z","turns":1,"principal":{"kind":"member","id":"u-1"}}
],"next_before":"22222222-2222-2222-2222-222222222222"}`

// sessionsServer serves GET /api/v1/sessions and records the query it was called with.
func sessionsServer(t *testing.T, gotQuery *string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		*gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(sessionsBody))
	})
	return httptest.NewServer(mux)
}

func TestRunSessionsTable(t *testing.T) {
	var query string
	srv := sessionsServer(t, &query)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")

	if err := run(t, e, "run", "sessions", "--surface", "embed", "--days", "7", "--limit", "2"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"surface=embed", "days=7", "limit=2"} {
		if !strings.Contains(query, want) {
			t.Errorf("query %q missing %q", query, want)
		}
	}
	got := out.String()
	for _, want := range []string{
		"CREATED", "TENANT", "TURNS", "FEEDBACK", "CONVERSATION", "SESSION",
		"yes-events", "1–5★💬", "waar vind ik mijn factuur", "Weekly numbers",
		"rc run sessions --before 22222222-2222-2222-2222-222222222222",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("table missing %q:\n%s", want, got)
		}
	}
	// An unrated conversation shows a dash, never a fake zero score.
	if !strings.Contains(got, "\t-\t") && !strings.Contains(got, "  -  ") {
		t.Errorf("unrated conversation missing the '-' feedback cell:\n%s", got)
	}
}

// JSON mode is a byte-faithful passthrough of the server payload, cursor included: a consumer must be
// able to page without rc reshaping the body.
func TestRunSessionsJSONPassthrough(t *testing.T) {
	var query string
	srv := sessionsServer(t, &query)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")

	if err := run(t, e, "run", "sessions"); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{`"next_before"`, `"first_question"`, `"principal"`, `"shared"`} {
		if !strings.Contains(got, want) {
			t.Errorf("json missing %q:\n%s", want, got)
		}
	}
	if query != "" {
		t.Errorf("no-flag call sent query %q, want none (server owns the defaults)", query)
	}
}

func TestRunSessionsRejectsUnknownSurface(t *testing.T) {
	var query string
	srv := sessionsServer(t, &query)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")

	err := run(t, e, "run", "sessions", "--surface", "carrier-pigeon")
	if err == nil || !strings.Contains(err.Error(), "invalid --surface") {
		t.Fatalf("err = %v, want an invalid --surface complaint", err)
	}
	if query != "" {
		t.Errorf("a locally-invalid surface still reached the server: %q", query)
	}
}
