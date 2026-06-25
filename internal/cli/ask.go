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
	scenario string
	from     string
	subject  string
	session  string
	noWait   bool
	timeout  time.Duration
}

const defaultAskFrom = "rc-ask@example.test"

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
			scenario, err := normalizeAskScenario(f.scenario)
			if err != nil {
				return err
			}

			sender, subject := askEmailFields(scenario, args[0], f.from, f.subject, cmd.Flags().Changed("from"), cmd.Flags().Changed("subject"))

			sub, raw, err := c.Submit(e.ctx(), client.SubmitRequest{
				Prompt:          args[0],
				Scenario:        scenario,
				SessionID:       f.session,
				Tenant:          e.scopeTenant(),
				BrainRef:        f.brainRef,
				ReasoningEffort: effort,
				Sender:          sender,
				Subject:         subject,
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

			// JSON remains a verbatim passthrough of /runs/{id}. Table mode is scenario-aware: email tries
			// the richer /full bundle for draft/note bodies, raw stays the lean single-answer view.
			if jsonMode {
				raw, err := c.Raw(e.ctx(), "GET", "/api/v1/runs/"+url.PathEscape(detail.RunID), nil)
				if err != nil {
					return err
				}
				return render.JSON(e.out, raw)
			}
			if scenario == "email" {
				full, _ := c.Full(e.ctx(), detail.RunID)
				render.AskEmail(e.out, detail, full)
				return nil
			}
			render.AskRaw(e.out, detail)
			return nil
		},
	}
	cmd.Flags().StringVar(&f.scenario, "scenario", "email", "answer shape: email or raw (alias: mcp)")
	cmd.Flags().StringVar(&f.from, "from", defaultAskFrom, "sender address for --scenario email")
	cmd.Flags().StringVar(&f.subject, "subject", "", "subject for --scenario email (default: compact prompt first line)")
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

func normalizeAskScenario(v string) (string, error) {
	switch scenario := strings.TrimSpace(strings.ToLower(v)); scenario {
	case "", "email":
		return "email", nil
	case "raw", "mcp":
		return "raw", nil
	default:
		return "", fmt.Errorf("invalid --scenario %q (want email or raw)", scenario)
	}
}

func askEmailFields(scenario, prompt, from, subject string, fromSet, subjectSet bool) (string, string) {
	if scenario != "email" {
		if fromSet {
			from = strings.TrimSpace(from)
		} else {
			from = ""
		}
		if subjectSet {
			subject = strings.TrimSpace(subject)
		} else {
			subject = ""
		}
		return from, subject
	}
	sender := strings.TrimSpace(from)
	if sender == "" {
		sender = defaultAskFrom
	}
	subj := strings.TrimSpace(subject)
	if subj == "" {
		subj = compactAskSubject(prompt)
	}
	return sender, subj
}

func compactAskSubject(prompt string) string {
	line := strings.TrimSpace(prompt)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	line = strings.Join(strings.Fields(line), " ")
	if line == "" {
		return "rc ask"
	}
	r := []rune(line)
	if len(r) > 80 {
		line = strings.TrimRight(string(r[:80]), " ") + "..."
	}
	return line
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
