package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/config"
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

// newProjectCmd builds the singular project-management surface. `rc projects` remains the read-only
// fleet list; singular verbs act on one active project.
func newProjectCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage the active project",
	}
	cmd.AddCommand(projectRenameCmd(e))
	return cmd
}

func projectRenameCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "rename <new-name>",
		Short: "Rename the active project slug and brain repo",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			project, err := projectForRename(e, c)
			if err != nil {
				return err
			}
			resp, raw, err := c.RenameProject(e.ctx(), project, args[0])
			if err != nil {
				return err
			}
			warnBrainMarkerRename(e, resp)
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			render.ProjectRename(e.out, resp)
			return nil
		},
	}
}

func warnBrainMarkerRename(e *env, resp *client.ProjectRenameResponse) {
	if resp == nil || e.resolved.Brain == nil {
		return
	}
	if _, err := config.UpdateBrainProject(e.resolved.Brain, resp.PreviousName, resp.Name); err != nil {
		fmt.Fprintf(e.err, "warning: project renamed on server, but updating %s failed: %v\n", config.MarkerFileName, err)
	}
}

func projectForRename(e *env, c *client.Client) (string, error) {
	if e.project != "" {
		return e.project, nil
	}
	resp, err := c.Projects(e.ctx())
	if err != nil {
		return "", err
	}
	switch len(resp.Projects) {
	case 1:
		if resp.Projects[0].Name != "" {
			return resp.Projects[0].Name, nil
		}
		return resp.Projects[0].ID, nil
	case 0:
		return "", fmt.Errorf("project rename needs one visible project; this token sees none")
	default:
		return "", fmt.Errorf("project rename needs one visible project; this token sees %d (pass --project <project>)", len(resp.Projects))
	}
}
