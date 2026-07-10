package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestProjectRenameExplicitScope(t *testing.T) {
	var gotProject, gotBody string
	srv := projectRenameServer(t, `{"projects":[{"id":"aaaaaaaa-0000-0000-0000-000000000001","name":"alpha"}]}`, &gotProject, &gotBody)
	defer srv.Close()

	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "--project", "alpha", "project", "rename", "delta"); err != nil {
		t.Fatalf("project rename --project: %v", err)
	}
	if gotProject != "alpha" {
		t.Fatalf("rename path project = %q, want alpha", gotProject)
	}
	if gotBody != `{"name":"delta"}` {
		t.Fatalf("rename body = %s, want name-only body", gotBody)
	}
	want := "renamed alpha -> delta (brain rootcause-brain-delta; github yes; local yes)\n"
	if out.String() != want {
		t.Fatalf("rename output = %q, want %q", out.String(), want)
	}
}

func TestProjectRenamePinnedScopeFromSingleVisibleProject(t *testing.T) {
	var gotProject, gotBody string
	srv := projectRenameServer(t, `{"projects":[{"id":"aaaaaaaa-0000-0000-0000-000000000001","name":"alpha"}]}`, &gotProject, &gotBody)
	defer srv.Close()

	e, _, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "rename", "delta"); err != nil {
		t.Fatalf("project rename pinned scope: %v", err)
	}
	if gotProject != "alpha" {
		t.Fatalf("rename path project = %q, want alpha", gotProject)
	}
	if gotBody != `{"name":"delta"}` {
		t.Fatalf("rename body = %s, want name-only body", gotBody)
	}
}

func TestProjectRenameUpdatesBrainMarker(t *testing.T) {
	var gotProject, gotBody string
	srv := projectRenameServer(t, `{"projects":[{"id":"aaaaaaaa-0000-0000-0000-000000000001","name":"alpha"}]}`, &gotProject, &gotBody)
	defer srv.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".rootcause.toml"), []byte("project = \"alpha\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	e, _, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "rename", "delta"); err != nil {
		t.Fatalf("project rename: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".rootcause.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(body), "project = \"delta\"\n"; got != want {
		t.Fatalf("marker = %q, want %q", got, want)
	}
}

func TestProjectRenameJSONPassthrough(t *testing.T) {
	var gotProject, gotBody string
	srv := projectRenameServer(t, `{"projects":[{"id":"aaaaaaaa-0000-0000-0000-000000000001","name":"alpha"}]}`, &gotProject, &gotBody)
	defer srv.Close()

	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "--project", "alpha", "project", "rename", "delta"); err != nil {
		t.Fatalf("project rename -o json: %v", err)
	}
	for _, want := range []string{`"previous_name": "alpha"`, `"name": "delta"`, `"extra": "kept"`} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("JSON passthrough missing %q:\n%s", want, out.String())
		}
	}
	if gotProject != "alpha" || gotBody != `{"name":"delta"}` {
		t.Fatalf("request = project %q body %s, want alpha/name-only", gotProject, gotBody)
	}
}

func TestProjectRenameAmbiguousVisibleProjects(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "project", "rename", "delta")
	if err == nil {
		t.Fatal("expected ambiguous-project error")
	}
	if !strings.Contains(err.Error(), "one visible project") || !strings.Contains(err.Error(), "--project") {
		t.Fatalf("error = %q, want single-project + --project hint", err.Error())
	}
}

func projectRenameServer(t *testing.T, projects string, gotProject, gotBody *string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(projects))
	})
	mux.HandleFunc("PATCH /api/v1/projects/{project}/rename", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		*gotProject = r.PathValue("project")
		*gotBody = readBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"aaaaaaaa-0000-0000-0000-000000000001","previous_name":"alpha","name":"delta","previous_brain_repo":"rootcause-brain-alpha","brain_repo":"rootcause-brain-delta","github_renamed":true,"local_dir_renamed":true,"url":"/projects/delta","extra":"kept"}`))
	})
	return httptest.NewServer(mux)
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
	for _, want := range []string{"UNKNOWN_PROJECT", "charlie", "rc project list"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
