package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/idutil"
)

// newIDCmd builds `rc id gmail <id>` / `rc id outlook <id>` — provider id translators. Pure math + a
// clickable URL, no network.
func newIDCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "id",
		Short: "Translate provider message/thread ids",
	}
	cmd.AddCommand(newIDGmailCmd(e), newIDOutlookCmd(e))
	return cmd
}

func newIDGmailCmd(e *env) *cobra.Command {
	var user string
	gmail := &cobra.Command{
		Use:   "gmail <id>",
		Short: "Translate Gmail hex/decimal/thread-f: ids + build a clickable URL",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			res := idutil.ClassifyGmailForUser(args[0], user)
			if e.jsonOut() {
				return writeJSON(e, res)
			}
			printGmailID(e, res)
			return nil
		},
	}
	gmail.Flags().StringVar(&user, "user", "0", "Gmail /u/N/ mailbox index for the web URL")
	return gmail
}

func printGmailID(e *env, r idutil.GmailResult) {
	_, _ = fmt.Fprintf(e.out, "input      : %s\n", r.Input)
	detected := r.Kind
	if r.Note != "" {
		detected += "  (" + r.Note + ")"
	}
	_, _ = fmt.Fprintf(e.out, "detected   : %s\n", detected)
	if r.Hex == "" {
		return
	}
	_, _ = fmt.Fprintf(e.out, "\napi hex    : %s\n", r.Hex)
	_, _ = fmt.Fprintf(e.out, "decimal    : %d\n", r.Decimal)
	_, _ = fmt.Fprintf(e.out, "thread-f:  : %s\n", r.ThreadF)
	_, _ = fmt.Fprintf(e.out, "msg-f:     : %s\n", r.MsgF)
	_, _ = fmt.Fprintf(e.out, "\nweb URL    : %s\n", r.WebURL)
}

func newIDOutlookCmd(e *env) *cobra.Command {
	outlook := &cobra.Command{
		Use:   "outlook <id>",
		Short: "Classify an Outlook/Graph id + tell you which DB column matches it",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			res := idutil.ClassifyOutlook(args[0])
			if e.jsonOut() {
				return writeJSON(e, res)
			}
			printOutlookID(e, res)
			return nil
		},
	}
	return outlook
}

func printOutlookID(e *env, r idutil.OutlookResult) {
	_, _ = fmt.Fprintf(e.out, "input      : %s\n", r.Input)
	if r.URLID != "" && r.URLID != r.Input {
		_, _ = fmt.Fprintf(e.out, "url id     : %s\n", r.URLID)
	}
	_, _ = fmt.Fprintf(e.out, "detected   : %s\n", r.Kind)
	_, _ = fmt.Fprintf(e.out, "note       : %s\n", r.Note)
	if r.EmbeddedGUID != "" {
		_, _ = fmt.Fprintf(e.out, "guid       : %s\n", r.EmbeddedGUID)
	}
	if r.MatchColumn != "" {
		_, _ = fmt.Fprintf(e.out, "\nlookup     : SELECT … WHERE %s = '%s'\n", r.MatchColumn, r.MatchValue)
	} else {
		_, _ = fmt.Fprintln(e.out, "\nlookup     : (not offline-resolvable — see note above)")
	}
}
