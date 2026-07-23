package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

var actionStatuses = map[string]bool{
	"proposed":  true,
	"executing": true,
	"succeeded": true,
	"failed":    true,
	"canceled":  true,
}

// newFleetActionsCmd builds the cross-run action discovery rung. The endpoint is already project/tenant
// scoped and cursor-paged; the CLI exhausts the requested window so operators do not need one call per
// run. JSON reassembles the raw rows without dropping additive server fields.
func newFleetActionsCmd(e *env) *cobra.Command {
	var days int
	var actions []string
	var statuses []string
	var format string
	cmd := &cobra.Command{
		Use:   "actions",
		Short: "Find actions across recent runs with exact params and run URLs",
		Long: "Find operator-visible actions across recent runs without downloading the fleet event feed. " +
			"Repeat --action and --status to OR exact values. Results are cursor-paged automatically. " +
			"Human output includes exact structured params and the full tokenized run URL; JSON preserves " +
			"the complete raw server rows.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if days < 1 {
				return fmt.Errorf("--days must be positive (the server clamps values above 14)")
			}
			if format != "human" && format != "agent" {
				return errBadFormat(format)
			}
			for _, action := range actions {
				if strings.TrimSpace(action) == "" {
					return fmt.Errorf("--action must not be empty")
				}
			}
			for _, status := range statuses {
				if !actionStatuses[status] {
					return fmt.Errorf("invalid --status %q: want proposed, executing, succeeded, failed, or canceled", status)
				}
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			items, capped, err := c.AllActions(e.ctx(), client.ActionFeedParams{
				Days:     days,
				Actions:  actions,
				Statuses: statuses,
				Project:  e.scopeProject(),
				Tenant:   e.scopeTenant(),
			})
			if err != nil {
				return err
			}
			if capped {
				warnCapped(e, "fleet actions hit the page cap — older actions omitted; narrow --days, --action, or --status")
			}
			if rawRowsJSON(e, cmd) {
				if items == nil {
					items = []client.ActionFeedItem{}
				}
				raw, err := json.Marshal(map[string]any{"items": items})
				if err != nil {
					return err
				}
				return e.renderJSON("fleet-actions", raw)
			}
			var out bytes.Buffer
			render.Actions(&out, items, format)
			return e.renderBytes("fleet-actions", "actions.txt", out.Bytes(), "text")
		},
	}
	cmd.Flags().IntVar(&days, "days", 14, "lookback window in days (positive; server clamps above 14)")
	cmd.Flags().StringArrayVar(&actions, "action", nil, "filter by exact action id (repeatable; OR)")
	cmd.Flags().StringArrayVar(&statuses, "status", nil, "filter by status: proposed|executing|succeeded|failed|canceled (repeatable; OR)")
	cmd.Flags().StringVar(&format, "format", "human", "output style: human|agent")
	return cmd
}
