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
	"time"

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

// Run renders one run's high-level view — the promised set: status, category, draft?/note?, cost,
// duration (plus kind/created/finished and a link to the run page). category/has_draft/has_note are
// top-level server fields now; cost prefers the run_health cost_usd and falls back to
// metadata.total_cost_usd; duration prefers duration_ms and falls back to finished−created. Optional
// rows (category, cost, turns, bash, run URL) print only when present so a running/incomplete run
// stays clean.
func Run(w io.Writer, r *client.RunDetail) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "Run:\t%s\n", r.RunID)
	fmt.Fprintf(tw, "Kind:\t%s\n", r.Kind)
	fmt.Fprintf(tw, "Status:\t%s\n", r.Status)
	if r.Category != "" {
		fmt.Fprintf(tw, "Category:\t%s\n", r.Category)
	}
	if oc := metaString(r.Metadata, "outcome"); oc != "" {
		fmt.Fprintf(tw, "Outcome:\t%s\n", oc)
	}
	fmt.Fprintf(tw, "Draft?:\t%s\n", yesNo(r.HasDraft))
	fmt.Fprintf(tw, "Note?:\t%s\n", yesNo(r.HasNote))
	fmt.Fprintf(tw, "Created:\t%s\n", r.CreatedAt)
	if r.FinishedAt != "" {
		fmt.Fprintf(tw, "Finished:\t%s\n", r.FinishedAt)
	}
	if d := runDetailDuration(r); d != "" {
		fmt.Fprintf(tw, "Duration:\t%s\n", d)
	}
	if cost := runCost(r); cost > 0 {
		fmt.Fprintf(tw, "Cost:\t$%.2f\n", cost)
	}
	if r.Turns > 0 {
		fmt.Fprintf(tw, "Turns:\t%d\n", r.Turns)
	}
	if r.BashTotal > 0 {
		fmt.Fprintf(tw, "Bash:\t%d\n", r.BashTotal)
	}
	fmt.Fprintf(tw, "Attachments:\t%d\n", len(r.Attachments))
	if r.RunURL != "" {
		fmt.Fprintf(tw, "View run:\t%s\n", r.RunURL)
	}
	tw.Flush()

	if r.Error != "" {
		fmt.Fprintf(w, "\nError:\n%s\n", r.Error)
	}
	if r.AnswerMarkdown != "" {
		fmt.Fprintf(w, "\nAnswer:\n%s\n", r.AnswerMarkdown)
	}
}

// runDetailDuration prefers the server's duration_ms, falling back to finished−created (a run_health
// miss leaves duration_ms zero). Blank when neither yields a positive span.
func runDetailDuration(r *client.RunDetail) string {
	if r.DurationMs > 0 {
		return duration(r.DurationMs)
	}
	return runDuration(r.CreatedAt, r.FinishedAt)
}

