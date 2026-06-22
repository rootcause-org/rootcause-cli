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
)

// env carries the shared, testable state through commands: the global flag values plus the writers.
// Tests inject baseURL/output and capture out/err here instead of relying on TTY detection or env.
type env struct {
	profile    string
	output     string // "", "json", or "table" (from -o/--output)
	baseURLOvr string // test-only override of the resolved base URL; empty in normal use

	out io.Writer
	err io.Writer
	in  io.Reader // stdin source for interactive prompts (rc login); nil → os.Stdin

	// resolvedTenant is the brain marker's tenant (if any), captured by the last newClient call so a
	// command can default an empty --tenant to the checkout's tenant. Empty for non-tenant brains.
	resolvedTenant string
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
	root.PersistentFlags().StringVar(&e.profile, "profile", "", "config profile to use (default: auto — bind to the brain in the current directory, else [default])")
	root.PersistentFlags().StringVarP(&e.output, "output", "o", "", "output format: json|table (default: auto-detect)")

	root.AddCommand(
		newStatusCmd(e),
		newRunsCmd(e),
		newRunCmd(e),
		newAskCmd(e),
		newConfigCmd(e),
		newEnvCmd(e),
		newTenantCmd(e),
		newLoginCmd(e),
		newWhoamiCmd(e),
	)
	return root
}

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

// newClient resolves config for the selected profile and builds an authenticated client. It errors
// clearly when no API key resolves — every command that calls this needs auth. The base URL can be
// overridden in tests to point at a stub server.
func (e *env) newClient() (*client.Client, error) {
	res, err := config.Load(e.profile)
	if err != nil {
		return nil, err
	}
	e.resolvedTenant = res.Tenant // so commands can default --tenant to the brain's tenant
	if e.baseURLOvr != "" {
		res.BaseURL = e.baseURLOvr
		res.BaseURLFromDefault = false
	}
	if res.APIKey == "" {
		// Inside a brain with no key, name the project and the one-command fix — never silently fall back
		// to a different project's key. Outside a brain, the generic hint.
		if res.Brain != nil {
			return nil, fmt.Errorf("this brain is project %q but no API key is configured for it\n"+
				"  fix: run `rc login` from here (writes %s), or set ROOTCAUSE_API_KEY, or add [profiles.%s] to %s",
				res.Brain.Project, config.SecretFileName, res.Brain.Project, config.ConfigPath())
		}
		return nil, fmt.Errorf("no API key: set ROOTCAUSE_API_KEY or add it to %s", config.ConfigPath())
	}
	// A resolved key but an unset base URL almost always means the user forgot ROOTCAUSE_BASE_URL and is
	// about to hit localhost — warn (to stderr, so it never pollutes piped output) rather than fail.
	if res.BaseURLFromDefault {
		fmt.Fprintf(e.err, "warning: no base URL set; defaulting to %s — set ROOTCAUSE_BASE_URL or base_url in your config profile\n", res.BaseURL)
	}
	return client.New(res.BaseURL, res.APIKey), nil
}

// tenantOr returns the explicit --tenant flag when set, else the brain marker's tenant (captured by
// newClient). Call after newClient so resolvedTenant is populated.
func (e *env) tenantOr(flag string) string {
	if flag != "" {
		return flag
	}
	return e.resolvedTenant
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
