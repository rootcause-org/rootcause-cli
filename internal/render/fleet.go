// This file is the FAT side of `rc fleet runs`: it ports runs_digest.py's view logic (the per-run flag line,
// the aggregate rates, the worst-offender shortlists, the flag legend) over the THIN /api/v1/runs rows.
// The server ships raw per-run health numbers + the view's own boolean flags; the ONE derived flag the
// view can't precompute — $! cost-spike (needs a per-kind median over the window) — is computed HERE, the
// same place runs_digest.py computes it. Two formats mirror the script: "human" (legend + table +
// aggregate + offenders) and "agent" (the full computed digest in token-lean form: ranked "look here
// first" shortlist, aggregate, model×cost×fallback, daily timeline, worst offenders — all with full
// ids — plus the per-run index). Pure functions of the rows so golden tests pin them.
package render

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// FleetOptions carries the rendered window's scope for the headers (mirrors runs_digest.py's args).
type FleetOptions struct {
	Days     int
	Kind     string // "" = all kinds
	Learning string // "" = all runs; otherwise the server-side learning filter
	Format   string // "human" | "agent"
	CtxWarn  int    // peak-context token threshold for the CTX flag + shortlist weight (0 disables)
	// ByModel / Timeline gate the heavier breakdowns out of the default human digest so it stays
	// scannable; -o json always carries them. ByModel = the model×cost×fallback table (which model
	// burned the spend, and how much was a fallback). Timeline = the per-day runs/errors/cost histogram.
	ByModel  bool
	Timeline bool
}

// stuckRunAfter is the wall-clock age past which a still-running run (finished_at NULL) is called STUCK
// in the digest. The server owns the authoritative stuck clock (cfg.RunTimeout) for the status page; the
// fleet digest has only the per-run rows, so it applies this conservative client-side threshold — well
// beyond any healthy run — purely to surface "this never finished" without a DB tunnel.
const stuckRunAfter = 30 * time.Minute

// DefaultCtxWarn is runs_digest.py's --ctx-warn default: peak agent context ≥ this is a context-rot risk.
const DefaultCtxWarn = 50_000

// fleetLegend is the human flag legend (runs_digest.py FLAG_LEGEND, condensed for the terminal).
const fleetLegend = `Flags: GD=grounding discarded · J0=analysis without journal · ERR×n=failing bash · ` +
	`BIG×n=huge stdout (>15KB) · $!=cost > 3× same-kind median · EGR×n=blocked egress · CTX·Nk=peak context ≥ ctx-warn · ` +
	`FB=model fallback (planned model failed) · LRN=dream-cycle learning signal`

// FleetGroup is one project's slice of the fan-out: its name + its paged run rows. The cross-project
// `rc fleet runs --all` builds one per project.
type FleetGroup struct {
	Project string
	Runs    []client.RunSummary
}

// FleetAll renders the cross-project digest: a per-project section (the same digest Fleet renders, under
// a project header) followed by a fleet total — run/done/error counts + total cost across every project.
// Pure function of the groups so a golden pins it.
func FleetAll(w io.Writer, groups []FleetGroup, opt FleetOptions) {
	if opt.CtxWarn == 0 {
		opt.CtxWarn = DefaultCtxWarn
	}
	for _, g := range groups {
		_, _ = fmt.Fprintf(w, "════ %s ════\n\n", g.Project)
		Fleet(w, g.Runs, opt)
		_, _ = fmt.Fprintln(w)
	}
	fleetTotal(w, groups)
}

