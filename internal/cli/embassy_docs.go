package cli

import "strings"

// embassyErrorsBase is the ONE place the integrator error index lives: every diagnostic the CLI prints
// (chat doctor, action doctor/probe/exec) links a code to its anchor there, so a moved doc is a one-line
// change and two commands can never drift to different URLs.
const embassyErrorsBase = "https://github.com/rootcause-org/rootcause-embassy/blob/main/docs/integrator/errors.md#"

// embassyDocsFor returns the docs URL for an error code (anchors are the lower-cased code).
func embassyDocsFor(code string) string {
	return embassyErrorsBase + strings.ToLower(code)
}
