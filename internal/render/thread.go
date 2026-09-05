// This file renders `rc run thread <id>` — provider/local thread resolution, the pre-agent pipeline
// outcome, then its newest-first runs with health + placement.
package render

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// ThreadTrace renders the provider-neutral channel row before the run table. That makes a triage skip or
// injection block diagnosable even when the pipeline deliberately minted no run.
func ThreadTrace(w io.Writer, t *client.ThreadTrace) {
	_, _ = fmt.Fprintf(w, "Thread: %s\n", t.ID)
	_, _ = fmt.Fprintf(w, "Resolved by: %s\n", resolvedLabel(t.ResolvedBy))

	if len(t.Threads) > 0 {
		_, _ = fmt.Fprintf(w, "\n%d channel thread(s):\n", len(t.Threads))
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "LOCAL\tPROVIDER\tTENANT\tSTATUS\tOUTCOME\tMSG\tDRAFT\tNOTE\tUPDATED")
		for _, th := range t.Threads {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%d\t%s\n",
				clipID(th.LocalThreadID, 8), th.Provider, orDash(th.Tenant, "-"), th.Status, th.Outcome,
				th.MessageCount, th.DraftCount, th.NoteCount, th.UpdatedAt.Format(time.RFC3339))
		}
		_ = tw.Flush()
		for _, th := range t.Threads {
			if sb := th.SecurityBlock; sb != nil {
				loc := sb.Stage
				if sb.Category != "" {
					loc += "/" + sb.Category
				}
				_, _ = fmt.Fprintf(w, "\nSECURITY-BLOCK (%s): %s\n", th.LocalThreadID, loc)
			}
			if hint := channelThreadHint(th); hint != "" {
				_, _ = fmt.Fprintf(w, "\nPipeline (%s): %s\n", th.LocalThreadID, hint)
			}
		}
	}

	if len(t.Runs) == 0 {
		if len(t.Threads) > 0 {
			_, _ = fmt.Fprintln(w, "\nNo run was enqueued for this channel thread.")
		} else {
			_, _ = fmt.Fprintln(w, "\nNo channel thread or run for this id.")
			_, _ = fmt.Fprintln(w, "Check the provider id and project/tenant scope.")
		}
		return
	}

	_, _ = fmt.Fprintf(w, "\n%d run(s), newest first:\n", len(t.Runs))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "RUN\tKIND\tSTATUS\tOUTCOME\tCATEGORY\tHEALTH\tPLACED\tCREATED\tTOPIC")
	for _, r := range t.Runs {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			clipID(r.RunID, 8), r.Kind, r.Status, r.Outcome, r.Category,
			healthFlags(r.Health), placement(r), r.CreatedAt, orDash(r.Topic, "-"))
	}
	_ = tw.Flush()
	renderThreadAttribution(w, t.Runs)

	// The deterministic verdict on the NEWEST run (runs are newest-first) — the one the operator cares
	// about ("did THIS turn get a draft, and if not, where did it stop").
	if hint := threadFailureHint(&t.Runs[0]); hint != "" {
		_, _ = fmt.Fprintf(w, "\nLikely: %s\n", hint)
	}
}

