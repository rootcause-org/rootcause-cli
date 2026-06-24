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

// Projects renders the fleet handle list (`rc projects`): one row per project (name + id), name-ordered
// as the server sends them. A pure function of the wire rows so a golden pins it.
func Projects(w io.Writer, resp *client.ProjectsResponse) {
	if len(resp.Projects) == 0 {
		fmt.Fprintln(w, "(no projects)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tID")
	for _, p := range resp.Projects {
		fmt.Fprintf(tw, "%s\t%s\n", p.Name, p.ID)
	}
	tw.Flush()
}

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
	if why := runWhy(r.Debug); why != "" {
		fmt.Fprintf(tw, "Why:\t%s\n", why)
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

// BrainDiff renders the ONE journal commit a run wrote to its brain: a header (short sha, author,
// time), the touched files with +adds/-dels, then the unified diff. found:false → a single "no brain
// changes from this run" line (the explicit empty case — a declined / swallowed run).
func BrainDiff(w io.Writer, d *client.BrainDiff) {
	if !d.Found {
		fmt.Fprintf(w, "Run: %s\n", d.RunID)
		fmt.Fprintln(w, "No brain changes from this run.")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "Run:\t%s\n", d.RunID)
	fmt.Fprintf(tw, "Commit:\t%s\n", shortSHA(d.SHA))
	if d.Author != "" {
		fmt.Fprintf(tw, "Author:\t%s\n", d.Author)
	}
	if d.CommittedAt != "" {
		fmt.Fprintf(tw, "Committed:\t%s\n", d.CommittedAt)
	}
	if subj := firstLine(d.Message); subj != "" {
		fmt.Fprintf(tw, "Message:\t%s\n", subj)
	}
	tw.Flush()

	if len(d.Files) > 0 {
		fmt.Fprintf(w, "\nFiles (%d):\n", len(d.Files))
		ftw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(ftw, "  FILE\tCHURN")
		for _, f := range d.Files {
			fmt.Fprintf(ftw, "  %s\t%s\n", f.Path, churn(f.Additions, f.Deletions))
		}
		ftw.Flush()
	}

	if strings.TrimSpace(d.Diff) != "" {
		fmt.Fprintf(w, "\nDiff:\n%s\n", strings.TrimRight(d.Diff, "\n"))
		if d.DiffTruncated {
			fmt.Fprintln(w, "… (diff truncated)")
		}
	}
}

// shortSHA clips a full commit sha to its 12-char prefix for the header line; a shorter/empty sha is
// returned as-is.
func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
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

// runWhy is the index-level one-liner explaining a surprising outcome: a truncated decline_reason, the
// forced-submission cause, the model the run fell back FROM, and any tripped guardrail — joined into a
// single terse line. Blank when there's nothing notable (the caller then omits the row). The untruncated
// detail lives in `rc run <id> --full`.
func runWhy(d *client.RunDebug) string {
	if d == nil {
		return ""
	}
	var parts []string
	if d.DeclineReason != "" {
		parts = append(parts, "declined — "+truncate(d.DeclineReason, 80))
	}
	if d.Guardrail != "" {
		parts = append(parts, "guardrail ("+d.Guardrail+")")
	}
	if d.Forced != "" {
		parts = append(parts, "forced ("+d.Forced+")")
	}
	if d.FallbackFrom != "" {
		parts = append(parts, "model fell back from "+d.FallbackFrom)
	}
	return strings.Join(parts, "; ")
}

// truncate clamps s to at most max runes, appending an ellipsis when it had to cut — keeping the index
// "why" line skimmable. Newlines are collapsed first so the one-liner stays on one line.
func truncate(s string, max int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", "")
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimRight(string(r[:max]), " ") + "…"
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
		// Terminal reply that DECLINED: the reasoned "why nothing" (no draft/note placed). This is the
		// one-read answer to "the run declined — why?" the lean trace previously couldn't show.
		if e.DeclineReason != "" {
			fmt.Fprintf(w, "    declined: %s\n", indentBlock(e.DeclineReason))
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
	// Debug flags sit right under Status — they explain HOW the run reached that status. The full
	// decline_reason (which can be a paragraph) is rendered as a block below; here we show only the
	// terse flags. Each row prints only when the signal is present, so a clean run stays clean.
	if d := r.Debug; d != nil {
		if d.Guardrail != "" {
			fmt.Fprintf(tw, "Guardrail:\t%s\n", d.Guardrail)
		}
		if d.Forced != "" {
			fmt.Fprintf(tw, "Forced:\t%s\n", d.Forced)
		}
		if d.FallbackFrom != "" {
			fmt.Fprintf(tw, "Fallback from:\t%s\n", d.FallbackFrom)
		}
		if d.RecoverableRetries > 0 {
			fmt.Fprintf(tw, "Recoverable retries:\t%d\n", d.RecoverableRetries)
		}
	}
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

	// The full decline_reason verbatim (untruncated, may span lines) — the headline "why nothing" for a
	// declined run. Rendered as a block since the index view only shows a truncated one-liner.
	if r.Debug != nil && r.Debug.DeclineReason != "" {
		fmt.Fprintf(w, "\nDecline reason:\n%s\n", r.Debug.DeclineReason)
	}

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
