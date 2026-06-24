package cli

import (
	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newProjectsCmd builds `rc projects`: list the fleet handles (id + name) over GET /api/v1/projects. With
// an all-projects admin token it lists every project; with a project-pinned token it lists just that one
// (so a customer key can confirm its binding). It's the entry point for fleet review and the seed the
// `--all` fan-out lists before hitting each project's read surface. -o json is a raw passthrough.
func newProjectsCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "projects",
		Short: "List the projects this token can see (the fleet, for an all-projects token)",
		Long: "Fetch GET /api/v1/projects and list the project handles (name + id). An all-projects admin " +
			"token lists the whole fleet; a project-scoped token lists only its own project. -o json passes " +
			"the raw server rows through.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			if e.jsonOut() {
				raw, rerr := c.Raw(e.ctx(), "GET", "/api/v1/projects", nil)
				if rerr != nil {
					return rerr
				}
				return render.JSON(e.out, raw)
			}
			resp, err := c.Projects(e.ctx())
			if err != nil {
				return err
			}
			render.Projects(e.out, resp)
			return nil
		},
	}
}
