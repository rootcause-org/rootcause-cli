package cli

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

func newProjectEgressCmd(e *env) *cobra.Command {
	var days int
	var host string
	var decision string
	cmd := &cobra.Command{
		Use:   "egress",
		Short: "Inspect outbound endpoints, volume, and unattributed traffic",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			gateway, gatewayCapped, err := c.AllEgress(e.ctx(), client.FeedParams{
				Days: days, Host: host, Decision: decision, Project: e.scopeProject(), Tenant: e.scopeTenant(),
			})
			if err != nil {
				return err
			}
			if gatewayCapped {
				warnCapped(e, "project egress: hit the gateway page cap — older rows omitted; narrow --host/--decision/--days")
			}
			httpRows, httpCapped, err := c.AllHTTPAudit(e.ctx(), client.HTTPAuditParams{
				Days: days, Host: host, Decision: decision, Project: e.scopeProject(), Tenant: e.scopeTenant(),
			})
			if err != nil {
				return err
			}
			if httpCapped {
				warnCapped(e, "project egress: hit the HTTP page cap — older rows omitted; narrow --host/--decision/--days")
			}
			if e.jsonOut() {
				raw, err := json.Marshal(map[string]any{"egress": gateway, "http": httpRows})
				if err != nil {
					return err
				}
				return e.renderJSON("project-egress", raw)
			}
			render.ProjectEgress(e.out, gateway, httpRows, days)
			return nil
		},
	}
	cmd.Flags().IntVar(&days, "days", 14, "window in days")
	cmd.Flags().StringVar(&host, "host", "", "filter by exact destination host")
	cmd.Flags().StringVar(&decision, "decision", "", "filter by decision: allow|block|error")
	return cmd
}
