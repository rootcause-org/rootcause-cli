package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// runsFlags holds the `rc run list` filter flags, bound per-command so each invocation is isolated.
type runsFlags struct {
	limit    int
	days     int
	kind     string
	category string
	outcome  string
	learning string
	reviewed bool
	before   string
	session  string
}

// newRunListCmd builds `rc run list`: the filterable list view of GET /api/v1/runs, leading with the run
// table. Filters are passed to the server as query params so pagination stays correct. Stable outcome
// and learning enums are checked locally; the server still owns range/kind/category/cursor validation.
func newRunListCmd(e *env) *cobra.Command {
	var f runsFlags
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent runs (filterable)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := validateRunFilters(f.outcome, f.learning); err != nil {
				return err
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			params := client.RunsParams{Limit: f.limit, Days: f.days, Kind: f.kind, Category: f.category, Outcome: f.outcome, Learning: f.learning, Reviewed: f.reviewed, Before: f.before, Session: f.session, Project: e.scopeProject(), Tenant: e.scopeTenant()}
			resp, raw, err := c.Runs(e.ctx(), params)
			if err != nil {
				return err
			}
			if render.IsJSON(e.mode(), e.out) {
				return render.JSON(e.out, raw)
			}
			render.Runs(e.out, resp)
			return nil
		},
	}
	cmd.Flags().IntVar(&f.limit, "limit", 0, "max runs to return (1..100, server default 50)")
	cmd.Flags().IntVar(&f.days, "days", 0, "lookback window in days (server default 14, max 90)")
	cmd.Flags().StringVar(&f.kind, "kind", "", "filter by kind: email|prompt|mcp|analysis|console|chat")
	cmd.Flags().StringVar(&f.category, "category", "", "filter by category (e.g. ok, timeout, cost_cap)")
	cmd.Flags().StringVar(&f.outcome, "outcome", "", "filter by outcome: answered|declined|failed|error|stuck|running|interrupted")
	cmd.Flags().StringVar(&f.learning, "learning", "", "filter by learning signal; bare means any, or use =feedback|sent_delta|triage_skipped|triage_corrected")
	cmd.Flags().Lookup("learning").NoOptDefVal = "any"
	cmd.Flags().BoolVar(&f.reviewed, "reviewed", false, "only runs with a 1–5 human review score (includes held-out eval runs)")
	cmd.Flags().StringVar(&f.before, "before", "", "cursor: run_id to page to the next (older) page (combines with --days)")
	cmd.Flags().StringVar(&f.session, "session", "", "list every run in this chat session")
	return cmd
}

func validateRunFilters(outcome, learning string) error {
	switch outcome {
	case "", "answered", "declined", "failed", "error", "stuck", "running", "interrupted":
	default:
		return fmt.Errorf("invalid --outcome %q (want answered, declined, failed, error, stuck, running, or interrupted)", outcome)
	}
	switch learning {
	case "", "any", "feedback", "sent_delta", "triage_skipped", "triage_corrected":
	default:
		return fmt.Errorf("invalid --learning %q (want any, feedback, sent_delta, triage_skipped, or triage_corrected)", learning)
	}
	return nil
}

// sessionsFlags holds the `rc run sessions` filters, bound per-command so each invocation is isolated.
type sessionsFlags struct {
	kind    string
	days    int
	surface string
	limit   int
	before  string
}

// newRunSessionsCmd builds `rc run sessions`: the CONVERSATION index (GET /api/v1/sessions), the rung
// above `rc run list`. It answers "which conversations happened" so the two existing drills have
// something to drill: `rc run thread <session_id>` for the turns, `rc run trace <run_id>` for one turn's
// bodies. Filters go to the server so paging stays correct.
func newRunSessionsCmd(e *env) *cobra.Command {
	var f sessionsFlags
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "List chat conversations (sessions) with turns, outcome and feedback",
		Long: "List chat conversations, newest first. A conversation is N runs sharing one session id: " +
			"drill its turns with `rc run thread <session_id>`, then one turn's bodies with " +
			"`rc run trace <run_id>`. The feedback column is the rollup over the conversation's rated " +
			"turns; `-o json` is the server payload verbatim, including next_before.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := validateSessionSurface(f.surface); err != nil {
				return err
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			params := client.SessionsParams{
				Kind: f.kind, Days: f.days, Surface: f.surface, Limit: f.limit, Before: f.before,
				Project: e.scopeProject(), Tenant: e.scopeTenant(),
			}
			resp, raw, err := c.Sessions(e.ctx(), params)
			if err != nil {
				return err
			}
			if render.IsJSON(e.mode(), e.out) {
				return render.JSON(e.out, raw)
			}
			render.Sessions(e.out, resp)
			return nil
		},
	}
	cmd.Flags().StringVar(&f.kind, "kind", "", "run kind grouped into sessions (server default: chat)")
	cmd.Flags().IntVar(&f.days, "days", 0, "lookback window in days (server default 14, max 90)")
	cmd.Flags().StringVar(&f.surface, "surface", "", "filter by chat surface: embed|dashboard|all (server default: all)")
	cmd.Flags().IntVar(&f.limit, "limit", 0, "max conversations to return (1..100, server default 50)")
	cmd.Flags().StringVar(&f.before, "before", "", "cursor: session_id to page to the next (older) page")
	return cmd
}

func validateSessionSurface(surface string) error {
	switch surface {
	case "", "embed", "dashboard", "all":
		return nil
	default:
		return fmt.Errorf("invalid --surface %q (want embed, dashboard, or all)", surface)
	}
}
