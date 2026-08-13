package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/config"
	"github.com/rootcause-org/rootcause-cli/internal/contextexport"
)

// newContextExportCmd builds `rc dev context-export` — the one command that shells OUT of the CLI: the
// renderer lives in the host repo because it reuses the production assembly packages, so rc locates a
// host checkout and runs it there. Fully local: no token, no API call (hence no newClient).
func newContextExportCmd(e *env) *cobra.Command {
	var opts contextexport.Options
	cmd := &cobra.Command{
		Use:   "context-export",
		Short: "Render a project's full opening context offline (needs a rootcause host checkout)",
		Long: "Render, offline, the complete opening context each model receives across a run's three model " +
			"steps — triage, the grounding pre-step, the main loop — as one markdown file per project, with " +
			"per-section sources and token estimates.\n\n" +
			"It runs the host repo's renderer (`go run ./cmd/context-export`), so it needs a local rootcause " +
			"host checkout: --host-repo, RC_HOST_REPO, or a sibling directory of this one.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			opts.Cwd = cwd
			opts.HostRepoEnv = os.Getenv("RC_HOST_REPO")
			brain, err := config.DiscoverBrain(cwd)
			if err != nil {
				return err
			}
			if brain != nil {
				opts.Project = brain.Project
				opts.BrainDir = brain.Dir
			}
			plan, err := contextexport.Resolve(opts)
			if err != nil {
				return err
			}
			return runContextExport(e, plan)
		},
	}
	cmd.Flags().StringVar(&opts.HostRepo, "host-repo", "", "path to a rootcause host checkout (default: RC_HOST_REPO, else a sibling directory)")
	cmd.Flags().StringVar(&opts.Suite, "suite", "", "grounding suite: a bare project name or a path (default: this brain's project)")
	cmd.Flags().StringVar(&opts.Out, "out", "", "output directory (default: ./.rootcause/context-export)")
	cmd.Flags().StringVar(&opts.KB, "kb", "", "override the suite's knowledge snapshot directory")
	cmd.Flags().StringVar(&opts.KBMount, "kb-mount", "", "override the suite's knowledge mount path")
	cmd.Flags().StringVar(&opts.AgentsMD, "agents-md", "", "override the suite's AGENTS.md path")
	return cmd
}

func runContextExport(e *env, plan contextexport.Plan) error {
	jsonMode := e.jsonOut()
	// The renderer's own progress must never pollute a JSON payload on stdout.
	progress := e.out
	if jsonMode {
		progress = e.err
	}
	_, _ = fmt.Fprintf(e.err, "host repo: %s · suite: %s · out: %s\n", plan.HostRepo, plan.Suite, plan.OutDir)

	cmd := exec.CommandContext(e.ctx(), "go", plan.Args...)
	cmd.Dir = plan.HostRepo
	cmd.Stdout = progress
	cmd.Stderr = e.err
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("context-export failed in %s: %w", plan.HostRepo, err)
	}

	if jsonMode {
		return writeJSON(e, map[string]string{
			"host_repo":   plan.HostRepo,
			"suite":       plan.Suite,
			"output_dir":  plan.OutDir,
			"brain_dir":   plan.BrainDir,
			"output_glob": filepath.Join(plan.OutDir, "*.md"),
		})
	}
	return nil
}
