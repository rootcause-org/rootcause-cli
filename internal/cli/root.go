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
	project    string // --project: select a project's token (and scope) without a brain
	tenant     string // --tenant: scope a request to a tenant by slug (where the endpoint accepts it)
	output     string // "", "json", or "table" (from -o/--output)
	baseURLOvr string // test-only override of the resolved base URL; empty in normal use
	tokenOvr   string // test-only static bearer; bypasses the token store + refresh

	out io.Writer
	err io.Writer
	in  io.Reader // stdin source for interactive prompts; nil → os.Stdin

	// openBrowser is the PKCE-login browser launcher; nil → oauth.OpenBrowser. Tests inject a stub that
	// drives the loopback callback so the flow runs without a real browser.
	openBrowser func(string) error

	// resolved is the config resolved by the last newClient call, so a command can read the brain's
	// tenant (to default --tenant) and the resolved profile without re-loading.
	resolved config.Resolved
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
	root := &cobra.Command{
		Use:           "rc",
		Short:         "rootcause CLI — a thin, scriptable client over the rootcause API",
		Version:       version,
		SilenceUsage:  true, // a runtime error isn't a usage error; don't dump help on it
		SilenceErrors: true, // Execute prints the error itself, verbatim
	}
	root.PersistentFlags().StringVar(&e.profile, "profile", "", "token profile to use (default: auto — the brain in the current directory, else \"default\")")
	root.PersistentFlags().StringVar(&e.project, "project", "", "target a project's token + scope (overrides the brain binding)")
	root.PersistentFlags().StringVar(&e.tenant, "tenant", "", "scope the request to a tenant by slug")
	root.PersistentFlags().StringVarP(&e.output, "output", "o", "", "output format: json|table (default: auto-detect)")

	root.AddCommand(
		newStatusCmd(e),
		newRunsCmd(e),
		newRunCmd(e),
		newFleetCmd(e),
		newPatternsCmd(e),
		newHealthCmd(e),
		newAskCmd(e),
		newConfigCmd(e),
		newEnvCmd(e),
		newTenantCmd(e),
		newLoginCmd(e),
		newLogoutCmd(e),
		newWhoamiCmd(e),
		newUpgradeCmd(e, version),
	)
	return root
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

// newClient resolves config for the selected profile/project and builds an OAuth-authenticated client.
// The bearer comes from the token store (refreshed transparently); it errors clearly with a "run `rc
// login`" prompt when there's no stored token. The base URL and token can be overridden in tests.
func (e *env) newClient() (*client.Client, error) {
	res, err := config.Load(e.profile, e.project)
	if err != nil {
		return nil, err
	}
	e.resolved = res // so commands can default --tenant to the brain's tenant

	baseURL := res.BaseURL
	if e.baseURLOvr != "" {
		baseURL = e.baseURLOvr
		res.BaseURLFromDefault = false
	}

	// Test seam: a fixed bearer bypasses the token store + refresh entirely.
	if e.tokenOvr != "" {
		return client.New(baseURL, client.StaticToken(e.tokenOvr)), nil
	}

	tok, ok, err := token.Load(res.Profile)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, notLoggedIn(res)
	}
	// A token is pinned to the issuer it was minted against — prefer that base URL so a command hits the
	// same server even if the ambient base URL drifted (unless a test override is in play).
	if e.baseURLOvr == "" && tok.BaseURL != "" {
		baseURL = tok.BaseURL
		res.BaseURLFromDefault = false
	}
	if res.BaseURLFromDefault {
		// Logged in but no base URL set anywhere → about to hit localhost. Warn to stderr (never stdout, so
		// piped output stays clean) rather than fail.
		fmt.Fprintf(e.err, "warning: no base URL set; defaulting to %s — set ROOTCAUSE_BASE_URL or base_url in your config profile\n", baseURL)
	}
	return client.New(baseURL, newLiveSource(res.Profile, baseURL)), nil
}

// notLoggedIn is the clear "no token for this profile" error, naming the project (and the one-command
// fix) when inside a brain so the user is never silently mis-scoped.
func notLoggedIn(res config.Resolved) error {
	if res.Brain != nil {
		return fmt.Errorf("this brain is project %q but you're not logged in for it\n"+
			"  fix: run `rc login` from here (use --device on a headless box)", res.Brain.Project)
	}
	return fmt.Errorf("not logged in (profile %q) — run `rc login`", res.Profile)
}

// tenantOr returns the explicit --tenant flag when set, else the brain marker's tenant (captured by
// newClient). Call after newClient so e.resolved is populated.
func (e *env) tenantOr(flag string) string {
	if flag != "" {
		return flag
	}
	return e.resolved.Tenant
}

// scopeTenant is the resolved tenant for a request: the persistent --tenant, else the brain's tenant.
func (e *env) scopeTenant() string {
	return e.tenantOr(e.tenant)
}

// tenantSlug is the explicitly-addressed tenant for `rc tenant settings` — the persistent --tenant only
// (no brain fallback: editing a tenant's record is an explicit act, never inferred from the checkout).
func (e *env) tenantSlug() string { return e.tenant }

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
		fmt.Fprintf(w, "%s: %s\n", apiErr.Code, apiErr.Message)
		// INVALID_SETTINGS carries per-field detail; print one line per field as specified.
		for _, f := range apiErr.Fields {
			fmt.Fprintf(w, "  %s: %s\n", f.Key, f.Message)
		}
		// A rejected --brain-ref usually means the ref isn't on the project's brain origin yet.
		if apiErr.Code == "BAD_BRAIN_REF" {
			fmt.Fprintln(w, "  push the ref to your project's brain first: git push origin <ref>")
		}
		return
	}
	fmt.Fprintf(w, "error: %s\n", err.Error())
}

// printNonEnvelopeHTTPError renders a non-2xx with no decodable error envelope. The bare "HTTP 405"
// the server returns here is opaque, so we lead with method + path + status text and special-case the
// two statuses a user actually hits: 405 (endpoint not deployed on this older server) and 404.
func printNonEnvelopeHTTPError(w io.Writer, e *client.APIError) {
	statusText := http.StatusText(e.Status)
	if e.Method != "" && e.Path != "" {
		fmt.Fprintf(w, "error: %s %s → HTTP %d %s\n", e.Method, e.Path, e.Status, statusText)
	} else {
		fmt.Fprintf(w, "error: HTTP %d %s\n", e.Status, statusText)
	}
	switch e.Status {
	case http.StatusMethodNotAllowed:
		fmt.Fprintln(w, "  endpoint not available on this server — it may be older than this CLI; the runs list endpoint isn't deployed")
	case http.StatusNotFound:
		fmt.Fprintln(w, "  not found — check the id/path, or this endpoint may not be available on this server")
	}
	if e.BaseURL != "" {
		fmt.Fprintf(w, "  base URL: %s\n", e.BaseURL)
	}
}
