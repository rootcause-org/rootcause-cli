package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newHealthCmd builds `rc health`: the health roll-up over the THIN /api/v1/health raw rows (mirror
// staleness + dead-lettered runs), porting health.py's healthy/unhealthy sections. It EXITS NON-ZERO when
// anything is unhealthy so it's CI/cron usable. -o json is a raw passthrough of the server's health rows
// (no verdict in JSON — the consumer decides). The verdict still drives the exit code in BOTH modes.
func newHealthCmd(e *env) *cobra.Command {
	var hours int
	var all bool
	cmd := &cobra.Command{
		Use:   "health",
		Short: "Roll up project health (mirrors + dead-letters); exits non-zero when unhealthy",
		Long: "Fetch GET /api/v1/health and render the healthy/unhealthy sections (stale/failing mirrors, " +
			"dead-lettered runs). Exits non-zero when unhealthy, so it's usable in CI/cron. --all fans out " +
			"across every project (all-projects token) and exits non-zero if ANY project is unhealthy. -o json " +
			"passes the raw server rows through; the exit code still reflects the verdict.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}

			if all {
				return runHealthAll(e, cmd, c, hours)
			}

			// JSON mode: raw passthrough of the server rows. We still fetch the typed body to compute the
			// verdict for the exit code (the rows ARE the wire struct, so re-marshaling is byte-faithful).
			resp, err := c.Health(e.ctx(), hours, e.scopeProject())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				raw, rerr := c.Raw(e.ctx(), "GET", healthPath(hours, e.scopeProject()), nil)
				if rerr != nil {
					return rerr
				}
				if rerr := render.JSON(e.out, raw); rerr != nil {
					return rerr
				}
				if !healthVerdict(resp) {
					silenceUsage(cmd)
					return errUnhealthy
				}
				return nil
			}

			healthy := render.Health(e.out, resp)
			if !healthy {
				silenceUsage(cmd)
				return errUnhealthy
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&hours, "hours", 24, "dead-letter window in hours")
	cmd.Flags().BoolVar(&all, "all", false, "fan out across every project (requires an all-projects token)")
	return cmd
}

// runHealthAll fans the health roll-up across the fleet: fetch each project's raw health rows with an
// explicit ?project= scope, render per-project sections (table mode) or a merged {project→rows} object
// (-o json), and return errUnhealthy if ANY project is unhealthy — so a fleet CI gate trips on the
// worst project. A per-project fetch error aborts (an incomplete fleet verdict would be misleading).
func runHealthAll(e *env, cmd *cobra.Command, c *client.Client, hours int) error {
	projects, err := fanOutProjects(e, c)
	if err != nil {
		return err
	}

	type entry struct {
		Project string                 `json:"project"`
		Health  *client.HealthResponse `json:"health"`
	}
	entries := make([]entry, 0, len(projects))
	allHealthy := true
	for _, proj := range projects {
		resp, ferr := c.Health(e.ctx(), hours, proj.ID)
		if ferr != nil {
			return fmt.Errorf("health --all: project %s: %w", proj.Name, ferr)
		}
		entries = append(entries, entry{Project: proj.Name, Health: resp})
		if !healthVerdict(resp) {
			allHealthy = false
		}
		if !e.jsonOut() {
			fmt.Fprintf(e.out, "════ %s ════\n", proj.Name)
			render.Health(e.out, resp)
			fmt.Fprintln(e.out)
		}
	}

	if e.jsonOut() {
		b, merr := json.Marshal(map[string]any{"projects": entries})
		if merr != nil {
			return merr
		}
		if rerr := render.JSON(e.out, b); rerr != nil {
			return rerr
		}
	} else {
		fmt.Fprintf(e.out, "════ FLEET ════\n  %d projects · ", len(projects))
		if allHealthy {
			fmt.Fprintln(e.out, "all healthy")
		} else {
			fmt.Fprintln(e.out, "UNHEALTHY (≥1 project)")
		}
	}

	if !allHealthy {
		silenceUsage(cmd)
		return errUnhealthy
	}
	return nil
}

// errUnhealthy is the sentinel `rc health` returns to force a non-zero exit when a section is unhealthy.
// It carries no message of its own — the rendered report (or the JSON) is the user-facing output; the
// command layer prints "error: unhealthy" only as the terse exit reason.
var errUnhealthy = fmt.Errorf("unhealthy")
