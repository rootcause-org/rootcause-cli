package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// Shared helpers for the observability commands (fleet / patterns / health) — the cap warning, the
// format validation, and the health verdict/exit plumbing — so the three command files stay thin
// adapters.

// warnCapped writes a one-line cap notice to stderr (never stdout, so piped JSON stays clean). The page
// loops surface this when they stop at the page cap — no silent truncation.
func warnCapped(e *env, msg string) {
	fmt.Fprintln(e.err, "warning: "+msg)
}

// errBadFormat is the client-side guard for --format (the only enum the commands validate locally; the
// server owns the rest).
func errBadFormat(got string) error {
	return fmt.Errorf("invalid --format %q: want human or agent", got)
}

// healthPath builds the /api/v1/health URL for the JSON-passthrough fetch — the same URL the typed
// Health() fetch hits, so -o json and the verdict can't diverge. project is the explicit fan-out scope
// ("" for a pinned token's own).
func healthPath(hours int, project string) string { return client.HealthPath(hours, project) }

// fanOutProjects resolves the project set the `--all` observability commands iterate over. It lists the
// fleet via GET /api/v1/projects, then enforces that `--all` is meaningful: an all-projects admin token
// sees every project (fan out), but a project-scoped token sees only its own (≤1) — for which `--all` is
// a no-op, so we fail with a friendly, actionable error rather than silently running one project. The
// returned handles carry the id each per-project call passes as ?project=.
func fanOutProjects(e *env, c *client.Client) ([]client.Project, error) {
	resp, err := c.Projects(e.ctx())
	if err != nil {
		return nil, err
	}
	if len(resp.Projects) <= 1 {
		name := "your project"
		if len(resp.Projects) == 1 {
			name = resp.Projects[0].Name
		}
		return nil, fmt.Errorf("--all needs an all-projects token, but this token is scoped to %s — "+
			"drop --all to review just that project, or `rc login` with an all-projects token", name)
	}
	return resp.Projects, nil
}

// healthVerdict is the render package's pure verdict, re-exported through the command layer so health.go
// reads it in JSON mode without importing render twice for one symbol.
func healthVerdict(h *client.HealthResponse) bool { return render.HealthVerdict(h) }

// silenceUsage marks a command so Cobra doesn't dump its help on the returned error — `rc health`'s
// non-zero exit is a verdict, not a usage mistake. (The root already sets SilenceUsage, but a child that
// returns an error after a successful render should not re-trigger help either.)
func silenceUsage(cmd *cobra.Command) { cmd.SilenceUsage = true }
