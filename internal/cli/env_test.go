package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// secretValue is the canned secret VALUE the env stub returns; it must NEVER appear in any stdout/stderr
// the env commands produce (it may only ever land in the 0600 ./.env file).
const secretValue = "sk_live_SECRET"

// TestEnvKeys covers `rc project env keys` in table + JSON: names only, never a value.
func TestEnvKeys(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()

	cases := []struct {
		name      string
		output    string
		args      []string
		wantOut   []string // substrings expected on stdout
		wantNoVal bool
	}{
		{
			name:    "table",
			output:  "table",
			args:    []string{"project", "env", "keys"},
			wantOut: []string{"FEATURE_FLAG", "REGION", "STRIPE_KEY"},
		},
		{
			name:    "json",
			output:  "json",
			args:    []string{"project", "env", "keys"},
			wantOut: []string{`"keys"`, `"FEATURE_FLAG"`, `"count": 3`},
		},
		{
			name:    "tenant",
			output:  "table",
			args:    []string{"project", "env", "keys", "--tenant", "acme"},
			wantOut: []string{"ACME_DSN", "FEATURE_FLAG", "REGION"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, out, errb := newTestEnv(t, srv, tc.output)
			if err := run(t, e, tc.args...); err != nil {
				t.Fatalf("run: %v (stderr=%s)", err, errb.String())
			}
			got := out.String()
			for _, want := range tc.wantOut {
				if !strings.Contains(got, want) {
					t.Errorf("stdout missing %q\n--- got ---\n%s", want, got)
				}
			}
			assertNoSecret(t, out.String(), errb.String())
		})
	}
}

// TestEnvPull covers `rc project env pull`: it writes a 0600 ./.env with the real VALUES, but prints only
// NAMES + count.
func TestEnvPull(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()

	t.Run("flat project writes 0600 .env", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		e, out, errb := newTestEnv(t, srv, "table")
		if err := run(t, e, "project", "env", "pull"); err != nil {
			t.Fatalf("run: %v (stderr=%s)", err, errb.String())
		}
		// stdout: names + count, no values.
		if !strings.Contains(out.String(), "STRIPE_KEY") || !strings.Contains(out.String(), "3 keys") {
			t.Errorf("stdout = %q, want names + count", out.String())
		}
		assertNoSecret(t, out.String(), errb.String())

		// file: holds the VALUES (that's the point) at mode 0600.
		path := filepath.Join(dir, ".env")
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat .env: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("file mode = %o, want 600", perm)
		}
		body, _ := os.ReadFile(path)
		if !strings.Contains(string(body), "STRIPE_KEY="+secretValue) {
			t.Errorf(".env missing the sealed value line; got:\n%s", body)
		}
		// sorted KEY=VALUE format (matches the host parser / rc_env.py).
		if want := "FEATURE_FLAG=project\nREGION=eu\nSTRIPE_KEY=" + secretValue + "\n"; string(body) != want {
			t.Errorf(".env body = %q, want %q", body, want)
		}
	})

	t.Run("tenant merge", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		e, out, errb := newTestEnv(t, srv, "table")
		if err := run(t, e, "project", "env", "pull", "--tenant", "acme"); err != nil {
			t.Fatalf("run: %v (stderr=%s)", err, errb.String())
		}
		assertNoSecret(t, out.String(), errb.String())
		body, _ := os.ReadFile(filepath.Join(dir, ".env"))
		if !strings.Contains(string(body), "ACME_DSN=postgres://acme@h/db") {
			t.Errorf(".env missing tenant key; got:\n%s", body)
		}
		if !strings.Contains(string(body), "FEATURE_FLAG=tenant") {
			t.Errorf("tenant did not override FEATURE_FLAG; got:\n%s", body)
		}
	})

	t.Run("re-pull tightens loose perms", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		// Pre-create a world-readable .env; pull must re-assert 0600.
		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("OLD=1\n"), 0o644); err != nil {
			t.Fatalf("seed loose .env: %v", err)
		}
		e, _, errb := newTestEnv(t, srv, "table")
		if err := run(t, e, "project", "env", "pull"); err != nil {
			t.Fatalf("run: %v (stderr=%s)", err, errb.String())
		}
		info, _ := os.Stat(filepath.Join(dir, ".env"))
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("file mode after re-pull = %o, want 600", perm)
		}
	})
}

// TestEnvDiff covers `rc project env diff`: in-sync (exit 0) vs drift (nonzero exit, names-only report).
func TestEnvDiff(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()

	t.Run("in sync after pull", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		// pull, then diff — must be in sync, exit 0.
		e, _, errb := newTestEnv(t, srv, "table")
		if err := run(t, e, "project", "env", "pull"); err != nil {
			t.Fatalf("pull: %v (stderr=%s)", err, errb.String())
		}
		e2, out, errb2 := newTestEnv(t, srv, "table")
		if err := run(t, e2, "project", "env", "diff"); err != nil {
			t.Fatalf("diff in-sync should exit 0, got: %v (stderr=%s)", err, errb2.String())
		}
		if !strings.Contains(out.String(), "in sync") {
			t.Errorf("stdout = %q, want 'in sync'", out.String())
		}
		assertNoSecret(t, out.String(), errb2.String())
	})

	t.Run("drift exits nonzero with names-only report", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		// Local .env: STRIPE_KEY differs, EXTRA only-local, REGION missing (only-server), FEATURE_FLAG ok.
		local := "FEATURE_FLAG=project\nSTRIPE_KEY=wrong\nEXTRA=1\n"
		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(local), 0o600); err != nil {
			t.Fatalf("seed .env: %v", err)
		}
		e, out, errb := newTestEnv(t, srv, "table")
		err := run(t, e, "project", "env", "diff")
		if err == nil {
			t.Fatalf("diff with drift should return an error (nonzero exit); stdout=%s", out.String())
		}
		got := out.String()
		for _, want := range []string{"DRIFT", "STRIPE_KEY", "EXTRA", "REGION"} {
			if !strings.Contains(got, want) {
				t.Errorf("drift report missing %q\n--- got ---\n%s", want, got)
			}
		}
		assertNoSecret(t, got, errb.String())
	})

	t.Run("json drift", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("STRIPE_KEY=wrong\n"), 0o600); err != nil {
			t.Fatalf("seed .env: %v", err)
		}
		e, out, errb := newTestEnv(t, srv, "json")
		if err := run(t, e, "project", "env", "diff"); err == nil {
			t.Fatalf("json diff with drift should return an error")
		}
		got := out.String()
		for _, want := range []string{`"in_sync": false`, `"value_differs"`, `"only_server"`} {
			if !strings.Contains(got, want) {
				t.Errorf("json drift missing %q\n--- got ---\n%s", want, got)
			}
		}
		assertNoSecret(t, got, errb.String())
	})
}

// assertNoSecret fails if a secret VALUE leaked into any rendered output.
func assertNoSecret(t *testing.T, outputs ...string) {
	t.Helper()
	for _, s := range outputs {
		if strings.Contains(s, secretValue) {
			t.Errorf("secret value leaked into output:\n%s", s)
		}
	}
}
