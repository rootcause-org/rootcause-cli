// Project handles: the `rc project list` / `rc project rename` views.
// The renderers are pure functions of the wire structs (no I/O beyond the passed writer, no clock) so
// golden tests pin them exactly. Timestamps are shown as the server sent them, keeping goldens stable.

package render

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// Projects renders the fleet handle list (`rc project list`): one row per project (name + id), name-ordered
// as the server sends them. A pure function of the wire rows so a golden pins it.
func Projects(w io.Writer, resp *client.ProjectsResponse) {
	if len(resp.Projects) == 0 {
		_, _ = fmt.Fprintln(w, "(no projects)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tID")
	for _, p := range resp.Projects {
		_, _ = fmt.Fprintf(tw, "%s\t%s\n", p.Name, p.ID)
	}
	_ = tw.Flush()
}

// ProjectRename renders the success echo from `rc project rename <new-name>`.
func ProjectRename(w io.Writer, resp *client.ProjectRenameResponse) {
	_, _ = fmt.Fprintf(w, "renamed %s -> %s (brain %s; github %s; local %s)\n",
		resp.PreviousName, resp.Name, resp.BrainRepo, yesNo(resp.GitHubRenamed), yesNo(resp.LocalDirRenamed))
}
