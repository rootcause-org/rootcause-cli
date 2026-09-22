// Package config resolves the two things every command needs before it can authenticate: a base URL
// and a PROFILE NAME (the key the OAuth token store is keyed by). The INTENT is a small, predictable
// split: the server URL is production unless ROOTCAUSE_BASE_URL is explicitly set, while the current
// directory may still supply project/tenant context.
//
// Auth itself moved to OAuth: tokens live in ~/.config/rootcause/tokens.json (see internal/token),
// keyed by profile. This package no longer holds any secret — it only decides WHICH profile's token to
// use and WHICH base URL to hit. A brain repo carries a committed, non-secret marker
// (.rootcause.toml: project, optional machine_token_env, and optional legacy tenant) that binds the
// directory to one project. A developer may also
// keep a gitignored per-checkout .rootcause/local.toml with tenant = "..." as an explicit local
// override. In auto mode this resolver first names the project profile; the command layer can fall back
// to "default" when no such token is stored and carry the marker's project as ?project= for an
// all-projects token.
//
// One checkout may need a SECOND project-bound token (tokens are one-project-only by design). The
// marker may therefore declare extra [[also]] bindings, each naming a project plus its own
// machine_token_env. `--project <slug>` (or RC_PROJECT) matching such a binding selects it: the profile,
// the machine-token env var and the pin check all move to that project, under exactly the primary
// binding's provenance rules. `--profile` still wins, and a project the marker does not bind keeps
// today'"'"'s meaning (a server-side scope only an all-projects token can use).
//
// `--project` is NOT a profile selector — it does not pick a token. It is a SERVER-SIDE scope (a
// uuid-or-name passed as ?project= on the read endpoints), meaningful only for an all-projects admin
// token; the command layer threads it into the client, not this resolver. (See internal/cli/root.go.)
//
// Precedence for the profile name (the token-store key):
//
//	explicit --profile <name>   → that profile (an AWS-style override; no brain binding)
//	otherwise, inside a brain:    an [[also]] binding whose project matches --project/RC_PROJECT, else the
//	                              marker's primary project (commands may fall back to default if absent)
//	otherwise:                    "default"
//
// Precedence for the base URL:
//
//	ROOTCAUSE_BASE_URL > built-in production default
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	// DefaultBaseURL is the built-in production API/app host.
	DefaultBaseURL = "https://app.replypen.com"
	LegacyBaseURL  = "https://rootcause.probackup.io"

	// MarkerFileName is the committed, non-secret per-brain marker binding the checkout to a project.
	// It is KEPT under OAuth — it carries project/tenant context and may name a machine-token env var,
	// but never contains the credential. A legacy base_url field still decodes for compatibility, but
	// no longer affects transport resolution.
	MarkerFileName = ".rootcause.toml"

	// LocalFileName is a gitignored per-brain developer overlay under the wholesale-ignored .rootcause
	// artifact dir. It only supplies local overrides, currently tenant.
	LocalFileName = ".rootcause/local.toml"

	// DefaultProfile is the profile name used outside any brain (and when no --profile/--project is given).
	DefaultProfile = "default"

	envBaseURL = "ROOTCAUSE_BASE_URL"

	// BindingPrimary / BindingAlso name which marker binding an invocation resolved to (Resolved.BindingKind).
	BindingPrimary = "primary"
	BindingAlso    = "also"
)

// Resolved is the effective config for one invocation. Profile is the token-store key the command's
// client authenticates with. BaseURLSource names where BaseURL came from: ROOTCAUSE_BASE_URL, a test
// override, or "built-in production" (nothing set one, we fell back to DefaultBaseURL). Brain is non-nil
// when a .rootcause.toml was discovered; Project/Tenant come from it. BaseURL is always non-empty.
// Project here is the BRAIN's project (the checkout's identity), NOT the --project scope override —
// that's a server-side selector the command layer owns, never a profile.
type Resolved struct {
	Profile string
	// BindingKind names which marker binding is in force: "primary", "also" (a [[also]] binding matched
	// --project/RC_PROJECT), or "" outside a brain / with an explicit --profile.
	BindingKind   string
	BaseURL       string
	BaseURLSource string
	Project       string
	Tenant        string
	TenantSource  string
	Brain         *Brain
}

