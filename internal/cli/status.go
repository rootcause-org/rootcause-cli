package cli

import (
	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newStatusCmd builds `rc status`: the fixed five-row view of GET /api/v1/runs, leading with the health
// summary then a compact recent-runs table. `rc run list` owns filtering and deeper pagination.
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
			params := client.RunsParams{Limit: 5, Project: e.scopeProject(), Tenant: e.scopeTenant()}
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
