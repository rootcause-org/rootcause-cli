package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newThreadCmd builds `rc run thread <id>`: run/provider/local/session resolution, its pre-agent
// pipeline outcome, then every run with status/health and placement.
func newThreadCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "thread <id>",
		Short: "Trace one run, provider/local thread, or session through pipeline and placement",
		Long: "Trace a run id, provider conversation id (Gmail thread or Intercom conversation), rootcause " +
			"thread UUID, or session id. A run id is resolved through its safe run-status identity first. " +
			"Shows the pre-agent channel outcome, then every run (newest first) " +
			"with health, placement, and a deterministic why-no-draft hint. An unknown id is a clean empty " +
			"answer, not an error.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			id := args[0]
			c, err := e.newClient()
			if err != nil {
				return err
			}
			lookupID, err := resolveThreadLookupID(e, c, id)
			if err != nil {
				return err
			}

			if render.IsJSON(e.mode(), e.out) {
				// Raw passthrough: emit exactly what the server sent (render, don't reshape) so jq sees the
				// true shape.
				raw, err := c.Raw(e.ctx(), "GET", client.ThreadTracePath(lookupID, e.scopeProject(), e.scopeTenant()), nil)
				if err != nil {
					return err
				}
				return render.JSON(e.out, raw)
			}

			tr, err := c.ThreadTrace(e.ctx(), lookupID, e.scopeProject(), e.scopeTenant())
			if err != nil {
				return err
			}
			render.ThreadTrace(e.out, tr)
			return nil
		},
	}
	return cmd
}

// resolveThreadLookupID makes a run id the cheapest universal drill handle. Non-run ids fall through
// unchanged on UNKNOWN_RUN; other errors remain loud so auth/scope/network failures cannot masquerade
// as an empty thread trace.
func resolveThreadLookupID(e *env, c *client.Client, id string) (string, error) {
	detail, err := c.Run(e.ctx(), id, e.scopeProject(), e.scopeTenant())
	if err == nil {
		if detail.LocalThreadID != "" {
			return detail.LocalThreadID, nil
		}
		if detail.ThreadID != "" {
			return detail.ThreadID, nil
		}
		if detail.SessionID != "" {
			return detail.SessionID, nil
		}
		return "", fmt.Errorf("run %s has no thread or session identity", id)
	}
	var apiErr *client.APIError
	if errors.As(err, &apiErr) && apiErr.Code == "UNKNOWN_RUN" {
		return id, nil
	}
	return "", err
}
