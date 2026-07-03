package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestProjectsTable pins `rc projects`: the fleet handle list (name + id), name-ordered as the server
// sends them.
func TestProjectsTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "projects"); err != nil {
		t.Fatalf("projects: %v", err)
	}
	got := out.String()
	for _, want := range []string{"alpha", "bravo", "NAME", "ID"} {
		if !strings.Contains(got, want) {
			t.Errorf("projects table missing %q\n%s", want, got)
		}
	}
}

// TestProjectsJSONPassthrough: -o json round-trips the server's project rows verbatim.
func TestProjectsJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "projects"); err != nil {
		t.Fatalf("projects -o json: %v", err)
	}
	var got struct {
		Projects []map[string]any `json:"projects"`
	}
	decodeJSON(t, out.Bytes(), &got)
	if len(got.Projects) != 2 {
		t.Fatalf("projects = %d, want 2; body=%s", len(got.Projects), out.String())
	}
}

// TestFleetAllJSON: `rc fleet --all` fans out across every project and emits the merged structure
// {projects:[{project,runs}], total_runs}. Each project pages the fleet fixtures (4 runs), so a
// two-project fleet totals 8.
func TestFleetAllJSON(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "fleet", "--all", "--kind", "fleet"); err != nil {
		t.Fatalf("fleet --all -o json: %v", err)
	}
	var got struct {
		Projects []struct {
			Project string           `json:"project"`
			Runs    []map[string]any `json:"runs"`
		} `json:"projects"`
		TotalRuns int `json:"total_runs"`
	}
	decodeJSON(t, out.Bytes(), &got)
	if len(got.Projects) != 2 {
		t.Fatalf("projects = %d, want 2; body=%s", len(got.Projects), out.String())
	}
	if got.Projects[0].Project != "alpha" || got.Projects[1].Project != "bravo" {
		t.Errorf("project order = %q,%q; want alpha,bravo", got.Projects[0].Project, got.Projects[1].Project)
	}
	if got.TotalRuns != len(got.Projects[0].Runs)+len(got.Projects[1].Runs) {
		t.Errorf("total_runs = %d, want sum of per-project runs", got.TotalRuns)
	}
}

// TestFleetAllScopedTokenErrors: `--all` against a project-scoped token (the fleet list returns just one
// project) is a friendly client-side error, not a stack trace — it names the project and the fix.
func TestFleetAllScopedTokenErrors(t *testing.T) {
	solo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/projects" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"projects":[{"id":"11111111-1111-1111-1111-111111111111","name":"only-me"}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer solo.Close()

	e, _, _ := newTestEnv(t, solo, "table")
	err := run(t, e, "fleet", "--all")
	if err == nil {
		t.Fatal("expected an error for --all with a project-scoped token")
	}
	if !strings.Contains(err.Error(), "only-me") || !strings.Contains(err.Error(), "all-projects token") {
		t.Errorf("error = %q, want it to name the project + an all-projects-token hint", err.Error())
	}
}

// TestUnknownProjectScopeFailsBeforeCommand asserts the global --project guard rejects a typo before the
// scoped command renders misleading local/client-side output.
func TestUnknownProjectScopeFailsBeforeCommand(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, errb := newTestEnv(t, srv, "table")
	err := run(t, e, "--project", "charlie", "status")
	if err == nil {
		t.Fatal("expected unknown-project error, got nil")
	}
	printError(errb, err)
	got := errb.String()
	for _, want := range []string{"UNKNOWN_PROJECT", "charlie", "rc projects"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
