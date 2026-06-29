package cli

import (
	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newGitHubCmd builds `rc github status` over GET /api/v1/github/status →
// {installed, account, install_url?}. JSON passthrough (the body is small and structured); the table
// path renders it as a key:value block so a human sees the install state at a glance.
func newGitHubCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "github", Short: "Inspect the GitHub App install for this project"}
	cmd.AddCommand(githubStatusCmd(e))
	return cmd
}

func githubStatusCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the GitHub App install status (installed/account/install_url)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			raw, err := c.GitHubStatus(e.ctx(), e.scopeProject())
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
