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
// ranking happens client-side. The clustered markdown is already agent-shaped, so human and agent formats
// render the same view — the point of an EXPLICIT --format is pinning that render when stdout is a pipe
// (auto mode alone would spill the raw high-volume rows). -o json stays the raw passthrough of the paged
// event + egress rows and wins over --format.
//
// run_patterns.py's run-error-theme + repeated-question sections read run BODIES the thin API doesn't
// expose (the index is category-only, by privacy design); the recurring-error signal is reconstructed
// from bash stderr signatures instead, and the question-runbook section is dropped (no input).
func newPatternsCmd(e *env) *cobra.Command {
	var days int
	var top int
	var kind string
	var format string
	var all bool
	cmd := &cobra.Command{
		Use:   "patterns",
		Short: "Cluster recent failures and outbound endpoint patterns",
		Long: "Page GET /api/v1/run-events, /api/v1/egress-log, and /api/v1/api-log and cluster them like run_patterns: bash-failure " +
			"signatures, blocked-egress hosts, allowed endpoint use, and abnormal write volume, with suggested-fix " +
			"stub. human and agent formats render the same clustered view; passing --format explicitly renders it " +
			"even when stdout is piped. --all fans out across every project (all-projects token), one clustered " +
			"section per project. -o json is a raw passthrough of all three paged feeds (keyed by project under " +
			"--all) and takes precedence over --format.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if format != "" && format != "human" && format != "agent" {
				return errBadFormat(format)
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			rawJSON := rawRowsJSON(e, cmd)
			if all {
				return runPatternsAll(e, c, days, top, kind, rawJSON)
			}

			fp := client.FeedParams{Days: days, Kind: kind, Project: e.scopeProject(), Tenant: e.scopeTenant()}
			feeds, err := fetchPatternsFeeds(e, c, fp, "patterns")
			if err != nil {
				return err
			}

			if rawJSON {
				return emitPatternsJSON(e, feeds)
			}
			render.Patterns(e.out, feeds.Events, feeds.Egress, feeds.HTTP,
				render.PatternsOptions{Days: days, Top: top, Kind: kind, DetailRedacted: feeds.Redacted})
			return nil
		},
	}
	cmd.Flags().IntVar(&days, "days", 14, "window in days")
	cmd.Flags().IntVar(&top, "top", 15, "max patterns per section")
	cmd.Flags().StringVar(&kind, "kind", "", "filter by kind: email|prompt|mcp|analysis|console|chat")
	cmd.Flags().StringVar(&format, "format", "human", "output style: human|agent (same clustered view; explicit --format forces it over a pipe)")
	cmd.Flags().BoolVar(&all, "all", false, "fan out across every project (requires an all-projects token)")
	return cmd
}

// patternsFeeds is one scope's raw mining input: the three paged feeds plus whether the server withheld
// row detail (a non-project-admin token) — the miner must SAY that, not report a clean fleet.
type patternsFeeds struct {
	Events   []client.RunEvent
	Egress   []client.EgressRow
	HTTP     []client.HTTPAuditRow
	Redacted bool
}

// fetchPatternsFeeds pages the three raw feeds for one scope, warning (never failing) on a page-cap hit.
// label namespaces the cap warning so a fan-out names the project.
func fetchPatternsFeeds(e *env, c *client.Client, fp client.FeedParams, label string) (patternsFeeds, error) {
	events, metaE, err := c.AllEvents(e.ctx(), fp)
	if err != nil {
		return patternsFeeds{}, err
	}
	if metaE.Capped {
		warnCapped(e, label+": hit the events page cap — older events omitted; narrow --kind/--days")
	}
	egress, metaG, err := c.AllEgress(e.ctx(), fp)
	if err != nil {
		return patternsFeeds{}, err
	}
	if metaG.Capped {
		warnCapped(e, label+": hit the egress page cap — older rows omitted; narrow --kind/--days")
	}
	httpRows, metaH, err := c.AllHTTPAudit(e.ctx(), client.HTTPAuditParams{
		Days: fp.Days, Project: fp.Project, Tenant: fp.Tenant,
	})
	if err != nil {
		return patternsFeeds{}, err
	}
	if metaH.Capped {
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
	return patternsFeeds{
		Events: events, Egress: egress, HTTP: httpRows,
		Redacted: metaE.DetailRedacted || metaG.DetailRedacted || metaH.DetailRedacted,
	}, nil
}

// runPatternsAll fans the pattern mining out across the fleet: page each project's feeds with an explicit
// ?project= scope and cluster them under a per-project header, or — when rawJSON (see rawRowsJSON) —
// merge them into a {project→{events,egress}} object. A per-project fetch error aborts.
func runPatternsAll(e *env, c *client.Client, days, top int, kind string, rawJSON bool) error {
	projects, err := fanOutProjects(e, c)
	if err != nil {
		return err
	}

	type entry struct {
		Project        string                `json:"project"`
		Events         []client.RunEvent     `json:"events"`
		Egress         []client.EgressRow    `json:"egress"`
		HTTP           []client.HTTPAuditRow `json:"http"`
		DetailRedacted bool                  `json:"detail_redacted,omitempty"`
	}
	entries := make([]entry, 0, len(projects))
	for _, proj := range projects {
		fp := client.FeedParams{Days: days, Kind: kind, Project: proj.ID}
		feeds, ferr := fetchPatternsFeeds(e, c, fp, "patterns --all ("+proj.Name+")")
		if ferr != nil {
			return fmt.Errorf("patterns --all: project %s: %w", proj.Name, ferr)
		}
		if feeds.Events == nil {
			feeds.Events = []client.RunEvent{}
		}
		if feeds.Egress == nil {
			feeds.Egress = []client.EgressRow{}
		}
		if feeds.HTTP == nil {
			feeds.HTTP = []client.HTTPAuditRow{}
		}
		entries = append(entries, entry{
			Project: proj.Name, Events: feeds.Events, Egress: feeds.Egress, HTTP: feeds.HTTP,
			DetailRedacted: feeds.Redacted,
		})
		if !rawJSON {
			_, _ = fmt.Fprintf(e.out, "════ %s ════\n", proj.Name)
			render.Patterns(e.out, feeds.Events, feeds.Egress, feeds.HTTP,
				render.PatternsOptions{Days: days, Top: top, Kind: kind, DetailRedacted: feeds.Redacted})
			_, _ = fmt.Fprintln(e.out)
		}
	}

	if rawJSON {
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
func emitPatternsJSON(e *env, feeds patternsFeeds) error {
	if feeds.Events == nil {
		feeds.Events = []client.RunEvent{}
	}
	if feeds.Egress == nil {
		feeds.Egress = []client.EgressRow{}
	}
	if feeds.HTTP == nil {
		feeds.HTTP = []client.HTTPAuditRow{}
	}
	out := map[string]any{"events": feeds.Events, "egress": feeds.Egress, "http": feeds.HTTP}
	// Carry the withheld-detail marker into the raw passthrough too: a consumer doing its own mining must
	// be able to tell thin rows from a quiet fleet.
	if feeds.Redacted {
		out["detail_redacted"] = true
	}
	b, err := json.Marshal(out)
	if err != nil {
		return err
	}
	return e.renderJSON("patterns", b)
}
