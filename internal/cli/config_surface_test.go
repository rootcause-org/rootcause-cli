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

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// Golden + contract tests for grouped project/dev surfaces (mailbox / env / database / model key /
// branding / GitHub / brain / run feedback+retry / admin). Mirrors the
// collections_test.go pattern: a stub server returns canned JSON, the test pins the rendered output (or
// the load-bearing stdout/stderr split for secrets).

// --- watched mailboxes (rc project mailbox ls/mode/connect) ---

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

// --- dev brain status / sync / edit / consolidate / developer access ---

// A 409 BRAIN_BOOT_CHECK_FAILED is terminal, not a transient sync failure: `rc dev brain sync` must
// exit non-zero and say the box kept its last-good commit rather than parroting the raw API error.
func TestBrainSyncBootCheckRefusal(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/whoami", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"email":"dev@example.test","project":{"id":"aaaaaaaa-0000-0000-0000-000000000001","name":"alpha","slug":"alpha"}}`))
	})
	mux.HandleFunc("POST /api/v1/projects/{project}/brain/sync", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"BRAIN_BOOT_CHECK_FAILED","message":"run-view symlink target missing: skills/shared"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "dev", "brain", "sync")
	if err == nil {
		t.Fatal("brain sync on a failed boot check should error")
	}
	if code := exitCodeFor(err); code == exitOK {
		t.Errorf("exit code = %d, want non-zero", code)
	}
	want := "brain sync refused: boot check failed — run-view symlink target missing: skills/shared; local main kept at last-good"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
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
			e := &env{out: &strings.Builder{}, err: &strings.Builder{}, tokenSource: testTokenSource("test"), baseURLOvr: "http://127.0.0.1:1", output: "table"}
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
			e := &env{out: &strings.Builder{}, err: &strings.Builder{}, tokenSource: testTokenSource("test"), baseURLOvr: "http://127.0.0.1:1", output: "table"}
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
			e := &env{out: &strings.Builder{}, err: &strings.Builder{}, tokenSource: testTokenSource("test"), baseURLOvr: "http://127.0.0.1:1", output: "table"}
			err := run(t, e, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
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

func TestDreamEvidenceShadowAliasAndFilters(t *testing.T) {
	var queries []map[string]string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/dream/evidence", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		queries = append(queries, map[string]string{
			"plane":          q.Get("plane"),
			"shadow":         q.Get("shadow"),
			"verdict":        q.Get("verdict"),
			"days":           q.Get("days"),
			"include_bodies": q.Get("include_bodies"),
		})
		_, _ = w.Write(fixture(t, "dream_evidence_shadow.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "dev", "learning", "evidence", "--plane", "shadow", "--verdict", "divergent_facts,missed_content", "--days", "14", "--include-bodies"); err != nil {
		t.Fatalf("shadow evidence alias: %v", err)
	}
	assertJSONEqual(t, fixture(t, "dream_evidence_shadow.json"), out.Bytes())

	var typed client.DreamEvidenceResponse
	decodeJSON(t, out.Bytes(), &typed)
	if len(typed.Deltas) != 1 || !typed.Deltas[0].Shadow || typed.Deltas[0].ShadowVerdict != "missed_content" || typed.Deltas[0].ServedScore == nil || *typed.Deltas[0].ServedScore != 3 || typed.Deltas[0].Topic == "" || typed.Deltas[0].QuestionExcerpt == "" {
		t.Fatalf("typed shadow evidence lost contract fields: %+v", typed.Deltas)
	}

	e, _, _ = newTestEnv(t, srv, "json")
	if err := run(t, e, "dev", "learning", "evidence", "--plane", "deltas", "--shadow=false"); err != nil {
		t.Fatalf("live evidence filter: %v", err)
	}

	if len(queries) != 2 {
		t.Fatalf("evidence requests = %d, want 2", len(queries))
	}
	if got := queries[0]; got["plane"] != "deltas" || got["shadow"] != "true" || got["verdict"] != "divergent_facts,missed_content" || got["days"] != "14" || got["include_bodies"] != "true" {
		t.Fatalf("shadow alias query = %#v", got)
	}
	if got := queries[1]; got["plane"] != "deltas" || got["shadow"] != "false" || got["verdict"] != "" || got["days"] != "" || got["include_bodies"] != "" {
		t.Fatalf("explicit live query = %#v", got)
	}
}

func TestDreamEvidenceRejectsInvalidShadowFlags(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"dev", "learning", "evidence", "--verdict", "close_enough"}, want: `invalid --verdict "close_enough"`},
		{args: []string{"dev", "learning", "evidence", "--plane", "shadow", "--shadow=false"}, want: `--plane shadow conflicts with --shadow=false`},
		{args: []string{"dev", "learning", "evidence", "--days", "0"}, want: `--days must be greater than zero`},
		{args: []string{"dev", "learning", "evidence", "--days", "-1"}, want: `--days must be greater than zero`},
	} {
		e, _, _ := newTestEnv(t, srv, "json")
		err := run(t, e, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v error = %v, want %q", tc.args, err, tc.want)
		}
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

// --- admin ---

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

// --- parseSetArgs value coercion ---

// The literal `null` must reach the server as JSON null for every kind (nullable knobs reset to
// inherit); the empty value keeps its per-kind clear meaning.
func TestParseSetArgsNullAndKinds(t *testing.T) {
	coerce := func(key string) valueKind {
		switch key {
		case "chat_hot_ttl_secs", "max_run_usd":
			return kindNumber
		case "pr.triggers":
			return kindList
		case "actions_enabled":
			return kindBool
		case "models.agent":
			return kindObject
		default:
			return kindString
		}
	}
	cases := []struct {
		name string
		arg  string
		want string // json.Marshal of the single patch value
	}{
		{"number null", "chat_hot_ttl_secs=null", `null`},
		{"string null", "persona.tone=null", `null`},
		{"bool null", "actions_enabled=null", `null`},
		{"list null", "pr.triggers=null", `null`},
		{"object null", "models.agent=null", `null`},
		{"number value", "max_run_usd=5", `5`},
		{"list clear stays empty array", "pr.triggers=", `[]`},
		{"object clear stays empty object", "models.agent=", `{}`},
		{"quoted null is a string", `persona.tone="null"`, `"\"null\""`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			patch, err := parseSetArgs([]string{tc.arg}, coerce)
			if err != nil {
				t.Fatalf("parseSetArgs(%q): %v", tc.arg, err)
			}
			key, _, _ := strings.Cut(tc.arg, "=")
			val, ok := patch[key]
			if !ok {
				t.Fatalf("patch missing key %q: %#v", key, patch)
			}
			b, err := json.Marshal(val)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(b) != tc.want {
				t.Fatalf("got %s, want %s", b, tc.want)
			}
		})
	}
}
