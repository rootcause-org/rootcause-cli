package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestActionSettingsProbeFailurePrintsStableDiagnostic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveActionDXProjects(w, r) {
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/action/probe" || r.URL.Query().Get("project") != "alpha" {
			t.Fatalf("request = %s %s", r.Method, r.URL.String())
		}
		_, _ = w.Write([]byte(`{"reachable":true,"status":405,"health":null,"latency_ms":2,"code":"EMBASSY_SIGNATURE_REJECTED","hint":"Use the same reverse secret on both sides.","docs":"https://github.com/rootcause-org/rootcause-embassy/blob/main/docs/integrator/errors.md#embassy_signature_rejected"}`))
	}))
	defer srv.Close()
	e, out, errOut := newTestEnv(t, srv, "table")
	err := run(t, e, "--project", "alpha", "project", "action-settings", "probe")
	if err == nil {
		t.Fatal("expected failure")
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q", out.String())
	}
	want := "EMBASSY_SIGNATURE_REJECTED: Use the same reverse secret on both sides. — https://github.com/rootcause-org/rootcause-embassy/blob/main/docs/integrator/errors.md#embassy_signature_rejected\n"
	if errOut.String() != want {
		t.Fatalf("stderr = %q, want %q", errOut.String(), want)
	}
}

func TestActionReverseSecretRotateStoresAndPrintsOnce(t *testing.T) {
	var gotSecret string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveActionDXProjects(w, r) {
			return
		}
		if r.Method != http.MethodPatch || r.URL.Path != "/api/v1/action" {
			t.Fatalf("request = %s %s", r.Method, r.URL.String())
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		gotSecret = body["action_reverse_secret"]
		_, _ = w.Write([]byte(`{"action_reverse_secret":{"value":"[configured]","effective":"[configured]"}}`))
	}))
	defer srv.Close()
	e, out, errOut := newTestEnv(t, srv, "table")
	if err := run(t, e, "--project", "alpha", "project", "action-settings", "reverse-secret", "rotate"); err != nil {
		t.Fatal(err)
	}
	printed := strings.TrimSpace(out.String())
	if len(gotSecret) != 64 || printed != gotSecret {
		t.Fatalf("stored/printed secret lengths = %d/%d, equal=%t", len(gotSecret), len(printed), printed == gotSecret)
	}
	if !strings.Contains(errOut.String(), "shown once") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestActionDoctorBundleIsRedacted(t *testing.T) {
	secret := "must-not-leak"
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"projects":[{"id":"aaaaaaaa-0000-0000-0000-000000000001","name":"alpha"}]}`))
	})
	mux.HandleFunc("GET /api/v1/console/action/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"project":"alpha","id":"ship_order","manifest":{},"digest":"sha256:abc","preflight":true,"catalog":{"id":"ship_order"}}`))
	})
	mux.HandleFunc("POST /api/v1/action/probe", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"reachable":true,"status":405,"allow_header":"POST","health":{"ok":true,"embassy":"go","version":"1.2.3","protocol":1,"capabilities":["actions"]},"latency_ms":3}`))
	})
	mux.HandleFunc("POST /api/v1/console/action/{id}/preflight", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"project":"alpha","id":"ship_order","status":"succeeded","dry_run":true,"duration_ms":4}`))
	})
	mux.HandleFunc("GET /api/v1/action", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"actions_enabled":{"effective":true},"action_mode":{"effective":"embassy"},"action_runner_url":{"effective":"https://private.example/secret-path"},"action_reverse_secret":{"effective":"` + secret + `"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "")
	if err := run(t, e, "--project", "alpha", "dev", "action", "doctor", "ship_order", "--params", `{"order_id":42}`, "--bundle"); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, forbidden := range []string{secret, "private.example", "order_id"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("bundle leaked %q: %s", forbidden, got)
		}
	}
	for _, required := range []string{`"embassy_version": "1.2.3"`, `"runner_url_configured": true`, `"reverse_secret_configured": true`} {
		if !strings.Contains(got, required) {
			t.Fatalf("bundle missing %q: %s", required, got)
		}
	}
}

func TestActionDoctorFailsWhenPreflightReturnsHTTP200Failure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"projects":[{"id":"aaaaaaaa-0000-0000-0000-000000000001","name":"alpha"}]}`))
	})
	mux.HandleFunc("GET /api/v1/console/action/{id}", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"project":"alpha","id":"ship_order","manifest":{},"digest":"sha256:abc","preflight":true,"catalog":{"id":"ship_order"}}`))
	})
	mux.HandleFunc("POST /api/v1/action/probe", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"reachable":true,"status":405,"health":{"ok":true,"embassy":"go","version":"1.2.3","protocol":1},"latency_ms":3}`))
	})
	mux.HandleFunc("POST /api/v1/console/action/{id}/preflight", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"project":"alpha","id":"ship_order","status":"preflight_failed","dry_run":true,"result":{"ok":false,"error":{"class":"PreflightFailed","message":"private detail"}},"duration_ms":4}`))
	})
	mux.HandleFunc("GET /api/v1/action", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"actions_enabled":{"effective":true},"action_mode":{"effective":"hosted"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, _, errOut := newTestEnv(t, srv, "table")
	if err := run(t, e, "--project", "alpha", "dev", "action", "doctor", "ship_order"); err == nil {
		t.Fatal("doctor succeeded despite preflight_failed")
	}
	if got := errOut.String(); !strings.Contains(got, "ACTION_FAILED:") || strings.Contains(got, "private detail") {
		t.Fatalf("stderr = %q", got)
	}
}

func TestActionDraftTestPassesTenantAndParams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveActionDXProjects(w, r) {
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/actions/drafts/ship_order/test" || r.URL.Query().Get("project") != "alpha" {
			t.Fatalf("request = %s %s", r.Method, r.URL.String())
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["tenant"] != "north" || body["params"].(map[string]any)["order_id"] != float64(42) {
			t.Fatalf("body = %#v", body)
		}
		_, _ = w.Write([]byte(`{"ok":true,"id":"ship_order","confirm_url":"https://app.replypen.com/confirm/redacted"}`))
	}))
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "--project", "alpha", "--tenant", "north", "project", "action", "draft", "test", "ship_order", "--params", `{"order_id":42}`); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"confirm_url"`) {
		t.Fatalf("stdout = %q", out.String())
	}
}

func serveActionDXProjects(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet || r.URL.Path != "/api/v1/projects" {
		return false
	}
	_, _ = w.Write([]byte(`{"projects":[{"id":"aaaaaaaa-0000-0000-0000-000000000001","name":"alpha"}]}`))
	return true
}
