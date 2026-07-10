package cli

import (
	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newThreadCmd builds `rc run thread <id>`: the trace of one thread (or session) id — every run for it,
// newest-first, with status/health, placement (draft/note), and a deterministic "where it likely
// failed" hint. The whole pipeline is in-process: the channel plane assembles the thread from local rows
// and enqueues a run; placement writes a draft/note back to the mailbox. There is no separate system to
// stitch in — this is the full picture.
func newThreadCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "thread <id>",
		Short: "Trace one thread/session: every run for it, with placement + a why-no-draft hint",
		Long: "Trace a thread or session id: every run for it (newest first), each with status, health " +
			"flags, what was placed (draft/note), and — when the newest run errored or declined — a " +
			"deterministic hint at where it likely failed.\n\n" +
			"The id may be a thread id OR a session UUID (the server falls back to session when no thread " +
			"matches). An unknown id is a clean empty answer, not an error.",
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
