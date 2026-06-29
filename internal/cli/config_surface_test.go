package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Golden + contract tests for the config-surface commands (mailbox / env per-key / database + controls
// / config openrouter-key / branding logo / github / brain / run feedback+retry / admin). Mirrors the
// collections_test.go pattern: a stub server returns canned JSON, the test pins the rendered output (or
// the load-bearing stdout/stderr split for secrets).

// --- watched mailboxes (rc mailbox ls/pause/resume/connect) ---

func TestMailboxWatchedListTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "mailbox", "ls"); err != nil {
		t.Fatalf("mailbox ls: %v", err)
	}
	assertGolden(t, "mailbox_watched_ls.golden", out.String())
}

func TestMailboxWatchedListJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "mailbox", "ls"); err != nil {
		t.Fatalf("mailbox ls -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "watched_mailboxes.json"), out.Bytes())
}

func TestMailboxPauseTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "mailbox", "pause", "11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatalf("mailbox pause: %v", err)
	}
	assertGolden(t, "mailbox_pause.golden", out.String())
}

// TestMailboxResumeNeedsAttention: a resume that hit a Subscribe failure is still a 200 — the item
// carries status:needs_attention + error_message, and the CLI surfaces (not errors on) that message.
func TestMailboxResumeNeedsAttention(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, errb := newTestEnv(t, srv, "table")
	if err := run(t, e, "mailbox", "resume", "needs-attn"); err != nil {
		t.Fatalf("mailbox resume: %v", err)
	}
	assertGolden(t, "mailbox_resume_needs_attn.golden", out.String())
	if !strings.Contains(errb.String(), "needs attention") {
		t.Errorf("expected a needs-attention note on stderr, got: %q", errb.String())
	}
}

// TestMailboxConnectURL: connect makes NO API call beyond whoami — it composes + prints the dashboard
// Connections URL to stdout (with --project resolving the slug) and a one-line hint to stderr.
func TestMailboxConnectURL(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, errb := newTestEnv(t, srv, "table")
	if err := run(t, e, "--project", "momentum-tools", "mailbox", "connect", "--provider", "google"); err != nil {
		t.Fatalf("mailbox connect: %v", err)
	}
	got := strings.TrimSpace(out.String())
	want := srv.URL + "/projects/momentum-tools/connections"
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
	if err := run(t, e, "mailbox", "connect", "--provider", "yahoo"); err == nil {
		t.Fatalf("expected an error for an invalid provider")
	}
}

// --- legacy routing table (rc mailbox route ls/add) ---

func TestMailboxRouteListTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "mailbox", "route", "ls"); err != nil {
		t.Fatalf("mailbox route ls: %v", err)
	}
	assertGolden(t, "mailbox_ls.golden", out.String())
}

func TestMailboxRouteAddTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "mailbox", "route", "add", "mailbox=support@acme.test", "tenant=acme"); err != nil {
		t.Fatalf("mailbox route add: %v", err)
	}
	assertGolden(t, "mailbox_add.golden", out.String())
}

// --- env per-key (secret hygiene: value rides via stdin, never echoed) ---

// TestEnvSetFromStdin: the VALUE is read from stdin (value omitted), reaches the server in the body
// (asserted server-side), and is NEVER echoed back to stdout.
func TestEnvSetFromStdin(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	e.in = strings.NewReader("sk_live_FROM_STDIN\n")
	if err := run(t, e, "env", "set", "key=STRIPE_KEY"); err != nil {
		t.Fatalf("env set: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "sk_live_FROM_STDIN") {
		t.Errorf("env set echoed the secret value: %q", got)
	}
	if got != "set STRIPE_KEY (env_grounding)\n" {
		t.Errorf("env set output = %q", got)
	}
}

// TestEnvSetActionPlane: --plane action targets env_action.
func TestEnvSetActionPlane(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	e.in = strings.NewReader("token123\n")
	if err := run(t, e, "env", "set", "key=PODIO_TOKEN", "--plane", "action"); err != nil {
		t.Fatalf("env set --plane action: %v", err)
	}
	if got := out.String(); got != "set PODIO_TOKEN (env_action)\n" {
		t.Errorf("env set action output = %q", got)
	}
}

func TestEnvRmTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "env", "rm", "STRIPE_KEY"); err != nil {
		t.Fatalf("env rm: %v", err)
	}
	if got := out.String(); got != "deleted STRIPE_KEY (env_grounding)\n" {
		t.Errorf("env rm output = %q", got)
	}
}

// TestEnvRevealSecret: reveal prints the value alone to stdout with a stderr warning (like connection reveal).
func TestEnvRevealSecret(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, errb := newTestEnv(t, srv, "table")
	if err := run(t, e, "env", "reveal", "STRIPE_KEY"); err != nil {
		t.Fatalf("env reveal: %v", err)
	}
	if got := out.String(); got != "sk_live_ENV_REVEALED\n" {
		t.Errorf("env reveal stdout = %q, want the bare secret", got)
	}
	if !strings.Contains(errb.String(), "live secret") {
		t.Errorf("env reveal missing stderr warning: %q", errb.String())
	}
}

// --- database collection + controls ---

func TestDatabaseListTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "database", "ls"); err != nil {
		t.Fatalf("database ls: %v", err)
	}
	assertGolden(t, "database_ls.golden", out.String())
}

func TestDatabaseGetTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "database", "get", "primary"); err != nil {
		t.Fatalf("database get: %v", err)
	}
	assertGolden(t, "database_get.golden", out.String())
}

func TestDatabaseSetTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "database", "set", "primary", "description=Primary OLTP"); err != nil {
		t.Fatalf("database set: %v", err)
	}
	assertGolden(t, "database_get.golden", out.String())
}

func TestDatabaseControlsGetJSON(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "database", "controls", "get", "primary"); err != nil {
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
	if err := run(t, e, "database", "controls", "set", "primary", `{"pii_masked":true}`); err != nil {
		t.Fatalf("database controls set (json): %v", err)
	}
}

// --- config openrouter-key (secret via stdin) ---

func TestOpenRouterKeySetFromStdin(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	e.in = strings.NewReader("sk-or-FROM_STDIN\n")
	if err := run(t, e, "config", "openrouter-key", "set"); err != nil {
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
	if err := run(t, e, "config", "openrouter-key", "clear"); err != nil {
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
	if err := run(t, e, "config", "openrouter-key", "reveal"); err != nil {
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
	if err := run(t, e, "branding", "logo", "set", path); err != nil {
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
	if err := run(t, e, "branding", "logo", "clear"); err != nil {
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
	if err := run(t, e, "github", "status"); err != nil {
		t.Fatalf("github status: %v", err)
	}
	assertGolden(t, "github_status.golden", out.String())
}

func TestGitHubStatusJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "github", "status"); err != nil {
		t.Fatalf("github status -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "github_status.json"), out.Bytes())
}

// --- brain edit / consolidate ---

func TestBrainEditTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "brain", "edit", "add", "a", "runbook", "for", "refunds"); err != nil {
		t.Fatalf("brain edit: %v", err)
	}
	assertGolden(t, "brain_edit.golden", out.String())
}

func TestBrainConsolidateTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "brain", "consolidate"); err != nil {
		t.Fatalf("brain consolidate: %v", err)
	}
	assertGolden(t, "brain_consolidate.golden", out.String())
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

// TestRunFeedbackRequiresInput: with neither --score nor --comment it's a clear client-side error.
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