// runCost prefers the run_health cost_usd (the run's TOTAL spend), falling back to
// metadata.total_cost_usd when the view row is missing. 0 ⇒ caller omits the row (no $0.00 line).
func runCost(r *client.RunDetail) float64 {
	if r.CostUSD > 0 {
		return r.CostUSD
	}
	if c, ok := metaFloat(r.Metadata, "total_cost_usd"); ok {
		return c
	}
	return 0
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// Events renders the per-event trace as a readable per-iteration block: a header line per event plus
// command / stdout / stderr / reply markers when present.
func Events(w io.Writer, resp *client.EventsResponse) {
	fmt.Fprintf(w, "Run: %s\n", resp.RunID)
	if len(resp.Events) == 0 {
		fmt.Fprintln(w, "No events.")
		return
	}
	// Renumber 1..N in the human view: the server's raw seq can be a negative sentinel block
	// (#-1000000, #-999999, …) that is meaningless to a reader. The raw seq is preserved in JSON/NDJSON.
	for i, e := range resp.Events {
		fmt.Fprintf(w, "\n#%d  %s  %s  exit=%d  %s  %s\n",
			i+1, e.Tool, e.Status, e.ExitCode, duration(e.DurationMs), e.At)
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

// Full renders the decomposed bundle (GET /runs/{id}/full) for a human: a run-header block then the
// per-event timeline. It's the table-mode counterpart to the JSONL seam — the same data, laid out to
// skim. Optional rows print only when present so a lean run stays clean. Full bodies (draft, notes,
// system prompt) are shown after the table since they can be long.
func Full(w io.Writer, f *client.FullResponse) {
	r := &f.Run
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "Run:\t%s\n", r.RunID)
	fmt.Fprintf(tw, "Status:\t%s\n", r.Status)
	fmt.Fprintf(tw, "Kind:\t%s\n", r.Kind)
	if r.Trigger != "" {
		fmt.Fprintf(tw, "Trigger:\t%s\n", r.Trigger)
	}
	if r.BrainRef != "" {
		fmt.Fprintf(tw, "Brain ref:\t%s\n", r.BrainRef)
	}
	if r.Model != "" {
		fmt.Fprintf(tw, "Model:\t%s\n", r.Model)
	}
	fmt.Fprintf(tw, "Created:\t%s\n", r.CreatedAt)
	if r.FinishedAt != "" {
		fmt.Fprintf(tw, "Finished:\t%s\n", r.FinishedAt)
	}
	if r.RunCostUSD > 0 {
		fmt.Fprintf(tw, "Cost:\t$%.2f\n", r.RunCostUSD)
	}
	if r.RunTotalTokens > 0 {
		fmt.Fprintf(tw, "Tokens:\t%d\n", r.RunTotalTokens)
	}
	if r.Question != "" {
		fmt.Fprintf(tw, "Question:\t%s\n", r.Question)
	}
	if tu := metaString(r.Metadata, "trace_url"); tu != "" {
		fmt.Fprintf(tw, "Trace:\t%s\n", tu)
	}
	tw.Flush()

	if len(r.Egress) > 0 {
		fmt.Fprintf(w, "\nEgress (%d):\n", len(r.Egress))
		etw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(etw, "  HOST\tCOUNT\tBLOCKED")
		for _, h := range r.Egress {
			fmt.Fprintf(etw, "  %s\t%d\t%s\n", h.Host, h.Count, yesNo(h.Blocked))
		}
		etw.Flush()
	}

	fmt.Fprintf(w, "\nTimeline (%d):\n", len(f.Events))
	for i, e := range f.Events {
		fmt.Fprintf(w, "\n#%d  %s  %s  exit=%d  %s  %s\n",
			i+1, eventTool(e.Tool, e.Label), e.Status, e.ExitCode, duration(e.DurationMs), e.At)
		if meta := eventCostLine(&e); meta != "" {
			fmt.Fprintf(w, "    %s\n", meta)
		}
		if e.Command != "" {
			fmt.Fprintf(w, "    $ %s\n", e.Command)
		}
		if len(e.Args) > 0 {
			fmt.Fprintf(w, "    args: %s\n", indentBlock(string(e.Args)))
		}
		if e.Reasoning != "" {
			fmt.Fprintf(w, "    reasoning: %s\n", indentBlock(e.Reasoning))
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

	if r.SystemPrompt != "" {
		fmt.Fprintf(w, "\nSystem prompt:\n%s\n", r.SystemPrompt)
	}
	if r.Draft != "" {
		fmt.Fprintf(w, "\nDraft:\n%s\n", r.Draft)
	}
	for _, n := range r.Notes {
		fmt.Fprintf(w, "\nNote (%s):\n%s\n", n.Key, n.Body)
	}
}

// eventTool joins a tool with its optional human label ("bash" vs "bash (read schema)").
func eventTool(tool, label string) string {
	if label != "" {
		return tool + " (" + label + ")"
	}
	return tool
}

// eventCostLine summarizes the ai_usage join for one event: model, cost, tokens — only the parts that
// are present. Blank when the event had no LLM usage (a plain tool call).
func eventCostLine(e *client.EventItem) string {
	var parts []string
	if e.Model != "" {
		parts = append(parts, e.Model)
	}
	if e.CostUSD > 0 {
		parts = append(parts, fmt.Sprintf("$%.4f", e.CostUSD))
	}
	if e.TotalTokens > 0 {
		parts = append(parts, fmt.Sprintf("%d tok", e.TotalTokens))
	}
	return strings.Join(parts, "  ")
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

// runDuration computes a human duration from the run's created/finished timestamps (RFC3339). Blank
// when either is missing/unparseable or the span is non-positive — the server doesn't send a
// duration_ms on the run detail, so this is the only way to show it.
func runDuration(created, finished string) string {
	if created == "" || finished == "" {
		return ""
	}
	start, err1 := time.Parse(time.RFC3339, created)
	end, err2 := time.Parse(time.RFC3339, finished)
	if err1 != nil || err2 != nil {
		return ""
	}
	ms := end.Sub(start).Milliseconds()
	if ms <= 0 {
		return ""
	}
	return duration(ms)
}

// metaString reads a string value from the freeform run metadata map; "" if absent or not a string.
func metaString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

// metaFloat reads a numeric value from the freeform run metadata map. JSON numbers decode to float64;
// ok is false when the key is absent or not a number.
func metaFloat(m map[string]any, key string) (float64, bool) {
	if m == nil {
		return 0, false
	}
	if f, ok := m[key].(float64); ok {
		return f, true
	}
	return 0, false
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
