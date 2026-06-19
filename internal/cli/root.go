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
}

// Execute is the binary entrypoint. It returns the process exit code so main stays trivial; any
// command error (including a typed APIError) is printed to stderr here, once.
func Execute(version string) int {
	e := &env{out: os.Stdout, err: os.Stderr}
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
	root.PersistentFlags().StringVar(&e.profile, "profile", "default", "config profile to use")
	root.PersistentFlags().StringVarP(&e.output, "output", "o", "", "output format: json|table (default: auto-detect)")

	root.AddCommand(
		newStatusCmd(e),
		newRunsCmd(e),
		newRunCmd(e),
		newConfigCmd(e),
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
	if e.baseURLOvr != "" {
		res.BaseURL = e.baseURLOvr
	}
	if res.APIKey == "" {
		return nil, fmt.Errorf("no API key: set ROOTCAUSE_API_KEY or add it to ~/.config/rootcause/config.toml")
	}
	return client.New(res.BaseURL, res.APIKey), nil
}

// ctx is the per-command context. A single place to add a timeout/signal later without touching each
// command.
func (e *env) ctx() context.Context { return context.Background() }

// printError renders any command error to stderr. An APIError is surfaced verbatim (code: message),
// with INVALID_SETTINGS field lines indented beneath; everything else prints its message as-is.
func printError(w io.Writer, err error) {
	var apiErr *client.APIError
	if asAPIError(err, &apiErr) {
		if apiErr.Code == "" {
			fmt.Fprintf(w, "error: HTTP %d\n", apiErr.Status)
			return
		}
		fmt.Fprintf(w, "%s: %s\n", apiErr.Code, apiErr.Message)
		// INVALID_SETTINGS carries per-field detail; print one line per field as specified.
		for _, f := range apiErr.Fields {
			fmt.Fprintf(w, "  %s: %s\n", f.Key, f.Message)
		}
		return
	}
	fmt.Fprintf(w, "error: %s\n", err.Error())
}
