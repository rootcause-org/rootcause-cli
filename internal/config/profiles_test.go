package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConfig writes a global config.toml under a temp XDG_CONFIG_HOME and points the env at it.
func writeConfig(t *testing.T, body string) {
	t.Helper()
	xdg := t.TempDir()
	dir := filepath.Join(xdg, "rootcause")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if body != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("XDG_CONFIG_HOME", xdg)
}

// brainDirWith creates a temp directory holding a .rootcause.toml marker, returning the dir.
func brainDirWith(t *testing.T, marker string) string {
	t.Helper()
	dir := t.TempDir()
	if marker != "" {
		if err := os.WriteFile(filepath.Join(dir, MarkerFileName), []byte(marker), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// clearEnv unsets ROOTCAUSE_BASE_URL so a case starts from a known baseline (t.Setenv restores).
func clearEnv(t *testing.T) {
	t.Helper()
	t.Setenv(envBaseURL, "")
}

func TestLoad_NoBrain_DefaultProfile(t *testing.T) {
	clearEnv(t)
	writeConfig(t, "[default]\nbase_url = \"https://default.example\"\n")

	res, err := load("", t.TempDir()) // auto mode, cwd has no marker
	if err != nil {
		t.Fatal(err)
	}
	if res.Profile != DefaultProfile {
		t.Errorf("profile=%q, want default", res.Profile)
	}
	if res.Brain != nil || res.Project != "" {
		t.Errorf("expected no brain binding outside a brain, got %+v", res)
	}
	if res.BaseURL != "https://default.example" || res.BaseURLFromDefault {
		t.Errorf("base=%q fromDefault=%v, want default profile base", res.BaseURL, res.BaseURLFromDefault)
	}
}

func TestLoad_NoBrain_NoConfig_BuiltInBase(t *testing.T) {
	clearEnv(t)
	writeConfig(t, "") // no config file at all

	res, err := load("", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if res.Profile != DefaultProfile {
		t.Errorf("profile=%q, want default", res.Profile)
	}
	if res.BaseURL != "https://rootcause.probackup.io" || !res.BaseURLFromDefault {
		t.Errorf("base=%q fromDefault=%v, want production built-in default", res.BaseURL, res.BaseURLFromDefault)
	}
}

func TestLoad_EnvBaseURLWins(t *testing.T) {
	clearEnv(t)
	writeConfig(t, "[default]\nbase_url = \"https://default.example\"\n")
	t.Setenv(envBaseURL, "https://env.example")

	res, err := load("", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if res.BaseURL != "https://env.example" || res.BaseURLFromDefault {
		t.Errorf("base=%q, want env override", res.BaseURL)
	}
}

func TestLoad_Brain_ProfileIsProject(t *testing.T) {
	clearEnv(t)
	writeConfig(t, "")
	dir := brainDirWith(t, "project = \"momentum-tools\"\nbase_url = \"https://rc.example\"\n")

	res, err := load("", dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Profile != "momentum-tools" || res.Project != "momentum-tools" {
		t.Errorf("profile/project = %q/%q, want momentum-tools", res.Profile, res.Project)
	}
	if res.Brain == nil || res.Brain.Dir != dir {
		t.Errorf("brain binding wrong: %+v", res.Brain)
	}
	if res.BaseURL != "https://rc.example" {
		t.Errorf("base=%q, want brain marker base", res.BaseURL)
	}
}

func TestLoad_Brain_BaseURLPrecedence(t *testing.T) {
	clearEnv(t)
	// A [profiles.<project>] base_url is the fallback when the marker omits one; the marker wins when set.
	writeConfig(t, "[profiles.momentum-tools]\nbase_url = \"https://profile.example\"\n")
	dir := brainDirWith(t, "project = \"momentum-tools\"\n") // marker, no base_url

	res, err := load("", dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.BaseURL != "https://profile.example" {
		t.Errorf("base=%q, want matching profile base", res.BaseURL)
	}

	// Env still wins over everything.
	t.Setenv(envBaseURL, "https://env.example")
	res2, _ := load("", dir)
	if res2.BaseURL != "https://env.example" {
		t.Errorf("env base must win, got %q", res2.BaseURL)
	}
}

func TestLoad_ExplicitProfileBypassesBrain(t *testing.T) {
	clearEnv(t)
	writeConfig(t, "[profiles.staging]\nbase_url = \"https://staging.example\"\n")
	dir := brainDirWith(t, "project = \"momentum-tools\"\n")

	res, err := load("staging", dir) // explicit --profile overrides the brain binding
	if err != nil {
		t.Fatal(err)
	}
	if res.Profile != "staging" {
		t.Errorf("profile=%q, want staging", res.Profile)
	}
	if res.Brain != nil || res.Project != "" {
		t.Errorf("explicit profile must not carry a brain binding: %+v", res)
	}
	if res.BaseURL != "https://staging.example" {
		t.Errorf("base=%q, want staging profile base", res.BaseURL)
	}
}

// --project is NO LONGER a profile/token selector — it's a server-side scope the command layer threads
// into each read request. So config resolution ignores it entirely: inside a brain, the profile + base
// URL still come from the brain marker regardless of any --project the user passed. (The scope itself is
// carried on env.project and asserted in the cli package, not here.)
func TestLoad_ProjectFlagIsNotAProfile(t *testing.T) {
	clearEnv(t)
	writeConfig(t, "[profiles.acme]\nbase_url = \"https://acme.example\"\n")
	dir := brainDirWith(t, "project = \"momentum-tools\"\nbase_url = \"https://rc.example\"\n")

	// load takes only the profile name; --project never reaches it. The brain binding stands.
	res, err := load("", dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Profile != "momentum-tools" || res.Project != "momentum-tools" {
		t.Errorf("profile/project = %q/%q, want momentum-tools (brain binding, not a --project profile)", res.Profile, res.Project)
	}
	if res.BaseURL != "https://rc.example" {
		t.Errorf("base=%q, want the brain marker's base — a stray --project must not select acme's profile", res.BaseURL)
	}
}

func TestLoad_TenantBrain(t *testing.T) {
	clearEnv(t)
	writeConfig(t, "")
	dir := brainDirWith(t, "project = \"dentai\"\ntenant = \"de-kies\"\nbase_url = \"https://rc.example\"\n")

	res, err := load("", dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Project != "dentai" || res.Tenant != "de-kies" {
		t.Errorf("project/tenant = %q/%q, want dentai/de-kies", res.Project, res.Tenant)
	}
	if res.TenantSource != MarkerFileName {
		t.Errorf("tenant source = %q, want %s", res.TenantSource, MarkerFileName)
	}
	if res.Brain == nil || res.Brain.Tenant != "de-kies" {
		t.Errorf("brain tenant not carried: %+v", res.Brain)
	}
}

func TestLoad_LocalTenantDefault(t *testing.T) {
	clearEnv(t)
	writeConfig(t, "")
	dir := brainDirWith(t, "project = \"dentai\"\nbase_url = \"https://rc.example\"\n")
	if err := os.MkdirAll(filepath.Join(dir, ".rootcause"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, LocalFileName), []byte("tenant = \"de-kies\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := load("", dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Project != "dentai" || res.Tenant != "de-kies" {
		t.Errorf("project/tenant = %q/%q, want dentai/de-kies", res.Project, res.Tenant)
	}
	if res.TenantSource != LocalFileName {
		t.Errorf("tenant source = %q, want %s", res.TenantSource, LocalFileName)
	}
	if res.Brain == nil || res.Brain.Tenant != "" {
		t.Errorf("local tenant should not mutate committed marker: %+v", res.Brain)
	}
}

func TestLoad_LocalTenantOverridesMarkerTenant(t *testing.T) {
	clearEnv(t)
	writeConfig(t, "")
	dir := brainDirWith(t, "project = \"dentai\"\ntenant = \"shared\"\nbase_url = \"https://rc.example\"\n")
	if err := os.MkdirAll(filepath.Join(dir, ".rootcause"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, LocalFileName), []byte("tenant = \"de-kies\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := load("", filepath.Join(dir, "nested"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Tenant != "de-kies" || res.TenantSource != LocalFileName {
		t.Errorf("tenant/source = %q/%q, want de-kies/%s", res.Tenant, res.TenantSource, LocalFileName)
	}
}

func TestDiscoverBrain_WalksUp(t *testing.T) {
	root := brainDirWith(t, "project = \"momentum-tools\"\n")
	nested := filepath.Join(root, "skills", "databases", "scripts")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := DiscoverBrain(nested)
	if err != nil {
		t.Fatal(err)
	}
	if b == nil || b.Project != "momentum-tools" || b.Dir != root {
		t.Errorf("walk-up discovery failed: %+v", b)
	}
}

func TestDiscoverBrain_MarkerMissingProject(t *testing.T) {
	dir := brainDirWith(t, "base_url = \"https://x.example\"\n") // no project field
	if _, err := DiscoverBrain(dir); err == nil {
		t.Fatal("expected an error for a marker without a project field")
	}
}