// fleetTotal renders the fan-out footer: the fleet-wide run/done/error counts, total cost, and a
// per-project one-line breakdown — the "whole fleet at a glance" the per-project sections roll up into.
func fleetTotal(w io.Writer, groups []FleetGroup) {
	_, _ = fmt.Fprintln(w, "════ FLEET TOTAL ════")
	var total, done, errc int
	var totalCost float64
	for _, g := range groups {
		for _, r := range g.Runs {
			total++
			switch r.Status {
			case "done":
				done++
			case "error":
				errc++
			}
			totalCost += cost(r)
		}
	}
	_, _ = fmt.Fprintf(w, "  %d projects · %d runs — done %d · error %d", len(groups), total, done, errc)
	if totalCost > 0 {
		_, _ = fmt.Fprintf(w, " · cost %s", costCell(totalCost))
	}
	_, _ = fmt.Fprintln(w)

	// Per-project rollup: the aggregates operators used to drop to db.py's GROUP BY project_id for —
	// run/error counts, cost, latency (avg/max secs), bash failures, blocked egress, and the
	// grounding-discarded / no-journal flag counts. One row per project.
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "  PROJECT\tRUNS\tERR\tCOST\tAVG_S\tMAX_S\tBASH_ERR\tEGR\tGD\tJ0")
	for _, g := range groups {
		var gerr, gbashErr, gegr, ggd, gj0 int
		var gcost, gsecsSum, gmaxSecs float64
		var gsecsN int
		for _, r := range g.Runs {
			if r.Status == "error" {
				gerr++
			}
			gcost += cost(r)
			if s := secsOf(r); s > 0 {
				gsecsSum += s
				gsecsN++
				if s > gmaxSecs {
					gmaxSecs = s
				}
			}
			if r.Health != nil {
				gbashErr += int(r.Health.BashErrCount)
				if r.Health.BlockedEgress > 0 {
					gegr++
				}
				if r.Health.GroundingDiscarded {
					ggd++
				}
				if r.Kind == "analysis" && r.Health.NoJournal {
					gj0++
				}
			}
		}
		avgSecs := 0.0
		if gsecsN > 0 {
			avgSecs = gsecsSum / float64(gsecsN)
		}
		_, _ = fmt.Fprintf(tw, "  %s\t%d\t%d\t%s\t%s\t%s\t%d\t%d\t%d\t%d\n",
			g.Project, len(g.Runs), gerr, costCell(gcost),
			secsCell(avgSecs), secsCell(gmaxSecs), gbashErr, gegr, ggd, gj0)
	}
	_ = tw.Flush()
}

// secsCell renders a wall-clock seconds value as "Ns" (rounded), "-" when zero/absent.
func secsCell(s float64) string {
	if s <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.0fs", s)
}

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

// isFallback is the run's clean model-fallback signal (run_health.is_fallback). False when no health
// block (baseline bearer) — the flag is safe but absent unless the index attached health.
func isFallback(r client.RunSummary) bool {
	return r.Health != nil && r.Health.IsFallback
}

// answeredModel is the model that actually answered (operator-tier); "" when absent.
func answeredModel(r client.RunSummary) string {
	if r.Health != nil {
		return r.Health.Model
	}
	return ""
}

// secsOf is the run's wall-clock seconds, derived from the index's duration_ms (the server omits
// duration on an unfinished run, so this is 0 for a still-running run). Used for the per-project
// avg/max latency rollup.
func secsOf(r client.RunSummary) float64 {
	if r.DurationMs <= 0 {
		return 0
	}
	return float64(r.DurationMs) / 1000.0
}

// isStuck is true for a run that is still 'running' (no finished_at) and older than stuckRunAfter — a
// run that never produced a callback. CreatedAt is RFC3339; an unparseable timestamp is treated as not
// stuck (don't invent an alarm from a bad field).
func isStuck(r client.RunSummary, now time.Time) bool {
	if r.Status != "running" || r.FinishedAt != "" {
		return false
	}
	created, err := time.Parse(time.RFC3339, r.CreatedAt)
	if err != nil {
		return false
	}
	return now.Sub(created) >= stuckRunAfter
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
	if h != nil {
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
	}
	if spikes[r.RunID] {
		f = append(f, "$!")
	}
	if h != nil {
		if h.BlockedEgress > 0 {
			f = append(f, fmt.Sprintf("EGR×%d", h.BlockedEgress))
		}
	}
	if ctxWarn > 0 && peakCtx(r) >= int64(ctxWarn) {
		f = append(f, "CTX·"+tokens(peakCtx(r)))
	}
	if h != nil && h.IsFallback {
		f = append(f, "FB")
	}
	if learning := learningLabel(r.Learning); learning != "-" {
		f = append(f, "LRN:"+strings.ReplaceAll(learning, ",", "+"))
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
	_, _ = fmt.Fprintf(w, "Fleet digest — last %d days · %s%s · %d runs\n\n", opt.Days, kinds, learningScope(opt.Learning), len(runs))
	_, _ = fmt.Fprintln(w, fleetLegend)
	_, _ = fmt.Fprintln(w)

	if len(runs) == 0 {
		_, _ = fmt.Fprintln(w, "(no runs in window)")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "RUN8\tKIND\tSTATUS\tDURATION\tCOST\tCTX\tFLAGS")
	for _, r := range runs {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			short8(r.RunID), r.Kind, r.Status, duration(r.DurationMs),
			costCell(cost(r)), ctxCell(peakCtx(r)), flagStr(r, spikes, opt.CtxWarn))
	}
	_ = tw.Flush()

	fleetAggregate(w, runs, opt)
	if opt.ByModel {
		fleetByModel(w, runs)
	}
	if opt.Timeline {
		fleetTimeline(w, runs)
	}
	fleetStuck(w, runs)
	fleetOffenders(w, runs, spikes, opt)
}

