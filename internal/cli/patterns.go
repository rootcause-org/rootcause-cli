package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newPatternsCmd builds `rc fleet patterns`: the failure/pattern miner over the THIN /run-events + /egress-log
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
		Short: "Cluster recent failures and outbound endpoint patterns",
		Long: "Page GET /api/v1/run-events, /api/v1/egress-log, and /api/v1/api-log and cluster them like run_patterns: bash-failure " +
			"signatures, blocked-egress hosts, allowed endpoint use, and abnormal write volume, with suggested-fix " +
			"stub. --all fans out across every project (all-projects token), one clustered section per project. " +
			"-o json is a raw passthrough of all three paged feeds (keyed by project under --all).",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			if all {
				return runPatternsAll(e, c, days, top, kind)
			}

			fp := client.FeedParams{Days: days, Kind: kind, Project: e.scopeProject(), Tenant: e.scopeTenant()}
			events, egress, httpRows, err := fetchPatternsFeeds(e, c, fp, "patterns")
			if err != nil {
				return err
			}

			if e.jsonOut() {
				return emitPatternsJSON(e, events, egress, httpRows)
			}
			render.Patterns(e.out, events, egress, httpRows, render.PatternsOptions{Days: days, Top: top, Kind: kind})
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
func fetchPatternsFeeds(e *env, c *client.Client, fp client.FeedParams, label string) ([]client.RunEvent, []client.EgressRow, []client.HTTPAuditRow, error) {
	events, capE, err := c.AllEvents(e.ctx(), fp)
	if err != nil {
		return nil, nil, nil, err
	}
	if capE {
		warnCapped(e, label+": hit the events page cap — older events omitted; narrow --kind/--days")
	}
	egress, capG, err := c.AllEgress(e.ctx(), fp)
	if err != nil {
		return nil, nil, nil, err
	}
	if capG {
		warnCapped(e, label+": hit the egress page cap — older rows omitted; narrow --kind/--days")
	}
	httpRows, capH, err := c.AllHTTPAudit(e.ctx(), client.HTTPAuditParams{
		Days: fp.Days, Project: fp.Project, Tenant: fp.Tenant,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	if capH {
		warnCapped(e, label+": hit the HTTP audit page cap — older rows omitted; narrow --days")
	}
	// The HTTP feed has no run-kind column. When --kind is active, retain only rows joined to the
	// kind-filtered run ids present in either thin feed.
	if fp.Kind != "" {
		runs := map[string]bool{}
		for _, event := range events {
			runs[event.RunID] = true
		}
		for _, row := range egress {
			runs[row.RunID] = true
		}
		filtered := httpRows[:0]
		for _, row := range httpRows {
			if runs[row.RunID] {
				filtered = append(filtered, row)
			}
		}
		httpRows = filtered
	}
	return events, egress, httpRows, nil
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
		Project string                `json:"project"`
		Events  []client.RunEvent     `json:"events"`
		Egress  []client.EgressRow    `json:"egress"`
		HTTP    []client.HTTPAuditRow `json:"http"`
	}
	entries := make([]entry, 0, len(projects))
	for _, proj := range projects {
		fp := client.FeedParams{Days: days, Kind: kind, Project: proj.ID}
		events, egress, httpRows, ferr := fetchPatternsFeeds(e, c, fp, "patterns --all ("+proj.Name+")")
		if ferr != nil {
			return fmt.Errorf("patterns --all: project %s: %w", proj.Name, ferr)
		}
		if events == nil {
			events = []client.RunEvent{}
		}
		if egress == nil {
			egress = []client.EgressRow{}
		}
		if httpRows == nil {
			httpRows = []client.HTTPAuditRow{}
		}
		entries = append(entries, entry{Project: proj.Name, Events: events, Egress: egress, HTTP: httpRows})
		if !e.jsonOut() {
			_, _ = fmt.Fprintf(e.out, "════ %s ════\n", proj.Name)
			render.Patterns(e.out, events, egress, httpRows, render.PatternsOptions{Days: days, Top: top, Kind: kind})
			_, _ = fmt.Fprintln(e.out)
		}
	}

	if e.jsonOut() {
		b, merr := json.Marshal(map[string]any{"projects": entries})
		if merr != nil {
			return merr
		}
		return e.renderJSON("patterns-all", b)
	}
	return nil
}

// emitPatternsJSON emits the paged raw inputs as one {events:[…],egress:[…]} object — the passthrough
// contract: the rows are the wire structs, reassembled across pages, no clustering applied.
func emitPatternsJSON(e *env, events []client.RunEvent, egress []client.EgressRow, httpRows []client.HTTPAuditRow) error {
	if events == nil {
		events = []client.RunEvent{}
	}
	if egress == nil {
		egress = []client.EgressRow{}
	}
	if httpRows == nil {
		httpRows = []client.HTTPAuditRow{}
	}
	b, err := json.Marshal(map[string]any{"events": events, "egress": egress, "http": httpRows})
	if err != nil {
		return err
	}
	return e.renderJSON("patterns", b)
}
