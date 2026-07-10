package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/config"
)

func TestEveryExecutableCommandDeclaresScope(t *testing.T) {
	e := &env{}
	root := newRootCmd(e, "test")
	var missing []string
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd.Run != nil || cmd.RunE != nil {
			if cmd.Annotations == nil || cmd.Annotations[scopeAnnotation] == "" {
				missing = append(missing, cmd.CommandPath())
			}
		}
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(root)
	if len(missing) > 0 {
		t.Fatalf("executable commands without scope metadata: %s", strings.Join(missing, ", "))
	}
}

func TestCanonicalScopeContracts(t *testing.T) {
	root := newRootCmd(&env{}, "test")
	for path, want := range map[string]scopeSpec{
		"status":                        {Project: true, Tenant: true},
		"run trace":                     {Project: true, Tenant: true},
		"fleet health":                  {Project: true, Tenant: true, AllProjects: true},
		"project connection add":        {Project: true, Tenant: true},
		"project mailbox mode":          {Project: true, Tenant: true},
		"project tenant profile set":    {Project: true},
		"project database controls set": {Project: true},
		"dev console action run":        {Project: true},
		"dev console database query":    {Project: true, Tenant: true},
		"dev api routes":                {},
		"self update":                   {},
	} {
		cmd, _, err := root.Find(strings.Fields(path))
		if err != nil {
			t.Fatalf("find %s: %v", path, err)
		}
		got, err := decodeScopeSpec(cmd.Annotations[scopeAnnotation])
		if err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if got != want {
			t.Errorf("%s scope = %+v, want %+v", path, got, want)
		}
	}
}

func TestUnsupportedSelectorsFailBeforeRequest(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "global tenant", args: []string{"--tenant", "acme", "dev", "api", "routes"}, want: "--tenant is not supported"},
		{name: "fleet-list project", args: []string{"--project", "alpha", "project", "list"}, want: "--project is not supported"},
		{name: "tenant record ambient", args: []string{"--tenant", "acme", "project", "tenant", "profile", "get", "acme"}, want: "--tenant is not supported"},
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

func TestAllRejectsNarrowerScope(t *testing.T) {
	for _, flag := range []string{"--project", "--tenant"} {
		e := &env{baseURLOvr: "http://127.0.0.1:1", tokenOvr: "test", out: &strings.Builder{}, err: &strings.Builder{}}
		err := run(t, e, flag, "alpha", "fleet", "runs", "--all")
		if err == nil || !strings.Contains(err.Error(), "--all cannot be combined") {
			t.Fatalf("%s error = %v", flag, err)
		}
	}
}

func TestCanonicalTenantTreeRequiresProjectOutsideBrain(t *testing.T) {
	e := &env{baseURLOvr: "http://127.0.0.1:1", tokenOvr: "test", out: &strings.Builder{}, err: &strings.Builder{}}
	err := run(t, e, "--tenant", "acme", "project", "repo", "ls")
	if err == nil || !strings.Contains(err.Error(), "--project <project> is required with --tenant") {
		t.Fatalf("error = %v", err)
	}
}

