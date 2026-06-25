// Package config resolves the two things every command needs before it can authenticate: a base URL
// and a PROFILE NAME (the key the OAuth token store is keyed by). The INTENT is a single, documented
// precedence so behavior is predictable. Running `rc` inside a brain checkout discovers THAT brain's
// project; the command layer may use it either as the profile name (when such a token exists) or as the
// server-side project scope when it falls back to the default profile.
//
// Auth itself moved to OAuth: tokens live in ~/.config/rootcause/tokens.json (see internal/token),
// keyed by profile. This package no longer holds any secret — it only decides WHICH profile's token to
// use and WHICH base URL to hit. A brain repo carries a committed, non-secret marker (.rootcause.toml:
// project + base_url) that binds the directory to one project. A developer may also keep a gitignored
// per-checkout .rootcause/local.toml with tenant = "..." as an explicit local override. In auto mode
// this resolver first names the project profile; the command layer can fall back
// to "default" when no such token is stored and carry the marker's project as ?project= for an
// all-projects token.
//
// `--project` is NOT a profile selector — it does not pick a token. It is a SERVER-SIDE scope (a
// uuid-or-name passed as ?project= on the read endpoints), meaningful only for an all-projects admin
// token; the command layer threads it into the client, not this resolver. (See internal/cli/root.go.)
//
// Precedence for the profile name (the token-store key):
//
//	explicit --profile <name>   → that profile (an AWS-style override; no brain binding)
//	otherwise, inside a brain:    the brain marker's project (commands may fall back to default if absent)
//	otherwise:                    "default"
//
// Precedence for the base URL (per field, env always wins):
//
//	ROOTCAUSE_BASE_URL > .rootcause.toml base_url > [profiles.<name>] base_url > built-in default
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

const (
	// DefaultBaseURL is the built-in fallback when neither config, brain marker, nor env sets one.
	DefaultBaseURL = "https://rootcause.probackup.io"

	// MarkerFileName is the committed, non-secret per-brain marker binding the checkout to a project.
	// It is KEPT under OAuth — it carries no secret, only the project binding + base URL.
	MarkerFileName = ".rootcause.toml"

	// LocalFileName is a gitignored per-brain developer overlay under the wholesale-ignored .rootcause
	// artifact dir. It only supplies local overrides, currently tenant.
	LocalFileName = ".rootcause/local.toml"

	// DefaultProfile is the profile name used outside any brain (and when no --profile/--project is given).
	DefaultProfile = "default"

	envBaseURL = "ROOTCAUSE_BASE_URL"
)

// Resolved is the effective config for one invocation. Profile is the token-store key the command's
// client authenticates with. BaseURLFromDefault is true when nothing set a base URL and we fell back to
// DefaultBaseURL. Brain is non-nil when a .rootcause.toml was discovered; Project/Tenant come from it.
// BaseURL is always non-empty. Project here is the BRAIN's project (the checkout's identity), NOT the
// --project scope override — that's a server-side selector the command layer owns, never a profile.
type Resolved struct {
	Profile            string
	BaseURL            string
	BaseURLFromDefault bool
	Project            string
	Tenant             string
	TenantSource       string
	Brain              *Brain
}

// Brain is the committed .rootcause.toml marker: the project this checkout belongs to plus its API
// endpoint. Dir is the directory the marker was found in. Tenant is a legacy/local override; the normal
// tenant-enabled path gets tenant scope from the active OAuth login.
type Brain struct {
	Project string `toml:"project"`
	Tenant  string `toml:"tenant"`
	BaseURL string `toml:"base_url"`
	Dir     string `toml:"-"`
}

// local is the optional gitignored per-checkout overlay. Keep it intentionally narrow: global config
// and environment variables already cover base URL/profile, while tenant is often developer-local.
type local struct {
	Tenant string `toml:"tenant"`
}

// profile is one [default] / [profiles.<name>] block in config.toml. Under OAuth it carries only a
// base_url override (the api_key field is gone — tokens live in the token store).
type profile struct {
	BaseURL string `toml:"base_url"`
}

