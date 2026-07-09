package cli

import (
	"net/url"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// runsFlags holds the `rc runs` filter flags, bound per-command so each invocation is isolated.
type runsFlags struct {
	limit    int
	kind     string
	category string
	before   string
}

// newRunsCmd builds `rc runs`: the filterable list view of GET /api/v1/runs, leading with the run
// table. Filters (limit/kind/category/before) are passed straight to the server as query params; the
// server owns validation (BAD_LIMIT/BAD_KIND/BAD_CATEGORY/BAD_CURSOR), and the CLI surfaces those
// codes verbatim rather than second-guessing them.
func newRunsCmd(e *env) *cobra.Command {
	var f runsFlags
	cmd := &cobra.Command{
		Use:   "runs",
		Short: "List recent runs (filterable)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			params := client.RunsParams{Limit: f.limit, Kind: f.kind, Category: f.category, Before: f.before, Project: e.scopeProject()}
			if render.IsJSON(e.mode(), e.out) {
				raw, err := c.Raw(e.ctx(), "GET", "/api/v1/runs"+queryString(params), nil)
				if err != nil {
					return err
				}
				return render.JSON(e.out, raw)
			}
			resp, err := c.Runs(e.ctx(), params)
			if err != nil {
				return err
			}
			render.Runs(e.out, resp)
			return nil
		},
	}
	cmd.Flags().IntVar(&f.limit, "limit", 0, "max runs to return (1..100, server default 50)")
	cmd.Flags().StringVar(&f.kind, "kind", "", "filter by kind: email|prompt|mcp|analysis|console")
	cmd.Flags().StringVar(&f.category, "category", "", "filter by category (e.g. ok, timeout, cost_cap)")
	cmd.Flags().StringVar(&f.before, "before", "", "cursor: run_id to page to the next (older) page")
	return cmd
}

// queryString builds the /api/v1/runs query for the raw passthrough path, mirroring client.Runs so
// JSON and table modes hit the identical URL. Kept here (not in client) because it's only the
// passthrough path that needs a pre-built string.
func queryString(p client.RunsParams) string {
	q := url.Values{}
	if p.Limit > 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	if p.Days > 0 {
		q.Set("days", strconv.Itoa(p.Days))
	}
	if p.Kind != "" {
		q.Set("kind", p.Kind)
	}
	if p.Category != "" {
		q.Set("category", p.Category)
	}
	if p.Before != "" {
		q.Set("before", p.Before)
	}
	if p.Project != "" {
		q.Set("project", p.Project)
	}
	if enc := q.Encode(); enc != "" {
		return "?" + enc
	}
	return ""
}
