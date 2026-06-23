// This file renders `rc thread <id>` — the rootcause-side trace of one thread/session: how the id
// resolved, a newest-first table of its runs with health flags, and a deterministic "where it likely
// failed" hint when the newest run errored/declined. It ports the diagnosis SPIRIT of the old
// rc_thread_debug.py (its processor-side decision tree), working purely from the safe per-run projection
// the API ships — no ReplyPen stitch yet (that's a separate, pending endpoint; the footer says so).
package render

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// shortID clips a run/thread id to its 8-char prefix for the compact table, matching rc_agent_debug's
// own short-id convention; a shorter id is returned as-is.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// ThreadTrace renders the rootcause-side thread trace: the id + how it resolved, the runs table (with a
// per-run health flag column), the "where it likely failed" hint on the newest run, and the pending-
// ReplyPen footer. An unresolved id (resolved_by "none", no runs) is an explicit, useful empty answer.
func ThreadTrace(w io.Writer, t *client.ThreadTrace) {
	fmt.Fprintf(w, "Thread: %s\n", t.ID)
	fmt.Fprintf(w, "Resolved by: %s\n", resolvedLabel(t.ResolvedBy))

	if len(t.Runs) == 0 {
		fmt.Fprintln(w, "\nNo runs on the rootcause side for this id.")
		fmt.Fprintln(w, "Either we never received a webhook for it, or the id isn't a thread/session we ran.")
		fmt.Fprintln(w, "\n"+replyPenFooter)
		return
	}

	fmt.Fprintf(w, "\n%d run(s), newest first:\n", len(t.Runs))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RUN\tKIND\tSTATUS\tOUTCOME\tCATEGORY\tHEALTH\tDRAFT\tCREATED\tTOPIC")
	for _, r := range t.Runs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			shortID(r.RunID), r.Kind, r.Status, r.Outcome, r.Category,
			healthFlags(r.Health), draftNote(r), r.CreatedAt, strOrBlank(r.Topic))
	}
	tw.Flush()

	// The deterministic verdict on the NEWEST run (runs are newest-first) — the one the operator cares
	// about ("did THIS turn get a draft, and if not, where did it stop").
	if hint := threadFailureHint(&t.Runs[0]); hint != "" {
		fmt.Fprintf(w, "\nLikely: %s\n", hint)
	}

	fmt.Fprintln(w, "\n"+replyPenFooter)
}

// replyPenFooter marks the one-sided scope: the ReplyPen half (did it send us the webhook? did it place
// our callback?) is a separate, not-yet-landed signed endpoint.
const replyPenFooter = "ReplyPen side pending (separate endpoint)"

// resolvedLabel turns the wire resolved_by into a reader-facing phrase.
func resolvedLabel(by string) string {
	switch by {
	case "thread":
		return "thread id"
	case "session":
		return "session id (thread id matched nothing)"
	default:
		return "nothing (unknown id)"
	}
}

// draftNote summarises the reply outcome of a run in one cell: draft / note / both / -.
func draftNote(r client.RunSummary) string {
	switch {
	case r.HasDraft && r.HasNote:
		return "draft+note"
	case r.HasDraft:
		return "draft"
	case r.HasNote:
		return "note"
	default:
		return "-"
	}
}

// healthFlags compresses the safe per-run health counts/flags into a terse triage cell — only the
// signals that are ON show, so a clean run reads "-". These are the same fields the run index ships and
// `rc fleet` flags on; the customer-safe subset (no spend) so this stays useful for a customer in their
// brain. nil health (the server omitted the block) → "-".
func healthFlags(h *client.RunHealth) string {
	if h == nil {
		return "-"
	}
	var f []string
	if h.BlockedEgress > 0 {
		f = append(f, fmt.Sprintf("egress✗%d", h.BlockedEgress))
	}
	if h.BashErrCount > 0 {
		f = append(f, fmt.Sprintf("basherr%d", h.BashErrCount))
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

// threadFailureHint is the deterministic "where it likely failed" verdict for the newest run, ported
// from rc_thread_debug.py's processor-side decision tree but built only from the safe projection the API
// ships (status / outcome / category / health flags + decline_reason). It names the failure class AND
// the fix direction, never a body. Blank for a healthy answered run (nothing to diagnose).
//
// Order matters: the most specific, actionable cause wins. The ReplyPen-side causes (webhook never sent,
// callback rejected) are NOT diagnosable here — that's the pending stitch; the footer covers them.
func threadFailureHint(r *client.RunSummary) string {
	// Egress block: a domain the agent needed wasn't on the allowlist — config fix, project-side.
	if r.Health != nil && r.Health.BlockedEgress > 0 {
		return "a grounding step was egress-blocked — add the host to the project's egress allowlist (config)."
	}

	switch r.Outcome {
	case "answered":
		return "" // a real draft/note was produced on our side; if no reply landed, it's the ReplyPen side.
	case "stuck":
		return "the run is stuck (running past the timeout) — it was likely reaped at a guardrail; check the run trace (`rc run <id> --full`)."
	case "error":
		return "the run errored on our side — read it with `rc run <id> --full`; runs.error names the cause."
	case "declined":
		why := strings.TrimSpace(r.DeclinedReason)
		if why != "" {
			return "the agent declined to draft — " + truncate(why, 120) + " (its own words; full text in `rc run <id> --full`)."
		}
		return "the agent declined to draft a reply — see `rc run <id> --full` for its reasoning."
	case "failed":
		// A guardrail fallback note (no real answer). The two guardrail classes need OPPOSITE fixes, so
		// distinguish them from the run's own words when present (mirrors rc_thread_debug.py).
		why := strings.ToLower(r.DeclinedReason)
		switch {
		case strings.Contains(why, "ended its turn") || strings.Contains(why, "reasoning steps") || strings.Contains(why, "model call failed"):
			return "the model gave up without drafting (a guardrail fallback note) — NOT a budget issue (cost is usually trivial); try a more capable tier or fix the brain skill that should have driven the tool calls."
		case strings.Contains(why, "cost budget") || strings.Contains(why, "wall-clock") || strings.Contains(why, "budget"):
			return "the run hit its budget (a guardrail fallback note) — raise the run's cost/time cap or tighten the brain skill so it answers in fewer steps."
		default:
			return "the run produced a guardrail fallback note, not a real answer — read `rc run <id> --full` to see which guardrail tripped."
		}
	}

	// running (not yet stuck) or any other non-terminal status: nothing failed yet.
	if r.Status == "running" {
		return "the newest run is still running — check back, or trace it with `rc run <id> --full`."
	}
	return ""
}
