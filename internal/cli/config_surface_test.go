package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Golden + contract tests for grouped project/dev surfaces (mailbox / env / database / model key /
// branding / GitHub / brain / run feedback+retry / admin). Mirrors the
// collections_test.go pattern: a stub server returns canned JSON, the test pins the rendered output (or
// the load-bearing stdout/stderr split for secrets).

// --- watched mailboxes (rc project mailbox ls/mode/connect) ---

func TestMailboxWatchedListTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "mailbox", "ls"); err != nil {
		t.Fatalf("project mailbox ls: %v", err)
	}
	assertGolden(t, "mailbox_watched_ls.golden", out.String())
}

func TestMailboxWatchedListJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "project", "mailbox", "ls"); err != nil {
		t.Fatalf("project mailbox ls -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "watched_mailboxes.json"), out.Bytes())
}

func TestMailboxModeTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "--project", "alpha", "project", "mailbox", "mode", "11111111-1111-1111-1111-111111111111", "watch"); err != nil {
		t.Fatalf("project mailbox mode: %v", err)
	}
	assertGolden(t, "mailbox_mode.golden", out.String())
}

// TestMailboxConnectURL: connect makes NO API call beyond whoami — it composes + prints the dashboard
// Connections URL to stdout (with --project resolving the slug) and a one-line hint to stderr.
func TestMailboxConnectURL(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, errb := newTestEnv(t, srv, "table")
	if err := run(t, e, "--project", "alpha", "project", "mailbox", "connect", "--provider", "google"); err != nil {
		t.Fatalf("project mailbox connect: %v", err)
	}
	got := strings.TrimSpace(out.String())
	want := srv.URL + "/projects/alpha/connections"
	if got != want {
		t.Errorf("connect URL = %q, want %q", got, want)
	}
	if !strings.Contains(errb.String(), "Connect google") {
		t.Errorf("expected a connect hint on stderr, got: %q", errb.String())
	}
}

func TestMailboxConnectInvalidProvider(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "mailbox", "connect", "--provider", "yahoo"); err == nil {
		t.Fatalf("expected an error for an invalid provider")
	}
}

// TestMailboxConnectIMAP: the password comes from $RC_MAILBOX_PASSWORD (never argv), the client applies
// the username→email / smtp-host→imap-host defaults (asserted server-side), and success prints the
// created item + a one-line canonical mode hint to stderr.
func TestMailboxConnectIMAP(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	t.Setenv("RC_MAILBOX_PASSWORD", "s3cr3t-from-env")
	e, out, errb := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "mailbox", "connect-imap", "--email", "info@acme.test", "--imap-host", "imap.acme.test"); err != nil {
		t.Fatalf("project mailbox connect-imap: %v", err)
	}
	assertGolden(t, "mailbox_connect_imap.golden", out.String())
	if !strings.Contains(errb.String(), "project mailbox mode mb-imap-1 live") {
		t.Errorf("expected a mailbox-mode hint on stderr, got: %q", errb.String())
	}
}

// TestMailboxConnectIMAPInUse: a duplicate mailbox surfaces the server's 409 MAILBOX_IN_USE verbatim.
func TestMailboxConnectIMAPInUse(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	t.Setenv("RC_MAILBOX_PASSWORD", "s3cr3t-from-env")
	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "project", "mailbox", "connect-imap", "--email", "dupe@acme.test", "--imap-host", "imap.acme.test")
	if err == nil || !strings.Contains(err.Error(), "MAILBOX_IN_USE") {
		t.Fatalf("expected a MAILBOX_IN_USE error, got: %v", err)
	}
}

// --- env per-key (secret hygiene: value rides via stdin, never echoed) ---

