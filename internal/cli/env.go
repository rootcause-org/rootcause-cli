package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// localEnvPath is the brain-dir-relative file `rc env` reads/writes — the CLI operates on the current
// directory's ./.env (run it from inside a brain clone), mirroring `rc_env.py`'s 0600 ./.env and the
// brain-dev convention.
const localEnvPath = ".env"

// envFileMode is the owner-only mode for the pulled ./.env — the file holds the project's PRODUCTION
// secrets even though they arrived over TLS, so it must never be group/world-readable.
const envFileMode = 0o600

// newEnvCmd builds `rc env`: bulk pull/diff/keys for the grounding env plus per-key set/rm/reveal over
// the sealed env collections. SECRET HYGIENE: pull writes values to the 0600 ./.env, diff/keys emit
// names only, set reads from stdin by default, and reveal is the only command that prints a value.
func newEnvCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Manage this project's sealed env secrets",
	}
	cmd.AddCommand(newEnvPullCmd(e), newEnvDiffCmd(e), newEnvKeysCmd(e),
		newEnvSetCmd(e), newEnvRmCmd(e), newEnvRevealCmd(e))
	return cmd
}

// envPlaneResource maps the --plane flag to the collection resource backing per-key env writes. The
// default is the GROUNDING plane (read-only data the agent grounds on); --plane action targets the
// ACTION plane (the credentials the write-path actions use). Both auto-exist in /meta/schema.
func envPlaneResource(plane string) (string, error) {
	switch plane {
	case "", "grounding":
		return "env_grounding", nil
	case "action":
		return "env_action", nil
	default:
		return "", fmt.Errorf("unknown --plane %q (use grounding|action)", plane)
	}
}

// newEnvSetCmd: `rc env set key=<K> value=<V> [--plane action]` — upsert ONE env var via POST
// /api/v1/<plane> (create is an upsert). The VALUE is a secret, so it's read from STDIN by preference:
// pass `value=-` (or omit value entirely) to read the value from stdin and keep it off the argv/process
// table. An inline `value=<V>` is accepted for convenience but lands in shell history.
func newEnvSetCmd(e *env) *cobra.Command {
	var plane string
	cmd := &cobra.Command{
		Use:   "set key=<K> [value=<V>] [--plane grounding|action]",
		Short: "Upsert one env var (value from STDIN by default; never echoed)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			resource, err := envPlaneResource(plane)
			if err != nil {
				return err
			}
			body, err := parseItemArgs(args)
			if err != nil {
				return err
			}
			if body["key"] == nil || body["key"] == "" {
				return fmt.Errorf("missing key=<K>")
			}
			// Secret hygiene: read the value from stdin when it's absent or explicitly "-", so it never
			// rides in argv. An inline value=<V> is honored as-is.
			if v, ok := body["value"]; !ok || v == "-" {
				secret, rerr := readSecretStdin(e, "env value")
				if rerr != nil {
					return rerr
				}
				body["value"] = secret
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			item, raw, err := c.Create(e.ctx(), resource, body, e.scopeProject(), e.scopeTenant())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			// Never echo the value back — print the name + plane only.
			_, _ = fmt.Fprintf(e.out, "set %s (%s)\n", body["key"], resource)
			_ = item
			return nil
		},
	}
	cmd.Flags().StringVar(&plane, "plane", "grounding", "which env plane: grounding|action")
	return cmd
}

// newEnvRmCmd: `rc env rm <K> [--plane action]` — DELETE /api/v1/<plane>/<K>.
func newEnvRmCmd(e *env) *cobra.Command {
	var plane string
	cmd := &cobra.Command{
		Use:   "rm <K> [--plane grounding|action]",
		Short: "Delete one env var",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			resource, err := envPlaneResource(plane)
			if err != nil {
				return err
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			raw, err := c.Delete(e.ctx(), resource, args[0], e.scopeProject(), e.scopeTenant())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				if len(raw) > 0 {
					return render.JSON(e.out, raw)
				}
				return render.JSON(e.out, []byte(`{"deleted":"`+args[0]+`"}`))
			}
			_, _ = fmt.Fprintf(e.out, "deleted %s (%s)\n", args[0], resource)
			return nil
		},
	}
	cmd.Flags().StringVar(&plane, "plane", "grounding", "which env plane: grounding|action")
	return cmd
}