func renderThreadAttribution(w io.Writer, runs []client.RunSummary) {
	hasAny := false
	for _, r := range runs {
		hasAny = hasAny || r.Attribution != nil
	}
	if !hasAny {
		return
	}
	_, _ = fmt.Fprintln(w, "\nExact linkage (full stable ids):")
	for _, r := range runs {
		a := r.Attribution
		if a == nil {
			continue
		}
		relation := "original turn"
		if a.RetryOf != "" {
			relation = "retry of " + a.RetryOf
		} else if a.ParentRunID != "" {
			relation = "clarifies " + a.ParentRunID
		}
		_, _ = fmt.Fprintf(w, "- run %s (%s)\n", r.RunID, relation)
		_, _ = fmt.Fprintf(w, "  conversation %s · provider thread %s · session %s\n", orDash(a.LocalThreadID, "-"), orDash(a.ThreadID, "-"), orDash(a.SessionID, "-"))
		if len(a.TriggerMessageIDs) == 0 {
			_, _ = fmt.Fprintln(w, "  trigger message: unavailable for this historical turn")
		} else {
			_, _ = fmt.Fprintf(w, "  trigger message%s: %s\n", plural(len(a.TriggerMessageIDs)), strings.Join(a.TriggerMessageIDs, ", "))
		}
		for _, d := range a.Drafts {
			_, _ = fmt.Fprintf(w, "  draft %s [%s]", d.DraftID, d.Status)
			if d.SentMessageID != "" {
				_, _ = fmt.Fprintf(w, " → sent message %s", d.SentMessageID)
			}
			_, _ = fmt.Fprintln(w)
		}
		if a.Feedback != nil {
			score := "unscored"
			if a.Feedback.Score != nil {
				score = fmt.Sprintf("score %d", *a.Feedback.Score)
			}
			_, _ = fmt.Fprintf(w, "  feedback on this run: %s", score)
			if comment := oneLine(a.Feedback.Comment); comment != "" {
				_, _ = fmt.Fprintf(w, " · %s", comment)
			}
			_, _ = fmt.Fprintln(w)
		}
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// resolvedLabel turns the wire resolved_by into a reader-facing phrase.
func resolvedLabel(by string) string {
	switch by {
	case "local_thread":
		return "rootcause thread id"
	case "external_thread":
		return "provider conversation id"
	case "thread":
		return "thread id"
	case "session":
		return "session id (thread id matched nothing)"
	default:
		return "nothing (unknown id)"
	}
}

func channelThreadHint(t client.ThreadTraceThread) string {
	switch t.Status {
	case "triage_skipped":
		why := oneLine(t.TriageExplanation)
		if why == "" {
			why = "triage decided this did not need processing"
		}
		hint := "triage skipped before run enqueue — " + why
		if t.NoteCount == 0 && (t.FeedbackLevel == "off" || t.FeedbackLevel == "runs") {
			hint += fmt.Sprintf(" No note was expected because mailbox feedback is %s.", t.FeedbackLevel)
		}
		return hint
	case "injection_blocked":
		return "prompt-injection screening blocked the thread before run enqueue"
	case "error":
		return "the channel pipeline failed before it could finish; inspect the pipeline job/logs"
	case "processor_failed":
		return "processing failed after enqueue; inspect the linked run and processor failure"
	case "new":
		if t.Outcome == "processing_off" {
			return "mailbox processing was off, so no run was expected"
		}
		return "the channel pipeline has not reached a terminal outcome"
	case "declined":
		if why := oneLine(t.DeclineReason); why != "" {
			return "the processor declined to draft — " + truncate(why, 240)
		}
	}
	return ""
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// placement summarises what a run placed back to the mailbox in one cell: draft / note / draft+note, or
// "declined" when a terminal run produced nothing on purpose (the agent chose not to draft). A run that
// produced nothing for a non-decline reason (error/stuck) reads "-".
func placement(r client.RunSummary) string {
	switch {
	case r.HasDraft && r.HasNote:
		return "draft+note"
	case r.HasDraft:
		return "draft"
	case r.HasNote:
		return "note"
	case r.Outcome == "declined":
		return "declined"
	default:
		return "-"
	}
}

// healthFlags compresses the safe per-run health counts/flags into a terse triage cell — only the
// signals that are ON show, so a clean run reads "-". These are the same fields the run index ships and
// `rc fleet runs` flags on. nil health (the server omitted the block) → "-".
func healthFlags(h *client.RunHealth) string {
	if h == nil {
		return "-"
	}
	var f []string
	if h.BlockedEgress > 0 {
		f = append(f, fmt.Sprintf("egress✗%d", h.BlockedEgress))
	}
	if real, explore := realBashErrH(h), h.BashErrExploreCount; real > 0 || explore > 0 {
		s := fmt.Sprintf("basherr%d", real)
		if explore > 0 {
			s += fmt.Sprintf(" (+%d explore)", explore)
		}
		f = append(f, s)
	}
	if h.GroundingDiscarded {
		f = append(f, "grounding✗")
	}
	if h.NoJournal {
		f = append(f, "nojournal")
	}
	if h.BigStdoutCount > 0 {
		f = append(f, fmt.Sprintf("bigout%d", h.BigStdoutCount))
	}
	if len(f) == 0 {
		return "-"
	}
	return strings.Join(f, ",")
}

// budgetMarkers are lowercased substrings of the server's guardrail text for "the run ran out of
// budget". The server reworded this ("cost budget" → "processing budget" / NL "verwerkingsbudget"), and
// historical rows keep the old wording forever, so every generation stays matched. The bare "budget"
// catch-all covers wordings we haven't seen; the specific entries document the ones we have.
var budgetMarkers = []string{"cost budget", "processing budget", "verwerkingsbudget", "wall-clock", "budget"}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// threadFailureHint is the deterministic "where it likely failed" verdict for the newest run, built only
// from the safe projection the API ships (status / outcome / category / health flags + decline_reason).
// It names the failure class AND the fix direction, never a body. Blank for a healthy answered run
// (nothing to diagnose).
//
// Order matters: the most specific, actionable cause wins. The whole pipeline is in-process — the
// channel plane assembles the thread and enqueues the run, and placement writes the draft/note to the
// mailbox — so every failure class below is diagnosable here.
func threadFailureHint(r *client.RunSummary) string {
	// Egress block: a domain the agent needed wasn't on the allowlist — config fix, project-side.
	if r.Health != nil && r.Health.BlockedEgress > 0 {
		return "a grounding step was egress-blocked — add the host to the project's egress allowlist (config)."
	}

	switch r.Outcome {
	case "answered":
		// A draft/note was produced. If no reply is visible in the mailbox, check placement rather than the
		// run: the draft/note write to the mailbox is the last step (`rc run trace <id>` shows what was placed).
		if !r.HasDraft && !r.HasNote {
			return "the run answered but nothing is recorded as placed — check placement to the mailbox (`rc run trace <id>`)."
		}
		return ""
	case "stuck":
		return "the run is stuck (running past the timeout) — it was likely reaped at a guardrail; check the run trace (`rc run trace <id>`)."
	case "error":
		return "the run errored on our side — read it with `rc run trace <id>`; runs.error names the cause."
	case "declined":
		why := strings.TrimSpace(r.DeclinedReason)
		if why != "" {
			return "the agent declined to draft — " + truncate(why, 120) + " (its own words; full text in `rc run trace <id>`)."
		}
		return "the agent declined to draft a reply — see `rc run trace <id>` for its reasoning."
	case "failed":
		// A guardrail fallback note (no real answer). The two guardrail classes need OPPOSITE fixes, so
		// distinguish them from the run's own words when present (mirrors rc_thread_debug.py).
		why := strings.ToLower(r.DeclinedReason)
		switch {
		case strings.Contains(why, "ended its turn") || strings.Contains(why, "reasoning steps") || strings.Contains(why, "model call failed"):
			return "the model gave up without drafting (a guardrail fallback note) — NOT a budget issue; try a more capable tier or fix the brain skill that should have driven the tool calls."
		case containsAny(why, budgetMarkers...):
			return "the run hit its budget (a guardrail fallback note) — raise the run's budget/time cap or tighten the brain skill so it answers in fewer steps."
		default:
			return "the run produced a guardrail fallback note, not a real answer — read `rc run trace <id>` to see which guardrail tripped."
		}
	}

	// running (not yet stuck) or any other non-terminal status: nothing failed yet.
	if r.Status == "running" {
		return "the newest run is still running — check back, or trace it with `rc run trace <id>`."
	}
	return ""
}
