package cli

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// errEmptyInstruction is the clear "nothing to do" error when no instruction arrives via args or stdin.
var errEmptyInstruction = errors.New("empty instruction — pass it as args or pipe it on stdin")

// newBrainCmd builds `rc brain edit <instruction>` and `rc brain consolidate` over the out-of-band
// brain-write queue (POST /api/v1/brain/{edit,consolidate}). Both are async — they return
// {queued, job_id}; the durable write lands later (the run is read-only to the brain). The instruction
// is joined from the args, or read from STDIN when none are given (so a long instruction can be piped).
func newBrainCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "brain", Short: "Inspect, sync, and queue out-of-band brain work"}
	cmd.AddCommand(brainStatusCmd(e), brainSyncCmd(e), brainEditCmd(e), brainConsolidateCmd(e))
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
			resp, raw, err := c.BrainStatus(e.ctx(), e.scopeProject())
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
		Use:     "sync",
		Aliases: []string{"refresh"},
		Short:   "Fetch origin/main and refresh deployed brain cache",
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			resp, raw, err := c.BrainSync(e.ctx(), e.scopeProject())
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
			raw, err := c.BrainEdit(e.ctx(), instruction, e.scopeProject())
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
			raw, err := c.BrainConsolidate(e.ctx(), e.scopeProject())
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
