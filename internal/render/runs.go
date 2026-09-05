// The run index views: `rc status` (health rollup first) and `rc run list`.
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

// Status renders the health summary first (the point of `rc status`) then the recent-runs table.
func Status(w io.Writer, resp *client.RunsResponse) {
	writeSummary(w, &resp.Summary)
	_, _ = fmt.Fprintln(w)
	Runs(w, resp)
}

// writeSummary renders the health rollup: overall health, per-source totals/errors, last success,
// last error, and the attention worklist.
func writeSummary(w io.Writer, s *client.Summary) {
	health := "DEGRADED"
	if s.Healthy {
		health = "healthy"
	}
	_, _ = fmt.Fprintf(w, "Health: %s\n", health)

	if len(s.CountsBySource) > 0 {
		_, _ = fmt.Fprintln(w, "\nSources:")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "  SOURCE\tTOTAL\tERRORS")
		// Stable order: sort source names so goldens don't flake on map iteration.
		for _, name := range sortedKeys(s.CountsBySource) {
			c := s.CountsBySource[name]
			_, _ = fmt.Fprintf(tw, "  %s\t%d\t%d\n", name, c.Total, c.Errors)
		}
		_ = tw.Flush()
	}

	if s.LastSuccess != nil {
		_, _ = fmt.Fprintf(w, "\nLast success: %s (%s) at %s\n", s.LastSuccess.RunID, s.LastSuccess.Source, s.LastSuccess.At)
	} else {
		_, _ = fmt.Fprintln(w, "\nLast success: none")
	}
	if s.LastError != nil {
		_, _ = fmt.Fprintf(w, "Last error:   %s (%s, %s) at %s\n", s.LastError.RunID, s.LastError.Source, s.LastError.Category, s.LastError.At)
	} else {
		_, _ = fmt.Fprintln(w, "Last error:   none")
	}

	if len(s.Attention) > 0 {
		_, _ = fmt.Fprintf(w, "\nNeeds attention (%d):\n", len(s.Attention))
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "  RUN\tSOURCE\tCATEGORY\tOUTCOME\tAT")
		for _, a := range s.Attention {
			_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", a.RunID, a.Source, a.Category, a.Outcome, a.At)
		}
		_ = tw.Flush()
	}
}

// Runs renders the recent-runs table (the lead view of `rc run list`). Shows the next-page cursor when
// the server returned one.
func Runs(w io.Writer, resp *client.RunsResponse) {
	if len(resp.Runs) == 0 {
		_, _ = fmt.Fprintln(w, "No runs.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "RUN\tKIND\tSOURCE\tSTATUS\tOUTCOME\tCATEGORY\tLEARNING\tDURATION\tCREATED")
	for _, r := range resp.Runs {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.RunID, r.Kind, r.Source, r.Status, r.Outcome, r.Category, learningLabel(r.Learning), duration(r.DurationMs), r.CreatedAt)
	}
	_ = tw.Flush()
	if resp.NextBefore != "" {
		_, _ = fmt.Fprintf(w, "\nMore: rc run list --before %s\n", resp.NextBefore)
	}
}

func learningLabel(l client.Learning) string {
	var signals []string
	if l.Feedback {
		signals = append(signals, "feedback")
	}
	if l.SentDelta {
		signals = append(signals, "sent_delta")
	}
	if l.TriageSkipped {
		signals = append(signals, "triage_skipped")
	}
	if l.TriageCorrected {
		signals = append(signals, "triage_corrected")
	}
	if len(signals) == 0 {
		return "-"
	}
	return strings.Join(signals, ",")
}

func sortedKeys(m map[string]client.SourceCount) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// simple insertion sort: source maps are tiny
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}
