// The `rc project env verify` view. Names only: this renderer must never be able to print a secret
// value, so it takes three lists of KEY names and nothing else.

package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// EnvDrift is the names-only drift between a local .env and the server's env.
type EnvDrift struct {
	ValueDiffers []string // present in both, value differs
	OnlyLocal    []string // present locally, absent on the server
	OnlyServer   []string // present on the server, absent locally
}

// inSync reports no drift in any of the three buckets.
func (d EnvDrift) inSync() bool {
	return len(d.ValueDiffers) == 0 && len(d.OnlyLocal) == 0 && len(d.OnlyServer) == 0
}

// EnvDiff prints the human drift report (names only).
func EnvDiff(w io.Writer, resp *client.EnvResponse, d EnvDrift) {
	if d.inSync() {
		_, _ = fmt.Fprintf(w, "in sync: %d keys match the server (%s)\n", len(resp.Keys), envScope(resp))
		return
	}
	_, _ = fmt.Fprintf(w, "DRIFT vs server (%s) — names only:\n", envScope(resp))
	if len(d.ValueDiffers) > 0 {
		_, _ = fmt.Fprintf(w, "  value differs : %s\n", strings.Join(d.ValueDiffers, ", "))
	}
	if len(d.OnlyLocal) > 0 {
		_, _ = fmt.Fprintf(w, "  only local    : %s\n", strings.Join(d.OnlyLocal, ", "))
	}
	if len(d.OnlyServer) > 0 {
		_, _ = fmt.Fprintf(w, "  only on server: %s\n", strings.Join(d.OnlyServer, ", "))
	}
}

// envScope describes the resolved scope for a status line: "project" or "project/tenant".
func envScope(resp *client.EnvResponse) string {
	if resp.Tenant != "" {
		return resp.Project + "/" + resp.Tenant
	}
	return resp.Project
}
