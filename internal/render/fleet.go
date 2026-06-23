// This file is the FAT side of `rc fleet`: it ports runs_digest.py's view logic (the per-run flag line,
// the aggregate rates, the worst-offender shortlists, the flag legend) over the THIN /api/v1/runs rows.
// The server ships raw per-run health numbers + the view's own boolean flags; the ONE derived flag the
// view can't precompute — $! cost-spike (needs a per-kind median over the window) — is computed HERE, the
// same place runs_digest.py computes it. Two formats mirror the script: "human" (legend + table +
// aggregate + offenders) and "agent" (a token-lean index: full ids + a ranked "look here first"
// shortlist). Pure functions of the rows so golden tests pin them.
package render

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// FleetOptions carries the rendered window's scope for the headers (mirrors runs_digest.py's args).
type FleetOptions struct {
	Days    int
	Kind    string // "" = all kinds
	Format  string // "human" | "agent"
	CtxWarn int    // peak-context token threshold for the CTX flag + shortlist weight (0 disables)
}

// DefaultCtxWarn is runs_digest.py's --ctx-warn default: peak agent context ≥ this is a context-rot risk.
const DefaultCtxWarn = 50_000

// fleetLegend is the human flag legend (runs_digest.py FLAG_LEGEND, condensed for the terminal).
const fleetLegend = `Flags: GD=grounding discarded · J0=analysis without journal · ERR×n=failing bash · ` +
	`BIG×n=huge stdout (>15KB) · $!=cost > 3× same-kind median · EGR×n=blocked egress · CTX·Nk=peak context ≥ ctx-warn`

// Fleet renders the digest in the requested format. Empty/unknown format → human.
func Fleet(w io.Writer, runs []client.RunSummary, opt FleetOptions) {
	if opt.CtxWarn == 0 {
		opt.CtxWarn = DefaultCtxWarn
	}
	if opt.Format == "agent" {
		fleetAgent(w, runs, opt)
		return
	}
	fleetHuman(w, runs, opt)
}

// --- flag + spike computation (ported from runs_digest.py) ---

// peakCtx is the run's peak agent context (operator-tier); 0 when absent (baseline bearer / old row).
func peakCtx(r client.RunSummary) int64 {
	if r.Health != nil && r.Health.PeakContextTokens != nil {
		return *r.Health.PeakContextTokens
	}
	return 0
}

// cost is the run's total spend (operator-tier); 0 when absent.
func cost(r client.RunSummary) float64 {
	if r.Health != nil && r.Health.CostUSD != nil {
		return *r.Health.CostUSD
	}
	return 0
}

// costSpikes returns the set of run ids whose cost > 3× the median cost of same-kind runs in the window —
// the one flag the server can't precompute (needs a median). Only kinds with ≥4 runs get a baseline
// (runs_digest.py's rule). Returns empty when no cost data (baseline bearer).
func costSpikes(runs []client.RunSummary) map[string]bool {
	byKind := map[string][]float64{}
	idsByKind := map[string][]string{}
	for _, r := range runs {
		byKind[r.Kind] = append(byKind[r.Kind], cost(r))
		idsByKind[r.Kind] = append(idsByKind[r.Kind], r.RunID)
	}
	spikes := map[string]bool{}
	for kind, costs := range byKind {
		if len(costs) < 4 {
			continue
		}
		m := median(costs)
		if m <= 0 {
			continue
		}
		for i, c := range costs {
			if c > 3*m {
				spikes[idsByKind[kind][i]] = true
			}
		}
	}
	return spikes
}

