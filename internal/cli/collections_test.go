package cli

import (
	"os"
	"strings"
	"testing"
)

// Collection noun commands (repo / connection / member / token): each pins the human table renderer +
// the -o json passthrough, and the sensitive item-verbs (connection reveal, token mint) assert the
// secret reaches stdout once with a stderr warning.

func TestCollectionLargeJSONValueSpillsButSecretsStayRaw(t *testing.T) {
	t.Setenv("RC_OUTPUT_SPILL_THRESHOLD", "80")
	srv := stubServer(t)
	defer srv.Close()

	outDir := t.TempDir()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "--out-dir", outDir, "--no-preview", "project", "repo", "add", "id=large-output", "git_url=https://github.com/acme/large-output.git"); err != nil {
		t.Fatalf("repo add large -o json: %v", err)
	}
	m := requireSpillManifest(t, out.Bytes())
	if m.Artifacts["response"].Path == "" || m.Artifacts["description"].Path == "" {
		t.Fatalf("collection manifest missing response/description artifacts: %s", out.String())
	}
	b, err := os.ReadFile(m.Artifacts["description"].Path)
	if err != nil {
		t.Fatalf("read description artifact: %v", err)
	}
	if !strings.Contains(string(b), "large collection value") {
		t.Fatalf("description artifact missing large value: %q", string(b))
	}

	rawDir := t.TempDir()
	eRaw, rawOut, _ := newTestEnv(t, srv, "json")
	if err := run(t, eRaw, "--out-dir", rawDir, "--raw-output", "project", "repo", "add", "id=large-output", "git_url=https://github.com/acme/large-output.git"); err != nil {
		t.Fatalf("repo add large --raw-output: %v", err)
	}
	if strings.Contains(rawOut.String(), `"spilled": true`) || !strings.Contains(rawOut.String(), "large collection value") {
		t.Fatalf("collection raw output not preserved:\n%s", rawOut.String())
	}
	if entries, err := os.ReadDir(rawDir); err != nil {
		t.Fatalf("read raw dir: %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("--raw-output wrote artifacts: %v", entries)
	}

	eReveal, revealOut, revealErr := newTestEnv(t, srv, "table")
	if err := run(t, eReveal, "project", "connection", "reveal", "11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatalf("connection reveal under spill threshold: %v", err)
	}
	if got := revealOut.String(); got != "sk_live_REVEALED_ONCE\n" || strings.Contains(got, "output too large") {
		t.Fatalf("reveal stdout changed: %q", got)
	}
	if !strings.Contains(revealErr.String(), "live secret") {
		t.Fatalf("reveal warning missing: %q", revealErr.String())
	}

	revealJSONDir := t.TempDir()
	eRevealJSON, revealJSONOut, _ := newTestEnv(t, srv, "json")
	if err := run(t, eRevealJSON, "--out-dir", revealJSONDir, "project", "connection", "reveal", "11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatalf("connection reveal -o json under spill threshold: %v", err)
	}
	assertJSONEqual(t, []byte(`{"secret":"sk_live_REVEALED_ONCE"}`), revealJSONOut.Bytes())
	if entries, err := os.ReadDir(revealJSONDir); err != nil {
		t.Fatalf("read reveal json dir: %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("reveal -o json wrote artifacts: %v", entries)
	}

	eMint, mintOut, mintErr := newTestEnv(t, srv, "table")
	if err := run(t, eMint, "project", "token", "mint", "scope=config:read"); err != nil {
		t.Fatalf("token mint under spill threshold: %v", err)
	}
	if !strings.Contains(mintOut.String(), "rc_refresh_MINTED_ONCE") || strings.Contains(mintOut.String(), "output too large") {
		t.Fatalf("mint stdout changed: %q", mintOut.String())
	}
	if !strings.Contains(mintErr.String(), "shown once") {
		t.Fatalf("mint warning missing: %q", mintErr.String())
	}

	mintJSONDir := t.TempDir()
	eMintJSON, mintJSONOut, _ := newTestEnv(t, srv, "json")
	if err := run(t, eMintJSON, "--out-dir", mintJSONDir, "project", "token", "mint", "scope=config:read"); err != nil {
		t.Fatalf("token mint -o json under spill threshold: %v", err)
	}
	assertJSONEqual(t, []byte(`{"refresh_token":"rc_refresh_MINTED_ONCE","scope":"config:read","status":"active"}`), mintJSONOut.Bytes())
	if entries, err := os.ReadDir(mintJSONDir); err != nil {
		t.Fatalf("read mint json dir: %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("mint -o json wrote artifacts: %v", entries)
	}
}

// TestConnectionRevealSecret: reveal prints the secret VALUE alone to stdout (captureable) and warns on
// stderr that it's sensitive and shown once.
func TestConnectionRevealSecret(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, errb := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "connection", "reveal", "11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatalf("connection reveal: %v", err)
	}
	if got := out.String(); got != "sk_live_REVEALED_ONCE\n" {
		t.Errorf("reveal stdout = %q, want the bare secret", got)
	}
	if !strings.Contains(errb.String(), "live secret") {
		t.Errorf("reveal missing stderr warning: %q", errb.String())
	}
}

func TestConnectionRevealJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "project", "connection", "reveal", "11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatalf("connection reveal -o json: %v", err)
	}
	assertJSONEqual(t, []byte(`{"secret":"sk_live_REVEALED_ONCE"}`), out.Bytes())
}

// TestTokenMintShowsRefreshToken: mint surfaces the refresh token once (printed) with a stderr warning.
func TestTokenMintShowsRefreshToken(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, errb := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "token", "mint", "scope=config:read"); err != nil {
		t.Fatalf("token mint: %v", err)
	}
	if !strings.Contains(out.String(), "rc_refresh_MINTED_ONCE") {
		t.Errorf("mint stdout missing refresh token: %q", out.String())
	}
	if !strings.Contains(errb.String(), "shown once") {
		t.Errorf("mint missing stderr warning: %q", errb.String())
	}
}

func TestTokenMintJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "project", "token", "mint", "scope=config:read"); err != nil {
		t.Fatalf("token mint -o json: %v", err)
	}
	assertJSONEqual(t, []byte(`{"refresh_token":"rc_refresh_MINTED_ONCE","scope":"config:read","status":"active"}`), out.Bytes())
}
