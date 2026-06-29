// This file holds the human-facing table renderers — one per command view. They exist so the TTY
// output stays compact and skim-friendly (lead with the signal: health summary, then runs); they are
// pure functions of the wire structs (no I/O beyond the passed writer, no clock) so golden tests can
// pin them exactly. Timestamps are shown as the server sent them (never time.Now), keeping goldens
// stable.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// Projects renders the fleet handle list (`rc projects`): one row per project (name + id), name-ordered
// as the server sends them. A pure function of the wire rows so a golden pins it.
func Projects(w io.Writer, resp *client.ProjectsResponse) {
	if len(resp.Projects) == 0 {
		_, _ = fmt.Fprintln(w, "(no projects)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tID")
	for _, p := range resp.Projects {
		_, _ = fmt.Fprintf(tw, "%s\t%s\n", p.Name, p.ID)
	}
	_ = tw.Flush()
}

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

// Runs renders the recent-runs table (the lead view of `rc runs`). Shows the next-page cursor when
// the server returned one.
func Runs(w io.Writer, resp *client.RunsResponse) {
	if len(resp.Runs) == 0 {
		_, _ = fmt.Fprintln(w, "No runs.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "RUN\tKIND\tSOURCE\tSTATUS\tOUTCOME\tCATEGORY\tDURATION\tCREATED")
	for _, r := range resp.Runs {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.RunID, r.Kind, r.Source, r.Status, r.Outcome, r.Category, duration(r.DurationMs), r.CreatedAt)
	}
	_ = tw.Flush()
	if resp.NextBefore != "" {
		_, _ = fmt.Fprintf(w, "\nMore: rc runs --before %s\n", resp.NextBefore)
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
	_, _ = fmt.Fprintf(tw, "Run:\t%s\n", r.RunID)
	_, _ = fmt.Fprintf(tw, "Kind:\t%s\n", r.Kind)
	_, _ = fmt.Fprintf(tw, "Status:\t%s\n", r.Status)
	if r.Category != "" {
		_, _ = fmt.Fprintf(tw, "Category:\t%s\n", r.Category)
	}
	if oc := metaString(r.Metadata, "outcome"); oc != "" {
		_, _ = fmt.Fprintf(tw, "Outcome:\t%s\n", oc)
	}
	if why := runWhy(r.Debug); why != "" {
		_, _ = fmt.Fprintf(tw, "Why:\t%s\n", why)
	}
	_, _ = fmt.Fprintf(tw, "Draft?:\t%s\n", yesNo(r.HasDraft))
	_, _ = fmt.Fprintf(tw, "Note?:\t%s\n", yesNo(r.HasNote))
	_, _ = fmt.Fprintf(tw, "Placed:\t%s\n", placedLabel(r.HasDraft, r.HasNote, metaString(r.Metadata, "outcome")))
	_, _ = fmt.Fprintf(tw, "Created:\t%s\n", r.CreatedAt)
	if r.FinishedAt != "" {
		_, _ = fmt.Fprintf(tw, "Finished:\t%s\n", r.FinishedAt)
	}
	if d := runDetailDuration(r); d != "" {
		_, _ = fmt.Fprintf(tw, "Duration:\t%s\n", d)
	}
	if cost := runCost(r); cost > 0 {
		_, _ = fmt.Fprintf(tw, "Cost:\t$%.2f\n", cost)
	}
	if r.Turns > 0 {
		_, _ = fmt.Fprintf(tw, "Turns:\t%d\n", r.Turns)
	}
	if r.BashTotal > 0 {
		_, _ = fmt.Fprintf(tw, "Bash:\t%d\n", r.BashTotal)
	}
	_, _ = fmt.Fprintf(tw, "Attachments:\t%d\n", len(r.Attachments))
	if r.RunURL != "" {
		_, _ = fmt.Fprintf(tw, "View run:\t%s\n", r.RunURL)
	}
	_ = tw.Flush()

	if r.Error != "" {
		_, _ = fmt.Fprintf(w, "\nError:\n%s\n", r.Error)
	}
	if r.AnswerMarkdown != "" {
		_, _ = fmt.Fprintf(w, "\nAnswer:\n%s\n", r.AnswerMarkdown)
	}
}

// AskEmail renders `rc ask --scenario email`: the reviewable support outcome first (draft, notes,
// actions/PR), with enough run metadata to jump into the trace.
func AskEmail(w io.Writer, r *client.RunDetail, full *client.FullResponse) {
	renderAskHeader(w, r, "email")
	if why := askDecline(r, full); why != "" {
		_, _ = fmt.Fprintf(w, "\nDecline reason:\n%s\n", why)
	}
	if draft := askDraft(r, full); draft != "" {
		_, _ = fmt.Fprintf(w, "\nDraft:\n%s\n", draft)
	}
	for _, n := range askNotes(r, full) {
		body := noteBody(n)
		if body == "" {
			continue
		}
		label := n.Key
		if label == "" {
			label = "note"
		}
		_, _ = fmt.Fprintf(w, "\nNote (%s):\n%s\n", label, body)
		if len(n.Actions) > 0 {
			renderNoteActions(w, n.Actions)
		}
	}
	renderProposedActions(w, askActions(r, full))
	renderSourcePR(w, askSourcePR(r, full))
	renderMetadata(w, askMetadata(r, full))
}

// AskRaw renders `rc ask --scenario raw`: one direct Markdown answer plus machine/action affordances.
func AskRaw(w io.Writer, r *client.RunDetail) {
	renderAskHeader(w, r, "raw")
	answer := strings.TrimSpace(r.AnswerMarkdown)
	if answer == "" {
		answer = strings.TrimSpace(r.DraftMarkdown)
	}
	if answer != "" {
		_, _ = fmt.Fprintf(w, "\nAnswer:\n%s\n", answer)
	}
	renderProposedActions(w, r.ProposedActions)
	renderSourcePR(w, r.SourcePR)
	renderMetadata(w, r.Metadata)
}

func renderAskHeader(w io.Writer, r *client.RunDetail, scenario string) {
	if r.Scenario != "" {
		scenario = r.Scenario
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "Run:\t%s\n", r.RunID)
	_, _ = fmt.Fprintf(tw, "Scenario:\t%s\n", scenario)
	_, _ = fmt.Fprintf(tw, "Status:\t%s\n", r.Status)
	if r.Category != "" {
		_, _ = fmt.Fprintf(tw, "Category:\t%s\n", r.Category)
	}
	if r.Outcome != "" {
		_, _ = fmt.Fprintf(tw, "Outcome:\t%s\n", r.Outcome)
	} else if oc := metaString(r.Metadata, "outcome"); oc != "" {
		_, _ = fmt.Fprintf(tw, "Outcome:\t%s\n", oc)
	}
	_, _ = fmt.Fprintf(tw, "Kind:\t%s\n", r.Kind)
	_, _ = fmt.Fprintf(tw, "Created:\t%s\n", r.CreatedAt)
	if r.FinishedAt != "" {
		_, _ = fmt.Fprintf(tw, "Finished:\t%s\n", r.FinishedAt)
	}
	if d := runDetailDuration(r); d != "" {
		_, _ = fmt.Fprintf(tw, "Duration:\t%s\n", d)
	}
	if cost := runCost(r); cost > 0 {
		_, _ = fmt.Fprintf(tw, "Cost:\t$%.2f\n", cost)
	}
	if r.Turns > 0 {
		_, _ = fmt.Fprintf(tw, "Turns:\t%d\n", r.Turns)
	}
	if r.BashTotal > 0 {
		_, _ = fmt.Fprintf(tw, "Bash:\t%d\n", r.BashTotal)
	}
	if r.RunURL != "" {
		_, _ = fmt.Fprintf(tw, "View run:\t%s\n", r.RunURL)
	} else if u := metaString(r.Metadata, "run_url"); u != "" {
		_, _ = fmt.Fprintf(tw, "View run:\t%s\n", u)
	}
	_ = tw.Flush()
	if r.Error != "" {
		_, _ = fmt.Fprintf(w, "\nError:\n%s\n", r.Error)
	}
}

func askDraft(r *client.RunDetail, full *client.FullResponse) string {
	if full != nil {
		for _, s := range []string{full.Run.Draft, full.Run.DraftMarkdown} {
			if strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	for _, s := range []string{r.DraftMarkdown, r.AnswerMarkdown} {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func askDecline(r *client.RunDetail, full *client.FullResponse) string {
	if full != nil {
		for _, s := range []string{full.Run.Decline, full.Run.DeclineReason} {
			if strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	if strings.TrimSpace(r.DeclineReason) != "" {
		return r.DeclineReason
	}
	if r.Debug != nil {
		return r.Debug.DeclineReason
	}
	return ""
}

func askNotes(r *client.RunDetail, full *client.FullResponse) []client.Note {
	if full != nil && len(full.Run.Notes) > 0 {
		return full.Run.Notes
	}
	return r.Notes
}

func askActions(r *client.RunDetail, full *client.FullResponse) []client.ProposedAction {
	if len(r.ProposedActions) > 0 {
		return r.ProposedActions
	}
	if full != nil {
		return full.Run.ProposedActions
	}
	return nil
}

func askSourcePR(r *client.RunDetail, full *client.FullResponse) *client.SourcePR {
	if r.SourcePR != nil {
		return r.SourcePR
	}
	if full != nil {
		return full.Run.SourcePR
	}
	return nil
}

func askMetadata(r *client.RunDetail, full *client.FullResponse) map[string]any {
	if full == nil || len(full.Run.Metadata) == 0 {
		return r.Metadata
	}
	if len(r.Metadata) == 0 {
		return full.Run.Metadata
	}
	out := make(map[string]any, len(full.Run.Metadata)+len(r.Metadata))
	for k, v := range full.Run.Metadata {
		out[k] = v
	}
	for k, v := range r.Metadata {
		out[k] = v
	}
	return out
}

func noteBody(n client.Note) string {
	for _, s := range []string{n.Body, n.BodyMarkdown, n.BodyText, n.BodyHTML} {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func renderNoteActions(w io.Writer, actions []client.NoteAction) {
	if len(actions) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "\nNote actions (%d):\n", len(actions))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "  ID\tLABEL")
	for _, a := range actions {
		_, _ = fmt.Fprintf(tw, "  %s\t%s\n", a.ID, a.Label)
	}
	_ = tw.Flush()
	for _, a := range actions {
		if a.URL != "" {
			_, _ = fmt.Fprintf(w, "    url: %s\n", a.URL)
		}
		if a.Description != "" {
			_, _ = fmt.Fprintf(w, "    %s\n", a.Description)
		}
	}
}

func renderProposedActions(w io.Writer, actions []client.ProposedAction) {
	if len(actions) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "\nActions (%d):\n", len(actions))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "  ID\tACTION\tLABEL")
	for _, a := range actions {
		_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\n", a.ID, a.Slug, a.Label)
	}
	_ = tw.Flush()
	for _, a := range actions {
		if a.URL != "" {
			_, _ = fmt.Fprintf(w, "    url: %s\n", a.URL)
		}
		if a.Description != "" {
			_, _ = fmt.Fprintf(w, "    %s\n", a.Description)
		}
	}
}

func renderSourcePR(w io.Writer, pr *client.SourcePR) {
	if pr == nil {
		return
	}
	_, _ = fmt.Fprintln(w, "\nPR:")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if pr.Title != "" {
		_, _ = fmt.Fprintf(tw, "  Title:\t%s\n", pr.Title)
	}
	if pr.Repo != "" {
		_, _ = fmt.Fprintf(tw, "  Repo:\t%s\n", pr.Repo)
	}
	if pr.URL != "" {
		_, _ = fmt.Fprintf(tw, "  URL:\t%s\n", pr.URL)
	}
	_ = tw.Flush()
	if strings.TrimSpace(pr.Body) != "" {
		_, _ = fmt.Fprintf(w, "\n%s\n", pr.Body)
	}
}

func renderMetadata(w io.Writer, md map[string]any) {
	keys := sortedMetadataKeys(md)
	if len(keys) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, "\nMetadata:")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, k := range keys {
		_, _ = fmt.Fprintf(tw, "  %s:\t%v\n", k, md[k])
	}
	_ = tw.Flush()
}

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
	_, _ = fmt.Fprintf(tw, "Commit:\t%s\n", shortSHA(d.SHA))
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

// placedLabel is the terse one-line summary of what a run placed back to the mailbox: draft / note /
// draft+note, "declined" when a terminal run produced nothing on purpose, else "-". The same vocabulary
// the thread-trace PLACED column uses, so the two views read consistently.
func placedLabel(hasDraft, hasNote bool, outcome string) string {
	switch {
	case hasDraft && hasNote:
		return "draft+note"
	case hasDraft:
		return "draft"
	case hasNote:
		return "note"
	case outcome == "declined":
		return "declined"
	default:
		return "-"
	}
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
	_, _ = fmt.Fprintf(w, "Run: %s\n", resp.RunID)
	if len(resp.Events) == 0 {
		_, _ = fmt.Fprintln(w, "No events.")
		return
	}
	// Renumber 1..N in the human view: the server's raw seq can be a negative sentinel block
	// (#-1000000, #-999999, …) that is meaningless to a reader. The raw seq is preserved in JSON/NDJSON.
	for i, e := range resp.Events {
		_, _ = fmt.Fprintf(w, "\n#%d  %s  %s  exit=%d  %s  %s\n",
			i+1, e.Tool, e.Status, e.ExitCode, duration(e.DurationMs), e.At)
		if e.Command != "" {
			_, _ = fmt.Fprintf(w, "    $ %s\n", e.Command)
		}
		if e.HasDraft || e.HasNote {
			_, _ = fmt.Fprintf(w, "    draft=%t note=%t\n", e.HasDraft, e.HasNote)
		}
		// Terminal reply that DECLINED: the reasoned "why nothing" (no draft/note placed). This is the
		// one-read answer to "the run declined — why?" the lean trace previously couldn't show.
		if e.DeclineReason != "" {
			_, _ = fmt.Fprintf(w, "    declined: %s\n", indentBlock(e.DeclineReason))
		}
		if e.Stdout != "" {
			_, _ = fmt.Fprintf(w, "    stdout: %s\n", indentBlock(e.Stdout))
		}
		if e.Stderr != "" {
			_, _ = fmt.Fprintf(w, "    stderr: %s\n", indentBlock(e.Stderr))
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
	_, _ = fmt.Fprintf(tw, "Run:\t%s\n", r.RunID)
	_, _ = fmt.Fprintf(tw, "Status:\t%s\n", r.Status)
	// Debug flags sit right under Status — they explain HOW the run reached that status. The full
	// decline_reason (which can be a paragraph) is rendered as a block below; here we show only the
	// terse flags. Each row prints only when the signal is present, so a clean run stays clean.
	if d := r.Debug; d != nil {
		if d.Guardrail != "" {
			_, _ = fmt.Fprintf(tw, "Guardrail:\t%s\n", d.Guardrail)
		}
		if d.Forced != "" {
			_, _ = fmt.Fprintf(tw, "Forced:\t%s\n", d.Forced)
		}
		if d.FallbackFrom != "" {
			_, _ = fmt.Fprintf(tw, "Fallback from:\t%s\n", d.FallbackFrom)
		}
		if d.RecoverableRetries > 0 {
			_, _ = fmt.Fprintf(tw, "Recoverable retries:\t%d\n", d.RecoverableRetries)
		}
	}
	_, _ = fmt.Fprintf(tw, "Kind:\t%s\n", r.Kind)
	if r.Trigger != "" {
		_, _ = fmt.Fprintf(tw, "Trigger:\t%s\n", r.Trigger)
	}
	if r.BrainRef != "" {
		_, _ = fmt.Fprintf(tw, "Brain ref:\t%s\n", r.BrainRef)
	}
	if r.BrainResolved != "" {
		_, _ = fmt.Fprintf(tw, "Brain resolved:\t%s\n", r.BrainResolved)
	}
	if r.Tenant != "" {
		_, _ = fmt.Fprintf(tw, "Tenant:\t%s\n", r.Tenant)
	}
	if settings := projectionSummary(r.TenantSettings); settings != "" {
		_, _ = fmt.Fprintf(tw, "Tenant settings:\t%s\n", settings)
	}
	drift, _ := client.TenantSettingsDrift(r.TenantSettings, r.TenantSettingsCurrent)
	if len(drift) > 0 {
		_, _ = fmt.Fprintf(tw, "Tenant settings drift:\t%d changed\n", len(drift))
	}
	if grounding := groundingSummary(r.GroundingSources); grounding != "" {
		_, _ = fmt.Fprintf(tw, "Grounding:\t%s\n", grounding)
	}
	if r.Model != "" {
		_, _ = fmt.Fprintf(tw, "Model:\t%s\n", r.Model)
	}
	_, _ = fmt.Fprintf(tw, "Created:\t%s\n", r.CreatedAt)
	if r.FinishedAt != "" {
		_, _ = fmt.Fprintf(tw, "Finished:\t%s\n", r.FinishedAt)
	}
	if r.RunCostUSD > 0 {
		_, _ = fmt.Fprintf(tw, "Cost:\t$%.2f\n", r.RunCostUSD)
	}
	if r.RunTotalTokens > 0 {
		_, _ = fmt.Fprintf(tw, "Tokens:\t%d\n", r.RunTotalTokens)
	}
	if r.Question != "" {
		_, _ = fmt.Fprintf(tw, "Question:\t%s\n", r.Question)
	}
	if tu := metaString(r.Metadata, "trace_url"); tu != "" {
		_, _ = fmt.Fprintf(tw, "Trace:\t%s\n", tu)
	}
	_ = tw.Flush()

	if len(drift) > 0 {
		_, _ = fmt.Fprintln(w, "\nCareful: when this run happened, these tenant settings differed from the current config.")
		for _, d := range drift {
			_, _ = fmt.Fprintf(w, "  %s: then %s; now %s\n", d.Key, d.Then, d.Now)
		}
	}

	renderGroundingSources(w, r.GroundingSources)

	// The full decline_reason verbatim (untruncated, may span lines) — the headline "why nothing" for a
	// declined run. Rendered as a block since the index view only shows a truncated one-liner.
	if r.Debug != nil && r.Debug.DeclineReason != "" {
		_, _ = fmt.Fprintf(w, "\nDecline reason:\n%s\n", r.Debug.DeclineReason)
	}

	if len(r.Egress) > 0 {
		_, _ = fmt.Fprintf(w, "\nEgress (%d):\n", len(r.Egress))
		etw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(etw, "  HOST\tCOUNT\tBLOCKED")
		for _, h := range r.Egress {
			_, _ = fmt.Fprintf(etw, "  %s\t%d\t%s\n", h.Host, h.Count, yesNo(h.Blocked))
		}
		_ = etw.Flush()
	}

	_, _ = fmt.Fprintf(w, "\nTimeline (%d):\n", len(f.Events))
	for i, e := range f.Events {
		_, _ = fmt.Fprintf(w, "\n#%d  %s  %s  exit=%d  %s  %s\n",
			i+1, eventTool(e.Tool, e.Label), e.Status, e.ExitCode, duration(e.DurationMs), e.At)
		if meta := eventCostLine(&e); meta != "" {
			_, _ = fmt.Fprintf(w, "    %s\n", meta)
		}
		if e.Command != "" {
			_, _ = fmt.Fprintf(w, "    $ %s\n", e.Command)
		}
		if len(e.Args) > 0 {
			_, _ = fmt.Fprintf(w, "    args: %s\n", indentBlock(string(e.Args)))
		}
		if e.Reasoning != "" {
			_, _ = fmt.Fprintf(w, "    reasoning: %s\n", indentBlock(e.Reasoning))
		}
		if e.HasDraft || e.HasNote {
			_, _ = fmt.Fprintf(w, "    draft=%t note=%t\n", e.HasDraft, e.HasNote)
		}
		if e.Stdout != "" {
			_, _ = fmt.Fprintf(w, "    stdout: %s\n", indentBlock(e.Stdout))
		}
		if e.Stderr != "" {
			_, _ = fmt.Fprintf(w, "    stderr: %s\n", indentBlock(e.Stderr))
		}
	}

	if r.SystemPrompt != "" {
		_, _ = fmt.Fprintf(w, "\nSystem prompt:\n%s\n", r.SystemPrompt)
	}
	if r.Draft != "" {
		_, _ = fmt.Fprintf(w, "\nDraft:\n%s\n", r.Draft)
	}
	for _, n := range r.Notes {
		_, _ = fmt.Fprintf(w, "\nNote (%s):\n%s\n", n.Key, n.Body)
	}
}

func groundingSummary(gs *client.GroundingSources) string {
	if gs == nil {
		return ""
	}
	if !gs.Captured {
		if gs.Reason != "" {
			return "not captured (" + gs.Reason + ")"
		}
		return "not captured"
	}
	parts := []string{fmt.Sprintf("%d sources", len(gs.Sources))}
	if attention := client.GroundingSourceAttentionCount(gs); attention > 0 {
		parts = append(parts, fmt.Sprintf("%d attention", attention))
	}
	if drift := client.GroundingSourceDriftCount(gs); drift > 0 {
		parts = append(parts, fmt.Sprintf("%d drift fields", drift))
	}
	if gs.CapturedAt != "" {
		parts = append(parts, "captured="+gs.CapturedAt)
	}
	if gs.CurrentCheckedAt != "" {
		parts = append(parts, "checked="+gs.CurrentCheckedAt)
	}
	return strings.Join(parts, "  ")
}

func renderGroundingSources(w io.Writer, gs *client.GroundingSources) {
	if gs == nil || !gs.Captured || len(gs.Sources) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "\nGrounding sources (%d):\n", len(gs.Sources))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "  !\tSOURCE\tSNAPSHOT\tSYNC\tCURRENT\tDRIFT")
	for _, s := range client.SortGroundingSources(gs.Sources) {
		_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\n",
			groundingMark(s), groundingSourceName(s), groundingSnapshot(s), groundingSync(s), groundingCurrent(s), groundingDrift(s))
	}
	_ = tw.Flush()
}

func groundingMark(s client.GroundingSource) string {
	if !s.Configured || !s.Available || !s.Mounted || len(s.Drift) > 0 {
		return "!"
	}
	return ""
}

func groundingSourceName(s client.GroundingSource) string {
	if s.Kind == "" {
		return s.Name
	}
	if s.Name == "" {
		return s.Kind
	}
	return s.Kind + "/" + s.Name
}

func groundingSnapshot(s client.GroundingSource) string {
	var parts []string
	if s.MountPath != "" {
		parts = append(parts, s.MountPath)
	}
	if ref := refSHA(s.Ref, s.CommitSHA); ref != "" {
		parts = append(parts, ref)
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

func groundingSync(s client.GroundingSource) string {
	var parts []string
	if !s.Configured {
		parts = append(parts, "not configured")
	}
	if !s.Available {
		parts = append(parts, "unavailable")
	}
	if !s.Mounted {
		parts = append(parts, "not mounted")
	}
	if len(parts) == 0 {
		if s.State != "" {
			parts = append(parts, s.State)
		} else {
			parts = append(parts, "ok")
		}
	}
	if s.LastOKAt != "" {
		parts = append(parts, "last_ok="+s.LastOKAt)
	}
	return strings.Join(parts, " ")
}

func groundingCurrent(s client.GroundingSource) string {
	if s.Current == nil {
		return "-"
	}
	var parts []string
	if ref := refSHA(s.Current.Ref, s.Current.CommitSHA); ref != "" {
		parts = append(parts, ref)
	}
	if s.Current.State != "" {
		parts = append(parts, s.Current.State)
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

func groundingDrift(s client.GroundingSource) string {
	if len(s.Drift) == 0 {
		return "-"
	}
	return strings.Join(s.Drift, ",")
}

func refSHA(ref, sha string) string {
	switch {
	case ref != "" && sha != "":
		return ref + "@" + shortSHA(sha)
	case ref != "":
		return ref
	case sha != "":
		return shortSHA(sha)
	default:
		return ""
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

func projectionSummary(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	snap, err := client.ParseTenantSettingsSnapshot(raw)
	if err != nil {
		return "present (unparseable)"
	}
	if snap == nil {
		return ""
	}
	var parts []string
	if snap.Source != "" {
		parts = append(parts, "source="+snap.Source)
	}
	if snap.SyncedAt != "" {
		parts = append(parts, "synced_at="+snap.SyncedAt)
	}
	if snap.Version != "" {
		parts = append(parts, "version="+snap.Version)
	}
	if selectors := client.BranchSelectorValues(snap.Settings); len(selectors) > 0 {
		parts = append(parts, fmt.Sprintf("selectors=%d", len(selectors)))
	}
	return strings.Join(parts, "  ")
}

// Settings renders the effective settings table: value (what's set, blank if unset), effective, and
// default per key. kb_enrich_model is shown only when the server included it.
// Settings renders the generic settings bag (`rc config get`): one row per key, in stable key order,
// with the override / effective / default / source. The CLI holds no per-key knowledge — it renders
// whatever keys the server sends, so a new server-side knob appears with no CLI change.
func Settings(w io.Writer, s *client.Settings) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "KEY\tVALUE\tEFFECTIVE\tDEFAULT\tSOURCE")
	for _, key := range settingKeys(*s) {
		f := (*s)[key]
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			key, jsonScalarOrBlank(f.Value), jsonScalar(f.Effective), jsonScalar(f.Default), f.Source)
	}
	_ = tw.Flush()
}

// Schema renders the config registry (`rc schema`): each resource, then a row per field with its type,
// enum (if any), write scopes, default, and help.
func Schema(w io.Writer, resp *client.SchemaResponse) {
	names := make([]string, 0, len(resp.Resources))
	for n := range resp.Resources {
		names = append(names, n)
	}
	sort.Strings(names)
	for i, n := range names {
		if i > 0 {
			_, _ = fmt.Fprintln(w)
		}
		_, _ = fmt.Fprintf(w, "%s\n", n)
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "  KEY\tTYPE\tENUM\tSCOPES\tDEFAULT\tHELP")
		for _, f := range resp.Resources[n].Fields {
			_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\n",
				f.Key, f.Type, strings.Join(f.Enum, "|"), strings.Join(f.Scopes, ","),
				jsonScalarOrBlank(f.Default), f.Help)
		}
		_ = tw.Flush()
	}
}

// ExplainField renders one field's full schema (`rc explain <key>`) as a key: value block — the
// human-readable twin of /meta/schema for a single knob.
func ExplainField(w io.Writer, resource string, f client.FieldSchema) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "key:\t%s\n", f.Key)
	_, _ = fmt.Fprintf(tw, "resource:\t%s\n", resource)
	_, _ = fmt.Fprintf(tw, "type:\t%s\n", f.Type)
	if len(f.Enum) > 0 {
		_, _ = fmt.Fprintf(tw, "enum:\t%s\n", strings.Join(f.Enum, ", "))
	}
	_, _ = fmt.Fprintf(tw, "scope:\t%s\n", f.Scope)
	_, _ = fmt.Fprintf(tw, "group:\t%s\n", f.Group)
	if len(f.Scopes) > 0 {
		_, _ = fmt.Fprintf(tw, "write scopes:\t%s\n", strings.Join(f.Scopes, ", "))
	}
	if f.Sensitive {
		_, _ = fmt.Fprintf(tw, "sensitive:\ttrue\n")
	}
	if v := jsonScalarOrBlank(f.Default); v != "" {
		_, _ = fmt.Fprintf(tw, "default:\t%s\n", v)
	}
	_, _ = fmt.Fprintf(tw, "help:\t%s\n", f.Help)
	_ = tw.Flush()
}

// Access renders `rc access` — what this token may do: its scope (project/all-projects), effective
// scopes, writable settings keys, reachable resources, and console reach.
func Access(w io.Writer, a *client.Access) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if a.AllProjects {
		_, _ = fmt.Fprintln(tw, "scope:\tall projects")
	} else if a.Project != nil {
		_, _ = fmt.Fprintf(tw, "scope:\tproject %s\n", a.Project.Name)
	}
	if a.Tenant != nil {
		_, _ = fmt.Fprintf(tw, "tenant:\t%s\n", a.Tenant.Slug)
	}
	_, _ = fmt.Fprintf(tw, "scopes:\t%s\n", strings.Join(a.Scopes, ", "))
	_, _ = fmt.Fprintf(tw, "writable keys:\t%s\n", joinOrDash(a.WritableKeys))
	_, _ = fmt.Fprintf(tw, "resources:\t%s\n", joinOrDash(a.Resources))
	_, _ = fmt.Fprintf(tw, "console:\tdb=%t bash=%t action=%t\n", a.Console.DB, a.Console.Bash, a.Console.Action)
	_ = tw.Flush()
}

// sortedKeys returns a settings map's keys in stable order for deterministic table output.
func settingKeys(s client.Settings) []string {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// scalar renders a JSON scalar (string/number/bool/null) for a table cell: a string unquoted, a
// number/bool as written, null/empty as "". Keeps the generic bag rendering type-agnostic.
func jsonScalar(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return strings.TrimSpace(string(raw))
	}
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return num(t)
	case bool:
		return fmt.Sprintf("%t", t)
	default:
		return strings.TrimSpace(string(raw))
	}
}

// scalarOrBlank is scalar but renders the zero value (empty string, 0) as "" so an unset override reads
// blank in the table.
func jsonScalarOrBlank(raw json.RawMessage) string {
	s := jsonScalar(raw)
	if s == "0" {
		return ""
	}
	return s
}

// joinOrDash joins a list with ", " or renders "-" when empty.
func joinOrDash(ss []string) string {
	if len(ss) == 0 {
		return "-"
	}
	return strings.Join(ss, ", ")
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

func sortedMetadataKeys(m map[string]any) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		switch k {
		case "outcome", "run_url":
			continue
		default:
			keys = append(keys, k)
		}
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}