// file mirrors ~/.config/rootcause/config.toml: a [default] profile plus named [profiles.<name>].
type file struct {
	Default  profile            `toml:"default"`
	Profiles map[string]profile `toml:"profiles"`
}

// Load resolves config for one invocation. profileName comes from --profile; empty means "auto" (bind
// to the brain in cwd, else [default]). --project is NOT resolved here — it's a server-side scope the
// command layer threads into the client, never a token-store key.
func Load(profileName string) (Resolved, error) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "" // a missing cwd only disables brain auto-discovery
	}
	return load(profileName, cwd)
}

// load is Load with cwd injected, so the resolution matrix is unit-testable without chdir.
func load(profileName, cwd string) (Resolved, error) {
	f, err := loadFile()
	if err != nil {
		return Resolved{}, err
	}

	// Explicit --profile <name>: a pure override, no brain binding (the documented escape hatch). A
	// named profile that doesn't exist is fine here — it just means "no base_url override stored for it";
	// the token store is the source of auth, not config.toml.
	if profileName != "" {
		prof := f.Profiles[profileName]
		if profileName == DefaultProfile {
			prof = f.Default
		}
		res := Resolved{Profile: profileName}
		res.BaseURL, res.BaseURLFromDefault = resolveBaseURL(prof.BaseURL)
		return res, nil
	}

	// Auto mode: are we inside a brain?
	brain, err := DiscoverBrain(cwd)
	if err != nil {
		return Resolved{}, err
	}
	if brain == nil {
		// Outside any brain: the [default] profile.
		res := Resolved{Profile: DefaultProfile}
		res.BaseURL, res.BaseURLFromDefault = resolveBaseURL(f.Default.BaseURL)
		return res, nil
	}

	// Inside a brain: first name the project profile. base_url: env > marker > matching profile > default.
	prof := f.Profiles[brain.Project]
	tenant, tenantSource, err := resolveTenant(brain)
	if err != nil {
		return Resolved{}, err
	}
	res := Resolved{
		Profile:      brain.Project,
		Project:      brain.Project,
		Tenant:       tenant,
		TenantSource: tenantSource,
		Brain:        brain,
	}
	res.BaseURL, res.BaseURLFromDefault = resolveBaseURL(brain.BaseURL, prof.BaseURL)
	return res, nil
}

// resolveBaseURL picks the first non-empty URL: env override, then the given candidates in order, then
// the built-in default (with the from-default flag set for diagnostics).
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

func resolveTenant(brain *Brain) (string, string, error) {
	tenant := brain.Tenant
	source := ""
	if tenant != "" {
		source = MarkerFileName
	}

	path := filepath.Join(brain.Dir, LocalFileName)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return tenant, source, nil
	} else if err != nil {
		return "", "", fmt.Errorf("stat %s: %w", path, err)
	}
	var l local
	if _, err := toml.DecodeFile(path, &l); err != nil {
		return "", "", fmt.Errorf("parse %s: %w", path, err)
	}
	if l.Tenant != "" {
		return l.Tenant, LocalFileName, nil
	}
	return tenant, source, nil
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

// loadFile reads ~/.config/rootcause/config.toml. A missing file is fine (the common case under OAuth —
// base URLs usually come from the brain marker or env); a malformed one is an error so the user isn't
// silently mis-scoped.
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

// ConfigPath is the resolved ~/.config/rootcause/config.toml path (exported for diagnostics/messages).
func ConfigPath() string {
	p, _ := configPath()
	return p
}

// ConfigDir is the resolved ~/.config/rootcause directory (XDG-style; honors XDG_CONFIG_HOME). The
// token store lives here too, so it's exported for internal/token to share the one resolution.
func ConfigDir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "rootcause"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(home, ".config", "rootcause"), nil
}

// configPath is ~/.config/rootcause/config.toml.
func configPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}
