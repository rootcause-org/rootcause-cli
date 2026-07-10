// This file renders the local-synthesis export views (`rc project corpus ls|get`, `rc project mailbox harvest`): the
// harvest/survey corpus exports and their lifecycle status. Pure functions of the wire items so a
// golden pins them.
package render

import (
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// Exports renders the export set as a table: id, kind, status, thread count, truncated, created, and
// completed. Empty → "(none)".
func Exports(w io.Writer, l *client.ExportList) {
	if l == nil || len(l.Exports) == 0 {
		_, _ = fmt.Fprintln(w, "(none)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tKIND\tSTATUS\tTHREADS\tTRUNCATED\tCREATED\tCOMPLETED")
	for _, x := range l.Exports {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			x.ID, x.Kind, x.Status, intPtrOrBlank(x.ThreadCount), boolLabel(x.Truncated),
			strOrBlank(x.CreatedAt), strOrBlank(x.CompletedAt))
	}
	_ = tw.Flush()
}

// Export renders one export as a key: value block, surfacing the error message when the export failed.
func Export(w io.Writer, x *client.ExportItem) {
	if x == nil {
		_, _ = fmt.Fprintln(w, "(no export returned)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "id:\t%s\n", x.ID)
	_, _ = fmt.Fprintf(tw, "kind:\t%s\n", x.Kind)
	_, _ = fmt.Fprintf(tw, "status:\t%s\n", x.Status)
	if x.MailboxID != "" {
		_, _ = fmt.Fprintf(tw, "mailbox:\t%s\n", x.MailboxID)
	}
	if x.Tenant != "" {
		_, _ = fmt.Fprintf(tw, "tenant:\t%s\n", x.Tenant)
	}
	if x.Cleaned != nil {
		_, _ = fmt.Fprintf(tw, "cleaned:\t%s\n", boolLabel(*x.Cleaned))
	}
	if x.ThreadCount != nil {
		_, _ = fmt.Fprintf(tw, "threads:\t%d\n", *x.ThreadCount)
	}
	_, _ = fmt.Fprintf(tw, "truncated:\t%s\n", boolLabel(x.Truncated))
	if x.CreatedAt != "" {
		_, _ = fmt.Fprintf(tw, "created:\t%s\n", x.CreatedAt)
	}
	if x.CompletedAt != "" {
		_, _ = fmt.Fprintf(tw, "completed:\t%s\n", x.CompletedAt)
	}
	if x.ConsumedAt != "" {
		_, _ = fmt.Fprintf(tw, "consumed:\t%s\n", x.ConsumedAt)
	}
	if x.Error != "" {
		_, _ = fmt.Fprintf(tw, "error:\t%s\n", x.Error)
	}
	_ = tw.Flush()
}

// intPtrOrBlank renders an *int as its value, or "-" when absent (the count isn't known until the
// export completes).
func intPtrOrBlank(v *int) string {
	if v == nil {
		return "-"
	}
	return strconv.Itoa(*v)
}

// boolLabel renders a bool as yes/no for the human table.
func boolLabel(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
