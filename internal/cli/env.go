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

// newEnvCmd builds `rc env pull|diff|keys` — the developer's self-serve sync of a project's PRODUCTION
// grounding .env over the Prompt API key (GET /api/v1/env). It is the external equivalent of the
// operator-only `scripts/rc_env.py` (--pull/--verify/--keys). SECRET HYGIENE: no subcommand ever
// prints a secret VALUE — pull writes them to the 0600 ./.env and reports NAMES + count; diff/keys
// emit NAMES only, in both table and JSON modes (the one place the CLI must reshape rather than
// pass-through, precisely because the response body carries live secrets).
func newEnvCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Sync this project's production grounding .env (pull/diff/keys)",
	}
	cmd.AddCommand(newEnvPullCmd(e), newEnvDiffCmd(e), newEnvKeysCmd(e))
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
