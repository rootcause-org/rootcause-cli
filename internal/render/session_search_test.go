package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/digest"
)

func TestSessionSearchGolden(t *testing.T) {
	result := &digest.SessionSearchResult{
		Project: "kampadmin-support", Tenant: "heyo",
		Rows:     []client.SessionSummary{{SessionID: "session-1", CreatedAt: "2026-10-01T12:00:00Z", FirstQuestion: "Waar vind ik de factuur?"}},
		Matches:  []digest.SessionSearchMatch{{Source: "message", Snippet: "De factuur staat bij Financieel.", RunID: "run-1"}},
		Coverage: digest.SessionSearchCoverage{Reason: "result_limit", Pages: 2, Scanned: 101, Transcripts: 1, NextBefore: "session-1"},
	}
	var out bytes.Buffer
	SessionSearch(&out, result)
	assertGolden(t, "session_search.golden", out.String())
}

func TestSessionSearchDrillsPinScopeAndQuote(t *testing.T) {
	for _, tenant := range []string{"", "tenant'$(bad)"} {
		result := &digest.SessionSearchResult{Project: "project'$(bad)", Tenant: tenant,
			Rows: []client.SessionSummary{{SessionID: "session-1"}}, Matches: []digest.SessionSearchMatch{{RunID: "run-1"}}}
		var out bytes.Buffer
		SessionSearch(&out, result)
		scope := "rc --project 'project'\\''$(bad)'"
		if tenant == "" {
			scope += " --scope project"
		} else {
			scope += " --tenant 'tenant'\\''$(bad)'"
		}
		for _, command := range []string{scope + " run thread 'session-1' --transcript", scope + " run debug 'run-1'"} {
			if !strings.Contains(out.String(), command) {
				t.Fatalf("missing scope/quote-safe drill %q: %s", command, out.String())
			}
		}
	}
}
