// Package config resolves the two things every authenticated command needs: a base URL and an API
// key. The INTENT is a single, documented precedence so behavior is predictable — and crucially, so
// that running `rc` inside a brain checkout targets THAT brain's project, not whatever a global
// [default] happens to point at.
//
// A brain repo carries a committed, non-secret marker (.rootcause.toml: project + base_url) that binds
// the directory to one project. When `rc` runs anywhere inside such a repo (no explicit --profile), it
// resolves the key for THAT project and — if it can't — fails LOUDLY naming the project, rather than
// silently falling through to [default] (the footgun this design removes). The key itself stays out of
// version control: it comes from the env, a gitignored .rootcause.secret.toml at the brain root, or a
// named profile in ~/.config/rootcause/config.toml.
//
// Precedence:
//
//	explicit --profile <name>           → that profile only (an AWS-style override; no brain binding)
//	otherwise, inside a brain (cwd):      env > .rootcause.secret.toml > [profiles.<project>] > LOUD ERROR
//	otherwise, outside any brain:         env > [default] > built-in default
//
// For each FIELD an env var still wins (a one-off `ROOTCAUSE_API_KEY=… rc …` overrides everything),
// matching the long-standing convention. base_url has a built-in default (localhost:8080); api_key
// does not — an unresolved key is a hard, clearly-worded error at the command layer.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

const (
	// DefaultBaseURL is the built-in fallback when neither config, brain marker, nor env sets one.
	DefaultBaseURL = "http://localhost:8080"

	// MarkerFileName is the committed, non-secret per-brain marker binding the checkout to a project.
	MarkerFileName = ".rootcause.toml"
	// SecretFileName is the gitignored per-brain file holding this project's api_key (written by `rc
	// login`). It MUST be in the brain's .gitignore — it carries a production bearer key.
	SecretFileName = ".rootcause.secret.toml"

	envAPIKey  = "ROOTCAUSE_API_KEY"
	envBaseURL = "ROOTCAUSE_BASE_URL"
)

// Resolved is the effective config for one invocation. BaseURLFromDefault is true when nothing set a
// base URL and we fell back to DefaultBaseURL — the command layer warns on this when a key IS set,
// since a key + the localhost default is almost always an unset-base-URL mistake. Brain is non-nil
// when a .rootcause.toml was discovered (drives the loud "no key for this brain" error). KeySource is
// a log-safe label of where the key came from ("env" | "brain-secret" | "profile:<name>" | "").
type Resolved struct {
	APIKey             string
	BaseURL            string
	BaseURLFromDefault bool
	Project            string
	Tenant             string
	KeySource          string
	Brain              *Brain
}

// Brain is the committed .rootcause.toml marker: the project this checkout belongs to plus its API
// endpoint. Dir is the directory the marker was found in (where the secret file is read/written).
// Tenant is set only for a TENANT brain (a delta repo over a tenant-enabled project, e.g. a clinic
// under the DentAI project) — it becomes the default --tenant for env/ask so the checkout resolves the
// project ∪ tenant scope without repeating the flag.
type Brain struct {
	Project string `toml:"project"`
	Tenant  string `toml:"tenant"`
	BaseURL string `toml:"base_url"`
	Dir     string `toml:"-"`
}

// profile is one [default] / [profiles.<name>] block in config.toml, and also the shape of a
// .rootcause.secret.toml (which carries just api_key, optionally base_url).
type profile struct {
	APIKey  string `toml:"api_key"`
	BaseURL string `toml:"base_url"`
}

// file mirrors ~/.config/rootcause/config.toml: a [default] profile plus named [profiles.<name>].
type file struct {
	Default  profile            `toml:"default"`
	Profiles map[string]profile `toml:"profiles"`
}

// Load resolves config for one invocation. profileName comes from --profile; an empty string means
// "auto" (bind to the brain in cwd, else [default]). A non-empty name is an explicit override that
// bypasses brain discovery entirely.
func Load(profileName string) (Resolved, error) {
	cwd, err := os.Getwd()
	if err != nil {
		// A missing cwd only disables brain auto-discovery; resolution can still proceed via env/profile.
		cwd = ""
	}
	return load(profileName, cwd)
}