func TestTenantCapableCommandsUseCanonicalTree(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]int{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"projects":[{"id":"aaaaaaaa-0000-0000-0000-000000000001","name":"alpha"}]}`))
	})
	register := func(method, path, body string) {
		mux.HandleFunc(method+" "+path, func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			seen[path]++
			mu.Unlock()
			_, _ = w.Write([]byte(body))
		})
	}
	register("GET", "/api/v1/projects/alpha/tenants/acme/repos", `[]`)
	register("GET", "/api/v1/projects/alpha/tenants/acme/brain/status", `{}`)
	register("GET", "/api/v1/projects/alpha/tenants/acme/mailboxes", `{"mailboxes":[]}`)
	register("GET", "/api/v1/projects/alpha/tenants/acme/exports", `{"exports":[]}`)
	register("GET", "/api/v1/projects/alpha/tenants/acme/triage/policy", `{}`)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	commands := [][]string{
		{"project", "repo", "ls"},
		{"dev", "brain", "status"},
		{"project", "mailbox", "ls"},
		{"project", "corpus", "ls"},
		{"project", "triage", "policy", "get"},
	}
	for _, command := range commands {
		e := newTestEnvAt(t, srv.URL, "json")
		args := append([]string{"--project", "alpha", "--tenant", "acme"}, command...)
		if err := run(t, e, args...); err != nil {
			t.Fatalf("%s: %v", strings.Join(command, " "), err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, path := range []string{
		"/api/v1/projects/alpha/tenants/acme/repos",
		"/api/v1/projects/alpha/tenants/acme/brain/status",
		"/api/v1/projects/alpha/tenants/acme/mailboxes",
		"/api/v1/projects/alpha/tenants/acme/exports",
		"/api/v1/projects/alpha/tenants/acme/triage/policy",
	} {
		if seen[path] != 1 {
			t.Errorf("%s called %d times, want 1", path, seen[path])
		}
	}
}

func TestRunAndObservabilitySelectorsReachURL(t *testing.T) {
	var gotRuns, gotRun string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"projects":[{"id":"aaaaaaaa-0000-0000-0000-000000000001","name":"alpha"}]}`))
	})
	mux.HandleFunc("GET /api/v1/runs", func(w http.ResponseWriter, r *http.Request) {
		gotRuns = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"runs":[],"summary":{}}`))
	})
	mux.HandleFunc("GET /api/v1/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		gotRun = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"run_id":"run-1","status":"done","kind":"prompt","scenario":"raw","category":"ok","created_at":"2026-01-01T00:00:00Z","attachments":[]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	for _, args := range [][]string{
		{"--project", "alpha", "--tenant", "acme", "status"},
		{"--project", "alpha", "--tenant", "acme", "run", "show", "run-1"},
	} {
		e := newTestEnvAt(t, srv.URL, "json")
		if err := run(t, e, args...); err != nil {
			t.Fatal(err)
		}
	}
	if gotRuns != "limit=5&project=alpha&tenant=acme" || gotRun != "project=alpha&tenant=acme" {
		t.Fatalf("queries: runs=%q run=%q", gotRuns, gotRun)
	}
}

func TestTenantTableOutputStartsWithScope(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"projects":[{"id":"aaaaaaaa-0000-0000-0000-000000000001","name":"alpha"}]}`))
	})
	mux.HandleFunc("GET /api/v1/runs", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"runs":[],"summary":{}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var out, errOut strings.Builder
	e := &env{output: "table", baseURLOvr: srv.URL, tokenOvr: "test-key", out: &out, err: &errOut}
	if err := run(t, e, "--project", "alpha", "--tenant", "acme", "status"); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.HasPrefix(got, "Scope: alpha / acme\n") {
		t.Fatalf("output = %q", got)
	}
}

func TestPinnedTenantTableOutputStartsWithScope(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/whoami", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"project":{"name":"alpha"},"tenant":{"slug":"acme"}}`))
	})
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"projects":[{"id":"p-1","name":"alpha"}]}`))
	})
	mux.HandleFunc("GET /api/v1/runs", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("project") != "alpha" || r.URL.Query().Get("tenant") != "acme" {
			t.Fatalf("scope query = %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"runs":[],"summary":{}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var out, errOut strings.Builder
	e := &env{output: "table", baseURLOvr: srv.URL, tokenOvr: "test-key", out: &out, err: &errOut}
	if err := run(t, e, "status"); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.HasPrefix(got, "Scope: alpha / acme\n") {
		t.Fatalf("output = %q", got)
	}
}

func TestResolveProjectForTenantFromLogin(t *testing.T) {
	for _, tc := range []struct {
		name   string
		whoami string
		want   string
		code   string
	}{
		{name: "pinned", whoami: `{"project":{"id":"p-1","name":"alpha"}}`, want: "alpha"},
		{name: "all projects", whoami: `{"all_projects":true}`, code: "PROJECT_REQUIRED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/whoami" {
					t.Fatalf("path = %s", r.URL.Path)
				}
				_, _ = w.Write([]byte(tc.whoami))
			}))
			defer srv.Close()
			e := &env{tenant: "acme", resolved: config.Resolved{}}
			err := e.resolveProjectForTenant(client.New(srv.URL, client.StaticToken("test")))
			if tc.code != "" {
				var apiErr *client.APIError
				if !asAPIError(err, &apiErr) || apiErr.Code != tc.code {
					t.Fatalf("error = %v, want %s", err, tc.code)
				}
				return
			}
			if err != nil || e.scopeProject() != tc.want {
				t.Fatalf("project=%q err=%v, want %q", e.scopeProject(), err, tc.want)
			}
		})
	}
}
