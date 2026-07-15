package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newFleetRunsCmd builds `rc fleet runs`: the fleet digest over GET /api/v1/runs (paged), porting runs_digest.py's
// per-run flag line + aggregate + worst offenders. The server ships raw per-run rows (+ the run_health
// triage block for an operator bearer); the CLI computes the one derived flag the server can't (the $!
// cost-spike, which needs a per-kind median) and renders the digest. --format agent emits the token-lean
// index for an agent to triage. In -o json it's a raw passthrough of the paged run rows (no rendering).
func newFleetRunsCmd(e *env) *cobra.Command {
	var days int
	var kind string
	var format string
	var ctxWarn int
	var all bool
	var byModel bool
	var timeline bool
	var learning string
	cmd := &cobra.Command{
		Use:   "runs",
		Short: "Fleet digest of recent runs (flags, rates, worst offenders)",
		Long: "Page GET /api/v1/runs and render the runs_digest view: a per-run line with health flags + " +
			"cost, the aggregate rates, and the worst-offender shortlists. --format agent gives a token-lean " +
			"index for an agent to triage. --all fans out across every project (all-projects token), grouped " +
			"per project with a fleet total. -o json is a raw passthrough of the paged run rows (keyed by " +
			"project under --all).",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if format != "" && format != "human" && format != "agent" {
				return errBadFormat(format)
			}
			if err := validateRunFilters("", learning); err != nil {
				return err
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			opt := render.FleetOptions{Days: days, Kind: kind, Learning: learning, Format: format, CtxWarn: ctxWarn, ByModel: byModel, Timeline: timeline}

			if all {
				return runFleetAll(e, c, kind, learning, opt)
			}

			// `days` is server-side so paging stops at the requested window instead of walking old history.
			// kind IS a server-side filter; --project scopes an all-projects token to one project
			// (disregarded for a pinned token).
			p := client.RunsParams{Days: days, Kind: kind, Learning: learning, Project: e.scopeProject(), Tenant: e.scopeTenant()}

			runs, capped, err := c.AllRuns(e.ctx(), p)
			if err != nil {
				return err
			}
			if capped {
				warnCapped(e, "fleet: hit the page cap — older runs omitted; narrow --kind or use rc run list --before")
			}

			if e.jsonOut() {
				return emitRunsJSON(e, runs)
			}
			render.Fleet(e.out, runs, opt)
			return nil
		},
	}
	cmd.Flags().IntVar(&days, "days", 7, "window label for the digest header (days)")
	cmd.Flags().StringVar(&kind, "kind", "", "filter by kind: email|prompt|mcp|analysis")
	cmd.Flags().StringVar(&format, "format", "human", "output style: human|agent")
	cmd.Flags().IntVar(&ctxWarn, "ctx-warn", render.DefaultCtxWarn, "peak-context tokens at/above which a run gets the CTX flag (0 disables)")
	cmd.Flags().BoolVar(&all, "all", false, "fan out across every project (requires an all-projects token)")
	cmd.Flags().BoolVar(&byModel, "by-model", false, "add the model×cost×fallback breakdown (which model burned the spend, how much was a fallback)")
	cmd.Flags().BoolVar(&timeline, "timeline", false, "add the per-day runs/errors/cost timeline")
	cmd.Flags().StringVar(&learning, "learning", "", "filter by learning signal; bare means any, or use =feedback|sent_delta|triage_skipped|triage_corrected")
	cmd.Flags().Lookup("learning").NoOptDefVal = "any"
	return cmd
}

// runFleetAll fans the digest out across the whole fleet: list projects, page each one's runs with an
// explicit ?project= scope, then render grouped-by-project with a fleet total. In -o json it emits the
// merged structure {projects:[{project, runs:[…]}], total_runs}. A per-project fetch error aborts (the
// digest is only honest if it's complete).
func runFleetAll(e *env, c *client.Client, kind, learning string, opt render.FleetOptions) error {
	projects, err := fanOutProjects(e, c)
	if err != nil {
		return err
	}

	groups := make([]render.FleetGroup, 0, len(projects))
	for _, proj := range projects {
		runs, capped, ferr := c.AllRuns(e.ctx(), client.RunsParams{Days: opt.Days, Kind: kind, Learning: learning, Project: proj.ID})
		if ferr != nil {
			return fmt.Errorf("fleet --all: project %s: %w", proj.Name, ferr)
		}
		if capped {
			warnCapped(e, "fleet --all: hit the page cap for "+proj.Name+" — older runs omitted; narrow --kind")
		}
		groups = append(groups, render.FleetGroup{Project: proj.Name, Runs: runs})
	}

	if e.jsonOut() {
		return emitFleetAllJSON(e, groups)
	}
	render.FleetAll(e.out, groups, opt)
	return nil
}

// emitFleetAllJSON emits the merged fan-out structure: one entry per project carrying its raw run rows,
// plus a fleet total. The rows are the verbatim wire structs (no client digest), reassembled across
// pages and grouped by project — the passthrough contract extended to the fan-out shape.
func emitFleetAllJSON(e *env, groups []render.FleetGroup) error {
	type projEntry struct {
		Project string              `json:"project"`
		Runs    []client.RunSummary `json:"runs"`
	}
	entries := make([]projEntry, 0, len(groups))
	total := 0
	for _, g := range groups {
		runs := g.Runs
		if runs == nil {
			runs = []client.RunSummary{}
		}
		entries = append(entries, projEntry{Project: g.Project, Runs: runs})
		total += len(runs)
	}
	b, err := json.Marshal(map[string]any{"projects": entries, "total_runs": total})
	if err != nil {
		return err
	}
	return e.renderJSON("fleet-all", b)
}

// emitRunsJSON emits the paged run rows as a single JSON object {runs:[…]} — the raw passthrough contract
// for a paged endpoint: every server field rides through verbatim (the rows are the wire struct), just
// reassembled across pages. No client-side digest in JSON mode.
func emitRunsJSON(e *env, runs []client.RunSummary) error {
	if runs == nil {
		runs = []client.RunSummary{}
	}
	b, err := json.Marshal(map[string]any{"runs": runs})
	if err != nil {
		return err
	}
	return e.renderJSON("fleet", b)
}