// Brain is the committed .rootcause.toml marker: the project this checkout belongs to. Dir is the
// directory the marker was found in. MachineTokenEnv names an optional secret source for headless
// agents; it never stores the token. Tenant is a legacy/local override; the normal tenant-enabled path
// gets tenant scope from the active OAuth login. A legacy base_url key in the marker is ignored (toml
// tolerates unknown keys); transport is env-or-production only.
type Brain struct {
	// Project/MachineTokenEnv are the EFFECTIVE binding for this invocation: the marker's primary pair,
	// or the matched [[also]] pair. Primary keeps the marker's own primary pair regardless of selection.
	Project         string    `toml:"project"`
	MachineTokenEnv string    `toml:"machine_token_env"`
	Tenant          string    `toml:"tenant"`
	Also            []Binding `toml:"also"`
	Dir             string    `toml:"-"`
	Primary         Binding   `toml:"-"`
	SelectedAlso    bool      `toml:"-"`
}

// Binding is one project ↔ machine-token-env pair. The marker's top level carries the primary one; each
// [[also]] table adds a secondary project this checkout may also authenticate as, with its own
// separately minted, project-pinned token. A binding never holds the credential, only the variable name.
type Binding struct {
	Project         string `toml:"project"`
	MachineTokenEnv string `toml:"machine_token_env"`
}

// local is the optional gitignored per-checkout overlay. Keep it intentionally narrow: tenant is often
// developer-local, while transport is intentionally env-or-production only.
type local struct {
	Tenant string `toml:"tenant"`
}

// Load resolves config for one invocation. profileName comes from --profile; empty means "auto" (bind
// to the brain in cwd, else [default]). --project is NOT resolved here — it's a server-side scope the
// command layer threads into the client, never a token-store key.
func Load(profileName string) (Resolved, error) {
	return LoadFor(profileName, "")
}

// LoadFor is Load plus the requested --project/RC_PROJECT selector, which can only ever pick one of the
// marker's own [[also]] bindings. Anything else (a project the marker does not bind, an explicit
// --profile, no brain) resolves exactly as Load does — the selector stays a server-side scope.
func LoadFor(profileName, project string) (Resolved, error) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "" // a missing cwd only disables brain auto-discovery
	}
	return loadFor(profileName, project, cwd)
}

// load is Load with cwd injected, so the resolution matrix is unit-testable without chdir.
func load(profileName, cwd string) (Resolved, error) { return loadFor(profileName, "", cwd) }

func loadFor(profileName, project, cwd string) (Resolved, error) {
	// Explicit --profile <name>: a pure override, no brain binding (the documented escape hatch). The
	// token store is the source of auth; config.toml base_url profiles are intentionally ignored.
	if profileName != "" {
		res := Resolved{Profile: profileName}
		applyBaseURL(&res)
		return res, nil
	}

	// Auto mode: are we inside a brain?
	brain, err := DiscoverBrain(cwd)
	if err != nil {
		return Resolved{}, err
	}
	if brain == nil {
		// Outside any brain: the default token profile.
		res := Resolved{Profile: DefaultProfile}
		applyBaseURL(&res)
		return res, nil
	}

	// A secondary binding is a full, independent identity: its own profile, its own machine-token env var
	// and its own pin check. The marker/local tenant belongs to the primary project, so it is NOT carried
	// over — an explicit --tenant still applies, resolved one layer up.
	if bind, ok := brain.selectAlso(project); ok {
		bound := *brain
		bound.Project = bind.Project
		bound.MachineTokenEnv = bind.MachineTokenEnv
		bound.Tenant = ""
		bound.SelectedAlso = true
		res := Resolved{
			Profile:     bind.Project,
			BindingKind: BindingAlso,
			Project:     bind.Project,
			Brain:       &bound,
		}
		applyBaseURL(&res)
		return res, nil
	}

	// Inside a brain: first name the project profile. Transport still stays env > production.
	tenant, tenantSource, err := resolveTenant(brain)
	if err != nil {
		return Resolved{}, err
	}
	res := Resolved{
		Profile:      brain.Project,
		BindingKind:  BindingPrimary,
		Project:      brain.Project,
		Tenant:       tenant,
		TenantSource: tenantSource,
		Brain:        brain,
	}
	applyBaseURL(&res)
	return res, nil
}