// newEnvRevealCmd: `rc env reveal <K> [--plane action]` — POST /api/v1/<plane>/<K>/reveal → {secret}.
// Prints the VALUE alone to stdout (capturable) with a stderr sensitivity warning, like connection
// reveal. The ONE place a per-key value is printed.
func newEnvRevealCmd(e *env) *cobra.Command {
	var plane string
	cmd := &cobra.Command{
		Use:   "reveal <K> [--plane grounding|action]",
		Short: "Print one env var's value (sensitive, shown once)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			resource, err := envPlaneResource(plane)
			if err != nil {
				return err
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			item, raw, err := c.Verb(e.ctx(), resource, args[0], "reveal", e.scopeProject(), e.scopeTenant())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			_, _ = fmt.Fprintln(e.err, "warning: this is a live secret — handle with care; it is shown once")
			render.Secret(e.out, item)
			return nil
		},
	}
	cmd.Flags().StringVar(&plane, "plane", "grounding", "which env plane: grounding|action")
	return cmd
}

func newEnvKeysCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "keys",
		Short: "List the key NAMES of the server's grounding env (log-safe, no values)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			resp, err := c.Env(e.ctx(), e.scopeTenant(), e.scopeProject())
			if err != nil {
				return err
			}
			names := sortedNames(resp.Keys)
			if render.IsJSON(e.mode(), e.out) {
				return writeJSON(e, map[string]any{"project": resp.Project, "tenant": resp.Tenant, "keys": names, "count": len(names)})
			}
			for _, n := range names {
				_, _ = fmt.Fprintln(e.out, n)
			}
			_, _ = fmt.Fprintf(e.err, "%d keys (%s)\n", len(names), scopeLabel(resp))
			return nil
		},
	}
	return cmd
}

func newEnvPullCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Fetch the production grounding env and write ./.env (0600); prints NAMES + count, never values",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			resp, err := c.Env(e.ctx(), e.scopeTenant(), e.scopeProject())
			if err != nil {
				return err
			}
			if err := writeEnvFile(localEnvPath, resp.Keys); err != nil {
				return err
			}
			names := sortedNames(resp.Keys)
			if render.IsJSON(e.mode(), e.out) {
				return writeJSON(e, map[string]any{
					"path": localEnvPath, "project": resp.Project, "tenant": resp.Tenant,
					"keys": names, "count": len(names),
				})
			}
			_, _ = fmt.Fprintf(e.out, "wrote %s (0600) — %d keys (%s):\n", localEnvPath, len(names), scopeLabel(resp))
			_, _ = fmt.Fprintf(e.out, "  %s\n", strings.Join(names, ", "))
			return nil
		},
	}
	return cmd
}

func newEnvDiffCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Compare local ./.env to the server (NAMES-only drift); nonzero exit on drift",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			resp, err := c.Env(e.ctx(), e.scopeTenant(), e.scopeProject())
			if err != nil {
				return err
			}
			local, err := readEnvFile(localEnvPath)
			if err != nil {
				return err
			}
			d := diffEnv(local, resp.Keys)

			if render.IsJSON(e.mode(), e.out) {
				if err := writeJSON(e, map[string]any{
					"in_sync": d.inSync(), "value_differs": d.valueDiffers,
					"only_local": d.onlyLocal, "only_server": d.onlyServer,
				}); err != nil {
					return err
				}
			} else {
				renderDiff(e, resp, d)
			}
			if !d.inSync() {
				// Nonzero exit on drift (like `rc_env.py --verify`): the detail is already printed; this
				// terse error only drives the exit code.
				return fmt.Errorf("drift: %d value-differs, %d only-local, %d only-server",
					len(d.valueDiffers), len(d.onlyLocal), len(d.onlyServer))
			}
			return nil
		},
	}
	return cmd
}

