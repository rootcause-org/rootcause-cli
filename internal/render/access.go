// `rc access`: what this token may do.
// The renderers are pure functions of the wire structs (no I/O beyond the passed writer, no clock) so
// golden tests pin them exactly. Timestamps are shown as the server sent them, keeping goldens stable.

package render

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// Access renders `rc access` — what this token may do: its scope (project/all-projects), effective
// scopes, writable settings keys, reachable resources, and console reach.
func Access(w io.Writer, a *client.Access) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if a.AllProjects {
		_, _ = fmt.Fprintln(tw, "scope:\tall projects")
	} else if a.Project != nil {
		_, _ = fmt.Fprintf(tw, "scope:\tproject %s\n", a.Project.Name)
	}
	if a.Tenant != nil {
		_, _ = fmt.Fprintf(tw, "tenant:\t%s\n", a.Tenant.Slug)
	}
	_, _ = fmt.Fprintf(tw, "scopes:\t%s\n", strings.Join(a.Scopes, ", "))
	_, _ = fmt.Fprintf(tw, "writable keys:\t%s\n", joinOrDash(a.WritableKeys))
	_, _ = fmt.Fprintf(tw, "resources:\t%s\n", joinOrDash(a.Resources))
	_, _ = fmt.Fprintf(tw, "console:\tdb=%t bash=%t action=%t\n", a.Console.DB, a.Console.Bash, a.Console.Action)
	if a.Formats.HarvestCorpus != "" {
		_, _ = fmt.Fprintf(tw, "harvest corpus:\t%s\n", a.Formats.HarvestCorpus)
	}
	_ = tw.Flush()
}
