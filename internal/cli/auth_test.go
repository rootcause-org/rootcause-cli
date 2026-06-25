package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rootcause-org/rootcause-cli/internal/token"
)

// oauthStub is a minimal OAuth server: a device flow that approves after the first poll, a refresh that
// rotates, and a revoke. It lets the login/refresh/logout paths run headlessly (no browser).
type oauthStub struct {
	srv       *httptest.Server
	polls     atomic.Int32
	revoked   atomic.Int32
	refreshes atomic.Int32
}

func newOAuthStub(t *testing.T) *oauthStub {
	t.Helper()
	s := &oauthStub{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oauth/device_authorization", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"device_code":"rcod_dev","user_code":"WDJB-MJHT",` +
			`"verification_uri":"https://rc.example/oauth/device",` +
			`"verification_uri_complete":"https://rc.example/oauth/device?user_code=WDJB-MJHT",` +
			`"expires_in":300,"interval":1}`))
	})
	mux.HandleFunc("POST /oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		switch r.PostFormValue("grant_type") {
		case "urn:ietf:params:oauth:grant-type:device_code":
			// First poll: still pending; second: approved (exercises the poll loop).
			if s.polls.Add(1) < 2 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"rcoa_first","refresh_token":"rcor_first","token_type":"Bearer","expires_in":3600}`))
		case "refresh_token":
			s.refreshes.Add(1)
			if r.PostFormValue("refresh_token") == "rcor_dead" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"expired"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"rcoa_refreshed","refresh_token":"rcor_rotated","token_type":"Bearer","expires_in":3600}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"unsupported_grant_type"}`))
		}
	})
	mux.HandleFunc("POST /oauth/revoke", func(w http.ResponseWriter, _ *http.Request) {
		s.revoked.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

// isolatedConfig points XDG at a temp dir so the token store is per-test.
func isolatedConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("ROOTCAUSE_BASE_URL", "")
}

// TestLoginDeviceStoresToken: `rc login --device` runs the device flow and persists the token under the
// resolved profile at 0600.
func TestLoginDeviceStoresToken(t *testing.T) {
	isolatedConfig(t)
	stub := newOAuthStub(t)

	var out, errb bytes.Buffer
	e := &env{profile: "default", baseURLOvr: stub.srv.URL, out: &out, err: &errb}
	if err := run(t, e, "login", "--device"); err != nil {
		t.Fatalf("login --device: %v (stderr=%s)", err, errb.String())
	}
	if stub.polls.Load() < 2 {
		t.Errorf("expected the poll loop to run at least twice, got %d", stub.polls.Load())
	}

	tok, ok, err := token.Load("default")
	if err != nil || !ok {
		t.Fatalf("token not stored: ok=%v err=%v", ok, err)
	}
	if tok.AccessToken != "rcoa_first" || tok.RefreshToken != "rcor_first" {
		t.Errorf("stored token wrong: %+v", tok)
	}
	if tok.BaseURL != stub.srv.URL {
		t.Errorf("token base URL = %q, want %q", tok.BaseURL, stub.srv.URL)
	}
	if tok.ExpiresAt.Before(time.Now()) {
		t.Errorf("expiry not set into the future: %v", tok.ExpiresAt)
	}
	// 0600 on disk.
	p, _ := token.Path()
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat token store: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("token store mode = %o, want 600", perm)
	}
}

// TestLogoutRevokesAndClears: `rc logout` revokes server-side and clears the local store.
func TestLogoutRevokesAndClears(t *testing.T) {
	isolatedConfig(t)
	stub := newOAuthStub(t)
	seedToken(t, "default", token.Token{
		AccessToken: "rcoa_x", RefreshToken: "rcor_x",
		ExpiresAt: time.Now().Add(time.Hour), BaseURL: stub.srv.URL,
	})

	var out, errb bytes.Buffer
	e := &env{profile: "default", baseURLOvr: stub.srv.URL, out: &out, err: &errb}
	if err := run(t, e, "logout"); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if stub.revoked.Load() == 0 {
		t.Error("expected logout to revoke server-side")
	}
	if _, ok, _ := token.Load("default"); ok {
		t.Error("token must be cleared after logout")
	}
}

// TestWhoamiLocal: `rc whoami` reports the brain project + logged-in status from local state, no server.
func TestWhoamiLocal(t *testing.T) {
	isolatedConfig(t)
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, ".rootcause.toml"),
		[]byte("project = \"momentum-tools\"\nbase_url = \"https://rc.example\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	seedToken(t, "momentum-tools", token.Token{
		AccessToken: "rcoa_x", RefreshToken: "rcor_x",
		ExpiresAt: time.Now().Add(time.Hour), BaseURL: "https://rc.example",
	})

	var out, errb bytes.Buffer
	e := &env{output: "table", out: &out, err: &errb}
	if err := run(t, e, "whoami"); err != nil {
		t.Fatalf("whoami: %v", err)
	}
	got := out.String()
	for _, want := range []string{"momentum-tools", "https://rc.example", "logged in"} {
		if !strings.Contains(got, want) {
			t.Errorf("whoami missing %q\n--- got ---\n%s", want, got)
		}
	}
}

func TestWhoamiBrainFallsBackToDefaultProfile(t *testing.T) {
	isolatedConfig(t)
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, ".rootcause.toml"),
		[]byte("project = \"pro-backup\"\nbase_url = \"https://rc.example\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	seedToken(t, "default", token.Token{
		AccessToken: "rcoa_x", RefreshToken: "rcor_x",
		ExpiresAt: time.Now().Add(time.Hour), BaseURL: "https://rc.example",
	})

	var out, errb bytes.Buffer
	e := &env{output: "table", out: &out, err: &errb}
	if err := run(t, e, "whoami"); err != nil {
		t.Fatalf("whoami: %v", err)
	}
	got := out.String()
	for _, want := range []string{"profile:   default", "project:   pro-backup", "brain scope via default profile", "logged in"} {
		if !strings.Contains(got, want) {
			t.Errorf("whoami missing %q\n--- got ---\n%s", want, got)
		}
	}
}