// fleetByModel renders the model×cost×fallback breakdown — the single highest-value fleet view: per
// answered model, the run count, total + avg cost, and HOW MANY were fallbacks (the loop swapped to it
// after a planned model failed). It's what surfaces "one model is N% of spend purely as a fallback".
// Only meaningful for an operator bearer (cost/model are operator-tier); a baseline window has no
// model/cost and the table is skipped. Sorted by total cost desc — the biggest spender leads.
func fleetByModel(w io.Writer, runs []client.RunSummary) {
	type agg struct {
		runs, fallbacks int
		total           float64
	}
	byModel := map[string]*agg{}
	var order []string
	for _, r := range runs {
		m := answeredModel(r)
		if m == "" {
			continue // baseline bearer / unstamped run — no model to attribute
		}
		a := byModel[m]
		if a == nil {
			a = &agg{}
			byModel[m] = a
			order = append(order, m)
		}
		a.runs++
		a.total += cost(r)
		if isFallback(r) {
			a.fallbacks++
		}
	}
	if len(byModel) == 0 {
		return
	}
	sort.SliceStable(order, func(i, j int) bool { return byModel[order[i]].total > byModel[order[j]].total })

	_, _ = fmt.Fprintln(w, "\nBy model (cost · fallbacks):")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "  MODEL\tRUNS\tCOST\tAVG\tFALLBACK")
	for _, m := range order {
		a := byModel[m]
		avg := 0.0
		if a.runs > 0 {
			avg = a.total / float64(a.runs)
		}
		fb := "-"
		if a.fallbacks > 0 {
			fb = fmt.Sprintf("%d (%d%%)", a.fallbacks, pct(a.fallbacks, a.runs))
		}
		_, _ = fmt.Fprintf(tw, "  %s\t%d\t%s\t%s\t%s\n", m, a.runs, costCell(a.total), costCell(avg), fb)
	}
	_ = tw.Flush()
}

// fleetTimeline renders the per-day runs/errors/cost histogram across the window — the "what changed
// today" anchor an operator reads before trusting absolute numbers. Buckets by the run's created_at
// date (UTC). Oldest → newest so the trend reads top-to-bottom. Skipped on an empty window.
func fleetTimeline(w io.Writer, runs []client.RunSummary) {
	type day struct {
		runs, errors int
		cost         float64
	}
	byDay := map[string]*day{}
	var dates []string
	for _, r := range runs {
		d := runDate(r)
		if d == "" {
			continue
		}
		b := byDay[d]
		if b == nil {
			b = &day{}
			byDay[d] = b
			dates = append(dates, d)
		}
		b.runs++
		if r.Status == "error" {
			b.errors++
		}
		b.cost += cost(r)
	}
	if len(byDay) == 0 {
		return
	}
	sort.Strings(dates) // ISO date strings sort chronologically

	_, _ = fmt.Fprintln(w, "\nDaily timeline:")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "  DAY\tRUNS\tERR\tCOST")
	for _, d := range dates {
		b := byDay[d]
		_, _ = fmt.Fprintf(tw, "  %s\t%d\t%d\t%s\n", d, b.runs, b.errors, costCell(b.cost))
	}
	_ = tw.Flush()
}