// flags computes the compact flag tokens for one run (runs_digest.py _flags), in the same order.
func flags(r client.RunSummary, spikes map[string]bool, ctxWarn int) []string {
	var f []string
	h := r.Health
	if h == nil {
		// No health block (baseline bearer): only the safe error flag is derivable; everything else needs
		// the view's counts. Return nothing — the digest still shows status/kind.
		return f
	}
	if h.GroundingDiscarded {
		f = append(f, "GD")
	}
	if r.Kind == "analysis" && h.NoJournal {
		f = append(f, "J0")
	}
	if h.BashErrCount > 0 {
		f = append(f, fmt.Sprintf("ERR×%d", h.BashErrCount))
	}
	if h.BigStdoutCount > 0 {
		f = append(f, fmt.Sprintf("BIG×%d", h.BigStdoutCount))
	}
	if spikes[r.RunID] {
		f = append(f, "$!")
	}
	if h.BlockedEgress > 0 {
		f = append(f, fmt.Sprintf("EGR×%d", h.BlockedEgress))
	}
	if ctxWarn > 0 && peakCtx(r) >= int64(ctxWarn) {
		f = append(f, "CTX·"+tokens(peakCtx(r)))
	}
	return f
}

func flagStr(r client.RunSummary, spikes map[string]bool, ctxWarn int) string {
	return strings.Join(flags(r, spikes, ctxWarn), " ")
}

// severity is runs_digest.py _severity: an error trumps everything; the rest weight an agent's attention.
// >0 ⇒ the run earns a shortlist line.
func severity(r client.RunSummary, spikes map[string]bool, ctxWarn int) int {
	s := 0
	if r.Status == "error" {
		s = 100
	}
	if spikes[r.RunID] {
		s += 50
	}
	if r.Health != nil {
		s += int(r.Health.BlockedEgress) * 40
		if r.Kind == "analysis" && r.Health.NoJournal {
			s += 20
		}
		s += int(r.Health.BashErrCount) * 10
		s += int(r.Health.BigStdoutCount) * 5
		if r.Health.GroundingDiscarded {
			s += 3
		}
	}
	if ctxWarn > 0 && peakCtx(r) >= int64(ctxWarn) {
		s += 15
	}
	return s
}

// --- human digest ---

func fleetHuman(w io.Writer, runs []client.RunSummary, opt FleetOptions) {
	spikes := costSpikes(runs)
	kinds := opt.Kind
	if kinds == "" {
		kinds = "all kinds"
	}
	fmt.Fprintf(w, "Fleet digest — last %d days · %s · %d runs\n\n", opt.Days, kinds, len(runs))
	fmt.Fprintln(w, fleetLegend)
	fmt.Fprintln(w)

	if len(runs) == 0 {
		fmt.Fprintln(w, "(no runs in window)")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RUN8\tKIND\tSTATUS\tDURATION\tCOST\tCTX\tFLAGS")
	for _, r := range runs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			short8(r.RunID), r.Kind, r.Status, duration(r.DurationMs),
			costCell(cost(r)), ctxCell(peakCtx(r)), flagStr(r, spikes, opt.CtxWarn))
	}
	tw.Flush()

	fleetAggregate(w, runs, opt)
	fleetOffenders(w, runs, spikes, opt)
}

