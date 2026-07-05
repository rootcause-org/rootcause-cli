package cli

import (
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
	if err := run(t, e, "repo", "ls"); err != nil {
		t.Fatalf("repo ls: %v", err)
	}
	assertGolden(t, "repo_ls.golden", out.String())
}

func TestRepoListJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "repo", "ls"); err != nil {
		t.Fatalf("repo ls -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "repos.json"), out.Bytes())
}

func TestRepoAddTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "repo", "add", "id=momentum-web", "git_url=https://github.com/acme/momentum-web.git"); err != nil {
		t.Fatalf("repo add: %v", err)
	}
	assertGolden(t, "repo_add.golden", out.String())
}

func TestRepoSetTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "repo", "set", "momentum-web", "description=Updated"); err != nil {
		t.Fatalf("repo set: %v", err)
	}
	assertGolden(t, "repo_set.golden", out.String())
}

func TestRepoRmTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "repo", "rm", "momentum-web"); err != nil {
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
	if err := run(t, e, "tenant", "ls"); err != nil {
		t.Fatalf("tenant ls: %v", err)
	}
	assertGolden(t, "tenant_ls.golden", out.String())
}

func TestTenantAddTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "tenant", "add", "slug=acme", "name=Acme Dental"); err != nil {
		t.Fatalf("tenant add: %v", err)
	}
	assertGolden(t, "tenant_add.golden", out.String())
}

func TestTenantGetTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "tenant", "get", "acme"); err != nil {
		t.Fatalf("tenant get: %v", err)
	}
	assertGolden(t, "tenant_add.golden", out.String())
}

func TestTenantGetJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "tenant", "get", "acme"); err != nil {
		t.Fatalf("tenant get -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "tenant_item.json"), out.Bytes())
}

func TestConnectionListTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "connection", "ls"); err != nil {
		t.Fatalf("connection ls: %v", err)
	}
	assertGolden(t, "connection_ls.golden", out.String())
}

func TestConnectionListJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "connection", "ls"); err != nil {
		t.Fatalf("connection ls -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "connections.json"), out.Bytes())
}

func TestConnectionAddTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "connection", "add", "name=podio", "kind=api_key"); err != nil {
		t.Fatalf("connection add: %v", err)
	}
	assertGolden(t, "connection_add.golden", out.String())
}

func TestConnectionProbeTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "connection", "probe", "notion.write", "--write", "--notion-page", "page-123", "--cleanup"); err != nil {
		t.Fatalf("connection probe: %v", err)
	}
	assertGolden(t, "connection_probe.golden", out.String())
}

func TestConnectionProbeJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "connection", "probe", "notion.write"); err != nil {
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
	if err := run(t, e, "connection", "reveal", "11111111-1111-1111-1111-111111111111"); err != nil {
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
	if err := run(t, e, "connection", "reveal", "11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatalf("connection reveal -o json: %v", err)
	}
	assertJSONEqual(t, []byte(`{"secret":"sk_live_REVEALED_ONCE"}`), out.Bytes())
}

func TestConnectionRotateTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "connection", "rotate", "11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatalf("connection rotate: %v", err)
	}
	assertGolden(t, "connection_rotate.golden", out.String())
}

func TestConnectionRmTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "connection", "rm", "11111111-1111-1111-1111-111111111111"); err != nil {
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
	if err := run(t, e, "member", "ls"); err != nil {
		t.Fatalf("member ls: %v", err)
	}
	assertGolden(t, "member_ls.golden", out.String())
}

func TestMemberListJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "member", "ls"); err != nil {
		t.Fatalf("member ls -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "members.json"), out.Bytes())
}

func TestMemberAddTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "member", "add", "id=carol@acme.test", "role=editor"); err != nil {
		t.Fatalf("member add: %v", err)
	}
	assertGolden(t, "member_add.golden", out.String())
}

func TestMemberRmTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "member", "rm", "bob@acme.test"); err != nil {
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
	if err := run(t, e, "token", "ls"); err != nil {
		t.Fatalf("token ls: %v", err)
	}
	assertGolden(t, "token_ls.golden", out.String())
}

func TestTokenListJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "token", "ls"); err != nil {
		t.Fatalf("token ls -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "tokens.json"), out.Bytes())
}

// TestTokenMintShowsRefreshToken: mint surfaces the refresh token once (printed) with a stderr warning.
func TestTokenMintShowsRefreshToken(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, errb := newTestEnv(t, srv, "table")
	if err := run(t, e, "token", "mint", "scope=config:read"); err != nil {
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
	if err := run(t, e, "token", "mint", "scope=config:read"); err != nil {
		t.Fatalf("token mint -o json: %v", err)
	}
	assertJSONEqual(t, []byte(`{"refresh_token":"rc_refresh_MINTED_ONCE","scope":"config:read","status":"active"}`), out.Bytes())
}

func TestTokenRevokeTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "token", "revoke", "tok_aaaa"); err != nil {
		t.Fatalf("token revoke: %v", err)
	}
	if got := out.String(); got != "revoked token tok_aaaa\n" {
		t.Errorf("token revoke output = %q", got)
	}
}