// runDate is the run's created_at date (YYYY-MM-DD, UTC) for the timeline bucket; "" if unparseable.
func runDate(r client.RunSummary) string {
	t, err := time.Parse(time.RFC3339, r.CreatedAt)
	if err != nil {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

// fleetStuck surfaces runs that are still 'running' with no finished_at past the stuck clock — runs that
// never produced a callback, which operators otherwise drop to db.py for. Rendered only when there ARE
// stuck runs (a clean fleet stays quiet). Full ids so a stuck run is one paste from `rc run show <id>`.
func fleetStuck(w io.Writer, runs []client.RunSummary) {
	now := time.Now()
	var stuck []client.RunSummary
	for _, r := range runs {
		if isStuck(r, now) {
			stuck = append(stuck, r)
		}
	}
	if len(stuck) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "\nStuck/running (no finish past %s):\n", stuckRunAfter)
	for _, r := range stuck {
		age := "?"
		if t, err := time.Parse(time.RFC3339, r.CreatedAt); err == nil {
			age = duration(now.Sub(t).Milliseconds())
		}
		_, _ = fmt.Fprintf(w, "  %s %s old (%s)\n", r.RunID, age, r.Kind)
	}
}

// fleetAggregate ports runs_digest.py's "## Aggregate" block: done/error counts, grounding-discarded
// rate, journal coverage (analyses), cost total/median, peak-context spread, blocked-egress hosts.
func fleetAggregate(w io.Writer, runs []client.RunSummary, opt FleetOptions) {
	_, _ = fmt.Fprintln(w, "\nAggregate:")
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
	_, _ = fmt.Fprintln(w, line)

	gd := 0
	for _, r := range runs {
		if r.Health != nil && r.Health.GroundingDiscarded {
			gd++
		}
	}
	_, _ = fmt.Fprintf(w, "  grounding discarded: %d/%d (%d%%)\n", gd, len(runs), pct(gd, len(runs)))

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
		_, _ = fmt.Fprintf(w, "  journal coverage (analyses): %d/%d (%d%%)\n", journaled, analyses, pct(journaled, analyses))
	}

	var costs []float64
	var total float64
	for _, r := range runs {
		c := cost(r)
		costs = append(costs, c)
		total += c
	}
	if total > 0 {
		_, _ = fmt.Fprintf(w, "  cost: total %s · median %s\n", costCell(total), costCell(median(costs)))
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
		_, _ = fmt.Fprintf(w, "  peak context: median %s · max %s · ≥%s: %d/%d (context-rot risk)\n",
			tokens(medianInt(ctxs)), tokens(maxCtx), tokens(int64(opt.CtxWarn)), rotted, len(runs))
	}

	hosts := egressHostsFromRuns(runs)
	if len(hosts) == 0 {
		_, _ = fmt.Fprintln(w, "  blocked-egress runs: none")
	} else {
		_, _ = fmt.Fprintf(w, "  blocked-egress: %d run(s) with blocked attempts\n", len(hosts))
	}
}

// egressHostsFromRuns returns the run ids that had ≥1 blocked egress (the digest only has per-run
// counts from /runs — the host detail is `rc fleet patterns`' job). Returns the count of affected runs.
func egressHostsFromRuns(runs []client.RunSummary) []string {
	var ids []string
	for _, r := range runs {
		if r.Health != nil && r.Health.BlockedEgress > 0 {
			ids = append(ids, r.RunID)
		}
	}
	return ids
}

