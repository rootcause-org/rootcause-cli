// One run, three depths: the `rc run show` header, the lean `rc run events` trace, and the full
// decomposed `rc run trace` bundle (timeline + grounding + tenant-settings drift).
// The renderers are pure functions of the wire structs (no I/O beyond the passed writer, no clock) so
// golden tests pin them exactly. Timestamps are shown as the server sent them, keeping goldens stable.

package render

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/digest"
)

// Run renders one run's high-level view — the promised set: status, category, draft?/note?, turns/bash,
// duration (plus kind/created/finished and a link to the run page). category/has_draft/has_note are
// top-level server fields now; duration prefers duration_ms and falls back to finished−created. Optional
// rows (category, turns, bash, run URL) print only when present so a running/incomplete run stays clean.
func Run(w io.Writer, r *client.RunDetail) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "Run:\t%s\n", r.RunID)
	if r.ThreadID != "" {
		_, _ = fmt.Fprintf(tw, "Thread:\t%s\n", r.ThreadID)
	}
	if r.SessionID != "" {
		_, _ = fmt.Fprintf(tw, "Session:\t%s\n", r.SessionID)
	}
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
// detail lives in `rc run trace <id>`.
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
	return strings.Join(parts, "; ")
}

// Events renders the per-event trace as a readable per-iteration block: a header line per event plus
// command / stdout / stderr / reply markers when present.
func Events(w io.Writer, resp *client.EventsResponse) {
	_, _ = fmt.Fprintf(w, "Run: %s\n", resp.RunID)
	if len(resp.Events) == 0 {
		// A redacted response is an empty list too — never let it read as "this run did nothing".
		if resp.DetailRedacted {
			_, _ = fmt.Fprintln(w, RedactedTraceNotice)
			return
		}
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

// Full renders the decomposed bundle (GET /runs/{id}/trace) for a human: a run-header block then the
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
	drift, _ := digest.TenantSettingsDrift(r.TenantSettings, r.TenantSettingsCurrent)
	if len(drift) > 0 {
		_, _ = fmt.Fprintf(tw, "Tenant settings drift:\t%d changed\n", len(drift))
	}
	if grounding := groundingSummary(r.GroundingSources); grounding != "" {
		_, _ = fmt.Fprintf(tw, "Grounding:\t%s\n", grounding)
	}
	_, _ = fmt.Fprintf(tw, "Created:\t%s\n", r.CreatedAt)
	if r.FinishedAt != "" {
		_, _ = fmt.Fprintf(tw, "Finished:\t%s\n", r.FinishedAt)
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

	// Withheld detail replaces the timeline rather than printing "Timeline (0):" — a sparse bundle from a
	// non-admin token is otherwise indistinguishable from a run that made no tool calls. The draft/notes
	// below still render: they are not part of what redaction withholds.
	redacted := f.Redacted() && len(f.Events) == 0
	if redacted {
		_, _ = fmt.Fprintf(w, "\n%s\n", RedactedTraceNotice)
		_, _ = fmt.Fprintln(w, "Events, prompts, and grounding are not served to this token.")
	} else {
		renderTimeline(w, f.Events)
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

func renderTimeline(w io.Writer, events []client.EventItem) {
	_, _ = fmt.Fprintf(w, "\nTimeline (%d):\n", len(events))
	for i, e := range events {
		_, _ = fmt.Fprintf(w, "\n#%d  %s  %s  exit=%d  %s  %s\n",
			i+1, eventTool(e.Tool, e.Label), e.Status, e.ExitCode, duration(e.DurationMs), e.At)
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
	if attention := digest.GroundingSourceAttentionCount(gs); attention > 0 {
		parts = append(parts, fmt.Sprintf("%d attention", attention))
	}
	if drift := digest.GroundingSourceDriftCount(gs); drift > 0 {
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
	for _, s := range digest.SortGroundingSources(gs.Sources) {
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
		return ref + "@" + clipID(sha, 12)
	case ref != "":
		return ref
	case sha != "":
		return clipID(sha, 12)
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
	if selectors := digest.BranchSelectorValues(snap.Settings); len(selectors) > 0 {
		parts = append(parts, fmt.Sprintf("selectors=%d", len(selectors)))
	}
	return strings.Join(parts, "  ")
}
