package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newHealthCmd builds `rc health`: the health roll-up over the THIN /api/v1/health raw rows (mirror
// staleness + dead-lettered runs), porting health.py's healthy/unhealthy sections. It EXITS NON-ZERO when
// anything is unhealthy so it's CI/cron usable. -o json is a raw passthrough of the server's health rows
// (no verdict in JSON — the consumer decides). The verdict still drives the exit code in BOTH modes.
func newHealthCmd(e *env) *cobra.Command {
	var hours int
	cmd := &cobra.Command{
		Use:   "health",
		Short: "Roll up project health (mirrors + dead-letters); exits non-zero when unhealthy",
		Long: "Fetch GET /api/v1/health and render the healthy/unhealthy sections (stale/failing mirrors, " +
			"dead-lettered runs). Exits non-zero when unhealthy, so it's usable in CI/cron. -o json passes the " +
			"raw server rows through; the exit code still reflects the verdict.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}

			// JSON mode: raw passthrough of the server rows. We still fetch the typed body to compute the
			// verdict for the exit code (the rows ARE the wire struct, so re-marshaling is byte-faithful).
			resp, err := c.Health(e.ctx(), hours)
			if err != nil {
				return err
			}
			if e.jsonOut() {
				raw, rerr := c.Raw(e.ctx(), "GET", healthPath(hours), nil)
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
	return cmd
}

// errUnhealthy is the sentinel `rc health` returns to force a non-zero exit when a section is unhealthy.
// It carries no message of its own — the rendered report (or the JSON) is the user-facing output; the
// command layer prints "error: unhealthy" only as the terse exit reason.
var errUnhealthy = fmt.Errorf("unhealthy")
