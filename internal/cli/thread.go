package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newThreadCmd builds `rc thread <id>`: the rootcause-side trace of one thread (or session) id — every
// run for it, newest-first, with status/health and a deterministic "where it likely failed" hint. It
// replaces the rootcause half of the old rc_thread_debug.py. The ReplyPen half (did ReplyPen send us the
// webhook? did our callback land?) is a SEPARATE, not-yet-deployed signed endpoint — until it lands, this
// prints a one-line stderr note and a footer saying the ReplyPen side is pending.
//
// --no-replypen is the forward-looking opt-out of that future stitch. Today the stitch doesn't exist, so
// the flag is accepted but a no-op (it only suppresses the pending-side stderr note); it's wired now so
// scripts can adopt it ahead of the stitch landing.
func newThreadCmd(e *env) *cobra.Command {
	var noReplyPen bool
	cmd := &cobra.Command{
		Use:   "thread <id>",
		Short: "Trace one thread/session: every rootcause run for it, with a why-no-draft hint",
		Long: "Trace a thread or session id on the rootcause side: every run for it (newest first), each " +
			"with status, health flags, and — when the newest run errored or declined — a deterministic hint " +
			"at where it likely failed.\n\n" +
			"The id may be a thread id OR a ReplyPen session UUID (the server falls back to session when no " +
			"thread matches). An unknown id is a clean empty answer, not an error.\n\n" +
			"NOTE: this is the ROOTCAUSE half only. The ReplyPen half (did ReplyPen send us the webhook, did " +
			"our callback land) is a separate signed endpoint that isn't wired yet.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			id := args[0]
			c, err := e.newClient()
			if err != nil {
				return err
			}

			if render.IsJSON(e.mode(), e.out) {
				// Raw passthrough: emit exactly what the server sent (render, don't reshape) so jq sees the
				// true shape, including the reserved `replypen` field.
				raw, err := c.Raw(e.ctx(), "GET", client.ThreadTracePath(id), nil)
				if err != nil {
					return err
				}
				return render.JSON(e.out, raw)
			}

			tr, err := c.ThreadTrace(e.ctx(), id)
			if err != nil {
				return err
			}
			render.ThreadTrace(e.out, tr)

			// TODO(thread-trace): once ReplyPen's signed /trace endpoint lands, stitch its side here (unless
			// --no-replypen): rootcause already holds the rootcause half; the CLI would fetch the ReplyPen
			// half and interleave it. The server-to-server call uses the project's webhook_secret
			// (replypen.Sign/Verify); the customer OAuth token never reaches ReplyPen.
			if !noReplyPen {
				fmt.Fprintln(e.err, "note: ReplyPen-side trace is pending (separate signed endpoint, not yet wired) — showing the rootcause side only")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&noReplyPen, "no-replypen", false, "skip the ReplyPen-side stitch (forward-looking; today the stitch isn't wired, so this only suppresses the pending-side note)")
	return cmd
}