// fleetAggregate ports runs_digest.py's "## Aggregate" block: done/error counts, grounding-discarded
// rate, journal coverage (analyses), cost total/median, peak-context spread, blocked-egress hosts.
func fleetAggregate(w io.Writer, runs []client.RunSummary, opt FleetOptions) {
	fmt.Fprintln(w, "\nAggregate:")
	done, errc := 0, 0
	for _, r := range runs {
		switch r.Status {
		case "done":
			done++
		case "error":
			errc++
		}
	}
	other := len(runs) - done - errc
	line := fmt.Sprintf("  runs: %d — done %d · error %d", len(runs), done, errc)
	if other > 0 {
		line += fmt.Sprintf(" · other %d", other)
	}
	fmt.Fprintln(w, line)

	gd := 0
	for _, r := range runs {
		if r.Health != nil && r.Health.GroundingDiscarded {
			gd++
		}
	}
	fmt.Fprintf(w, "  grounding discarded: %d/%d (%d%%)\n", gd, len(runs), pct(gd, len(runs)))

	var analyses, journaled int
	for _, r := range runs {
		if r.Kind == "analysis" {
			analyses++
			if r.Health != nil && !r.Health.NoJournal {
				journaled++
			}
		}
	}
	if analyses > 0 {
		fmt.Fprintf(w, "  journal coverage (analyses): %d/%d (%d%%)\n", journaled, analyses, pct(journaled, analyses))
	}

	var costs []float64
	var total float64
	for _, r := range runs {
		c := cost(r)
		costs = append(costs, c)
		total += c
	}
	if total > 0 {
		fmt.Fprintf(w, "  cost: total %s · median %s\n", costCell(total), costCell(median(costs)))
	}

	var ctxs []int64
	var maxCtx int64
	rotted := 0
	for _, r := range runs {
		c := peakCtx(r)
		if c > 0 {
			ctxs = append(ctxs, c)
		}
		if c > maxCtx {
			maxCtx = c
		}
		if opt.CtxWarn > 0 && c >= int64(opt.CtxWarn) {
			rotted++
		}
	}
	if len(ctxs) > 0 {
		fmt.Fprintf(w, "  peak context: median %s · max %s · ≥%s: %d/%d (context-rot risk)\n",
			tokens(medianInt(ctxs)), tokens(maxCtx), tokens(int64(opt.CtxWarn)), rotted, len(runs))
	}

	hosts := egressHostsFromRuns(runs)
	if len(hosts) == 0 {
		fmt.Fprintln(w, "  blocked-egress runs: none")
	} else {
		fmt.Fprintf(w, "  blocked-egress: %d run(s) with blocked attempts\n", len(hosts))
	}
}

// egressHostsFromRuns returns the run ids that had ≥1 blocked egress (the digest only has per-run
// counts from /runs — the host detail is `rc patterns`' job). Returns the count of affected runs.
func egressHostsFromRuns(runs []client.RunSummary) []string {
	var ids []string
	for _, r := range runs {
		if r.Health != nil && r.Health.BlockedEgress > 0 {
			ids = append(ids, r.RunID)
		}
	}
	return ids
}

// fleetOffenders ports the "## Worst offenders" block — full ids ready to paste into `rc run <id>`.
func fleetOffenders(w io.Writer, runs []client.RunSummary, spikes map[string]bool, opt FleetOptions) {
	fmt.Fprintln(w, "\nWorst offenders (full ids — `rc run <id> --debug`):")
	printed := false

	topCost := topByCost(runs, 3)
	if len(topCost) > 0 {
		printed = true
		fmt.Fprintln(w, "  Top cost:")
		for _, r := range topCost {
			fmt.Fprintf(w, "    %s %s (%s, %s)\n", r.RunID, costCell(cost(r)), r.Kind, r.Status)
		}
	}

	topErr := topByBashErr(runs, 3)
	if len(topErr) > 0 {
		printed = true
		fmt.Fprintln(w, "  Top bash failures:")
		for _, r := range topErr {
			fmt.Fprintf(w, "    %s ERR×%d (%s, %s)\n", r.RunID, r.Health.BashErrCount, r.Kind, r.Status)
		}
	}

	topCtx := topByCtx(runs, opt.CtxWarn, 3)
	if len(topCtx) > 0 {
		printed = true
		fmt.Fprintln(w, "  Top context (rot risk):")
		for _, r := range topCtx {
			fmt.Fprintf(w, "    %s ctx %s (%s, %s)\n", r.RunID, tokens(peakCtx(r)), r.Kind, r.Status)
		}
	}

	var egr []client.RunSummary
	for _, r := range runs {
		if r.Health != nil && r.Health.BlockedEgress > 0 {
			egr = append(egr, r)
		}
	}
	if len(egr) > 0 {
		printed = true
		fmt.Fprintln(w, "  Blocked egress:")
		for _, r := range egr {
			fmt.Fprintf(w, "    %s EGR×%d (%s, %s)\n", r.RunID, r.Health.BlockedEgress, r.Kind, r.Status)
		}
	}

	if !printed {
		fmt.Fprintln(w, "  (none — a clean window)")
	}
}

// --- agent (token-lean) index ---

