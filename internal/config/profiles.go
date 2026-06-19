// Package config resolves the two things every authenticated command needs: a base URL and an API
// key. The INTENT is a single, documented precedence so behavior is predictable across shells and
// machines — and it follows the common convention (env wins, for one-off invocations):
//
//	environment variable  >  config.toml (selected profile)  >  built-in default
//
// i.e. an env var OVERRIDES the chosen profile's value; the profile overrides the built-in default.
// Practical consequence: an exported ROOTCAUSE_API_KEY / ROOTCAUSE_BASE_URL shadows a profile's key /
// url — to actually use a profile's values, unset the matching env var. base_url has a built-in
// default (localhost:8080); api_key does not — an unresolved key is a hard, clearly-worded error at
// the command layer (commands that need auth), not a silent empty.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

const (
	// DefaultBaseURL is the built-in fallback when neither config nor env sets one.
	DefaultBaseURL = "http://localhost:8080"

	envAPIKey  = "ROOTCAUSE_API_KEY"
	envBaseURL = "ROOTCAUSE_BASE_URL"
)

// Resolved is the effective config for one invocation. BaseURLFromDefault is true when neither env nor
// config set a base URL and we fell back to DefaultBaseURL — the command layer warns on this when a
// key IS set, since a key + the localhost default is almost always an unset-base-URL mistake.
type Resolved struct {
	APIKey             string
	BaseURL            string
	BaseURLFromDefault bool
}

// profile is one [default] / [profiles.<name>] block in config.toml.
type profile struct {
	APIKey  string `toml:"api_key"`
	BaseURL string `toml:"base_url"`
}

// file mirrors ~/.config/rootcause/config.toml: a [default] profile plus named [profiles.<name>].
type file struct {
	Default  profile            `toml:"default"`
	Profiles map[string]profile `toml:"profiles"`
}

// Load resolves config for the given profile name ("default" selects the [default] block). Precedence
// per field (see package doc): env var > config profile value > built-in default. A missing config
// file is fine (env-only usage); a malformed one is an error so the user isn't silently mis-scoped. A
// named profile that doesn't exist is an error (a typo'd --profile must not silently fall through to
// env and hit the wrong server).
func Load(profileName string) (Resolved, error) {
	prof, ok, err := loadProfile(profileName)
	if err != nil {
		return Resolved{}, err
	}
	// ok=false means no config file at all (env-only mode); prof is the zero profile, so the file layer
	// simply contributes nothing below.
	_ = ok

	// Layer low → high: built-in default, then config profile, then env. The env var wins so a one-off
	// `ROOTCAUSE_BASE_URL=… rc …` overrides whatever the profile has.
	apiKey := prof.APIKey // config layer
	if v := os.Getenv(envAPIKey); v != "" {
		apiKey = v // env overrides config
	}

	baseURL := DefaultBaseURL
	baseURLFromDefault := true
	if prof.BaseURL != "" {
		baseURL = prof.BaseURL
		baseURLFromDefault = false
	}
	if v := os.Getenv(envBaseURL); v != "" {
		baseURL = v
		baseURLFromDefault = false
	}

	return Resolved{APIKey: apiKey, BaseURL: baseURL, BaseURLFromDefault: baseURLFromDefault}, nil
}

// loadProfile reads config.toml and returns the requested profile. ok=false means the file doesn't
// exist (env-only mode). A present file with a missing *named* profile is an error.
func loadProfile(profileName string) (profile, bool, error) {
	path, err := configPath()
	if err != nil {
		return profile{}, false, err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return profile{}, false, nil
	}

	var f file
	if _, err := toml.DecodeFile(path, &f); err != nil {
		return profile{}, false, fmt.Errorf("parse %s: %w", path, err)
	}

	if profileName == "" || profileName == "default" {
		return f.Default, true, nil
	}
	prof, ok := f.Profiles[profileName]
	if !ok {
		return profile{}, false, fmt.Errorf("profile %q not found in %s", profileName, path)
	}
	return prof, true, nil
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
