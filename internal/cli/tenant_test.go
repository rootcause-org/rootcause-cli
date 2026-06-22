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

// TestTenantSettingsGetTable pins the grouped human render: schema-labelled sections in x-group order,
// fields in x-order within a group, raw key + Dutch label, header with tenant/version.
func TestTenantSettingsGetTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "tenant", "settings", "get", "--tenant", "de-kies"); err != nil {
		t.Fatalf("get: %v", err)
	}
	assertGolden(t, "tenant_get.golden", out.String())
}

// TestTenantSettingsGetJSON asserts -o json passes the whole record through (tenant_id/version/
// applied_at + raw settings), round-tripping to the server's body.
func TestTenantSettingsGetJSON(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "tenant", "settings", "get", "--tenant", "de-kies"); err != nil {
		t.Fatalf("get -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "tenant_settings.json"), out.Bytes())
}

// TestTenantSettingsGetMissingTenant asserts get without --tenant is a clear client-side error.
func TestTenantSettingsGetMissingTenant(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "tenant", "settings", "get")
	if err == nil || !strings.Contains(err.Error(), "--tenant") {
		t.Fatalf("expected --tenant required error, got %v", err)
	}
}

// TestTenantSettingsGet404 asserts an unknown tenant surfaces the server's uniform 404 as a typed
// APIError (NOT_FOUND).
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

// TestTenantSettingsSetSparseBody is the load-bearing contract: `set` sends ONLY the keys given,
// coerced to their schema types, with source:"cli", and an explicit JSON null for `key=` / --unset.
func TestTenantSettingsSetSparseBody(t *testing.T) {
	var gotBody string
	srv := bodyCaptureServer(t, &gotBody)
	defer srv.Close()
	e := newTestEnvAt(t, srv.URL, "table")
	err := run(t, e, "tenant", "settings", "set", "--tenant", "de-kies",
		"reschedule_method=propose_one_book",      // scalar enum
		"latecancel_free_count=2",                 // integer
		"blacklist_enabled=true",                  // boolean
		"general_treatments=dentist,periodontics", // array-with-item-enum
		"contact_phone=",                          // empty value → null (unconfigure)
		"--unset", "header_short_name",            // --unset → null
	)
	if err != nil {
		t.Fatalf("set: %v", err)
	}

	var req struct {
		Settings map[string]any `json:"settings"`
		Source   string         `json:"source"`
	}
	if err := json.Unmarshal([]byte(gotBody), &req); err != nil {
		t.Fatalf("patch body not JSON: %v (body=%s)", err, gotBody)
	}
	if req.Source != "cli" {
		t.Errorf("source = %q, want cli", req.Source)
	}
	want := map[string]any{
		"reschedule_method":     "propose_one_book",
		"latecancel_free_count": float64(2), // JSON number decodes to float64
		"blacklist_enabled":     true,
		"general_treatments":    []any{"dentist", "periodontics"},
		"contact_phone":         nil,
		"header_short_name":     nil,
	}
	if !reflect.DeepEqual(req.Settings, want) {
		t.Errorf("settings body mismatch\n got: %#v\nwant: %#v", req.Settings, want)
	}
	// Sparse: a key never mentioned must be absent entirely (not sent as null).
	if _, present := req.Settings["newpatient_method"]; present {
		t.Errorf("untouched key leaked into the sparse body: %#v", req.Settings)
	}
	// Explicit null must serialize as JSON null — the distinction that drives the unconfigure path.
	if !strings.Contains(gotBody, `"contact_phone":null`) {
		t.Errorf("contact_phone= must send JSON null; body=%s", gotBody)
	}
}

// TestTenantSettingsSetEnumRejectedClientSide asserts a bad enum value fails BEFORE any request — the
// fast client-side coercion check against the fetched schema.
func TestTenantSettingsSetEnumRejectedClientSide(t *testing.T) {
	var gotBody string
	srv := bodyCaptureServer(t, &gotBody)
	defer srv.Close()
	e := newTestEnvAt(t, srv.URL, "table")
	err := run(t, e, "tenant", "settings", "set", "--tenant", "de-kies", "reschedule_method=banana")
	if err == nil {
		t.Fatal("expected client-side enum rejection, got nil")
	}
	if !strings.Contains(err.Error(), "reschedule_method") || !strings.Contains(err.Error(), "propose_one_book") {
		t.Errorf("error should name the field + allowed values, got: %v", err)
	}
	if gotBody != "" {
		t.Errorf("a client-side rejection must not hit the server; body=%s", gotBody)
	}
}

