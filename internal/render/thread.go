// This file renders `rc run thread <id>` — the trace of one thread/session: how the id resolved, a
// newest-first table of its runs with health flags + placement (draft/note), and a deterministic "where
// it likely failed" hint when the newest run errored/declined. The whole pipeline is in-process: the
// channel plane assembles the thread and enqueues a run, then placement writes a draft/note back to the
// mailbox. The diagnosis works purely from the safe per-run projection the API ships.
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

// ThreadTrace renders the thread trace: the id + how it resolved, the runs table (with a per-run health
// flag column + placement), and the "where it likely failed" hint on the newest run. An unresolved id
// (resolved_by "none", no runs) is an explicit, useful empty answer.
func ThreadTrace(w io.Writer, t *client.ThreadTrace) {
	_, _ = fmt.Fprintf(w, "Thread: %s\n", t.ID)
	_, _ = fmt.Fprintf(w, "Resolved by: %s\n", resolvedLabel(t.ResolvedBy))

	if len(t.Runs) == 0 {
		_, _ = fmt.Fprintln(w, "\nNo runs for this id.")
		_, _ = fmt.Fprintln(w, "Either the channel plane never enqueued a run for it (no inbound assembled), or the id isn't a thread/session we ran.")
		return
	}

	_, _ = fmt.Fprintf(w, "\n%d run(s), newest first:\n", len(t.Runs))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "RUN\tKIND\tSTATUS\tOUTCOME\tCATEGORY\tHEALTH\tPLACED\tCREATED\tTOPIC")
	for _, r := range t.Runs {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			shortID(r.RunID), r.Kind, r.Status, r.Outcome, r.Category,
			healthFlags(r.Health), placement(r), r.CreatedAt, strOrBlank(r.Topic))
	}
	_ = tw.Flush()

	// The deterministic verdict on the NEWEST run (runs are newest-first) — the one the operator cares
	// about ("did THIS turn get a draft, and if not, where did it stop").
	if hint := threadFailureHint(&t.Runs[0]); hint != "" {
		_, _ = fmt.Fprintf(w, "\nLikely: %s\n", hint)
	}
}

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
// `rc fleet runs` flags on; the customer-safe subset (no spend) so this stays useful for a customer in their
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
			return "the model gave up without drafting (a guardrail fallback note) — NOT a budget issue (cost is usually trivial); try a more capable tier or fix the brain skill that should have driven the tool calls."
		case strings.Contains(why, "cost budget") || strings.Contains(why, "wall-clock") || strings.Contains(why, "budget"):
			return "the run hit its budget (a guardrail fallback note) — raise the run's cost/time cap or tighten the brain skill so it answers in fewer steps."
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
