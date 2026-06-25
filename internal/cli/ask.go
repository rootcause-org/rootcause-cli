package cli

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// askFlags holds `rc ask`'s flags, bound per-command so each invocation is isolated. The tenant comes
// from the persistent --tenant (or the brain marker), not a local flag.
type askFlags struct {
	brainRef string
	effort   string
	session  string
	noWait   bool
	timeout  time.Duration
}

// newAskCmd builds `rc ask "<question>"` — the trigger verb. It POSTs the prompt to /api/v1/runs
// (OAuth-authed, optionally ?project=-scoped), then by DEFAULT waits, polling /runs/{id} until the run
// reaches a terminal status, and prints the same summary as `rc run <id>`. --no-wait returns the run_id
// immediately. --brain-ref runs the question against a non-main brain ref (a dev/* branch) — the project
// dev's "test without pushing main" loop. The CLI stays thin: it triggers + polls + renders; all run
// logic lives server-side.
func newAskCmd(e *env) *cobra.Command {
	var f askFlags
	cmd := &cobra.Command{
		Use:   "ask <question>",
		Short: "Trigger a run from a question and wait for the answer",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			jsonMode := render.IsJSON(e.mode(), e.out)

			effort, err := normalizeAskEffort(f.effort, cmd.Flags().Changed("effort"))
			if err != nil {
				return err
			}

			sub, raw, err := c.Submit(e.ctx(), client.SubmitRequest{
				Prompt:          args[0],
				SessionID:       f.session,
				Tenant:          e.scopeTenant(),
				BrainRef:        f.brainRef,
				ReasoningEffort: effort,
				Project:         e.scopeProject(),
			})
			if err != nil {
				return err
			}

			// --no-wait: emit the run id and return. JSON echoes the verbatim 202 body so `jq -r .run_id`
			// works and no server field is dropped; table prints the id alone (script-capturable) with a
			// poll hint on stderr.
			if f.noWait {
				if jsonMode {
					return render.JSON(e.out, raw)
				}
				_, _ = fmt.Fprintln(e.out, sub.RunID)
				_, _ = fmt.Fprintf(e.err, "submitted — poll with: rc run %s\n", sub.RunID)
				return nil
			}

			detail, err := waitForRun(e, c, sub, f.timeout)
			if err != nil {
				return err
			}

			// Render the terminal run exactly like `rc run <id>`: JSON is a verbatim passthrough of
			// /runs/{id} (so the seam matches), table uses the typed detail we already polled.
			if jsonMode {
				raw, err := c.Raw(e.ctx(), "GET", "/api/v1/runs/"+url.PathEscape(detail.RunID), nil)
				if err != nil {
					return err
				}
				return render.JSON(e.out, raw)
			}
			render.Run(e.out, detail)
			return nil
		},
	}
	cmd.Flags().StringVar(&f.effort, "effort", "", "reasoning effort override: default, pro, or max")
	cmd.Flags().StringVar(&f.brainRef, "brain-ref", "", "run against a non-main brain ref (e.g. dev/refund-rework) — a test run")
	cmd.Flags().StringVar(&f.session, "session", "", "session id to thread the run onto")
	cmd.Flags().BoolVar(&f.noWait, "no-wait", false, "submit and print the run_id immediately, without waiting")
	cmd.Flags().DurationVar(&f.timeout, "timeout", 5*time.Minute, "max time to wait for a terminal status")
	return cmd
}

func normalizeAskEffort(v string, set bool) (string, error) {
	if !set {
		return "", nil
	}
	switch effort := strings.TrimSpace(v); effort {
	case "default", "pro", "max":
		return effort, nil
	default:
		return "", fmt.Errorf("invalid --effort %q (want default, pro, or max)", effort)
	}
}

// waitForRun polls /runs/{id} until the run reaches a terminal status or the timeout elapses, printing
// a terse live status line on a TTY (to stderr, so it never pollutes piped/JSON stdout). The interval
// is the server's poll_after_ms hint, with a sane floor/default.
func waitForRun(e *env, c *client.Client, sub *client.SubmitResponse, timeout time.Duration) (*client.RunDetail, error) {
	interval := time.Duration(sub.PollAfterMs) * time.Millisecond
	if interval <= 0 {
		interval = time.Second
	}
	ctx, cancel := context.WithTimeout(e.ctx(), timeout)
	defer cancel()

	showProgress := render.IsTerminal(e.err)
	for {
		detail, err := c.Run(ctx, sub.RunID)
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return nil, fmt.Errorf("timed out after %s waiting for run %s", timeout, sub.RunID)
			}
			return nil, err
		}
		if isTerminalStatus(detail.Status) {
			if showProgress {
				_, _ = fmt.Fprintf(e.err, "\r\033[K") // clear the progress line before the summary prints
			}
			return detail, nil
		}
		if showProgress {
			_, _ = fmt.Fprintf(e.err, "\r\033[K%s … %s", sub.RunID, detail.Status)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			if showProgress {
				_, _ = fmt.Fprintf(e.err, "\r\033[K")
			}
			return nil, fmt.Errorf("timed out after %s waiting for run %s (last status: %s)", timeout, sub.RunID, detail.Status)
		case <-timer.C:
		}
	}
}

// isTerminalStatus reports whether a run status is final. The server's in-progress states are
// queued/running; treating "everything else non-empty" as terminal means a new terminal state (e.g.
// failed, cancelled) ends the wait rather than hanging until timeout.
func isTerminalStatus(s string) bool {
	switch s {
	case "", "queued", "running", "pending":
		return false
	default:
		return true
	}
}
