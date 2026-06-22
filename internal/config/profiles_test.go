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

// brainDir creates a temp directory holding a .rootcause.toml marker (and optionally a secret file),
// returning the dir so tests can resolve against it.
func brainDirWith(t *testing.T, marker, secret string) string {
	t.Helper()
	dir := t.TempDir()
	if marker != "" {
		if err := os.WriteFile(filepath.Join(dir, MarkerFileName), []byte(marker), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if secret != "" {
		if err := os.WriteFile(filepath.Join(dir, SecretFileName), []byte(secret), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// clearEnv unsets the two env vars so a case starts from a known-empty baseline (t.Setenv restores).
func clearEnv(t *testing.T) {
	t.Helper()
	t.Setenv(envAPIKey, "")
	t.Setenv(envBaseURL, "")
}

func TestLoad_NoBrain_EnvKeyWins(t *testing.T) {
	clearEnv(t)
	writeConfig(t, "[default]\napi_key = \"from-default\"\nbase_url = \"https://default.example\"\n")
	t.Setenv(envAPIKey, "from-env")

	res, err := load("", t.TempDir()) // auto mode, cwd has no marker
	if err != nil {
		t.Fatal(err)
	}
	if res.APIKey != "from-env" || res.KeySource != "env" {
		t.Errorf("key=%q src=%q, want from-env/env", res.APIKey, res.KeySource)
	}
	if res.Brain != nil || res.Project != "" {
		t.Errorf("expected no brain binding outside a brain, got %+v", res)
	}
	// base_url still comes from [default] (no env override).
	if res.BaseURL != "https://default.example" {
		t.Errorf("base=%q, want default profile base", res.BaseURL)
	}
}

func TestLoad_NoBrain_DefaultProfile(t *testing.T) {
	clearEnv(t)
	writeConfig(t, "[default]\napi_key = \"from-default\"\n")

	res, err := load("", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if res.APIKey != "from-default" || res.KeySource != "profile:default" {
		t.Errorf("key=%q src=%q, want from-default/profile:default", res.APIKey, res.KeySource)
	}
	if res.BaseURL != DefaultBaseURL || !res.BaseURLFromDefault {
		t.Errorf("base=%q fromDefault=%v, want built-in default", res.BaseURL, res.BaseURLFromDefault)
	}
}

func TestLoad_NoBrain_NoKey(t *testing.T) {
	clearEnv(t)
	writeConfig(t, "") // no config file at all

	res, err := load("", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if res.APIKey != "" || res.Brain != nil {
		t.Errorf("expected empty key and no brain, got %+v", res)
	}
}

func TestLoad_Brain_SecretFile(t *testing.T) {
	clearEnv(t)
	writeConfig(t, "")
	dir := brainDirWith(t,
		"project = \"momentum-tools\"\nbase_url = \"https://rc.example\"\n",
		"api_key = \"from-secret\"\n")

	res, err := load("", dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.APIKey != "from-secret" || res.KeySource != "brain-secret" {
		t.Errorf("key=%q src=%q, want from-secret/brain-secret", res.APIKey, res.KeySource)
	}
	if res.Project != "momentum-tools" || res.Brain == nil || res.Brain.Dir != dir {
		t.Errorf("brain binding wrong: %+v", res)
	}
	if res.BaseURL != "https://rc.example" {
		t.Errorf("base=%q, want brain marker base", res.BaseURL)
	}
}

func TestLoad_Brain_GlobalProfileKey(t *testing.T) {
	clearEnv(t)
	writeConfig(t, "[profiles.momentum-tools]\napi_key = \"from-profile\"\nbase_url = \"https://profile.example\"\n")
	dir := brainDirWith(t, "project = \"momentum-tools\"\n", "") // marker, no secret, no base_url

	res, err := load("", dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.APIKey != "from-profile" || res.KeySource != "profile:momentum-tools" {
		t.Errorf("key=%q src=%q, want from-profile/profile:momentum-tools", res.APIKey, res.KeySource)
	}
	// No base in marker → falls to the matching profile's base_url.
	if res.BaseURL != "https://profile.example" {
		t.Errorf("base=%q, want profile base", res.BaseURL)
	}
}

func TestLoad_Brain_NoKey_LoudErrorPath(t *testing.T) {
	clearEnv(t)
	writeConfig(t, "[default]\napi_key = \"dentai-key\"\n") // [default] must NOT leak into a brain
	dir := brainDirWith(t, "project = \"momentum-tools\"\n", "")

	res, err := load("", dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.APIKey != "" {
		t.Errorf("expected empty key (no silent [default] fallback), got %q from %q", res.APIKey, res.KeySource)
	}
	if res.Brain == nil || res.Project != "momentum-tools" {
		t.Errorf("expected brain set for the loud error, got %+v", res)
	}
}

func TestLoad_Brain_EnvOverridesSecret(t *testing.T) {
	clearEnv(t)
	writeConfig(t, "")
	dir := brainDirWith(t, "project = \"momentum-tools\"\n", "api_key = \"from-secret\"\n")
	t.Setenv(envAPIKey, "from-env")

	res, err := load("", dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.APIKey != "from-env" || res.KeySource != "env" {
		t.Errorf("env must win inside a brain: key=%q src=%q", res.APIKey, res.KeySource)
	}
}

func TestLoad_ExplicitProfileBypassesBrain(t *testing.T) {
	clearEnv(t)
	writeConfig(t, "[profiles.staging]\napi_key = \"staging-key\"\nbase_url = \"https://staging.example\"\n")
	dir := brainDirWith(t, "project = \"momentum-tools\"\n", "api_key = \"from-secret\"\n")

	res, err := load("staging", dir) // explicit --profile overrides the brain binding
	if err != nil {
		t.Fatal(err)
	}
	if res.APIKey != "staging-key" {
		t.Errorf("explicit profile should win over brain secret: %q", res.APIKey)
	}
	if res.Brain != nil || res.Project != "" {
		t.Errorf("explicit profile must not carry a brain binding: %+v", res)
	}
}

func TestLoad_UnknownProfileErrors(t *testing.T) {
	clearEnv(t)
	writeConfig(t, "[default]\napi_key = \"x\"\n")
	if _, err := load("nope", t.TempDir()); err == nil {
		t.Fatal("expected an error for an unknown named profile")
	}
}

func TestLoad_BaseURLPrecedence(t *testing.T) {
	clearEnv(t)
	writeConfig(t, "[profiles.momentum-tools]\napi_key = \"k\"\nbase_url = \"https://profile.example\"\n")
	// secret base_url should beat the marker base_url, which beats the profile base_url.
	dir := brainDirWith(t,
		"project = \"momentum-tools\"\nbase_url = \"https://marker.example\"\n",
		"api_key = \"k\"\nbase_url = \"https://secret.example\"\n")

	res, err := load("", dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.BaseURL != "https://secret.example" {
		t.Errorf("base=%q, want secret base (highest non-env)", res.BaseURL)
	}

	// With env set, env wins over everything.
	t.Setenv(envBaseURL, "https://env.example")
	res2, _ := load("", dir)
	if res2.BaseURL != "https://env.example" {
		t.Errorf("env base must win, got %q", res2.BaseURL)
	}
}

func TestLoad_TenantBrain(t *testing.T) {
	clearEnv(t)
	writeConfig(t, "")
	dir := brainDirWith(t,
		"project = \"dentai\"\ntenant = \"de-kies\"\nbase_url = \"https://rc.example\"\n",
		"api_key = \"k\"\n")

	res, err := load("", dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Project != "dentai" || res.Tenant != "de-kies" {
		t.Errorf("project/tenant = %q/%q, want dentai/de-kies", res.Project, res.Tenant)
	}
	if res.Brain == nil || res.Brain.Tenant != "de-kies" {
		t.Errorf("brain tenant not carried: %+v", res.Brain)
	}
}

func TestDiscoverBrain_WalksUp(t *testing.T) {
	clearEnv(t)
	root := brainDirWith(t, "project = \"momentum-tools\"\n", "")
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
	dir := brainDirWith(t, "base_url = \"https://x.example\"\n", "") // no project field
	if _, err := DiscoverBrain(dir); err == nil {
		t.Fatal("expected an error for a marker without a project field")
	}
}