// fleetOffenders ports the "## Worst offenders" block — full ids ready to paste into `rc run show <id>`. Each
// offender line carries the FULL triage tail (cost · secs · turns · bash_err · peak-ctx · FB) so a drill
// needs no follow-up query: the operator sees at a glance whether a top-cost run was also a fallback, ran
// long, or burned context. The fallback offenders block calls out the runs that swapped models.
func fleetOffenders(w io.Writer, runs []client.RunSummary, spikes map[string]bool, opt FleetOptions) {
	_, _ = fmt.Fprintln(w, "\nWorst offenders (full ids — `rc run debug <id>`):")
	printed := false

	topCost := topByCost(runs, 3)
	if len(topCost) > 0 {
		printed = true
		_, _ = fmt.Fprintln(w, "  Top cost:")
		for _, r := range topCost {
			_, _ = fmt.Fprintf(w, "    %s %s — %s\n", r.RunID, costCell(cost(r)), offenderTail(r))
		}
	}

	topErr := topByBashErr(runs, 3)
	if len(topErr) > 0 {
		printed = true
		_, _ = fmt.Fprintln(w, "  Top bash failures:")
		for _, r := range topErr {
			_, _ = fmt.Fprintf(w, "    %s ERR×%d — %s\n", r.RunID, r.Health.BashErrCount, offenderTail(r))
		}
	}

	topCtx := topByCtx(runs, opt.CtxWarn, 3)
	if len(topCtx) > 0 {
		printed = true
		_, _ = fmt.Fprintln(w, "  Top context (rot risk):")
		for _, r := range topCtx {
			_, _ = fmt.Fprintf(w, "    %s ctx %s — %s\n", r.RunID, tokens(peakCtx(r)), offenderTail(r))
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
		_, _ = fmt.Fprintln(w, "  Blocked egress:")
		for _, r := range egr {
			_, _ = fmt.Fprintf(w, "    %s EGR×%d — %s\n", r.RunID, r.Health.BlockedEgress, offenderTail(r))
		}
	}

	var fb []client.RunSummary
	for _, r := range runs {
		if isFallback(r) {
			fb = append(fb, r)
		}
	}
	if len(fb) > 0 {
		printed = true
		// Most expensive fallbacks first — the spend a fallback model quietly drove.
		sort.SliceStable(fb, func(i, j int) bool { return cost(fb[i]) > cost(fb[j]) })
		_, _ = fmt.Fprintln(w, "  Model fallbacks:")
		for _, r := range fb {
			planned := r.Health.PlannedModel
			if planned == "" {
				planned = "?"
			}
			_, _ = fmt.Fprintf(w, "    %s %s←%s — %s\n", r.RunID, answeredModel(r), planned, offenderTail(r))
		}
	}

	if !printed {
		_, _ = fmt.Fprintln(w, "  (none — a clean window)")
	}
}

// offenderTail is the compact triage tail every worst-offender line shares: kind/status, then the
// per-run health scalars an operator would otherwise re-query (cost, wall-clock secs, turns, bash
// failures, peak context, and the fallback marker). Built only from fields the index already carries.
func offenderTail(r client.RunSummary) string {
	parts := []string{fmt.Sprintf("%s, %s", r.Kind, r.Status)}
	if c := cost(r); c > 0 {
		parts = append(parts, costCell(c))
	}
	if s := secsOf(r); s > 0 {
		parts = append(parts, fmt.Sprintf("%.0fs", s))
	}
	if r.Health != nil {
		if r.Health.Turns > 0 {
			parts = append(parts, fmt.Sprintf("%dt", r.Health.Turns))
		}
		if r.Health.BashErrCount > 0 {
			parts = append(parts, fmt.Sprintf("ERR×%d", r.Health.BashErrCount))
		}
	}
	if pc := peakCtx(r); pc > 0 {
		parts = append(parts, "ctx "+tokens(pc))
	}
	if isFallback(r) {
		parts = append(parts, "FB")
	}
	if h := errorHead(r); h != "" {
		parts = append(parts, `"`+h+`"`)
	}
	return strings.Join(parts, " · ")
}

// errorHead is the run's 120-char host-error head (run_health.error_head) — the error-class
// discriminator that saves a per-run `rc run debug` drill; '' when the run has no error.
func errorHead(r client.RunSummary) string {
	if r.Health == nil {
		return ""
	}
	return r.Health.ErrorHead
}

// --- agent (token-lean) digest ---

// fleetAgent emits the COMPUTED digest for an agent to read whole: the ranked shortlist, then the same
// rollup blocks the human render computes (aggregate, model×cost×fallback, daily timeline, stuck runs,
// worst offenders — reused, not re-derived), then the full per-run index. Every id is a full UUID so a
// drill is one paste. Unlike the human digest, by-model and timeline are always on: an agent reads the
// digest once instead of re-running with flags.
func fleetAgent(w io.Writer, runs []client.RunSummary, opt FleetOptions) {
	spikes := costSpikes(runs)
	_, _ = fmt.Fprintf(w, "runs — last %dd%s · %d runs\n\n", opt.Days, learningScope(opt.Learning), len(runs))
	if len(runs) == 0 {
		_, _ = fmt.Fprintln(w, "(no runs in window)")
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
		_, _ = fmt.Fprintln(w, "look here first:")
		for i, r := range flagged {
			if i >= shortlistCap {
				_, _ = fmt.Fprintf(w, "  … +%d more flagged (see all runs)\n", len(flagged)-shortlistCap)
				break
			}
			reason := flagStr(r, spikes, opt.CtxWarn)
			if r.Status == "error" {
				reason = strings.TrimSpace("err " + reason)
				if h := errorHead(r); h != "" {
					reason += ` "` + h + `"`
				}
			}
			_, _ = fmt.Fprintf(w, "  %s  %s\n", r.RunID, reason)
		}
	}

	fleetAggregate(w, runs, opt)
	fleetByModel(w, runs)
	fleetTimeline(w, runs)
	fleetStuck(w, runs)
	fleetOffenders(w, runs, spikes, opt)

	_, _ = fmt.Fprintln(w, "\nall runs (newest first):")
	for _, r := range runs {
		_, _ = fmt.Fprintf(w, "  %s  %s  %s  %s  c%s  %s\n",
			r.RunID, r.Kind, r.Status, costCell(cost(r)), tokens(peakCtx(r)), flagStr(r, spikes, opt.CtxWarn))
	}
	_, _ = fmt.Fprintln(w, "\ndrill: rc run debug <id>")
}

func learningScope(learning string) string {
	if learning == "" {
		return ""
	}
	return " · learning=" + learning
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
