package cli

import (
	"encoding/json"
	"net/url"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newRunCmd builds `rc run <id>` and `rc run <id> --events`. Without --events it's GET
// /api/v1/runs/{id} (one run, high level); with --events it's the full per-event trace. In JSON mode
// the trace is emitted as NDJSON (one event object per line) — streamable and jq-friendly, not a
// wrapping array — while the high-level view stays a single JSON object.
func newRunCmd(e *env) *cobra.Command {
	var events bool
	cmd := &cobra.Command{
		Use:   "run <id>",
		Short: "Show one run (add --events for the full trace)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			id := args[0]
			c, err := e.newClient()
			if err != nil {
				return err
			}
			jsonMode := render.IsJSON(e.mode(), e.out)

			if events {
				resp, err := c.Events(e.ctx(), id)
				if err != nil {
					return err
				}
				if jsonMode {
					return emitNDJSON(e, resp.Events)
				}
				render.Events(e.out, resp)
				return nil
			}

			if jsonMode {
				raw, err := c.Raw(e.ctx(), "GET", "/api/v1/runs/"+url.PathEscape(id), nil)
				if err != nil {
					return err
				}
				return render.JSON(e.out, raw)
			}
			detail, err := c.Run(e.ctx(), id)
			if err != nil {
				return err
			}
			render.Run(e.out, detail)
			return nil
		},
	}
	cmd.Flags().BoolVar(&events, "events", false, "show the full per-event trace")
	return cmd
}

// emitNDJSON writes one compact JSON object per event line. We re-marshal the typed events (rather
// than passing the server's wrapping {run_id,events:[...]} through) precisely to get the NDJSON shape
// the spec asks for: `| jq` over a stream, no enclosing array.
func emitNDJSON(e *env, events []client.Event) error {
	enc := json.NewEncoder(e.out)
	enc.SetEscapeHTML(false)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			return err
		}
	}
	return nil
}