// TestEnvSetFromStdin: the VALUE is read from stdin (value omitted), reaches the server in the body
// (asserted server-side), and is NEVER echoed back to stdout.
func TestEnvSetFromStdin(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	e.in = strings.NewReader("sk_live_FROM_STDIN\n")
	if err := run(t, e, "project", "env", "set", "key=STRIPE_KEY"); err != nil {
		t.Fatalf("project env set: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "sk_live_FROM_STDIN") {
		t.Errorf("project env set echoed the secret value: %q", got)
	}
	if got != "set STRIPE_KEY (env_grounding)\n" {
		t.Errorf("project env set output = %q", got)
	}
}

// TestEnvSetActionPlane: --plane action targets env_action.
func TestEnvSetActionPlane(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	e.in = strings.NewReader("token123\n")
	if err := run(t, e, "project", "env", "set", "key=PODIO_TOKEN", "--plane", "action"); err != nil {
		t.Fatalf("project env set --plane action: %v", err)
	}
	if got := out.String(); got != "set PODIO_TOKEN (env_action)\n" {
		t.Errorf("project env set action output = %q", got)
	}
}

func TestEnvRmTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "env", "rm", "STRIPE_KEY"); err != nil {
		t.Fatalf("project env rm: %v", err)
	}
	if got := out.String(); got != "deleted STRIPE_KEY (env_grounding)\n" {
		t.Errorf("project env rm output = %q", got)
	}
}

// TestEnvRevealSecret: reveal prints the value alone to stdout with a stderr warning (like connection reveal).
func TestEnvRevealSecret(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, errb := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "env", "reveal", "STRIPE_KEY"); err != nil {
		t.Fatalf("project env reveal: %v", err)
	}
	if got := out.String(); got != "sk_live_ENV_REVEALED\n" {
		t.Errorf("project env reveal stdout = %q, want the bare secret", got)
	}
	if !strings.Contains(errb.String(), "live secret") {
		t.Errorf("project env reveal missing stderr warning: %q", errb.String())
	}
}

// --- database collection + controls ---

func TestDatabaseListTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "database", "ls"); err != nil {
		t.Fatalf("database ls: %v", err)
	}
	assertGolden(t, "database_ls.golden", out.String())
}

func TestDatabaseGetTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "database", "get", "primary"); err != nil {
		t.Fatalf("database get: %v", err)
	}
	assertGolden(t, "database_get.golden", out.String())
}

func TestDatabaseSetTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "database", "set", "primary", "description=Primary OLTP"); err != nil {
		t.Fatalf("database set: %v", err)
	}
	assertGolden(t, "database_get.golden", out.String())
}

func TestDatabaseControlsGetJSON(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "project", "database", "controls", "get", "primary"); err != nil {
		t.Fatalf("database controls get: %v", err)
	}
	assertJSONEqual(t, fixture(t, "database_controls.json"), out.Bytes())
}

// TestDatabaseControlsSetJSON: a JSON-object arg is sent verbatim as the PATCH body (pii_masked arrives
// as a JSON bool, asserted server-side).
func TestDatabaseControlsSetJSON(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "project", "database", "controls", "set", "primary", `{"pii_masked":true}`); err != nil {
		t.Fatalf("database controls set (json): %v", err)
	}
}

// --- config openrouter-key (secret via stdin) ---

func TestOpenRouterKeySetFromStdin(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	e.in = strings.NewReader("sk-or-FROM_STDIN\n")
	if err := run(t, e, "project", "model-key", "openrouter", "set"); err != nil {
		t.Fatalf("openrouter-key set: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "sk-or-FROM_STDIN") {
		t.Errorf("openrouter-key set echoed the secret: %q", got)
	}
	if got != "OpenRouter key stored\n" {
		t.Errorf("openrouter-key set output = %q", got)
	}
}

func TestOpenRouterKeyClear(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "model-key", "openrouter", "clear"); err != nil {
		t.Fatalf("openrouter-key clear: %v", err)
	}
	if got := out.String(); got != "OpenRouter key cleared\n" {
		t.Errorf("openrouter-key clear output = %q", got)
	}
}

func TestOpenRouterKeyReveal(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, errb := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "model-key", "openrouter", "reveal"); err != nil {
		t.Fatalf("openrouter-key reveal: %v", err)
	}
	if got := out.String(); got != "sk-or-REVEALED_ONCE\n" {
		t.Errorf("openrouter-key reveal stdout = %q, want the bare secret", got)
	}
	if !strings.Contains(errb.String(), "live secret") {
		t.Errorf("openrouter-key reveal missing stderr warning: %q", errb.String())
	}
}

// --- branding logo (multipart) ---

func TestBrandingLogoSetTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "logo.png")
	// A minimal PNG header is enough — the stub asserts the multipart part + filename, not the pixels.
	if err := os.WriteFile(path, []byte("\x89PNG\r\n\x1a\n"), 0o644); err != nil {
		t.Fatalf("write logo: %v", err)
	}
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "branding", "logo", "set", path); err != nil {
		t.Fatalf("branding logo set: %v", err)
	}
	if !strings.Contains(out.String(), "uploaded logo logo.png (image/png") {
		t.Errorf("branding logo set output = %q", out.String())
	}
}

func TestBrandingLogoClear(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "branding", "logo", "clear"); err != nil {
		t.Fatalf("branding logo clear: %v", err)
	}
	if got := out.String(); got != "logo cleared\n" {
		t.Errorf("branding logo clear output = %q", got)
	}
}

// --- github status ---

func TestGitHubStatusTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "github", "status"); err != nil {
		t.Fatalf("github status: %v", err)
	}
	assertGolden(t, "github_status.golden", out.String())
}

func TestGitHubStatusJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "project", "github", "status"); err != nil {
		t.Fatalf("github status -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "github_status.json"), out.Bytes())
}

// --- dev brain status / sync / edit / consolidate / developer access ---

func TestBrainStatusTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "dev", "brain", "status"); err != nil {
		t.Fatalf("dev brain status: %v", err)
	}
	assertGolden(t, "brain_status.golden", out.String())
}

func TestBrainStatusJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "dev", "brain", "status"); err != nil {
		t.Fatalf("dev brain status -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "brain_status.json"), out.Bytes())
}

func TestBrainSyncTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "dev", "brain", "sync"); err != nil {
		t.Fatalf("dev brain sync: %v", err)
	}
	assertGolden(t, "brain_sync.golden", out.String())
}

func TestBrainPromoteTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "dev", "brain", "promote", "--channel", "stable", "--sha", "D2F9DE784AB7CDED001F2B6AC86892795F58A8CE"); err != nil {
		t.Fatalf("dev brain promote: %v", err)
	}
	assertGolden(t, "brain_promote.golden", out.String())
}

func TestBrainPromoteJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "--project", "alpha", "dev", "brain", "promote", "--channel", "stable", "--sha", "d2f9de784ab7cded001f2b6ac86892795f58a8ce"); err != nil {
		t.Fatalf("dev brain promote -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "brain_promote.json"), out.Bytes())
}

// A refusing preflight must render the offenders AND exit non-zero, so a script can gate on it.
func TestBrainPreflightTableRefusesWithNonZeroExit(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "dev", "brain", "preflight", "--sha", "D2F9DE784AB7CDED001F2B6AC86892795F58A8CE")
	if err == nil {
		t.Fatal("a refusing preflight must return an error")
	}
	assertGolden(t, "brain_preflight.golden", out.String())
}

func TestBrainPreflightJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "--project", "alpha", "dev", "brain", "preflight", "--sha", "d2f9de784ab7cded001f2b6ac86892795f58a8ce"); err == nil {
		t.Fatal("a refusing preflight must return an error")
	}
	assertJSONEqual(t, fixture(t, "brain_preflight.json"), out.Bytes())
}

// preflight is project-only: an ambient --tenant would silently narrow a fleet-wide question.
func TestBrainPreflightRejectsTenantSelector(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "--tenant", "de-kies", "dev", "brain", "preflight", "--sha", "d2f9de784ab7cded001f2b6ac86892795f58a8ce")
	if err == nil || !strings.Contains(err.Error(), "--tenant is not supported") {
		t.Fatalf("tenant selector error = %v", err)
	}
}

func TestMirrorRefreshTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "dev", "mirror", "refresh", "--repo", "kampadmin-rootcause-common", "--expect-sha", "D2F9DE784AB7CDED001F2B6AC86892795F58A8CE"); err != nil {
		t.Fatalf("dev mirror refresh: %v", err)
	}
	assertGolden(t, "mirror_refresh.golden", out.String())
}

func TestMirrorRefreshJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "--project", "alpha", "dev", "mirror", "refresh", "--repo", "kampadmin-rootcause-common", "--expect-sha", "d2f9de784ab7cded001f2b6ac86892795f58a8ce"); err != nil {
		t.Fatalf("dev mirror refresh -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "mirror_refresh.json"), out.Bytes())
}

func TestMirrorRefreshRejectsInvalidSHAAndTenantSelector(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"short sha", []string{"dev", "mirror", "refresh", "--repo", "common", "--expect-sha", "222222222222"}, "full 40-character"},
		{"tenant selector", []string{"--tenant", "de-kies", "dev", "mirror", "refresh", "--repo", "common", "--expect-sha", "d2f9de784ab7cded001f2b6ac86892795f58a8ce"}, "--tenant is not supported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &env{out: &strings.Builder{}, err: &strings.Builder{}, tokenOvr: "test", baseURLOvr: "http://127.0.0.1:1", output: "table"}
			err := run(t, e, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestBrainPromoteRejectsInvalidInputsAndTenantSelector(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"channel", []string{"dev", "brain", "promote", "--channel", "main", "--sha", "d2f9de784ab7cded001f2b6ac86892795f58a8ce"}, "stable or edge"},
		{"short sha", []string{"dev", "brain", "promote", "--channel", "stable", "--sha", "222222222222"}, "full 40-character"},
		{"tenant selector", []string{"--tenant", "de-kies", "dev", "brain", "promote", "--channel", "stable", "--sha", "d2f9de784ab7cded001f2b6ac86892795f58a8ce"}, "--tenant is not supported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &env{out: &strings.Builder{}, err: &strings.Builder{}, tokenOvr: "test", baseURLOvr: "http://127.0.0.1:1", output: "table"}
			err := run(t, e, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestBrainPromoteTenantPinnedLoginUsesOnlyProjectRoute(t *testing.T) {
	called := false
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/whoami", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"email":"maintainer@example.test","project":{"id":"p1","name":"dentai"},"tenant":{"id":"t1","slug":"de-kies"}}`))
	})
	mux.HandleFunc("POST /api/v1/projects/dentai/brain/promote", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"NOT_FOUND","message":"not found"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "dev", "brain", "promote", "--channel", "stable", "--sha", "d2f9de784ab7cded001f2b6ac86892795f58a8ce")
	if err == nil {
		t.Fatal("expected tenant-pinned promotion denial")
	}
	if !called {
		t.Fatal("promotion did not use canonical project route")
	}
}

// publishSHA is the canonical 40-hex commit the publish tests promote + verify against.
const publishSHA = "d2f9de784ab7cded001f2b6ac86892795f58a8ce"

// publishStub wires the three endpoints `dev brain publish` chains. syncBody/statusBody are the raw
// JSON each returns; a non-nil promoteErr makes promote fail with that envelope code. Each hit flips
// the matching seen flag so a test can assert the chain stopped at the right gate.
type publishSeen struct{ sync, promote, status bool }

func publishStub(t *testing.T, syncBody, statusBody, promoteCode string) (*httptest.Server, *publishSeen) {
	t.Helper()
	seen := &publishSeen{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/whoami", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"project":{"id":"p1","name":"alpha"}}`))
	})
	mux.HandleFunc("POST /api/v1/projects/alpha/brain/sync", func(w http.ResponseWriter, _ *http.Request) {
		seen.sync = true
		_, _ = w.Write([]byte(syncBody))
	})
	mux.HandleFunc("POST /api/v1/projects/alpha/brain/promote", func(w http.ResponseWriter, r *http.Request) {
		seen.promote = true
		var body struct{ Channel, SHA string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.SHA != publishSHA {
			t.Fatalf("promote sha = %q, want %q", body.SHA, publishSHA)
		}
		if promoteCode != "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, `{"error":{"code":%q,"message":"unreachable"}}`, promoteCode)
			return
		}
		_, _ = w.Write([]byte(`{"project":"alpha","channel":"stable","old_sha":"3333333333333333333333333333333333333333","new_sha":"` + publishSHA + `","changed":true,"idempotent":false}`))
	})
	mux.HandleFunc("GET /api/v1/projects/alpha/brain/status", func(w http.ResponseWriter, _ *http.Request) {
		seen.status = true
		_, _ = w.Write([]byte(statusBody))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, seen
}

const publishSyncOK = `{"project":"alpha","sync":{"fetched":true,"fast_forwarded":true,"manual_reconcile":false,"after":{"available":true,"state":"current"}}}`

func publishStatus(resolved string, matchesOrigin bool) string {
	return fmt.Sprintf(`{"project":"alpha","status":{"available":true,"ref":"main","state":"current","channels":[{"channel":"stable","resolved_sha":%q,"matches_origin":%t,"state":"current"}]}}`, resolved, matchesOrigin)
}

func TestBrainPublishHappyPathJSONEnvelope(t *testing.T) {
	srv, seen := publishStub(t, publishSyncOK, publishStatus(publishSHA, true), "")
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "dev", "brain", "publish", "--channel", "stable", "--sha", strings.ToUpper(publishSHA)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if !seen.sync || !seen.promote || !seen.status {
		t.Fatalf("chain incomplete: %+v", *seen)
	}
	var env struct {
		Project  string `json:"project"`
		Channel  string `json:"channel"`
		SHA      string `json:"sha"`
		OldSHA   string `json:"old_sha"`
		Verified bool   `json:"verified"`
		Sync     struct {
			FastForwarded bool `json:"fast_forwarded"`
		} `json:"sync"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, out.String())
	}
	if env.Project != "alpha" || env.Channel != "stable" || env.SHA != publishSHA || env.OldSHA == "" || !env.Verified || !env.Sync.FastForwarded {
		t.Fatalf("envelope = %+v", env)
	}
}

func TestBrainPublishManualReconcileStopsBeforePromote(t *testing.T) {
	syncReconcile := `{"project":"alpha","sync":{"manual_reconcile":true,"after":{"state":"diverged"}}}`
	srv, seen := publishStub(t, syncReconcile, publishStatus(publishSHA, true), "")
	e, _, _ := newTestEnv(t, srv, "json")
	err := run(t, e, "dev", "brain", "publish", "--channel", "stable", "--sha", publishSHA)
	if err == nil || !strings.Contains(err.Error(), "diverged") {
		t.Fatalf("error = %v, want manual-reconcile", err)
	}
	if seen.promote {
		t.Fatal("promote ran despite manual reconcile")
	}
}

func TestBrainPublishPromoteUnreachableStopsBeforeVerify(t *testing.T) {
	srv, seen := publishStub(t, publishSyncOK, publishStatus(publishSHA, true), "BRAIN_SHA_UNREACHABLE")
	e, _, errb := newTestEnv(t, srv, "json")
	err := run(t, e, "dev", "brain", "publish", "--channel", "stable", "--sha", publishSHA)
	if err == nil {
		t.Fatal("expected promote failure")
	}
	if seen.status {
		t.Fatal("verify ran despite promote failure")
	}
	printError(errb, err)
	if !strings.Contains(errb.String(), "did you `git push`") {
		t.Fatalf("missing push hint: %q", errb.String())
	}
}

func TestBrainPublishVerifyMismatchFails(t *testing.T) {
	srv, seen := publishStub(t, publishSyncOK, publishStatus("aaaa000000000000000000000000000000000000", true), "")
	e, _, _ := newTestEnv(t, srv, "json")
	err := run(t, e, "dev", "brain", "publish", "--channel", "stable", "--sha", publishSHA)
	if err == nil || !strings.Contains(err.Error(), "verify failed") {
		t.Fatalf("error = %v, want verify failure", err)
	}
	if !seen.promote {
		t.Fatal("promote should have run before verify")
	}
}

func TestBrainPublishRejectsInvalidInputsAndTenantSelectors(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"channel", []string{"dev", "brain", "publish", "--channel", "main", "--sha", publishSHA}, "stable or edge"},
		{"short sha", []string{"dev", "brain", "publish", "--channel", "stable", "--sha", "222222222222"}, "full 40-character"},
		{"tenant flag", []string{"--tenant", "de-kies", "dev", "brain", "publish", "--channel", "stable", "--sha", publishSHA}, "--tenant is not supported"},
		{"scope tenant", []string{"--scope", "tenant", "dev", "brain", "publish", "--channel", "stable", "--sha", publishSHA}, "--scope tenant is not supported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &env{out: &strings.Builder{}, err: &strings.Builder{}, tokenOvr: "test", baseURLOvr: "http://127.0.0.1:1", output: "table"}
			err := run(t, e, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestBrainEditTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "dev", "brain", "edit", "add", "a", "runbook", "for", "refunds"); err != nil {
		t.Fatalf("dev brain edit: %v", err)
	}
	assertGolden(t, "brain_edit.golden", out.String())
}

func TestBrainConsolidateTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "dev", "brain", "consolidate"); err != nil {
		t.Fatalf("dev brain consolidate: %v", err)
	}
	assertGolden(t, "brain_consolidate.golden", out.String())
}

func TestBrainDeveloperInviteTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "--project", "alpha", "--tenant", "evident", "dev", "brain", "developer", "invite", "ardeae-praktijk"); err != nil {
		t.Fatalf("dev brain developer invite: %v", err)
	}
	assertGolden(t, "brain_developer_invitation.golden", out.String())
}

func TestBrainDeveloperInviteJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "--project", "alpha", "--tenant", "evident", "dev", "brain", "developer", "invite", "ardeae-praktijk"); err != nil {
		t.Fatalf("dev brain developer invite -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "brain_developer_invitation.json"), out.Bytes())
}

func TestBrainDeveloperInviteActiveOutput(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"projects":[{"id":"p1","name":"alpha"}]}`))
	})
	mux.HandleFunc("POST /api/v1/projects/alpha/tenants/evident/brain/developers/invitations", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"project":"alpha","tenant":"evident","repository":"rootcause-org/rootcause-brain-dentai-tenant-evident","github_handle":"ardeae-praktijk","permission":"write","state":"active"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "--project", "alpha", "--tenant", "evident", "dev", "brain", "developer", "invite", "ardeae-praktijk"); err != nil {
		t.Fatal(err)
	}
	assertGolden(t, "brain_developer_invitation_active.golden", out.String())
}

func TestBrainDeveloperInviteUsesTenantBrainMarker(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, ".rootcause.toml"), []byte("project = \"dentai\"\ntenant = \"evident\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var gotPath string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects/dentai/tenants/evident/brain/developers/invitations", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write(fixture(t, "brain_developer_invitation.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "json")
	e.profile = "" // auto mode discovers project+tenant from the checkout marker.
	if err := run(t, e, "dev", "brain", "developer", "invite", "ardeae-praktijk"); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/projects/dentai/tenants/evident/brain/developers/invitations" {
		t.Fatalf("request path = %q", gotPath)
	}
}

func TestBrainDeveloperInviteRequiresTenant(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"projects":[{"id":"p1","name":"alpha"}]}`))
	})
	mux.HandleFunc("GET /api/v1/whoami", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"project":{"id":"p1","name":"alpha"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "--project", "alpha", "dev", "brain", "developer", "invite", "ardeae-praktijk")
	if err == nil || !strings.Contains(err.Error(), "TENANT_REQUIRED") {
		t.Fatalf("error = %v, want TENANT_REQUIRED", err)
	}
}

func TestBrainDeveloperInviteScopeProjectDoesNotRefillTenantFromLogin(t *testing.T) {
	posted := false
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/whoami", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"project":{"id":"p1","name":"alpha"},"tenant":{"id":"t1","slug":"evident"}}`))
	})
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"projects":[{"id":"p1","name":"alpha"}]}`))
	})
	mux.HandleFunc("POST /api/v1/projects/alpha/tenants/evident/brain/developers/invitations", func(http.ResponseWriter, *http.Request) {
		posted = true
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "--scope", "project", "dev", "brain", "developer", "invite", "ardeae-praktijk")
	if err == nil || !strings.Contains(err.Error(), "TENANT_REQUIRED") {
		t.Fatalf("error = %v, want TENANT_REQUIRED", err)
	}
	if posted {
		t.Fatal("tenant invitation POSTed despite --scope project")
	}
}

// --- dream evidence / triage ---

func TestDreamEvidenceJSON(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "dev", "learning", "evidence", "--limit", "7", "--plane", "triage", "--include-bodies"); err != nil {
		t.Fatalf("dream evidence: %v", err)
	}
	if got := out.String(); !strings.Contains(got, `"feedback"`) || !strings.Contains(got, `"deltas"`) {
		t.Fatalf("dream evidence output missing planes: %s", got)
	}
}

func TestDreamEvidenceRejectsUnknownPlane(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "json")
	err := run(t, e, "dev", "learning", "evidence", "--plane", "journal")
	if err == nil || !strings.Contains(err.Error(), `invalid --plane "journal"`) {
		t.Fatalf("dream evidence bad plane error = %v", err)
	}
}

func TestTriagePolicyAndRules(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "project", "triage", "policy", "get"); err != nil {
		t.Fatalf("triage policy get: %v", err)
	}
	if !strings.Contains(out.String(), `"guidance"`) {
		t.Fatalf("triage policy get output = %s", out.String())
	}
	out.Reset()
	if err := run(t, e, "project", "triage", "policy", "set", "Only answer support requests"); err != nil {
		t.Fatalf("triage policy set: %v", err)
	}
	out.Reset()
	if err := run(t, e, "project", "triage", "rules", "ls"); err != nil {
		t.Fatalf("triage rules ls: %v", err)
	}
	out.Reset()
	if err := run(t, e, "project", "triage", "rules", "add", "effect=skip", "match_kind=subject_contains", "pattern=newsletter", "priority=10", "enabled=false"); err != nil {
		t.Fatalf("triage rules add: %v", err)
	}
	out.Reset()
	if err := run(t, e, "project", "triage", "rules", "set", "rule2", "enabled=true"); err != nil {
		t.Fatalf("triage rules set: %v", err)
	}
	out.Reset()
	if err := run(t, e, "project", "triage", "rules", "rm", "rule2"); err != nil {
		t.Fatalf("triage rules rm: %v", err)
	}
	if !strings.Contains(out.String(), `"deleted": "rule2"`) {
		t.Fatalf("triage rules rm output = %s", out.String())
	}
}

// --- run feedback / retry ---

func TestRunFeedbackTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "run", "feedback", "11111111-1111-1111-1111-111111111111", "--score", "1", "--comment", "great draft"); err != nil {
		t.Fatalf("run feedback: %v", err)
	}
	if got := out.String(); got != "feedback recorded for run 11111111-1111-1111-1111-111111111111\n" {
		t.Errorf("run feedback output = %q", got)
	}
}

// TestRunFeedbackProcessedTable: the operator plane rides a PATCH and prints its own terse line.
func TestRunFeedbackProcessedTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "run", "feedback", "11111111-1111-1111-1111-111111111111", "--processed", "--resolution-note", "fixed the brain skill"); err != nil {
		t.Fatalf("run feedback --processed: %v", err)
	}
	if got := out.String(); got != "feedback marked processed with a resolution note for run 11111111-1111-1111-1111-111111111111\n" {
		t.Errorf("run feedback --processed output = %q", got)
	}
}

// TestRunFeedbackScoreAndProcessed: one invocation spanning both planes does POST then PATCH, and -o json
// renders the merged state the PATCH returned (not the POST ack).
func TestRunFeedbackScoreAndProcessed(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "run", "feedback", "11111111-1111-1111-1111-111111111111",
		"--score", "1", "--comment", "great draft", "--unprocessed"); err != nil {
		t.Fatalf("run feedback score+processed: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `"processed": false`) || !strings.Contains(got, `"comment": "great draft"`) {
		t.Errorf("merged feedback state = %s", got)
	}
	if strings.Contains(got, `"recorded"`) {
		t.Errorf("json should be the PATCH merged state, got %s", got)
	}
}

// TestRunFeedbackProcessedExclusive: the two processed flags contradict each other; reject client-side.
func TestRunFeedbackProcessedExclusive(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "run", "feedback", "11111111-1111-1111-1111-111111111111", "--processed", "--unprocessed")
	if err == nil || !strings.Contains(err.Error(), "processed") {
		t.Fatalf("want mutually-exclusive error, got %v", err)
	}
}

// TestRunFeedbackRequiresInput: with no flag at all it's a clear client-side error.
func TestRunFeedbackRequiresInput(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "run", "feedback", "11111111-1111-1111-1111-111111111111")
	if err == nil || !strings.Contains(err.Error(), "nothing to record") {
		t.Fatalf("want nothing-to-record error, got %v", err)
	}
}

// TestRunRetryPrintsNewID: retry prints the NEW run id on stdout (the table path), capturable for chaining.
func TestRunRetryPrintsNewID(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "run", "retry", "11111111-1111-1111-1111-111111111111", "--tier", "pro"); err != nil {
		t.Fatalf("run retry: %v", err)
	}
	if got := out.String(); got != "99999999-9999-9999-9999-999999999999\n" {
		t.Errorf("run retry output = %q, want the new run id", got)
	}
}

func TestRunProcessThreadPrintsStatusURL(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "run", "process-thread", "thread-1"); err != nil {
		t.Fatalf("run process-thread: %v", err)
	}
	if got := out.String(); got != "/api/v1/projects/alpha/inbox/threads/thread-1\n" {
		t.Errorf("run process-thread output = %q", got)
	}
}

// --- admin ---

func TestAdminUserListTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "admin", "user", "ls"); err != nil {
		t.Fatalf("admin user ls: %v", err)
	}
	assertGolden(t, "admin_user_ls.golden", out.String())
}

func TestAdminUserAddTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "admin", "user", "add", "email=dana@acme.test", "admin=true"); err != nil {
		t.Fatalf("admin user add: %v", err)
	}
	assertGolden(t, "admin_user_add.golden", out.String())
}

func TestAdminProjectListTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "admin", "project", "ls"); err != nil {
		t.Fatalf("admin project ls: %v", err)
	}
	assertGolden(t, "admin_project_ls.golden", out.String())
}

// TestAdminProjectAddShowsSecret: the webhook_secret is printed (in the item) AND the shown-once warning
// goes to stderr.
func TestAdminProjectAddShowsSecret(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, errb := newTestEnv(t, srv, "table")
	if err := run(t, e, "admin", "project", "add", "name=momentum-tools", "default_tier=pro"); err != nil {
		t.Fatalf("admin project add: %v", err)
	}
	if !strings.Contains(out.String(), "whsec_SHOWN_ONCE") {
		t.Errorf("admin project add missing webhook_secret: %q", out.String())
	}
	if !strings.Contains(errb.String(), "shown once") {
		t.Errorf("admin project add missing stderr warning: %q", errb.String())
	}
}

func TestAdminCatalogUpsertTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "admin", "catalog", "upsert", "key=podio", "kind=api_key"); err != nil {
		t.Fatalf("admin catalog upsert: %v", err)
	}
	assertGolden(t, "admin_catalog_upsert.golden", out.String())
}

// `mailbox test` re-runs the live check on a CONNECTED mailbox: it renders the stage checklist and,
// crucially, exits non-zero when a required stage failed so a script/CI can gate on it.
func TestMailboxTestRendersChecklistAndExitCode(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()

	for _, tc := range []struct {
		name    string
		id      string
		wantErr bool
		want    []string
	}{
		{name: "green", id: "green", want: []string{"[ok]   Sign in over IMAP", "connected — all checks passed"}},
		{name: "warning is not a failure", id: "warn", want: []string{"[warn] Place a draft for review", "could not save a test draft", "connected, with a limitation"}},
		{name: "failure exits non-zero", id: "broken", wantErr: true, want: []string{"[FAIL] Sign in over IMAP", "rejected the username or password", "[--]   Open the INBOX", "not connected"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, out, _ := newTestEnv(t, srv, "table")
			err := run(t, e, "--project", "alpha", "project", "mailbox", "test", tc.id)
			if tc.wantErr && err == nil {
				t.Fatal("a failed connection check exited 0 — nothing would gate on it")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("mailbox test: %v", err)
			}
			for _, want := range tc.want {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output missing %q:\n%s", want, out.String())
				}
			}
		})
	}
}

// TestMailboxSeedIMAP: the seed path sends NO password at all (asserted server-side), reports the parked
// awaiting_credential status, and prints the no-login password link on stdout so it can be piped.
func TestMailboxSeedIMAP(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, errb := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "mailbox", "seed-imap", "--email", "info@acme.test", "--imap-host", "imap.acme.test"); err != nil {
		t.Fatalf("project mailbox seed-imap: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "awaiting_credential") {
		t.Errorf("seed output missing the parked status: %q", got)
	}
	if !strings.Contains(got, "https://rc.test/mailbox-password/mb-imap-2?sig=abc") {
		t.Errorf("seed output missing the password link: %q", got)
	}
	if !strings.Contains(errb.String(), "send that link") {
		t.Errorf("expected a send-the-link hint on stderr, got: %q", errb.String())
	}
}

// TestMailboxPasswordLink: the reprint command emits ONLY the URL on stdout, so it pipes cleanly.
func TestMailboxPasswordLink(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "mailbox", "password-link", "mb-imap-2"); err != nil {
		t.Fatalf("project mailbox password-link: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "https://rc.test/mailbox-password/mb-imap-2?sig=abc" {
		t.Errorf("password-link stdout = %q, want just the URL", got)
	}
}
