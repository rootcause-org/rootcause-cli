// Package cli is the command surface: it wires cobra commands onto the client + render layers, holds
// the global flags (--profile, -o/--output), and owns the one cross-cutting concern the rest of the
// CLI must not repeat — building an authenticated client from resolved config and printing API errors
// verbatim. Each command file is a thin adapter: parse flags → one client call → render. No business
// logic lives here.
package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/config"
	"github.com/rootcause-org/rootcause-cli/internal/render"
	"github.com/rootcause-org/rootcause-cli/internal/token"
)

// env carries the shared, testable state through commands: the global flag values plus the writers.
// Tests inject baseURL/output/token and capture out/err here instead of relying on TTY detection or a
// real token store.
type env struct {
	profile    string // --profile: an explicit token-store profile (AWS-style override)
	project    string // --project: server-side project scope; it never selects a token profile
	tenant     string // --tenant: explicit tenant override where an endpoint accepts it
	output     string // "", "json", or "table" (from -o/--output)
	outDir     string // --out-dir: local artifact directory for large stdout/JSON payloads
	noPreview  bool   // --no-preview: suppress head/tail previews in spill manifests
	rawOutput  bool   // --raw-output: exact full stdout, disabling spill manifests
	baseURLOvr string // test-only override of the resolved base URL; empty in normal use
	tokenOvr   string // test-only static bearer; bypasses the token store + refresh

	out io.Writer
	err io.Writer
	in  io.Reader // stdin source for interactive prompts; nil → os.Stdin

	// openBrowser is the PKCE-login browser launcher; nil → oauth.OpenBrowser. Tests inject a stub that
	// drives the loopback callback so the flow runs without a real browser.
	openBrowser func(string) error

	// resolved is the config resolved by the last newClient call, so a command can read local overrides
	// and the resolved profile without re-loading.
	resolved config.Resolved

	// autoProject is set when a command falls back from a missing brain-named profile to the default
	// profile. It lets all-projects tokens keep the checkout's project scope without the user repeating
	// --project in every brain repo.
	autoProject string
	loginTenant string
	scopeHeader bool
}

// Execute is the binary entrypoint. It returns the process exit code so main stays trivial; any
// command error (including a typed APIError) is printed to stderr here, once.
func Execute(version string) int {
	e := &env{out: os.Stdout, err: os.Stderr, in: os.Stdin}
	root := newRootCmd(e, version)
	if err := root.Execute(); err != nil {
		printError(e.err, err)
		return 1
	}
	return 0
}

// newRootCmd assembles the root command + global flags + subcommands. Split out so tests can build a
// root against an in-memory env and a stub server.
func newRootCmd(e *env, version string) *cobra.Command {
	cobra.EnableCommandSorting = false
	root := &cobra.Command{
		Use:           "rc",
		Short:         "rootcause CLI — a thin, scriptable client over the rootcause API",
		Version:       version,
		SilenceUsage:  true, // a runtime error isn't a usage error; don't dump help on it
		SilenceErrors: true, // Execute prints the error itself, verbatim
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateCommandScope(e, cmd); err != nil {
				return err
			}
			installScopeHeader(e, cmd)
			return nil
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.AddGroup(
		&cobra.Group{ID: "start", Title: "Start:"},
		&cobra.Group{ID: "manage", Title: "Manage:"},
		&cobra.Group{ID: "develop", Title: "Develop:"},
		&cobra.Group{ID: "operate", Title: "Operate:"},
		&cobra.Group{ID: "local", Title: "Local:"},
	)
	root.SetHelpCommandGroupID("local")
	root.PersistentFlags().StringVar(&e.profile, "profile", "", "token profile to use (default: auto — the brain in the current directory, else \"default\")")
	root.PersistentFlags().StringVar(&e.project, "project", "", "scope the request to one project by name or id (requires an all-projects token)")
	root.PersistentFlags().StringVar(&e.tenant, "tenant", "", "override the login tenant where supported")
	root.PersistentFlags().StringVarP(&e.output, "output", "o", "", "output format: json|table (default: auto-detect)")
	root.PersistentFlags().StringVar(&e.outDir, "out-dir", "", "directory for large output artifacts (default: RC_OUTPUT_DIR or .rootcause/output)")
	root.PersistentFlags().BoolVar(&e.noPreview, "no-preview", false, "write large output artifacts but omit head/tail previews")
	root.PersistentFlags().BoolVar(&e.rawOutput, "raw-output", false, "disable progressive output spill and print full payloads to stdout")

	root.AddCommand(newTopLevelCommands(e, root, version)...)
	root.InitDefaultHelpCmd()
	for _, cmd := range root.Commands() {
		if cmd.Name() == "help" {
			cmd.Hidden = true
			break
		}
	}
	root.SetUsageTemplate(strings.ReplaceAll(root.UsageTemplate(), `(or .IsAvailableCommand (eq .Name "help"))`, `.IsAvailableCommand`))
	makeGroupsStrict(root)
	annotateCommandScopes(root)
	return root
}

// makeGroupsStrict preserves help for a bare group while rejecting stray positional tokens. Cobra's
// default for a non-runnable group is to print help and succeed even when the token is an unknown child;
// that would make removed command paths look live after the clean break.
func makeGroupsStrict(root *cobra.Command) {
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		for _, child := range cmd.Commands() {
			walk(child)
		}
		if cmd == root || len(cmd.Commands()) == 0 || cmd.Run != nil || cmd.RunE != nil {
			return
		}
		cmd.Args = cobra.NoArgs
		cmd.RunE = func(cmd *cobra.Command, _ []string) error { return cmd.Help() }
	}
	walk(root)
}

