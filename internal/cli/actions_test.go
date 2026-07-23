package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestFleetActionsPagesFiltersAndPreservesRawRows(t *testing.T) {
	var queries []map[string][]string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/actions", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		q := r.URL.Query()
		queries = append(queries, map[string][]string{
			"days":   q["days"],
			"action": q["action"],
			"status": q["status"],
			"cursor": q["cursor"],
		})
		w.Header().Set("Content-Type", "application/json")
		if q.Get("cursor") == "" {
			_, _ = w.Write(fixture(t, "actions_feed_p1.json"))
			return
		}
		_, _ = w.Write(fixture(t, "actions_feed_p2.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	e, out, _ := newTestEnv(t, srv, "json")
	err := run(t, e, "fleet", "actions", "--days", "14",
		"--action", "create_appointment", "--action", "update_appointment",
		"--status", "succeeded", "--status", "proposed")
	if err != nil {
		t.Fatalf("fleet actions: %v", err)
	}
	if len(queries) != 2 {
		t.Fatalf("requests = %d, want 2", len(queries))
	}
	for i, q := range queries {
		if !reflect.DeepEqual(q["days"], []string{"14"}) ||
			!reflect.DeepEqual(q["action"], []string{"create_appointment", "update_appointment"}) ||
			!reflect.DeepEqual(q["status"], []string{"succeeded", "proposed"}) {
			t.Fatalf("request %d filters = %#v", i+1, q)
		}
	}
	if got := queries[1]["cursor"]; !reflect.DeepEqual(got, []string{"opaque/cursor+one=="}) {
		t.Fatalf("page 2 cursor = %#v, want opaque cursor unchanged", got)
	}

	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if len(body.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(body.Items))
	}
	if _, ok := body.Items[0]["server_extension"]; !ok {
		t.Fatalf("raw additive field dropped: %#v", body.Items[0])
	}
	if body.Items[1]["run_id"] != nil || body.Items[1]["run_url"] != nil {
		t.Fatalf("nullable fields changed: %#v", body.Items[1])
	}
}

func TestFleetActionsHumanIncludesParamsAndTokenizedURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/actions", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(fixture(t, "actions_feed_p2.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "fleet", "actions"); err != nil {
		t.Fatalf("fleet actions table: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"96fc4444-4444-4444-8444-444444444444",
		"update_appointment",
		`"appointment_id":987`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("table missing %q:\n%s", want, got)
		}
	}
}

func TestFleetActionsRejectsInvalidFiltersBeforeRequest(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "days low", args: []string{"fleet", "actions", "--days", "0"}, want: "--days must be positive"},
		{name: "action empty", args: []string{"fleet", "actions", "--action="}, want: "--action must not be empty"},
		{name: "status", args: []string{"fleet", "actions", "--status", "completed"}, want: `invalid --status "completed"`},
		{name: "format", args: []string{"fleet", "actions", "--format", "yaml"}, want: `invalid --format "yaml"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &env{baseURLOvr: "http://127.0.0.1:1", tokenOvr: "test", out: &strings.Builder{}, err: &strings.Builder{}}
			err := run(t, e, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestFleetActionsAgentRendersWhenPiped(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/actions", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(fixture(t, "actions_feed_p2.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	e, out, _ := newTestEnv(t, srv, "")
	if err := run(t, e, "fleet", "actions", "--format", "agent"); err != nil {
		t.Fatalf("fleet actions --format agent: %v", err)
	}
	got := out.String()
	if !strings.HasPrefix(got, "ACTION id=") || !strings.Contains(got, `params={"appointment_id":987`) {
		t.Fatalf("agent view not pinned over pipe:\n%s", got)
	}
}

func TestFleetActionsExplicitJSONWinsOverAgentFormat(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/actions", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(fixture(t, "actions_feed_p2.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "fleet", "actions", "--format", "agent"); err != nil {
		t.Fatalf("fleet actions -o json --format agent: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(out.String()), "{") {
		t.Fatalf("explicit JSON did not win:\n%s", out.String())
	}
}

func TestFleetActionsLetsServerClampLargeDays(t *testing.T) {
	var got string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/actions", func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query().Get("days")
		_, _ = w.Write([]byte(`{"items":[]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	e, _, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "fleet", "actions", "--days", "30"); err != nil {
		t.Fatalf("fleet actions --days 30: %v", err)
	}
	if got != "30" {
		t.Fatalf("days query = %q, want 30 for server-side clamp", got)
	}
}