func TestWhoamiShowsLocalTenantSource(t *testing.T) {
	isolatedConfig(t)
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, ".rootcause.toml"),
		[]byte("project = \"dentai\"\nbase_url = \"https://rc.example\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".rootcause"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".rootcause", "local.toml"),
		[]byte("tenant = \"de-kies\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	seedToken(t, "dentai", token.Token{
		AccessToken: "rcoa_x", RefreshToken: "rcor_x",
		ExpiresAt: time.Now().Add(time.Hour), BaseURL: "https://rc.example",
	})

	var out, errb bytes.Buffer
	e := &env{output: "table", out: &out, err: &errb}
	if err := run(t, e, "whoami"); err != nil {
		t.Fatalf("whoami: %v", err)
	}
	got := out.String()
	for _, want := range []string{"profile:   dentai", "project:   dentai", "tenant:    de-kies (.rootcause/local.toml)"} {
		if !strings.Contains(got, want) {
			t.Errorf("whoami missing %q\n--- got ---\n%s", want, got)
		}
	}
}

func TestWhoamiJSONIncludesTenantSource(t *testing.T) {
	isolatedConfig(t)
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, ".rootcause.toml"),
		[]byte("project = \"dentai\"\nbase_url = \"https://rc.example\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	seedToken(t, "dentai", token.Token{
		AccessToken: "rcoa_x", RefreshToken: "rcor_x",
		ExpiresAt: time.Now().Add(time.Hour), BaseURL: "https://rc.example",
	})

	var out, errb bytes.Buffer
	e := &env{output: "json", out: &out, err: &errb}
	if err := run(t, e, "--tenant", "de-kies", "whoami"); err != nil {
		t.Fatalf("whoami: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode whoami json: %v\n%s", err, out.String())
	}
	if got["tenant"] != "de-kies" || got["tenant_source"] != "--tenant" {
		t.Fatalf("tenant/source = %v/%v, want de-kies/--tenant", got["tenant"], got["tenant_source"])
	}
}

// TestRefreshOn401: an expired stored access token is refreshed transparently before the request, and
// the rotated pair is persisted.
func TestRefreshOn401(t *testing.T) {
	isolatedConfig(t)
	stub := newOAuthStub(t)

	// A stored token already past expiry → Token() refreshes pre-emptively.
	seedToken(t, "default", token.Token{
		AccessToken: "rcoa_stale", RefreshToken: "rcor_live",
		ExpiresAt: time.Now().Add(-time.Hour), BaseURL: stub.srv.URL,
	})

	src := newLiveSource("default", stub.srv.URL)
	got, err := src.Token(t.Context())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if got != "rcoa_refreshed" {
		t.Errorf("token = %q, want the refreshed access token", got)
	}
	if stub.refreshes.Load() != 1 {
		t.Errorf("expected exactly one refresh, got %d", stub.refreshes.Load())
	}
	// The rotated refresh token is persisted for next time.
	stored, _, _ := token.Load("default")
	if stored.RefreshToken != "rcor_rotated" || stored.AccessToken != "rcoa_refreshed" {
		t.Errorf("rotated pair not persisted: %+v", stored)
	}
}

// TestRefreshConcurrentRotation: when a refresh gets invalid_grant because a SIBLING process already
// rotated the token (parallel agents), we adopt the sibling's fresh access token instead of forcing a
// spurious re-login. Simulated by having the stub write a fresh store entry as a side effect of the
// invalid_grant response.
func TestRefreshConcurrentRotation(t *testing.T) {
	isolatedConfig(t)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		// The stale refresh is rejected — but, like a sibling that rotated first, we leave a fresh
		// (unexpired) token in the store before responding.
		_ = token.Save("default", token.Token{
			AccessToken: "rcoa_sibling", RefreshToken: "rcor_sibling",
			ExpiresAt: time.Now().Add(time.Hour), BaseURL: "http://stub",
		})
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	seedToken(t, "default", token.Token{
		AccessToken: "rcoa_stale", RefreshToken: "rcor_stale",
		ExpiresAt: time.Now().Add(-time.Hour), BaseURL: srv.URL,
	})

	src := newLiveSource("default", srv.URL)
	got, err := src.Token(t.Context())
	if err != nil {
		t.Fatalf("expected to adopt the sibling's token, got error: %v", err)
	}
	if got != "rcoa_sibling" {
		t.Errorf("token = %q, want the sibling's fresh access token", got)
	}
}

// TestRefreshDeadTokenPromptsReauth: a dead refresh token surfaces a "run `rc login`" error.
func TestRefreshDeadTokenPromptsReauth(t *testing.T) {
	isolatedConfig(t)
	stub := newOAuthStub(t)
	seedToken(t, "default", token.Token{
		AccessToken: "rcoa_stale", RefreshToken: "rcor_dead",
		ExpiresAt: time.Now().Add(-time.Hour), BaseURL: stub.srv.URL,
	})

	src := newLiveSource("default", stub.srv.URL)
	_, err := src.Token(t.Context())
	if err == nil || !strings.Contains(err.Error(), "rc login") {
		t.Fatalf("expected a re-login prompt, got %v", err)
	}
}

// seedToken writes a token into the (isolated) store for a profile.
func seedToken(t *testing.T, profile string, tok token.Token) {
	t.Helper()
	if err := token.Save(profile, tok); err != nil {
		t.Fatalf("seed token: %v", err)
	}
}