func fleetAgent(w io.Writer, runs []client.RunSummary, opt FleetOptions) {
	spikes := costSpikes(runs)
	fmt.Fprintf(w, "runs — last %dd · %d runs\n\n", opt.Days, len(runs))
	if len(runs) == 0 {
		fmt.Fprintln(w, "(no runs in window)")
		return
	}

	// "look here first": flagged runs ranked by severity (stable: runs are newest-first). Capped so it
	// stays a shortlist; the overflow count is printed, never silently dropped.
	const shortlistCap = 15
	var flagged []client.RunSummary
	for _, r := range runs {
		if severity(r, spikes, opt.CtxWarn) > 0 {
			flagged = append(flagged, r)
		}
	}
	sort.SliceStable(flagged, func(i, j int) bool {
		return severity(flagged[i], spikes, opt.CtxWarn) > severity(flagged[j], spikes, opt.CtxWarn)
	})
	if len(flagged) > 0 {
		fmt.Fprintln(w, "look here first:")
		for i, r := range flagged {
			if i >= shortlistCap {
				fmt.Fprintf(w, "  … +%d more flagged (see all runs)\n", len(flagged)-shortlistCap)
				break
			}
			reason := flagStr(r, spikes, opt.CtxWarn)
			if r.Status == "error" {
				reason = strings.TrimSpace("err " + reason)
			}
			fmt.Fprintf(w, "  %s  %s\n", r.RunID, reason)
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintln(w, "all runs (newest first):")
	for _, r := range runs {
		fmt.Fprintf(w, "  %s  %s  %s  %s  c%s  %s\n",
			r.RunID, r.Kind, r.Status, costCell(cost(r)), tokens(peakCtx(r)), flagStr(r, spikes, opt.CtxWarn))
	}
	fmt.Fprintln(w, "\ndrill: rc run <id> --debug")
}

// --- sorting helpers ---

func topByCost(runs []client.RunSummary, n int) []client.RunSummary {
	var c []client.RunSummary
	for _, r := range runs {
		if cost(r) > 0 {
			c = append(c, r)
		}
	}
	sort.SliceStable(c, func(i, j int) bool { return cost(c[i]) > cost(c[j]) })
	return clip(c, n)
}

func topByBashErr(runs []client.RunSummary, n int) []client.RunSummary {
	var c []client.RunSummary
	for _, r := range runs {
		if r.Health != nil && r.Health.BashErrCount > 0 {
			c = append(c, r)
		}
	}
	sort.SliceStable(c, func(i, j int) bool { return c[i].Health.BashErrCount > c[j].Health.BashErrCount })
	return clip(c, n)
}

func topByCtx(runs []client.RunSummary, ctxWarn, n int) []client.RunSummary {
	if ctxWarn <= 0 {
		return nil
	}
	var c []client.RunSummary
	for _, r := range runs {
		if peakCtx(r) >= int64(ctxWarn) {
			c = append(c, r)
		}
	}
	sort.SliceStable(c, func(i, j int) bool { return peakCtx(c[i]) > peakCtx(c[j]) })
	return clip(c, n)
}

func clip(s []client.RunSummary, n int) []client.RunSummary {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// --- small formatters ---

func short8(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// tokens renders a token count compactly: <1000 verbatim, else "Nk" (floored), "0" when zero.
func tokens(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%dk", n/1000)
}

// costCell renders a cost as $X.XXXX, blank ("-") when zero/absent (so a baseline bearer's blank cost
// doesn't read as $0).
func costCell(c float64) string {
	if c <= 0 {
		return "-"
	}
	return fmt.Sprintf("$%.4f", c)
}

// ctxCell renders peak context as "Nk"/"N", blank when absent.
func ctxCell(n int64) string {
	if n <= 0 {
		return "-"
	}
	return tokens(n)
}

func pct(a, b int) int {
	if b == 0 {
		return 0
	}
	return 100 * a / b
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func medianInt(xs []int64) int64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]int64(nil), xs...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}
