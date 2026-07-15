package cli

import (
	"encoding/json"
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
	cmd.AddCommand(brainStatusCmd(e), brainSyncCmd(e), brainPromoteCmd(e), brainPublishCmd(e), brainEditCmd(e), brainConsolidateCmd(e))
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

// brainPublishResult is the -o json receipt: it lets an agent gate on exit code alone, with the raw
// sync result and the resolved channel SHA carried through for logging.
type brainPublishResult struct {
	Project  string                 `json:"project"`
	Channel  string                 `json:"channel"`
	SHA      string                 `json:"sha"`
	OldSHA   string                 `json:"old_sha"`
	Sync     client.BrainSyncResult `json:"sync"`
	Verified bool                   `json:"verified"`
}

// brainPublishCmd chains sync → promote → status-verify with gating between them — the one rc command
// that fans a single intent across three server calls, replacing the by-hand choreography operators ran
// against `rc dev brain {sync,promote,status}`. Sync and status are forced to project scope (tenant="")
// so an ambient tenant can never split them onto the overlay while promote moves the project channel.
func brainPublishCmd(e *env) *cobra.Command {
	var channel, sha string
	cmd := &cobra.Command{
		Use:   "publish --channel stable|edge --sha <commit>",
		Short: "Sync, promote an exact tested commit, and verify one project brain channel",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if channel != "stable" && channel != "edge" {
				return fmt.Errorf("--channel must be stable or edge")
			}
			if !fullGitSHA.MatchString(sha) {
				return fmt.Errorf("--sha must be an exact full 40-character commit SHA")
			}
			sha = strings.ToLower(sha)
			c, err := e.newClient()
			if err != nil {
				return err
			}
			if err := e.resolvePinnedProject(c); err != nil {
				return err
			}
			project := e.scopeProject()

			syncResp, _, err := c.BrainSync(e.ctx(), project, "")
			if err != nil {
				return err
			}
			if syncResp.Sync.ManualReconcile {
				return fmt.Errorf("brain box clone is %q and needs manual reconcile — reconcile the box clone, see `rc dev brain status`", syncResp.Sync.After.State)
			}

			promoteResp, _, err := c.BrainPromote(e.ctx(), project, client.BrainPromoteRequest{Channel: channel, SHA: sha})
			if err != nil {
				return err
			}

			statusResp, _, err := c.BrainStatus(e.ctx(), project, "")
			if err != nil {
				return err
			}
			ch := findBrainChannel(statusResp.Status.Channels, channel)
			if ch == nil {
				return fmt.Errorf("verify failed: brain status did not report channel %q", channel)
			}
			if ch.ResolvedSHA != sha || !ch.MatchesOrigin {
				return fmt.Errorf("verify failed: channel %q resolved %s (matches origin: %t), want %s", channel, dashOr(ch.ResolvedSHA), ch.MatchesOrigin, sha)
			}

			if e.jsonOut() {
				raw, err := json.Marshal(brainPublishResult{
					Project:  statusResp.Project,
					Channel:  channel,
					SHA:      sha,
					OldSHA:   promoteResp.OldSHA,
					Sync:     syncResp.Sync,
					Verified: true,
				})
				if err != nil {
					return err
				}
				return render.JSON(e.out, raw)
			}
			render.BrainStatus(e.out, statusResp)
			return nil
		},
	}
	cmd.Flags().StringVar(&channel, "channel", "", "managed channel to publish (stable or edge)")
	cmd.Flags().StringVar(&sha, "sha", "", "exact full 40-character commit SHA (must be pushed to the brain origin)")
	_ = cmd.MarkFlagRequired("channel")
	_ = cmd.MarkFlagRequired("sha")
	return cmd
}

func findBrainChannel(channels []client.BrainChannelStatus, name string) *client.BrainChannelStatus {
	for i := range channels {
		if channels[i].Channel == name {
			return &channels[i]
		}
	}
	return nil
}

func dashOr(s string) string {
	if s == "" {
		return "-"
	}
	return s
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
