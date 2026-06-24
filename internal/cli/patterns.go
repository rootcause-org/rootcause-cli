package cli

import (
	"encoding/json"
	"fmt"

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
	var all bool
	cmd := &cobra.Command{
		Use:   "patterns",
		Short: "Cluster recent failures into ranked patterns (bash + blocked egress)",
		Long: "Page GET /api/v1/runs/events and /runs/egress and cluster them like run_patterns: bash-failure " +
			"signatures (label + exit + masked stderr) and blocked-egress hosts, each ending in a suggested-fix " +
			"stub. --all fans out across every project (all-projects token), one clustered section per project. " +
			"-o json is a raw passthrough of the paged event + egress rows (keyed by project under --all).",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			if all {
				return runPatternsAll(e, c, days, top, kind)
			}

			fp := client.FeedParams{Days: days, Kind: kind, Project: e.scopeProject()}
			events, egress, err := fetchPatternsFeeds(e, c, fp, "patterns")
			if err != nil {
				return err
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
	cmd.Flags().BoolVar(&all, "all", false, "fan out across every project (requires an all-projects token)")
	return cmd
}

// fetchPatternsFeeds pages the two raw feeds (events + egress) for one scope, warning (never failing) on
// a page-cap hit. label namespaces the cap warning so a fan-out names the project.
func fetchPatternsFeeds(e *env, c *client.Client, fp client.FeedParams, label string) ([]client.RunEvent, []client.EgressRow, error) {
	events, capE, err := c.AllEvents(e.ctx(), fp)
	if err != nil {
		return nil, nil, err
	}
	if capE {
		warnCapped(e, label+": hit the events page cap — older events omitted; narrow --kind/--days")
	}
	egress, capG, err := c.AllEgress(e.ctx(), fp)
	if err != nil {
		return nil, nil, err
	}
	if capG {
		warnCapped(e, label+": hit the egress page cap — older rows omitted; narrow --kind/--days")
	}
	return events, egress, nil
}

// runPatternsAll fans the pattern mining out across the fleet: page each project's feeds with an explicit
// ?project= scope and cluster them under a per-project header (table mode) or merge them into a
// {project→{events,egress}} object (-o json). A per-project fetch error aborts.
func runPatternsAll(e *env, c *client.Client, days, top int, kind string) error {
	projects, err := fanOutProjects(e, c)
	if err != nil {
		return err
	}

	type entry struct {
		Project string             `json:"project"`
		Events  []client.RunEvent  `json:"events"`
		Egress  []client.EgressRow `json:"egress"`
	}
	entries := make([]entry, 0, len(projects))
	for _, proj := range projects {
		fp := client.FeedParams{Days: days, Kind: kind, Project: proj.ID}
		events, egress, ferr := fetchPatternsFeeds(e, c, fp, "patterns --all ("+proj.Name+")")
		if ferr != nil {
			return fmt.Errorf("patterns --all: project %s: %w", proj.Name, ferr)
		}
		if events == nil {
			events = []client.RunEvent{}
		}
		if egress == nil {
			egress = []client.EgressRow{}
		}
		entries = append(entries, entry{Project: proj.Name, Events: events, Egress: egress})
		if !e.jsonOut() {
			fmt.Fprintf(e.out, "════ %s ════\n", proj.Name)
			render.Patterns(e.out, events, egress, render.PatternsOptions{Days: days, Top: top, Kind: kind})
			fmt.Fprintln(e.out)
		}
	}

	if e.jsonOut() {
		b, merr := json.Marshal(map[string]any{"projects": entries})
		if merr != nil {
			return merr
		}
		return render.JSON(e.out, b)
	}
	return nil
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
