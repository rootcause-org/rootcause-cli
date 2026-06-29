package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/dnsdetect"
)

// `provider detect` and `id` are fully LOCAL commands: no token, no base URL, no network beyond DNS
// (for `provider detect`). They are pure functions over their input, so they never call newClient.

// newProviderCmd builds `rc provider detect <domain|email>` — classify the email backend from public DNS
// and report whether rootcause can onboard it. rootcause has DNS-detectable channel adapters for google
// (Gmail/Workspace) and microsoft (M365/Graph); intercom is app-config, not DNS-detectable. The DNS
// resolver is the live one here; the logic lives in internal/dnsdetect (unit-tested offline with a fake
// resolver).
func newProviderCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "provider",
		Short: "Provider (channel) utilities",
	}
	detect := &cobra.Command{
		Use:   "detect <domain|email> [more…]",
		Short: "Detect a domain's email backend (google/microsoft/other) from DNS",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			r := dnsdetect.NewNetResolver()
			results := make([]dnsdetect.Result, 0, len(args))
			for _, target := range args {
				results = append(results, dnsdetect.Detect(e.ctx(), r, target))
			}
			if e.jsonOut() {
				// One result → a bare object; many → an array.
				if len(results) == 1 {
					return writeJSON(e, results[0])
				}
				return writeJSON(e, results)
			}
			for i, r := range results {
				if i > 0 {
					_, _ = fmt.Fprintln(e.out)
				}
				printDetect(e, r)
			}
			return nil
		},
	}
	cmd.AddCommand(detect)
	return cmd
}

func printDetect(e *env, r dnsdetect.Result) {
	mark := "NOT SUPPORTED"
	if r.Supported {
		mark = "SUPPORTED"
	}
	_, _ = fmt.Fprintf(e.out, "=== %s  (%s) ===\n", r.Input, r.Domain)
	_, _ = fmt.Fprintf(e.out, "  provider   : %s\n", r.Provider)
	_, _ = fmt.Fprintf(e.out, "  supported  : %s\n", mark)
	_, _ = fmt.Fprintf(e.out, "  confidence : %s\n", r.Confidence)
	if len(r.MX) > 0 {
		_, _ = fmt.Fprintf(e.out, "  mx         : %s\n", joinComma(r.MX))
	}
	if r.SPF != "" {
		_, _ = fmt.Fprintf(e.out, "  spf        : %s\n", r.SPF)
	}
	for _, s := range r.Signals {
		_, _ = fmt.Fprintf(e.out, "  - %s\n", s)
	}
	for _, n := range r.Notes {
		_, _ = fmt.Fprintf(e.out, "  ! %s\n", n)
	}
}

func joinComma(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ", "
		}
		out += x
	}
	return out
}
