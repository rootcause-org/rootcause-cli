package cli

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newPatternsCmd builds `rc patterns`: the failure/pattern miner over the THIN /runs/events + /runs/egress
// feeds (both paged), porting run_patterns.py's bash-failure + blocked-egress clustering with masked
// signatures and a `suggested fix:` stub per cluster. The server ships raw rows; ALL masking/grouping/
// ranking happens client-side. -o json is a raw passthrough of the paged event + egress rows.
//
// run_patterns.py's run-error-theme + repeated-question sections read run BODIES the thin API doesn't
// expose (the index is category-only, by privacy design); the recurring-error signal is reconstructed
// from bash stderr signatures instead, and the question-runbook section is dropped (no input).
func newPatternsCmd(e *env) *cobra.Command {
	var days int
	var top int
	var kind string
	cmd := &cobra.Command{
		Use:   "patterns",
		Short: "Cluster recent failures into ranked patterns (bash + blocked egress)",
		Long: "Page GET /api/v1/runs/events and /runs/egress and cluster them like run_patterns: bash-failure " +
			"signatures (label + exit + masked stderr) and blocked-egress hosts, each ending in a suggested-fix " +
			"stub. -o json is a raw passthrough of the paged event + egress rows.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			fp := client.FeedParams{Days: days, Kind: kind}

			events, capE, err := c.AllEvents(e.ctx(), fp)
			if err != nil {
				return err
			}
			if capE {
				warnCapped(e, "patterns: hit the events page cap — older events omitted; narrow --kind/--days")
			}
			egress, capG, err := c.AllEgress(e.ctx(), fp)
			if err != nil {
				return err
			}
			if capG {
				warnCapped(e, "patterns: hit the egress page cap — older rows omitted; narrow --kind/--days")
			}

			if e.jsonOut() {
				return emitPatternsJSON(e, events, egress)
			}
			render.Patterns(e.out, events, egress, render.PatternsOptions{Days: days, Top: top, Kind: kind})
			return nil
		},
	}
	cmd.Flags().IntVar(&days, "days", 14, "window in days")
	cmd.Flags().IntVar(&top, "top", 15, "max patterns per section")
	cmd.Flags().StringVar(&kind, "kind", "", "filter by kind: email|prompt|mcp|analysis")
	return cmd
}

// emitPatternsJSON emits the paged raw inputs as one {events:[…],egress:[…]} object — the passthrough
// contract: the rows are the wire structs, reassembled across pages, no clustering applied.
func emitPatternsJSON(e *env, events []client.RunEvent, egress []client.EgressRow) error {
	if events == nil {
		events = []client.RunEvent{}
	}
	if egress == nil {
		egress = []client.EgressRow{}
	}
	b, err := json.Marshal(map[string]any{"events": events, "egress": egress})
	if err != nil {
		return err
	}
	return render.JSON(e.out, b)
}
