package cli

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// errEmptyInstruction is the clear "nothing to do" error when no instruction arrives via args or stdin.
var errEmptyInstruction = errors.New("empty instruction — pass it as args or pipe it on stdin")

// newBrainCmd groups project-brain cache inspection/promotion with the out-of-band edit/consolidation
// queue. Promotion is the synchronous, exact-SHA exception; edits remain async and durable writes land
// outside a run. Long edit instructions can be piped on STDIN.
func newBrainCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "brain", Short: "Inspect, sync, promote, and queue out-of-band brain work"}
	cmd.AddCommand(brainStatusCmd(e), brainSyncCmd(e), brainPromoteCmd(e), brainEditCmd(e), brainConsolidateCmd(e))
	return cmd
}

func brainStatusCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show deployed brain cache status",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			resp, raw, err := c.BrainStatus(e.ctx(), e.scopeProject(), e.scopeTenant())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			render.BrainStatus(e.out, resp)
			return nil
		},
	}
}

func brainSyncCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Fetch origin/main and refresh deployed brain cache",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			resp, raw, err := c.BrainSync(e.ctx(), e.scopeProject(), e.scopeTenant())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			render.BrainSync(e.out, resp)
			return nil
		},
	}
}

var fullGitSHA = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

func brainPromoteCmd(e *env) *cobra.Command {
	var channel, sha string
	cmd := &cobra.Command{
		Use:   "promote --channel stable|edge --sha <commit>",
		Short: "Promote an exact tested commit to a project brain channel",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if channel != "stable" && channel != "edge" {
				return fmt.Errorf("--channel must be stable or edge")
			}
			if !fullGitSHA.MatchString(sha) {
				return fmt.Errorf("--sha must be an exact full 40-character commit SHA")
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			if err := e.resolvePinnedProject(c); err != nil {
				return err
			}
			resp, raw, err := c.BrainPromote(e.ctx(), e.scopeProject(), client.BrainPromoteRequest{Channel: channel, SHA: strings.ToLower(sha)})
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			render.BrainPromote(e.out, resp)
			return nil
		},
	}
	cmd.Flags().StringVar(&channel, "channel", "", "managed channel to move (stable or edge)")
	cmd.Flags().StringVar(&sha, "sha", "", "exact full 40-character commit SHA")
	_ = cmd.MarkFlagRequired("channel")
	_ = cmd.MarkFlagRequired("sha")
	return cmd
}

func brainEditCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "edit <instruction…>",
		Short: "Queue a brain edit from a plain-language instruction (or STDIN)",
		Args:  cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			instruction := strings.TrimSpace(strings.Join(args, " "))
			if instruction == "" {
				in, err := readAllStdin(e)
				if err != nil {
					return err
				}
				instruction = strings.TrimSpace(in)
			}
			if instruction == "" {
				return errEmptyInstruction
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			raw, err := c.BrainEdit(e.ctx(), instruction, e.scopeProject(), e.scopeTenant())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			render.Item(e.out, asItem(raw))
			return nil
		},
	}
}

func brainConsolidateCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "consolidate",
		Short: "Queue the consolidation cron on demand",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			raw, err := c.BrainConsolidate(e.ctx(), e.scopeProject(), e.scopeTenant())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			render.Item(e.out, asItem(raw))
			return nil
		},
	}
}
