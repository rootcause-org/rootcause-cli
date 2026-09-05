// `rc brain diff <run>`: the one journal commit a run wrote to its brain.
// The renderers are pure functions of the wire structs (no I/O beyond the passed writer, no clock) so
// golden tests pin them exactly. Timestamps are shown as the server sent them, keeping goldens stable.

package render

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// BrainDiff renders the ONE journal commit a run wrote to its brain: a header (short sha, author,
// time), the touched files with +adds/-dels, then the unified diff. found:false → a single "no brain
// changes from this run" line (the explicit empty case — a declined / swallowed run).
func BrainDiff(w io.Writer, d *client.BrainDiff) {
	if !d.Found {
		_, _ = fmt.Fprintf(w, "Run: %s\n", d.RunID)
		_, _ = fmt.Fprintln(w, "No brain changes from this run.")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "Run:\t%s\n", d.RunID)
	_, _ = fmt.Fprintf(tw, "Commit:\t%s\n", clipID(d.SHA, 12))
	if d.Author != "" {
		_, _ = fmt.Fprintf(tw, "Author:\t%s\n", d.Author)
	}
	if d.CommittedAt != "" {
		_, _ = fmt.Fprintf(tw, "Committed:\t%s\n", d.CommittedAt)
	}
	if subj := firstLine(d.Message); subj != "" {
		_, _ = fmt.Fprintf(tw, "Message:\t%s\n", subj)
	}
	_ = tw.Flush()

	if len(d.Files) > 0 {
		_, _ = fmt.Fprintf(w, "\nFiles (%d):\n", len(d.Files))
		ftw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(ftw, "  FILE\tCHURN")
		for _, f := range d.Files {
			_, _ = fmt.Fprintf(ftw, "  %s\t%s\n", f.Path, churn(f.Additions, f.Deletions))
		}
		_ = ftw.Flush()
	}

	if strings.TrimSpace(d.Diff) != "" {
		_, _ = fmt.Fprintf(w, "\nDiff:\n%s\n", strings.TrimRight(d.Diff, "\n"))
		if d.DiffTruncated {
			_, _ = fmt.Fprintln(w, "… (diff truncated)")
		}
	}
}

// firstLine returns the commit subject — the message's first line.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// churn renders one file's line churn as "+A/-D"; a binary file (additions -1, the server's numstat
// "-") reads "binary".
func churn(adds, dels int) string {
	if adds < 0 || dels < 0 {
		return "binary"
	}
	return fmt.Sprintf("+%d/-%d", adds, dels)
}
