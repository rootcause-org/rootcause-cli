package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

func TestTenantSettingsGetTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "tenant", "settings", "get", "--tenant", "de-kies"); err != nil {
		t.Fatalf("get: %v", err)
	}
	assertGolden(t, "tenant_get.golden", out.String())
}

func TestTenantSettingsGetJSON(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "tenant", "settings", "get", "--tenant", "de-kies"); err != nil {
		t.Fatalf("get -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "hierarchy_tenant_settings.json"), out.Bytes())
}

func TestTenantSettingsGetMissingTenant(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "tenant", "settings", "get")
	if err == nil || !strings.Contains(err.Error(), "--tenant") {
		t.Fatalf("expected --tenant required error, got %v", err)
	}
}

func TestTenantSettingsGet404(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "tenant", "settings", "get", "--tenant", "ghost")
	var apiErr *client.APIError
	if !asAPIError(err, &apiErr) {
		t.Fatalf("expected *client.APIError, got %T: %v", err, err)
	}
	if apiErr.Code != "NOT_FOUND" {
		t.Errorf("code = %q, want NOT_FOUND", apiErr.Code)
	}
}

func TestTenantSettingsSetNestedBody(t *testing.T) {
	var gotBody string
	srv := hierarchyBodyCaptureServer(t, &gotBody)
	defer srv.Close()
	e := newTestEnvAt(t, srv.URL, "table")
	err := run(t, e, "tenant", "settings", "set", "--tenant", "de-kies",
		"persona.tone=tenant crisp",
		"channel.labeling_enabled=true",
		"persona.signature=",
		"--unset", "persona.guidance",
	)
	if err != nil {
		t.Fatalf("set: %v", err)
	}

	var req map[string]map[string]any
	if err := json.Unmarshal([]byte(gotBody), &req); err != nil {
		t.Fatalf("patch body not JSON: %v (body=%s)", err, gotBody)
	}
	want := map[string]map[string]any{
		"persona": {
			"tone":      "tenant crisp",
			"signature": nil,
			"guidance":  nil,
		},
		"channel": {
			"labeling_enabled": true,
		},
	}
	if !reflect.DeepEqual(req, want) {
		t.Errorf("patch body mismatch\n got: %#v\nwant: %#v", req, want)
	}
	if !strings.Contains(gotBody, `"signature":null`) {
		t.Errorf("empty value must send JSON null; body=%s", gotBody)
	}
}

func TestTenantSettingsSetBadBool(t *testing.T) {
	var gotBody string
	srv := hierarchyBodyCaptureServer(t, &gotBody)
	defer srv.Close()
	e := newTestEnvAt(t, srv.URL, "table")
	err := run(t, e, "tenant", "settings", "set", "--tenant", "de-kies", "channel.labeling_enabled=maybe")
	if err == nil || !strings.Contains(err.Error(), "boolean") {
		t.Fatalf("expected boolean-coercion error, got %v", err)
	}
	if gotBody != "" {
		t.Errorf("bad-bool rejection must not hit the server; body=%s", gotBody)
	}
}

func TestTenantSettingsSetServerDetails(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	c := client.New(srv.URL, client.StaticToken("test-key"))
	_, err := c.PatchHierarchySettings(context.Background(), "tenant", "alpha", "de-kies", map[string]any{
		"persona": map[string]any{"tone": "nope"},
	}, true)
	if err == nil {
		t.Fatal("expected server INVALID_SETTINGS")
	}
	var errb bytes.Buffer
	printError(&errb, err)
	got := errb.String()
	if !strings.Contains(got, "INVALID_SETTINGS") || !strings.Contains(got, "persona.tone") {
		t.Errorf("missing details[] field error; got: %q", got)
	}
}

func TestTenantSettingsSchemaDump(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "tenant", "settings", "schema"); err != nil {
		t.Fatalf("schema: %v", err)
	}
	assertJSONEqual(t, fixture(t, "meta_schema.json"), out.Bytes())
	if !strings.Contains(out.String(), "hierarchy_settings") {
		t.Fatalf("schema output missing hierarchy_settings:\n%s", out.String())
	}
}

func TestProjectHierarchySettingsGetJSON(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "config", "hierarchy", "get"); err != nil {
		t.Fatalf("config hierarchy get: %v", err)
	}
	assertJSONEqual(t, fixture(t, "hierarchy_project_settings.json"), out.Bytes())
}

func TestMailboxSettingsSetTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "mailbox", "settings", "set", "11111111-1111-1111-1111-111111111111", "persona.tone=mailbox direct"); err != nil {
		t.Fatalf("mailbox settings set: %v", err)
	}
	if !strings.Contains(out.String(), "mailbox direct (mailbox)") {
		t.Fatalf("mailbox settings output missing resolved mailbox value:\n%s", out.String())
	}
}

func TestRoutesTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "routes"); err != nil {
		t.Fatalf("routes: %v", err)
	}
	if !strings.Contains(out.String(), "GET /api/v1/runs/{id}/trace") || !strings.Contains(out.String(), "deprecated") {
		t.Fatalf("routes output missing manifest rows:\n%s", out.String())
	}
}

func TestOpenAPIJSON(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "openapi"); err != nil {
		t.Fatalf("openapi: %v", err)
	}
	if !strings.Contains(out.String(), `"openapi": "3.1.0"`) || !strings.Contains(out.String(), `/api/v1/runs/{id}/trace`) {
		t.Fatalf("openapi output missing expected contract:\n%s", out.String())
	}
}

func newTestEnvAt(t *testing.T, baseURL, output string) *env {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("ROOTCAUSE_BASE_URL", "")
	var out, errb bytes.Buffer
	return &env{profile: "default", output: output, baseURLOvr: baseURL, tokenOvr: "test-key", out: &out, err: &errb}
}

func hierarchyBodyCaptureServer(t *testing.T, dst *string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"projects":[{"id":"aaaaaaaa-0000-0000-0000-000000000001","name":"alpha"}]}`))
	})
	mux.HandleFunc("GET /api/v1/whoami", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"project":{"id":"aaaaaaaa-0000-0000-0000-000000000001","name":"alpha","slug":"alpha"}}`))
	})
	mux.HandleFunc("PATCH /api/v1/projects/{project}/tenants/{slug}/settings", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		*dst = readBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"scope":"tenant","project":"alpha","tenant":"de-kies","settings":` + *dst + `}`))
	})
	return httptest.NewServer(mux)
}
