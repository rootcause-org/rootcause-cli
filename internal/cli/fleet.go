package cli

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newFleetCmd builds `rc fleet`: the fleet digest over GET /api/v1/runs (paged), porting runs_digest.py's
// per-run flag line + aggregate + worst offenders. The server ships raw per-run rows (+ the run_health
// triage block for an operator bearer); the CLI computes the one derived flag the server can't (the $!
// cost-spike, which needs a per-kind median) and renders the digest. --format agent emits the token-lean
// index for an agent to triage. In -o json it's a raw passthrough of the paged run rows (no rendering).
func newFleetCmd(e *env) *cobra.Command {
	var days int
	var kind string
	var format string
	var ctxWarn int
	cmd := &cobra.Command{
		Use:   "fleet",
		Short: "Fleet digest of recent runs (flags, rates, worst offenders)",
		Long: "Page GET /api/v1/runs and render the runs_digest view: a per-run line with health flags + " +
			"cost, the aggregate rates, and the worst-offender shortlists. --format agent gives a token-lean " +
			"index for an agent to triage. -o json is a raw passthrough of the paged run rows.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if format != "" && format != "human" && format != "agent" {
				return errBadFormat(format)
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			// The run index has no days filter (it's keyset-paged), so --days is a header label here — the
			// digest window is the recent runs the index returns. kind IS a server-side filter.
			p := client.RunsParams{Kind: kind}

			runs, capped, err := c.AllRuns(e.ctx(), p)
			if err != nil {
				return err
			}
			if capped {
				warnCapped(e, "fleet: hit the page cap — older runs omitted; narrow --kind or use rc runs --before")
			}

			if e.jsonOut() {
				return emitRunsJSON(e, runs)
			}
			render.Fleet(e.out, runs, render.FleetOptions{
				Days: days, Kind: kind, Format: format, CtxWarn: ctxWarn,
			})
			return nil
		},
	}
	cmd.Flags().IntVar(&days, "days", 7, "window label for the digest header (days)")
	cmd.Flags().StringVar(&kind, "kind", "", "filter by kind: email|prompt|mcp|analysis")
	cmd.Flags().StringVar(&format, "format", "human", "output style: human|agent")
	cmd.Flags().IntVar(&ctxWarn, "ctx-warn", render.DefaultCtxWarn, "peak-context tokens at/above which a run gets the CTX flag (0 disables)")
	return cmd
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
	return render.JSON(e.out, b)
}
