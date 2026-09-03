package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
	cmd := &cobra.Command{Use: "brain", Short: "Inspect, publish, and manage brain repositories"}
	cmd.AddCommand(brainStatusCmd(e), brainSyncCmd(e), brainRenderCmd(e), brainPreflightCmd(e), brainPromoteCmd(e), brainPublishCmd(e), brainEditCmd(e), brainConsolidateCmd(e), newBrainDeveloperCmd(e))
	return cmd
}

func newBrainDeveloperCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "developer", Short: "Manage tenant brain developer access"}
	cmd.AddCommand(brainDeveloperInviteCmd(e))
	return cmd
}

func brainDeveloperInviteCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "invite <github-handle>",
		Short: "Invite a GitHub user to this tenant brain repository",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			project, tenant, err := e.requiredTenantBrainScope(c, "invite a brain developer")
			if err != nil {
				return err
			}
			resp, raw, err := c.InviteBrainDeveloper(e.ctx(), project, tenant, client.BrainDeveloperInvitationRequest{GitHubHandle: args[0]})
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			render.BrainDeveloperInvitation(e.out, resp)
			return nil
		},
	}
}

// requiredTenantBrainScope resolves the canonical project+tenant tree for a command that has no
// project-wide form. Explicit selectors and checkout markers win; a tenant-pinned OAuth login fills only
// missing pieces. purpose completes "tenant scope is required to …" in the fail-closed error.
func (e *env) requiredTenantBrainScope(c *client.Client, purpose string) (string, string, error) {
	project := e.scopeProject()
	if project == "" {
		project = e.resolved.Project
	}
	tenant := e.scopeTenant()
	projectRouteForced := e.scope == scopeSelectorProject
	if project == "" || tenant == "" {
		scope, err := c.Whoami(e.ctx())
		if err != nil {
			return "", "", err
		}
		if project == "" && scope.Project != nil {
			project = scope.Project.Name
			if project == "" {
				project = scope.Project.ID
			}
		}
		// `--scope project` is an explicit routing instruction. Never silently undo it from the
		// tenant bound into whoami; this command has no project-wide form and must fail closed.
		if tenant == "" && !projectRouteForced && scope.Tenant != nil {
			tenant = scope.Tenant.Slug
			if tenant == "" {
				tenant = scope.Tenant.Name
			}
		}
	}
	if project == "" {
		return "", "", &client.APIError{Status: http.StatusBadRequest, Code: "PROJECT_REQUIRED", Message: "--project <project> is required for an all-projects login"}
	}
	if tenant == "" {
		return "", "", &client.APIError{Status: http.StatusBadRequest, Code: "TENANT_REQUIRED", Message: "tenant scope is required to " + purpose + " (use --tenant or a tenant brain checkout)"}
	}
	return project, tenant, nil
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
			if err := e.resolvePinnedProject(c); err != nil {
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

// brainBootCheckError translates the server's 409 BRAIN_BOOT_CHECK_FAILED into the operator sentence:
// nothing is broken on the box, the candidate commit simply never booted and main stayed last-good.
// Returned as a plain error (not the APIError) so the terse reason isn't all the user sees; retrying
// is pointless — the fix is a new brain commit.
func brainBootCheckError(err error) error {
	var apiErr *client.APIError
	if asAPIError(err, &apiErr) && apiErr.Code == "BRAIN_BOOT_CHECK_FAILED" {
		return fmt.Errorf("brain sync refused: boot check failed — %s; local main kept at last-good", apiErr.Message)
	}
	return err
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
			if err := e.resolvePinnedProject(c); err != nil {
				return err
			}
			resp, raw, err := c.BrainSync(e.ctx(), e.scopeProject(), e.scopeTenant())
			if err != nil {
				return brainBootCheckError(err)
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

// brainRenderCmd shows what ONE tenant actually gets: the server compiles the projection ({{ }}
// placeholders + rc:branch regions) in memory and returns the files verbatim. preflight answers
// pass/fail for a whole channel; render is the eyeball. Nothing is written to the brain cache.
func brainRenderCmd(e *env) *cobra.Command {
	var channel, sha string
	var paths []string
	var all bool
	cmd := &cobra.Command{
		Use:   "render --tenant <slug> [--path AGENTS.md] [--all] [--sha <commit> | --channel stable|edge]",
		Short: "Show a tenant's compiled brain projection",
		Long: "Show a tenant's compiled brain projection.\n\n" +
			"Artifacts are keyed by TENANT, not by sha: every run overwrites .rootcause/output/brain-render-<tenant>/. " +
			"To compare two shas/channels, move the first render aside before the second.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if sha != "" && channel != "" {
				return fmt.Errorf("--sha and --channel are mutually exclusive")
			}
			if sha != "" && !fullGitSHA.MatchString(sha) {
				return fmt.Errorf("--sha must be an exact full 40-character commit SHA")
			}
			if channel != "" && channel != "stable" && channel != "edge" {
				return fmt.Errorf("--channel must be stable or edge")
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			project, tenant, err := e.requiredTenantBrainScope(c, "render a brain projection")
			if err != nil {
				return err
			}
			req := client.BrainRenderRequest{Tenant: tenant, SHA: strings.ToLower(sha), Channel: channel, All: all}
			if !all {
				req.Paths = paths
			}
			resp, raw, err := c.BrainRender(e.ctx(), project, req)
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return e.renderJSON("brain-render-"+tenant, raw)
			}
			var out bytes.Buffer
			render.BrainRender(&out, resp)
			return e.renderBytes("brain-render-"+tenant, "render.txt", out.Bytes(), "text")
		},
	}
	cmd.Flags().StringArrayVar(&paths, "path", []string{"AGENTS.md"}, "brain-relative path or glob to render (repeatable)")
	cmd.Flags().BoolVar(&all, "all", false, "render every templated file (ignores --path)")
	cmd.Flags().StringVar(&sha, "sha", "", "exact full 40-character commit SHA to compile (default: the tenant's resolved channel)")
	cmd.Flags().StringVar(&channel, "channel", "", "managed channel to compile (stable or edge)")
	return cmd
}

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

// brainPreflightCmd dry-runs the promote gate: the server compiles the candidate for every tenant pinned
// to the channel and reports who would degrade or break, without moving anything. Same flags as promote
// (and the same project-only scope) so an operator can preflight, read, then promote the identical SHA.
// Exits non-zero on a refusing verdict, so a script can gate on it without parsing.
func brainPreflightCmd(e *env) *cobra.Command {
	var channel, sha string
	cmd := &cobra.Command{
		Use:   "preflight --sha <commit> [--channel stable|edge]",
		Short: "Dry-run a channel promotion: which tenants would the candidate commit break?",
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
			resp, raw, err := c.BrainPreflight(e.ctx(), e.scopeProject(), client.BrainPreflightRequest{Channel: channel, SHA: strings.ToLower(sha)})
			if err != nil {
				return err
			}
			if e.jsonOut() {
				if err := render.JSON(e.out, raw); err != nil {
					return err
				}
			} else {
				render.BrainPreflight(e.out, resp)
			}
			if !resp.Canary.OK {
				return fmt.Errorf("preflight failed: promoting %s to %s would break tenants", sha[:12], channel)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&channel, "channel", "stable", "managed channel the candidate would move (stable or edge)")
	cmd.Flags().StringVar(&sha, "sha", "", "exact full 40-character commit SHA")
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
				return brainBootCheckError(err)
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
			if err := e.resolvePinnedProject(c); err != nil {
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
			if err := e.resolvePinnedProject(c); err != nil {
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
