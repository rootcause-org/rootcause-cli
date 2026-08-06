package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

func newMirrorCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "mirror", Short: "Refresh project source mirrors"}
	cmd.AddCommand(mirrorRefreshCmd(e))
	return cmd
}

func mirrorRefreshCmd(e *env) *cobra.Command {
	var repo, expectedSHA string
	cmd := &cobra.Command{
		Use:   "refresh --repo <name> --expect-sha <commit>",
		Short: "Refresh mirrors and verify one repository at an exact commit",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			repo = strings.TrimSpace(repo)
			if repo == "" {
				return fmt.Errorf("--repo is required")
			}
			if !fullGitSHA.MatchString(expectedSHA) {
				return fmt.Errorf("--expect-sha must be an exact full 40-character commit SHA")
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			if err := e.resolvePinnedProject(c); err != nil {
				return err
			}
			resp, raw, err := c.MirrorRefresh(e.ctx(), e.scopeProject(), client.MirrorRefreshRequest{
				Repo: repo, ExpectedSHA: strings.ToLower(expectedSHA),
			})
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			render.MirrorRefresh(e.out, resp)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "", "configured mirror name")
	cmd.Flags().StringVar(&expectedSHA, "expect-sha", "", "exact full 40-character commit SHA expected after refresh")
	_ = cmd.MarkFlagRequired("repo")
	_ = cmd.MarkFlagRequired("expect-sha")
	return cmd
}
