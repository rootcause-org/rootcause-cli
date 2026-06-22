package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootcause-org/rootcause-cli/internal/config"
)

// writeMarker drops a committed .rootcause.toml naming the given project into dir.
func writeMarker(t *testing.T, dir, project string) {
	t.Helper()
	body := "project = \"" + project + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, config.MarkerFileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestLoginWritesSecret covers `rc login --api-key`: it verifies against the stub /env (whose project
// is "momentum-tools", matching the marker) and writes a 0600 .rootcause.secret.toml.
func TestLoginWritesSecret(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()

	dir := t.TempDir()
	t.Chdir(dir)
	writeMarker(t, dir, "momentum-tools")

	e, out, errb := newTestEnv(t, srv, "table")
	if err := run(t, e, "login", "--api-key", "test-key"); err != nil {
		t.Fatalf("login: %v (stderr=%s)", err, errb.String())
	}
	if !strings.Contains(out.String(), "logged in to momentum-tools") {
		t.Errorf("stdout = %q, want login confirmation", out.String())
	}

	path := filepath.Join(dir, config.SecretFileName)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat secret: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("secret file mode = %o, want 600", perm)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), `api_key = "test-key"`) {
		t.Errorf("secret body = %q, want api_key line", body)
	}
}

// TestLoginRejectsProjectMismatch covers the safety check: a key that resolves (server-side) to a
// different project than the marker is refused, not stored.
func TestLoginRejectsProjectMismatch(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()

	dir := t.TempDir()
	t.Chdir(dir)
	writeMarker(t, dir, "some-other-project") // stub /env says "momentum-tools" → mismatch
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "login", "--api-key", "test-key")
	if err == nil {
		t.Fatal("expected login to refuse a project mismatch")
	}
	if !strings.Contains(err.Error(), "different project") && !strings.Contains(err.Error(), "wrong key") {
		t.Errorf("error = %v, want a mismatch refusal", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, config.SecretFileName)); statErr == nil {
		t.Error("secret file must not be written on mismatch")
	}
}

// TestLoginOutsideBrain errors clearly when there's no marker to bind to.
func TestLoginOutsideBrain(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	t.Chdir(t.TempDir()) // no .rootcause.toml here
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "login", "--api-key", "test-key", "--no-verify")
	if err == nil || !strings.Contains(err.Error(), config.MarkerFileName) {
		t.Fatalf("expected a no-marker error mentioning %s, got %v", config.MarkerFileName, err)
	}
}

// TestWhoamiBrainBinding shows the resolved project + key source, and confirms against the server.
func TestWhoamiBrainBinding(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()

	dir := t.TempDir()
	t.Chdir(dir)
	writeMarker(t, dir, "momentum-tools")
	// The stub's requireAuth demands Bearer "test-key", so the brain secret must carry that value.
	if err := os.WriteFile(filepath.Join(dir, config.SecretFileName), []byte("api_key = \"test-key\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	e, out, _ := newTestEnv(t, srv, "table")
	t.Setenv("ROOTCAUSE_API_KEY", "") // resolve through the brain secret, not the env newTestEnv set
	if err := run(t, e, "whoami"); err != nil {
		t.Fatalf("whoami: %v", err)
	}
	got := out.String()
	for _, want := range []string{"momentum-tools", "brain-secret", "server says: momentum-tools"} {
		if !strings.Contains(got, want) {
			t.Errorf("whoami missing %q\n--- got ---\n%s", want, got)
		}
	}
}
