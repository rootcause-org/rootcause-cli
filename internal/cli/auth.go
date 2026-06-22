package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/config"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newLoginCmd builds `rc login` — the one-command way to give a brain checkout its API key. It writes
// the key to a gitignored .rootcause.secret.toml at the brain root, so that every subsequent `rc`
// inside this repo auto-targets the right project with no flag, no export. By default it verifies the
// key against the server and refuses to store it if the key resolves to a DIFFERENT project than the
// committed .rootcause.toml marker says — catching the "pasted the wrong project's key" mistake before
// it can mis-scope a run.
func newLoginCmd(e *env) *cobra.Command {
	var apiKey string
	var noVerify bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Store this brain's API key in a gitignored " + config.SecretFileName,
		Long: "Store the project's Prompt-API key for the brain checkout you're standing in, so `rc`\n" +
			"auto-targets this project from here on. The key is written to a gitignored\n" +
			config.SecretFileName + " (0600) at the brain root — never committed.\n\n" +
			"Run from inside a brain repo (one with a committed " + config.MarkerFileName + "). Pass the key\n" +
			"with --api-key, or omit it to paste on stdin.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			brain, err := config.DiscoverBrain(cwd)
			if err != nil {
				return err
			}
			if brain == nil {
				return fmt.Errorf("no %s found here — run `rc login` from inside a brain checkout "+
					"(a repo with a committed %s naming its project)", config.MarkerFileName, config.MarkerFileName)
			}

			key := strings.TrimSpace(apiKey)
			if key == "" {
				fmt.Fprintf(e.err, "API key for %s: ", brain.Project)
				line, rerr := readLine(e.in)
				if rerr != nil {
					return fmt.Errorf("read key from stdin: %w", rerr)
				}
				key = strings.TrimSpace(line)
			}
			if key == "" {
				return fmt.Errorf("no API key provided (pass --api-key or pipe the key on stdin)")
			}

			if !noVerify {
				base, berr := loginBaseURL(e)
				if berr != nil {
					return berr
				}
				resp, verr := client.New(base, key).Env(e.ctx(), brain.Tenant)
				if verr != nil {
					return fmt.Errorf("could not verify the key against %s: %w\n"+
						"  (use --no-verify to store it without checking)", base, verr)
				}
				if resp.Project != "" && resp.Project != brain.Project {
					return fmt.Errorf("that key resolves to project %q, but this brain's %s says %q — "+
						"wrong key? (refusing to store)", resp.Project, config.MarkerFileName, brain.Project)
				}
			}

			path := filepath.Join(brain.Dir, config.SecretFileName)
			if err := config.WriteSecret(path, key); err != nil {
				return err
			}
			fmt.Fprintf(e.out, "logged in to %s — wrote %s (0600)\n", brain.Project, config.SecretFileName)
			fmt.Fprintf(e.err, "make sure %s is gitignored — it holds a production key\n", config.SecretFileName)
			return nil
		},
	}
	cmd.Flags().StringVar(&apiKey, "api-key", "", "the API key to store (omit to read from stdin)")
	cmd.Flags().BoolVar(&noVerify, "no-verify", false, "store without the server round-trip that confirms the key matches this brain's project")
	return cmd
}

// newWhoamiCmd builds `rc whoami` — answer "which project will rc hit from here, and why?" without
// running anything. It prints the resolved project (from the brain marker), the base URL, and a
// log-safe label of where the key came from. By default it also confirms against the server (the key
// resolves the project server-side), so a stale or wrong binding shows up immediately.
func newWhoamiCmd(e *env) *cobra.Command {
	var noVerify bool
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Show which project rc targets from here (brain binding, base URL, key source)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			res, err := config.Load(e.profile)
			if err != nil {
				return err
			}
			base := res.BaseURL
			if e.baseURLOvr != "" {
				base = e.baseURLOvr
			}

			// Best-effort server confirmation: only when a key resolved and the user didn't opt out.
			// Pass the brain's tenant so a tenant-enabled project confirms instead of TENANT_REQUIRED.
			serverProject, serverErr := "", error(nil)
			if !noVerify && res.APIKey != "" {
				if resp, verr := client.New(base, res.APIKey).Env(e.ctx(), res.Tenant); verr != nil {
					serverErr = verr
				} else {
					serverProject = resp.Project
				}
			}

			if render.IsJSON(e.mode(), e.out) {
				return writeJSON(e, map[string]any{
					"project":        emptyDash(res.Project),
					"tenant":         res.Tenant,
					"base_url":       base,
					"key_source":     res.KeySource,
					"brain_dir":      brainDir(res),
					"server_project": serverProject,
				})
			}

			fmt.Fprintf(e.out, "project:     %s\n", emptyDash(res.Project))
			if res.Tenant != "" {
				fmt.Fprintf(e.out, "tenant:      %s\n", res.Tenant)
			}
			fmt.Fprintf(e.out, "base URL:    %s\n", base)
			fmt.Fprintf(e.out, "key source:  %s\n", emptyOr(res.KeySource, "(none — no key resolved)"))
			if res.Brain != nil {
				fmt.Fprintf(e.out, "brain:       %s\n", res.Brain.Dir)
			}
			switch {
			case serverErr != nil:
				fmt.Fprintf(e.err, "warning: could not confirm with the server: %v\n", serverErr)
			case serverProject != "":
				mark := "✓"
				if res.Project != "" && serverProject != res.Project {
					mark = "✗ MISMATCH — the key belongs to a different project"
				}
				fmt.Fprintf(e.out, "server says: %s %s\n", serverProject, mark)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&noVerify, "no-verify", false, "skip the server round-trip that confirms the resolved key's project")
	return cmd
}

// loginBaseURL resolves the endpoint `rc login` verifies against, reusing the SAME precedence as every
// other command (config.Load: env > .rootcause.secret.toml > marker > profile > default) so the two
// can never drift. baseURLOvr is the test seam. config.Load's BaseURL is always non-empty.
func loginBaseURL(e *env) (string, error) {
	if e.baseURLOvr != "" {
		return e.baseURLOvr, nil
	}
	res, err := config.Load(e.profile)
	if err != nil {
		return "", err
	}
	return res.BaseURL, nil
}

// readLine reads a single line from r (stdin), trimming the trailing newline. A nil reader falls back
// to os.Stdin so the command works outside tests.
func readLine(r io.Reader) (string, error) {
	if r == nil {
		r = os.Stdin
	}
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return line, nil
}

func brainDir(res config.Resolved) string {
	if res.Brain != nil {
		return res.Brain.Dir
	}
	return ""
}

func emptyDash(s string) string { return emptyOr(s, "—") }

func emptyOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
