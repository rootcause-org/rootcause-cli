// `rc spam ls`: a project's allow+block lists as one table.
// The renderers are pure functions of the wire structs (no I/O beyond the passed writer, no clock) so
// golden tests pin them exactly. Timestamps are shown as the server sent them, keeping goldens stable.

package render

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// SpamRules renders a project's spam allow+block lists (`rc spam ls`) as one table: verdict, pattern,
// match type, source, and — each only when the server populated it on any row — the mailbox scope and
// created date. The rows are shown in the order the server sent them (the CLI never reorders; -o json
// carries the raw body).
func SpamRules(w io.Writer, rules []client.SpamRule) {
	if len(rules) == 0 {
		_, _ = fmt.Fprintln(w, "(no spam rules)")
		return
	}
	showMailbox, showCreated := false, false
	for _, r := range rules {
		if r.Mailbox != "" {
			showMailbox = true
		}
		if r.CreatedAt != "" {
			showCreated = true
		}
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	header := "VERDICT\tPATTERN\tTYPE\tSOURCE"
	if showMailbox {
		header += "\tMAILBOX"
	}
	if showCreated {
		header += "\tCREATED"
	}
	_, _ = fmt.Fprintln(tw, header)
	for _, r := range rules {
		row := fmt.Sprintf("%s\t%s\t%s\t%s", r.Verdict, r.Pattern, orDash(r.MatchType, "-"), orDash(r.Source, "-"))
		if showMailbox {
			row += "\t" + orDash(r.Mailbox, "-")
		}
		if showCreated {
			row += "\t" + orDash(r.CreatedAt, "-")
		}
		_, _ = fmt.Fprintln(tw, row)
	}
	_ = tw.Flush()
}
