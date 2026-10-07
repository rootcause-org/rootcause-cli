package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/rootcause-org/rootcause-cli/internal/digest"
)

func SessionSearch(w io.Writer, result *digest.SessionSearchResult) {
	scope := " --project " + sessionSearchQuote(result.Project)
	if result.Tenant != "" {
		scope += " --tenant " + sessionSearchQuote(result.Tenant)
	} else {
		scope += " --scope project"
	}
	if len(result.Rows) == 0 {
		_, _ = fmt.Fprintln(w, "No matching sessions in scanned rows.")
	}
	for i, row := range result.Rows {
		match := result.Matches[i]
		_, _ = fmt.Fprintf(w, "%s  %s  %s\n", row.CreatedAt, row.SessionID, sessionLabel(row))
		if match.Snippet != "" {
			_, _ = fmt.Fprintf(w, "  %s: %s\n", match.Source, match.Snippet)
		}
		_, _ = fmt.Fprintf(w, "  rc%s run thread %s --transcript\n", scope, sessionSearchQuote(row.SessionID))
		if match.RunID != "" {
			_, _ = fmt.Fprintf(w, "  rc%s run debug %s\n", scope, sessionSearchQuote(match.RunID))
		}
	}
	c := result.Coverage
	_, _ = fmt.Fprintf(w, "\nCoverage: complete=%t (%s); %d pages, %d sessions scanned, %d transcripts read.\n",
		c.Complete, c.Reason, c.Pages, c.Scanned, c.Transcripts)
	if c.NextBefore != "" {
		_, _ = fmt.Fprintf(w, "Resume with the same filters and --before %s\n", sessionSearchQuote(c.NextBefore))
	}
}

// The shared outputspill helper is intentionally outside the renderer's allowed dependencies.
func sessionSearchQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