func applyBaseURL(res *Resolved) {
	if v := os.Getenv(envBaseURL); v != "" {
		res.BaseURL = CanonicalBaseURL(v)
		res.BaseURLSource = envBaseURL
		return
	}
	res.BaseURL = DefaultBaseURL
	res.BaseURLSource = "built-in production"
}

// CanonicalBaseURL maps the legacy production hostname onto the customer-facing ReplyPen app host.
// Custom/staging hosts pass through unchanged.
func CanonicalBaseURL(u string) string {
	if u == LegacyBaseURL {
		return DefaultBaseURL
	}
	return u
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
			if verr := validateMarker(&b, path); verr != nil {
				return nil, verr
			}
			b.Primary = Binding{Project: b.Project, MachineTokenEnv: b.MachineTokenEnv}
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

// selectAlso picks the [[also]] binding the requested project names. An empty request, or one naming the
// primary project, returns false — the primary binding stays the default for this checkout.
func (b *Brain) selectAlso(project string) (Binding, bool) {
	if b == nil || project == "" || project == b.Primary.Project {
		return Binding{}, false
	}
	for _, bind := range b.Also {
		if bind.Project == project {
			return bind, true
		}
	}
	return Binding{}, false
}

// validateMarker enforces the two things a multi-binding marker must guarantee before any credential is
// touched: every binding names a project and a well-formed env var, and no project or variable is claimed
// twice (which would make "which token am I sending?" ambiguous).
func validateMarker(b *Brain, path string) error {
	if b.Project == "" {
		return fmt.Errorf("%s has no `project` field — it must name the project this brain belongs to", path)
	}
	if b.MachineTokenEnv != "" && !validMachineTokenEnvName(b.MachineTokenEnv) {
		return fmt.Errorf("%s machine_token_env must match RC_REFRESH_TOKEN_[A-Z0-9_]+", path)
	}
	projects := map[string]bool{b.Project: true}
	envs := map[string]bool{}
	if b.MachineTokenEnv != "" {
		envs[b.MachineTokenEnv] = true
	}
	for _, bind := range b.Also {
		if bind.Project == "" {
			return fmt.Errorf("%s has an [[also]] binding without a `project` field", path)
		}
		if bind.MachineTokenEnv == "" {
			return fmt.Errorf("%s [[also]] binding for project %q needs its own machine_token_env", path, bind.Project)
		}
		if !validMachineTokenEnvName(bind.MachineTokenEnv) {
			return fmt.Errorf("%s [[also]] machine_token_env must match RC_REFRESH_TOKEN_[A-Z0-9_]+", path)
		}
		if projects[bind.Project] {
			return fmt.Errorf("%s binds project %q twice — each project may appear once", path, bind.Project)
		}
		if envs[bind.MachineTokenEnv] {
			return fmt.Errorf("%s uses machine_token_env %q twice — each binding needs its own variable", path, bind.MachineTokenEnv)
		}
		projects[bind.Project] = true
		envs[bind.MachineTokenEnv] = true
	}
	return nil
}

func validMachineTokenEnvName(name string) bool {
	const prefix = "RC_REFRESH_TOKEN_"
	if !strings.HasPrefix(name, prefix) || len(name) == len(prefix) {
		return false
	}
	for _, r := range name[len(prefix):] {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

// UpdateBrainProject rewrites the committed brain marker after the server has renamed a project. It
// preserves the rest of the file and only updates a checkout that was bound to oldProject.
func UpdateBrainProject(brain *Brain, oldProject, newProject string) (bool, error) {
	if brain == nil || brain.Primary.Project != oldProject || oldProject == newProject {
		return false, nil
	}
	path := filepath.Join(brain.Dir, MarkerFileName)
	body, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	lines := strings.SplitAfter(string(body), "\n")
	changed := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "project") && strings.Contains(trimmed, "=") {
			suffix := ""
			if strings.HasSuffix(line, "\n") {
				suffix = "\n"
			}
			lines[i] = fmt.Sprintf("project = %q%s", newProject, suffix)
			changed = true
			break
		}
	}
	if !changed {
		if len(lines) > 0 && !strings.HasSuffix(lines[len(lines)-1], "\n") {
			lines[len(lines)-1] += "\n"
		}
		lines = append(lines, fmt.Sprintf("project = %q\n", newProject))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "")), 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	brain.Primary.Project = newProject
	if !brain.SelectedAlso {
		brain.Project = newProject
	}
	return true, nil
}