// jsonOut reports whether output should be JSON for the current mode + destination.
func (e *env) jsonOut() bool { return render.IsJSON(e.mode(), e.out) }

// mode maps the -o/--output flag to a render.Mode (empty → auto-detect from the destination).
func (e *env) mode() render.Mode {
	switch e.output {
	case "json":
		return render.ModeJSON
	case "table":
		return render.ModeTable
	default:
		return render.ModeAuto
	}
}

// newClient resolves config for the selected profile and builds an OAuth-authenticated client. The
// bearer comes from the token store (refreshed transparently); it errors clearly with a "run `rc auth login`"
// prompt when there's no stored token. --project is NOT resolved here — it's a server-side scope the
// commands thread into each read request (see scopeProject). The base URL and token can be overridden in
// tests.
func (e *env) newClient() (*client.Client, error) {
	res, err := config.Load(e.profile)
	if err != nil {
		return nil, err
	}
	e.autoProject = ""
	e.resolved = res // so commands can apply any local tenant override

	baseURL := res.BaseURL
	if e.baseURLOvr != "" {
		baseURL = e.baseURLOvr
		res.BaseURLFromDefault = false
		res.BaseURLSource = "test override"
		e.resolved = res
	}

	// Test seam: a fixed bearer bypasses the token store + refresh entirely.
	if e.tokenOvr != "" {
		c := client.New(baseURL, client.StaticToken(e.tokenOvr))
		e.resolveScopeHeader(c)
		if err := e.validateProjectScope(c); err != nil {
			return nil, err
		}
		return c, nil
	}

	_, ok, err := token.Load(res.Profile)
	if err != nil {
		return nil, err
	}
	if !ok {
		if e.profile == "" && res.Brain != nil {
			if _, fallbackOK, ferr := token.Load(config.DefaultProfile); ferr != nil {
				return nil, ferr
			} else if fallbackOK {
				res.Profile = config.DefaultProfile
				e.autoProject = res.Brain.Project
				e.resolved = res
				ok = true
			}
		}
	}
	if !ok {
		return nil, notLoggedIn(res)
	}
	c := client.New(baseURL, newLiveSource(res.Profile, baseURL))
	if err := e.resolveProjectForTenant(c); err != nil {
		return nil, err
	}
	e.resolveScopeHeader(c)
	if err := e.validateProjectScope(c); err != nil {
		return nil, err
	}
	return c, nil
}

// resolveScopeHeader learns an implicit tenant pin only for human output. Failure is non-fatal: the
// command's real request remains authoritative and can still render without the convenience header.
func (e *env) resolveScopeHeader(c *client.Client) {
	if !e.scopeHeader || e.scopeTenant() != "" {
		return
	}
	scope, err := c.Whoami(e.ctx())
	if err != nil || scope == nil || scope.Tenant == nil {
		return
	}
	e.loginTenant = scope.Tenant.Slug
	if e.loginTenant == "" {
		e.loginTenant = scope.Tenant.Name
	}
	if e.scopeProject() == "" && scope.Project != nil {
		e.autoProject = scope.Project.Name
		if e.autoProject == "" {
			e.autoProject = scope.Project.ID
		}
	}
}

// resolveProjectForTenant supplies the canonical project-tree prefix when a project-pinned login uses
// an explicit/local tenant selector outside a brain checkout. The token is authoritative: an
// all-projects login still has to name --project instead of guessing among visible projects.
func (e *env) resolveProjectForTenant(c *client.Client) error {
	if e.scopeTenant() == "" || e.scopeProject() != "" {
		return nil
	}
	return e.resolvePinnedProject(c)
}

// resolvePinnedProject supplies the project-tree prefix for commands whose canonical API has no flat
// current-project form. A global login must choose --project explicitly.
func (e *env) resolvePinnedProject(c *client.Client) error {
	if e.scopeProject() != "" {
		return nil
	}
	scope, err := c.Whoami(e.ctx())
	if err != nil {
		return err
	}
	if scope.Project == nil {
		return &client.APIError{
			Status:  http.StatusBadRequest,
			Code:    "PROJECT_REQUIRED",
			Message: "--project <project> is required for an all-projects login",
		}
	}
	e.autoProject = scope.Project.Name
	if e.autoProject == "" {
		e.autoProject = scope.Project.ID
	}
	return nil
}

