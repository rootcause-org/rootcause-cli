package cli

import (
	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newStatusCmd builds `rc status`: the no-filter view of GET /api/v1/runs, leading with the health
// summary then a compact recent-runs table. Same endpoint as `rc runs`; the only difference is which
// view leads (summary here, table there). It takes no filters by design — status is the default
// "everything at a glance" view.
func newStatusCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Health summary + recent runs",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			// --project scopes an all-projects token to one project (disregarded for a pinned token).
			params := client.RunsParams{Project: e.scopeProject()}
			// JSON mode is a verbatim passthrough so `| jq` sees the true response; table mode decodes
			// into the typed struct for rendering.
			if render.IsJSON(e.mode(), e.out) {
				raw, err := c.Raw(e.ctx(), "GET", "/api/v1/runs"+queryString(params), nil)
				if err != nil {
					return err
				}
				return render.JSON(e.out, raw)
			}
			resp, err := c.Runs(e.ctx(), params)
			if err != nil {
				return err
			}
			render.Status(e.out, resp)
			return nil
		},
	}
}
