package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

func newTopLevelCommands(e *env, root *cobra.Command, version string) []*cobra.Command {
	status := newStatusCmd(e)
	status.GroupID = "start"
	ask := newAskCmd(e)
	ask.GroupID = "start"
	run := newRunCmd(e)
	run.GroupID = "start"
	project := newProjectSurfaceCmd(e, version)
	project.GroupID = "manage"
	dev := newDevCmd(e)
	dev.GroupID = "develop"
	fleet := newFleetSurfaceCmd(e)
	fleet.GroupID = "operate"
	admin := newAdminCmd(e)
	admin.GroupID = "operate"
	auth := newAuthCmd(e)
	auth.GroupID = "local"
	self := newSelfCmd(e, root, version)
	self.GroupID = "local"
	return []*cobra.Command{status, ask, run, project, dev, fleet, admin, auth, self}
}

func newProjectSurfaceCmd(e *env, version string) *cobra.Command {
	cmd := &cobra.Command{Use: "project", Short: "Manage project configuration and resources"}
	cmd.AddCommand(
		newProjectListCmd(e),
		projectRenameCmd(e),
		newProjectSettingsSurfaceCmd(e),
		newTenantCmd(e),
		newMailboxCmd(e),
		newTriageCmd(e),
		newSpamCmd(e),
		newModelKeyCmd(e),
		newConnectionCmd(e),
		newKnowledgeCmd(e, version),
		newCorpusCmd(e),
		newDatabaseCmd(e),
		newRepoCmd(e),
		newMemberCmd(e),
		newTokenCmd(e),
		newBrandingCmd(e),
		newEnvCmd(e),
		newGitHubCmd(e),
		newActionConfigCmd(e),
		newProjectEgressCmd(e),
	)
	return cmd
}

func newProjectSettingsSurfaceCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "settings", Short: "Read, change, and describe project settings"}
	runtime := &cobra.Command{Use: "runtime", Short: "Manage flat runtime settings"}
	runtime.AddCommand(newBagGetCmd(e, "/api/v1/settings"), newBagSetCmd(e, "/api/v1/settings"))
	cmd.AddCommand(runtime, newProjectHierarchySettingsCmd(e), newExplainCmd(e), newSchemaCmd(e))
	return cmd
}

func newModelKeyCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "model-key", Short: "Manage model-provider credentials"}
	cmd.AddCommand(newOpenRouterKeyCmd(e))
	return cmd
}

func newKnowledgeCmd(e *env, version string) *cobra.Command {
	cmd := &cobra.Command{Use: "knowledge", Short: "Search knowledge content and configure synchronization"}
	content := &cobra.Command{Use: "content", Short: "List, search, and export knowledge articles"}
	content.AddCommand(newKBListCmd(e), newKBSearchCmd(e, version), newKBExportCmd(e, version))
	sync := &cobra.Command{Use: "sync", Short: "Manage knowledge synchronization settings"}
	sync.AddCommand(newBagGetCmd(e, "/api/v1/kb"), newBagSetCmd(e, "/api/v1/kb"))
	cmd.AddCommand(content, sync)
	return cmd
}

func newDevCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "dev", Short: "Develop and inspect project behavior"}
	console := &cobra.Command{Use: "console", Short: "Use guarded production consoles"}
	console.AddCommand(newConsoleDatabaseCmd(e), newBashCmd(e), newActionCmd(e), newCapabilitiesCmd(e))
	learning := &cobra.Command{Use: "learning", Short: "Inspect learning and consolidation inputs"}
	learning.AddCommand(dreamEvidenceCmd(e))
	api := &cobra.Command{Use: "api", Short: "Inspect the public API contract"}
	api.AddCommand(newRoutesCmd(e), newOpenAPICmd(e))
	tools := &cobra.Command{Use: "tools", Short: "Use local provider and identifier utilities"}
	tools.AddCommand(newIDCmd(e), newProviderCmd(e))
	cmd.AddCommand(newBrainCmd(e), console, learning, api, tools)
	return cmd
}

func newFleetSurfaceCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "fleet", Short: "Operate and inspect project health"}
	cmd.AddCommand(newFleetRunsCmd(e), newHealthCmd(e), newPatternsCmd(e))
	return cmd
}

func newAuthCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "auth", Short: "Manage local authentication and inspect access"}
	cmd.AddCommand(newLoginCmd(e), newLogoutCmd(e), newAuthStatusCmd(e), newAccessCmd(e))
	return cmd
}

func newSelfCmd(e *env, root *cobra.Command, version string) *cobra.Command {
	cmd := &cobra.Command{Use: "self", Short: "Manage the rc installation and shell integration"}
	cmd.AddCommand(newSelfUpdateCmd(e, version), newSelfDoctorCmd(e, version), newCompletionCmd(root))
	return cmd
}

func newCompletionCmd(root *cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:       "completion bash|zsh|fish|powershell",
		Short:     "Generate a shell completion script",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(_ *cobra.Command, args []string) error {
			var generate func(io.Writer) error
			switch args[0] {
			case "bash":
				generate = func(w io.Writer) error { return root.GenBashCompletionV2(w, true) }
			case "zsh":
				generate = root.GenZshCompletion
			case "fish":
				generate = func(w io.Writer) error { return root.GenFishCompletion(w, true) }
			case "powershell":
				generate = root.GenPowerShellCompletionWithDesc
			default:
				return fmt.Errorf("unsupported shell %q", args[0])
			}
			return generate(root.OutOrStdout())
		},
	}
	return cmd
}