// envDiff is the names-only drift between a local and the server env.
type envDiff struct {
	valueDiffers []string // present in both, value differs
	onlyLocal    []string // present locally, absent on the server
	onlyServer   []string // present on the server, absent locally
}

func (d envDiff) inSync() bool {
	return len(d.valueDiffers) == 0 && len(d.onlyLocal) == 0 && len(d.onlyServer) == 0
}

// diffEnv computes the names-only drift, sorted — mirrors `rc_env.py --verify`'s three buckets.
func diffEnv(local, server map[string]string) envDiff {
	var d envDiff
	for k, lv := range local {
		if sv, ok := server[k]; !ok {
			d.onlyLocal = append(d.onlyLocal, k)
		} else if sv != lv {
			d.valueDiffers = append(d.valueDiffers, k)
		}
	}
	for k := range server {
		if _, ok := local[k]; !ok {
			d.onlyServer = append(d.onlyServer, k)
		}
	}
	sort.Strings(d.valueDiffers)
	sort.Strings(d.onlyLocal)
	sort.Strings(d.onlyServer)
	return d
}

// renderDiff prints the human drift report (names only).
func renderDiff(e *env, resp *client.EnvResponse, d envDiff) {
	if d.inSync() {
		_, _ = fmt.Fprintf(e.out, "in sync: %d keys match the server (%s)\n", len(resp.Keys), scopeLabel(resp))
		return
	}
	_, _ = fmt.Fprintf(e.out, "DRIFT vs server (%s) — names only:\n", scopeLabel(resp))
	if len(d.valueDiffers) > 0 {
		_, _ = fmt.Fprintf(e.out, "  value differs : %s\n", strings.Join(d.valueDiffers, ", "))
	}
	if len(d.onlyLocal) > 0 {
		_, _ = fmt.Fprintf(e.out, "  only local    : %s\n", strings.Join(d.onlyLocal, ", "))
	}
	if len(d.onlyServer) > 0 {
		_, _ = fmt.Fprintf(e.out, "  only on server: %s\n", strings.Join(d.onlyServer, ", "))
	}
}

// scopeLabel describes the resolved scope for a status line: "project" or "project/tenant".
func scopeLabel(resp *client.EnvResponse) string {
	if resp.Tenant != "" {
		return resp.Project + "/" + resp.Tenant
	}
	return resp.Project
}

// sortedNames returns the map's keys, sorted — the only safe projection of a secret-bearing map.
func sortedNames(m map[string]string) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// writeJSON marshals v (already a names-only / non-secret projection) to e.out, indented.
func writeJSON(e *env, v any) error {
	enc := json.NewEncoder(e.out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// writeEnvFile writes vars as a .env body (sorted KEY=VALUE, verbatim values) at mode 0600. The format
// matches the host parser (internal/secret.parseEnv) and rc_env.py: first '=' splits, value taken
// literally. O_TRUNC replaces an existing file; an explicit Chmod re-asserts 0600 even when the file
// already existed with looser perms.
func writeEnvFile(path string, vars map[string]string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, envFileMode)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	var b strings.Builder
	for _, k := range sortedNames(vars) {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(vars[k])
		b.WriteByte('\n')
	}
	if _, err := f.WriteString(b.String()); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	if err := os.Chmod(path, envFileMode); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

// readEnvFile parses a local .env exactly like the host (internal/secret.parseEnv) and rc_env.py: one
// KEY=VALUE per line, blank lines and '#'-comments skipped, first '=' splits, value verbatim. A
// missing file is a clear error pointing at `rc env pull`.
func readEnvFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no %s here — run `rc env pull` first (run from inside the brain clone)", path)
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	out := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		if key == "" {
			continue
		}
		out[key] = line[eq+1:]
	}
	return out, nil
}