// TestTenantSettingsSetBadInt asserts integer coercion fails client-side with a clear message.
func TestTenantSettingsSetBadInt(t *testing.T) {
	var gotBody string
	srv := bodyCaptureServer(t, &gotBody)
	defer srv.Close()
	e := newTestEnvAt(t, srv.URL, "table")
	err := run(t, e, "tenant", "settings", "set", "--tenant", "de-kies", "latecancel_free_count=lots")
	if err == nil || !strings.Contains(err.Error(), "integer") {
		t.Fatalf("expected integer-coercion error, got %v", err)
	}
	if gotBody != "" {
		t.Errorf("bad-int rejection must not hit the server; body=%s", gotBody)
	}
}

// TestTenantSettingsSetServer400 drives the SERVER 400 validation_failed path explicitly via the
// client (a value the client lets through but the server rejects), asserting field_errors surface
// cleanly through the shared printError path. The CLI's set passes the bad value as a free-text-shaped
// scalar; here we hit the client directly so the test isn't coupled to client-side coercion.
func TestTenantSettingsSetServer400(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	c := client.New(srv.URL, "test-key")
	_, err := c.PatchTenantSettings(context.Background(), "de-kies", client.TenantSettingsPatchRequest{
		Settings: map[string]any{"reschedule_method": "nope"},
		Source:   "cli",
	})
	if err == nil {
		t.Fatal("expected server 400 validation_failed")
	}
	var errb bytes.Buffer
	printError(&errb, err)
	got := errb.String()
	if !strings.Contains(got, "validation_failed") {
		t.Errorf("missing verbatim error code; got: %q", got)
	}
	if !strings.Contains(got, "reschedule_method") {
		t.Errorf("missing field error; got: %q", got)
	}
}

// TestTenantSettingsSetFreeTextSucceeds asserts a free-text (no-enum) key passes client coercion and
// the merged record is echoed/rendered.
func TestTenantSettingsSetFreeTextSucceeds(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, errb := newTestEnv(t, srv, "json")
	if err := run(t, e, "tenant", "settings", "set", "--tenant", "de-kies", "booking_min_leadtime=24 uur"); err != nil {
		t.Fatalf("free-text set should succeed, got: %v (stderr=%s)", err, errb.String())
	}
	if !strings.Contains(out.String(), "24 uur") {
		t.Errorf("merged record should echo the value; got:\n%s", out.String())
	}
}

// TestTenantSettingsSchemaDump asserts `schema` dumps the enriched schema (x-* metadata intact).
func TestTenantSettingsSchemaDump(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "tenant", "settings", "schema"); err != nil {
		t.Fatalf("schema: %v", err)
	}
	assertJSONEqual(t, fixture(t, "tenant_schema.json"), out.Bytes())
	if !strings.Contains(out.String(), "x-groups") || !strings.Contains(out.String(), "x-label-nl") {
		t.Errorf("dump dropped x-* metadata:\n%s", out.String())
	}
}

// newTestEnvAt builds an env pointed at an arbitrary base URL (a per-test stub), capturing stdout. Used
// by the body-assertion tests that need their own recording server rather than the shared stub.
func newTestEnvAt(t *testing.T, baseURL, output string) *env {
	t.Helper()
	t.Setenv("ROOTCAUSE_API_KEY", "test-key")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out, errb bytes.Buffer
	return &env{profile: "default", output: output, baseURLOvr: baseURL, out: &out, err: &errb}
}

// bodyCaptureServer records the PATCH body into *dst and echoes the sent settings back; it also serves
// the schema (the set path fetches it first for coercion). *dst stays "" if no PATCH is made — the
// assertion that a client-side rejection never reaches the server.
func bodyCaptureServer(t *testing.T, dst *string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tenants/settings/schema", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "tenant_schema.json"))
	})
	mux.HandleFunc("PATCH /api/v1/tenants/{slug}/settings", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		*dst = readBody(t, r)
		var req struct {
			Settings json.RawMessage `json:"settings"`
		}
		_ = json.Unmarshal([]byte(*dst), &req)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tenant_id":"de-kies","version":"sha256:patched","applied_at":"2026-06-22T10:00:00Z","settings":` + string(req.Settings) + `}`))
	})
	return httptest.NewServer(mux)
}
