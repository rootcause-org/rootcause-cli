package cli

import (
	"os"
	"strings"
	"testing"
)

// Collection noun commands (repo / connection / member / token): each pins the human table renderer +
// the -o json passthrough, and the sensitive item-verbs (connection reveal, token mint) assert the
// secret reaches stdout once with a stderr warning.

func TestRepoListTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "repo", "ls"); err != nil {
		t.Fatalf("repo ls: %v", err)
	}
	assertGolden(t, "repo_ls.golden", out.String())
}

func TestRepoListJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "project", "repo", "ls"); err != nil {
		t.Fatalf("repo ls -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "repos.json"), out.Bytes())
}

func TestRepoAddTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "repo", "add", "id=momentum-web", "git_url=https://github.com/acme/momentum-web.git"); err != nil {
		t.Fatalf("repo add: %v", err)
	}
	assertGolden(t, "repo_add.golden", out.String())
}

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

func TestRepoSetTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "repo", "set", "momentum-web", "description=Updated"); err != nil {
		t.Fatalf("repo set: %v", err)
	}
	assertGolden(t, "repo_set.golden", out.String())
}

func TestRepoRmTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "repo", "rm", "momentum-web"); err != nil {
		t.Fatalf("repo rm: %v", err)
	}
	if got := out.String(); got != "deleted repos momentum-web\n" {
		t.Errorf("repo rm output = %q", got)
	}
}

func TestTenantListTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "tenant", "ls"); err != nil {
		t.Fatalf("tenant ls: %v", err)
	}
	assertGolden(t, "tenant_ls.golden", out.String())
}

func TestTenantAddTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "tenant", "add", "slug=acme", "name=Acme Dental"); err != nil {
		t.Fatalf("tenant add: %v", err)
	}
	assertGolden(t, "tenant_add.golden", out.String())
}

func TestTenantGetTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "tenant", "get", "acme"); err != nil {
		t.Fatalf("tenant get: %v", err)
	}
	assertGolden(t, "tenant_add.golden", out.String())
}

func TestTenantGetJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "project", "tenant", "get", "acme"); err != nil {
		t.Fatalf("tenant get -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "tenant_item.json"), out.Bytes())
}

func TestConnectionListTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "connection", "ls"); err != nil {
		t.Fatalf("connection ls: %v", err)
	}
	assertGolden(t, "connection_ls.golden", out.String())
}

func TestConnectionListJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "project", "connection", "ls"); err != nil {
		t.Fatalf("connection ls -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "connections.json"), out.Bytes())
}

func TestConnectionAddTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "connection", "add", "name=podio", "kind=api_key"); err != nil {
		t.Fatalf("connection add: %v", err)
	}
	assertGolden(t, "connection_add.golden", out.String())
}

func TestConnectionProbeTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "connection", "probe", "notion.write", "--write", "--notion-page", "page-123", "--cleanup"); err != nil {
		t.Fatalf("connection probe: %v", err)
	}
	assertGolden(t, "connection_probe.golden", out.String())
}

func TestConnectionProbeJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "project", "connection", "probe", "notion.write"); err != nil {
		t.Fatalf("connection probe -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "connection_probe.json"), out.Bytes())
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

func TestConnectionRotateTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "connection", "rotate", "11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatalf("connection rotate: %v", err)
	}
	assertGolden(t, "connection_rotate.golden", out.String())
}

func TestConnectionRmTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "connection", "rm", "11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatalf("connection rm: %v", err)
	}
	if got := out.String(); got != "revoked and deleted connection 11111111-1111-1111-1111-111111111111\n" {
		t.Errorf("connection rm output = %q", got)
	}
}

func TestMemberListTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "member", "ls"); err != nil {
		t.Fatalf("member ls: %v", err)
	}
	assertGolden(t, "member_ls.golden", out.String())
}

func TestMemberListJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "project", "member", "ls"); err != nil {
		t.Fatalf("member ls -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "members.json"), out.Bytes())
}

func TestMemberAddTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "member", "add", "id=carol@acme.test", "role=editor"); err != nil {
		t.Fatalf("member add: %v", err)
	}
	assertGolden(t, "member_add.golden", out.String())
}

func TestMemberRmTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "member", "rm", "bob@acme.test"); err != nil {
		t.Fatalf("member rm: %v", err)
	}
	if got := out.String(); got != "deleted members bob@acme.test\n" {
		t.Errorf("member rm output = %q", got)
	}
}

func TestTokenListTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "token", "ls"); err != nil {
		t.Fatalf("token ls: %v", err)
	}
	assertGolden(t, "token_ls.golden", out.String())
}

func TestTokenListJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "project", "token", "ls"); err != nil {
		t.Fatalf("token ls -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "tokens.json"), out.Bytes())
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

func TestTokenRevokeTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "token", "revoke", "tok_aaaa"); err != nil {
		t.Fatalf("token revoke: %v", err)
	}
	if got := out.String(); got != "revoked token tok_aaaa\n" {
		t.Errorf("token revoke output = %q", got)
	}
}