// load is Load with cwd injected, so the resolution matrix is unit-testable without chdir.
func load(profileName, cwd string) (Resolved, error) {
	f, err := loadFile()
	if err != nil {
		return Resolved{}, err
	}

	// Explicit --profile <name>: pure override, no brain binding (the documented escape hatch).
	if profileName != "" {
		prof, perr := f.selectProfile(profileName)
		if perr != nil {
			return Resolved{}, perr
		}
		return resolveFromProfile(prof, profileName), nil
	}

	// Auto mode: are we inside a brain?
	brain, err := DiscoverBrain(cwd)
	if err != nil {
		return Resolved{}, err
	}
	if brain == nil {
		// Outside any brain: the [default] profile applies (legacy behavior).
		return resolveFromProfile(f.Default, "default"), nil
	}

	// Inside a brain: env > .rootcause.secret.toml > [profiles.<project>]. A missing key is NOT a silent
	// fallthrough — APIKey stays empty and Brain is set, so newClient emits the loud, project-named error.
	secret, serr := loadSecret(brain.Dir)
	if serr != nil {
		return Resolved{}, serr
	}
	prof := f.Profiles[brain.Project] // zero value if there's no such profile

	res := Resolved{Project: brain.Project, Tenant: brain.Tenant, Brain: brain}
	switch {
	case os.Getenv(envAPIKey) != "":
		res.APIKey, res.KeySource = os.Getenv(envAPIKey), "env"
	case secret.APIKey != "":
		res.APIKey, res.KeySource = secret.APIKey, "brain-secret"
	case prof.APIKey != "":
		res.APIKey, res.KeySource = prof.APIKey, "profile:"+brain.Project
	}
	res.BaseURL, res.BaseURLFromDefault = resolveBaseURL(secret.BaseURL, brain.BaseURL, prof.BaseURL)
	return res, nil
}

// resolveFromProfile resolves a single profile (the explicit-override and outside-a-brain paths): env
// key wins over the profile's key; env base wins over the profile's base over the built-in default.
func resolveFromProfile(prof profile, name string) Resolved {
	var res Resolved
	switch {
	case os.Getenv(envAPIKey) != "":
		res.APIKey, res.KeySource = os.Getenv(envAPIKey), "env"
	case prof.APIKey != "":
		res.APIKey, res.KeySource = prof.APIKey, "profile:"+name
	}
	res.BaseURL, res.BaseURLFromDefault = resolveBaseURL(prof.BaseURL)
	return res
}

// resolveBaseURL picks the first non-empty URL: env override, then the given candidates in order, then
// the built-in default (with the from-default flag set so the command layer can warn).
func resolveBaseURL(candidates ...string) (string, bool) {
	if v := os.Getenv(envBaseURL); v != "" {
		return v, false
	}
	for _, u := range candidates {
		if u != "" {
			return u, false
		}
	}
	return DefaultBaseURL, true
}

// DiscoverBrain walks up from start looking for the nearest committed .rootcause.toml marker. Returns
// nil (not an error) when none is found before the filesystem root — that's the "not in a brain" case.
func DiscoverBrain(start string) (*Brain, error) {
	if start == "" {
		return nil, nil
	}
	dir := start
	for {
		path := filepath.Join(dir, MarkerFileName)
		if _, err := os.Stat(path); err == nil {
			var b Brain
			if _, derr := toml.DecodeFile(path, &b); derr != nil {
				return nil, fmt.Errorf("parse %s: %w", path, derr)
			}
			if b.Project == "" {
				return nil, fmt.Errorf("%s has no `project` field — it must name the project this brain belongs to", path)
			}
			b.Dir = dir
			return &b, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, nil // reached the filesystem root
		}
		dir = parent
	}
}

// loadSecret reads the gitignored .rootcause.secret.toml at the brain root, if present. A missing file
// is fine (the key may come from env or a profile); only a malformed one is an error.
func loadSecret(dir string) (profile, error) {
	path := filepath.Join(dir, SecretFileName)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return profile{}, nil
	}
	var s profile
	if _, err := toml.DecodeFile(path, &s); err != nil {
		return profile{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return s, nil
}

// loadFile reads ~/.config/rootcause/config.toml. A missing file is fine (env / brain-secret usage); a
// malformed one is an error so the user isn't silently mis-scoped.
func loadFile() (file, error) {
	path, err := configPath()
	if err != nil {
		return file{}, err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return file{}, nil
	}
	var f file
	if _, err := toml.DecodeFile(path, &f); err != nil {
		return file{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return f, nil
}

// selectProfile returns the named profile. "" / "default" select [default]; a named profile that
// doesn't exist is an error (a typo'd --profile must not silently fall through to env and hit the wrong
// server).
func (f file) selectProfile(name string) (profile, error) {
	if name == "" || name == "default" {
		return f.Default, nil
	}
	prof, ok := f.Profiles[name]
	if !ok {
		path, _ := configPath()
		return profile{}, fmt.Errorf("profile %q not found in %s", name, path)
	}
	return prof, nil
}

// WriteSecret writes (or replaces) the brain's .rootcause.secret.toml with api_key at mode 0600. The
// key holds a production bearer token, so the file must never be group/world-readable.
func WriteSecret(path, apiKey string) error {
	body := fmt.Sprintf("api_key = %q\n", apiKey)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	// Re-assert 0600 even if the file pre-existed with looser perms.
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

// ConfigPath is the resolved ~/.config/rootcause/config.toml path (exported for diagnostics/messages).
func ConfigPath() string {
	p, _ := configPath()
	return p
}

// configPath is ~/.config/rootcause/config.toml (XDG-style; honors XDG_CONFIG_HOME).
func configPath() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "rootcause", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(home, ".config", "rootcause", "config.toml"), nil
}