// notLoggedIn is the clear "no token for this profile" error. Inside a brain this is reached only
// when neither the brain-named profile nor default has a token, so name the project and the fix.
func notLoggedIn(res config.Resolved) error {
	if res.Brain != nil {
		return fmt.Errorf("this brain is project %q but you're not logged in for it\n"+
			"  fix: run `rc auth login` from here (use --device on a headless box)", res.Brain.Project)
	}
	return fmt.Errorf("not logged in (profile %q) — run `rc auth login`", res.Profile)
}

// tenantOr returns the explicit --tenant flag when set, else any local tenant override captured by
// newClient. Call after newClient so e.resolved is populated.
func (e *env) tenantOr(flag string) string {
	if flag != "" {
		return flag
	}
	return e.resolved.Tenant
}

// scopeTenant is the explicit/local tenant override for a request. Empty lets the login-bound tenant win
// server-side on tenant-enabled projects.
func (e *env) scopeTenant() string {
	if tenant := e.tenantOr(e.tenant); tenant != "" {
		return tenant
	}
	return e.loginTenant
}

// scopeProject is the explicit --project scope (id-or-name) for a read request: it rides as ?project= on
// the observability endpoints, where an all-projects admin token uses it to scope a single project. Empty
// for the common case (a pinned token reads its own project; the param is then disregarded server-side).
// It is NOT a profile/token selector — the token is resolved independently of it.
func (e *env) scopeProject() string {
	if e.project != "" {
		return e.project
	}
	if e.scopeTenant() != "" && e.resolved.Project != "" {
		return e.resolved.Project
	}
	return e.autoProject
}

// validateProjectScope checks an explicit/brain-derived project scope against the fleet handles the
// token can see. It catches typos before a command renders local state or makes a scoped write.
func (e *env) validateProjectScope(c *client.Client) error {
	project := e.project
	if project == "" {
		project = e.autoProject
	}
	if project == "" {
		return nil
	}
	resp, err := c.Projects(e.ctx())
	if err != nil {
		return err
	}
	for _, p := range resp.Projects {
		if project == p.ID || project == p.Name {
			return nil
		}
	}
	return &client.APIError{
		Status:  http.StatusNotFound,
		Code:    "UNKNOWN_PROJECT",
		Message: fmt.Sprintf("unknown project %q (run `rc project list` to see available projects)", project),
	}
}

// ctx is the per-command context. A single place to add a timeout/signal later without touching each
// command.
func (e *env) ctx() context.Context { return context.Background() }

// printError renders any command error to stderr. A JSON-envelope APIError is surfaced verbatim
// (code: message), with INVALID_SETTINGS field lines indented beneath. A no-envelope APIError (a
// plain-text non-2xx — proxy, or an older server missing the endpoint) gets method + path + status
// text + base URL so the user can see WHAT was hit WHERE, with a pointed hint for the common 404/405.
func printError(w io.Writer, err error) {
	var apiErr *client.APIError
	if asAPIError(err, &apiErr) {
		if apiErr.Code == "" {
			printNonEnvelopeHTTPError(w, apiErr)
			return
		}
		_, _ = fmt.Fprintf(w, "%s: %s\n", apiErr.Code, apiErr.Message)
		// INVALID_SETTINGS carries per-field detail; print one line per field as specified.
		for _, f := range apiErr.Fields {
			_, _ = fmt.Fprintf(w, "  %s: %s\n", f.Key, f.Message)
		}
		// A rejected --brain-ref usually means the ref isn't on the project's brain origin yet.
		if apiErr.Code == "BAD_BRAIN_REF" {
			_, _ = fmt.Fprintln(w, "  push the ref to your project's brain first: git push origin <ref>")
		}
		return
	}
	_, _ = fmt.Fprintf(w, "error: %s\n", err.Error())
}

// printNonEnvelopeHTTPError renders a non-2xx with no decodable error envelope. The bare "HTTP 405"
// the server returns here is opaque, so we lead with method + path + status text and special-case the
// two statuses a user actually hits: 405 (endpoint not deployed on this older server) and 404.
func printNonEnvelopeHTTPError(w io.Writer, e *client.APIError) {
	statusText := http.StatusText(e.Status)
	if e.Method != "" && e.Path != "" {
		_, _ = fmt.Fprintf(w, "error: %s %s → HTTP %d %s\n", e.Method, e.Path, e.Status, statusText)
	} else {
		_, _ = fmt.Fprintf(w, "error: HTTP %d %s\n", e.Status, statusText)
	}
	switch e.Status {
	case http.StatusMethodNotAllowed:
		_, _ = fmt.Fprintln(w, "  endpoint not available on this server — it may be older than this CLI; the runs list endpoint isn't deployed")
	case http.StatusNotFound:
		_, _ = fmt.Fprintln(w, "  not found — check the id/path, or this endpoint may not be available on this server")
	}
	if e.BaseURL != "" {
		_, _ = fmt.Fprintf(w, "  base URL: %s\n", e.BaseURL)
	}
}
