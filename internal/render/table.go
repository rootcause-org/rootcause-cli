// This file holds the human-facing table renderers — one per command view. They exist so the TTY
// output stays compact and skim-friendly (lead with the signal: health summary, then runs); they are
// pure functions of the wire structs (no I/O beyond the passed writer, no clock) so golden tests can
// pin them exactly. Timestamps are shown as the server sent them (never time.Now), keeping goldens
// stable.
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
	fmt.Fprintln(w)
	Runs(w, resp)
}

// writeSummary renders the health rollup: overall health, per-source totals/errors, last success,
// last error, and the attention worklist.
func writeSummary(w io.Writer, s *client.Summary) {
	health := "DEGRADED"
	if s.Healthy {
		health = "healthy"
	}
	fmt.Fprintf(w, "Health: %s\n", health)

	if len(s.CountsBySource) > 0 {
		fmt.Fprintln(w, "\nSources:")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  SOURCE\tTOTAL\tERRORS")
		// Stable order: sort source names so goldens don't flake on map iteration.
		for _, name := range sortedKeys(s.CountsBySource) {
			c := s.CountsBySource[name]
			fmt.Fprintf(tw, "  %s\t%d\t%d\n", name, c.Total, c.Errors)
		}
		tw.Flush()
	}

	if s.LastSuccess != nil {
		fmt.Fprintf(w, "\nLast success: %s (%s) at %s\n", s.LastSuccess.RunID, s.LastSuccess.Source, s.LastSuccess.At)
	} else {
		fmt.Fprintln(w, "\nLast success: none")
	}
	if s.LastError != nil {
		fmt.Fprintf(w, "Last error:   %s (%s, %s) at %s\n", s.LastError.RunID, s.LastError.Source, s.LastError.Category, s.LastError.At)
	} else {
		fmt.Fprintln(w, "Last error:   none")
	}

	if len(s.Attention) > 0 {
		fmt.Fprintf(w, "\nNeeds attention (%d):\n", len(s.Attention))
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  RUN\tSOURCE\tCATEGORY\tOUTCOME\tAT")
		for _, a := range s.Attention {
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", a.RunID, a.Source, a.Category, a.Outcome, a.At)
		}
		tw.Flush()
	}
}

// Runs renders the recent-runs table (the lead view of `rc runs`). Shows the next-page cursor when
// the server returned one.
func Runs(w io.Writer, resp *client.RunsResponse) {
	if len(resp.Runs) == 0 {
		fmt.Fprintln(w, "No runs.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RUN\tKIND\tSOURCE\tSTATUS\tOUTCOME\tCATEGORY\tDURATION\tCREATED")
	for _, r := range resp.Runs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.RunID, r.Kind, r.Source, r.Status, r.Outcome, r.Category, duration(r.DurationMs), r.CreatedAt)
	}
	tw.Flush()
	if resp.NextBefore != "" {
		fmt.Fprintf(w, "\nMore: rc runs --before %s\n", resp.NextBefore)
	}
}

// Run renders one run's high-level view. answer/error/metadata appear only when present.
func Run(w io.Writer, r *client.RunDetail) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "Run:\t%s\n", r.RunID)
	fmt.Fprintf(tw, "Kind:\t%s\n", r.Kind)
	fmt.Fprintf(tw, "Status:\t%s\n", r.Status)
	fmt.Fprintf(tw, "Created:\t%s\n", r.CreatedAt)
	if r.FinishedAt != "" {
		fmt.Fprintf(tw, "Finished:\t%s\n", r.FinishedAt)
	}
	fmt.Fprintf(tw, "Attachments:\t%d\n", len(r.Attachments))
	tw.Flush()

	if r.Error != "" {
		fmt.Fprintf(w, "\nError:\n%s\n", r.Error)
	}
	if r.AnswerMarkdown != "" {
		fmt.Fprintf(w, "\nAnswer:\n%s\n", r.AnswerMarkdown)
	}
}

// Events renders the per-event trace as a readable per-iteration block: a header line per event plus
// command / stdout / stderr / reply markers when present.
func Events(w io.Writer, resp *client.EventsResponse) {
	fmt.Fprintf(w, "Run: %s\n", resp.RunID)
	if len(resp.Events) == 0 {
		fmt.Fprintln(w, "No events.")
		return
	}
	for _, e := range resp.Events {
		fmt.Fprintf(w, "\n#%d  %s  %s  exit=%d  %s  %s\n",
			e.Seq, e.Tool, e.Status, e.ExitCode, duration(e.DurationMs), e.At)
		if e.Command != "" {
			fmt.Fprintf(w, "    $ %s\n", e.Command)
		}
		if e.HasDraft || e.HasNote {
			fmt.Fprintf(w, "    draft=%t note=%t\n", e.HasDraft, e.HasNote)
		}
		if e.Stdout != "" {
			fmt.Fprintf(w, "    stdout: %s\n", indentBlock(e.Stdout))
		}
		if e.Stderr != "" {
			fmt.Fprintf(w, "    stderr: %s\n", indentBlock(e.Stderr))
		}
	}
}

// Settings renders the effective settings table: value (what's set, blank if unset), effective, and
// default per key. kb_enrich_model is shown only when the server included it.
func Settings(w io.Writer, s *client.Settings) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tVALUE\tEFFECTIVE\tDEFAULT")
	fmt.Fprintf(tw, "max_run_usd\t%s\t%s\t%s\n",
		numOrBlank(s.MaxRunUSD.Value), num(s.MaxRunUSD.Effective), num(s.MaxRunUSD.Default))
	fmt.Fprintf(tw, "default_tier\t%s\t%s\t%s\n",
		strOrBlank(s.DefaultTier.Value), s.DefaultTier.Effective, s.DefaultTier.Default)
	fmt.Fprintf(tw, "image_model\t%s\t%s\t%s\n",
		strOrBlank(s.ImageModel.Value), s.ImageModel.Effective, s.ImageModel.Default)
	if s.KBEnrichModel != nil {
		fmt.Fprintf(tw, "kb_enrich_model\t%s\t%s\t%s\n",
			strOrBlank(s.KBEnrichModel.Value), s.KBEnrichModel.Effective, s.KBEnrichModel.Default)
	}
	tw.Flush()
}

// --- small formatting helpers ---

// duration renders ms as a compact human string; blank when absent (unfinished run / no duration).
func duration(ms int64) string {
	if ms <= 0 {
		return "-"
	}
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	secs := float64(ms) / 1000.0
	if secs < 60 {
		return fmt.Sprintf("%.1fs", secs)
	}
	mins := int(secs) / 60
	rem := int(secs) % 60
	return fmt.Sprintf("%dm%ds", mins, rem)
}

func num(f float64) string { return fmt.Sprintf("%g", f) }
func numOrBlank(f float64) string {
	if f == 0 {
		return "-"
	}
	return num(f)
}
func strOrBlank(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// indentBlock collapses a multi-line stdout/stderr into a single readable line for the trace, keeping
// the table compact (full payloads remain available via -o json).
func indentBlock(s string) string {
	s = strings.TrimRight(s, "\n")
	return strings.ReplaceAll(s, "\n", " ")
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
