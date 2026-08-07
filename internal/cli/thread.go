package cli

import (
	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newThreadCmd builds `rc run thread <id>`: provider/local thread resolution, its pre-agent pipeline
// outcome, then every run with status/health and placement.
func newThreadCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "thread <id>",
		Short: "Trace one provider/local thread or session through pipeline, run, and placement",
		Long: "Trace a provider conversation id (Gmail thread or Intercom conversation), rootcause thread " +
			"UUID, or session id. Shows the pre-agent channel outcome first, then every run (newest first) " +
			"with health, placement, and a deterministic why-no-draft hint. An unknown id is a clean empty " +
			"answer, not an error.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			id := args[0]
			c, err := e.newClient()
			if err != nil {
				return err
			}

			if render.IsJSON(e.mode(), e.out) {
				// Raw passthrough: emit exactly what the server sent (render, don't reshape) so jq sees the
				// true shape.
				raw, err := c.Raw(e.ctx(), "GET", client.ThreadTracePath(id, e.scopeProject(), e.scopeTenant()), nil)
				if err != nil {
					return err
				}
				return render.JSON(e.out, raw)
			}

			tr, err := c.ThreadTrace(e.ctx(), id, e.scopeProject(), e.scopeTenant())
			if err != nil {
				return err
			}
			render.ThreadTrace(e.out, tr)
			return nil
		},
	}
	return cmd
}
