package render

import (
	"strings"
	"testing"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// The budget guardrail hint must survive the server's rewordings: the old "cost budget" (historical
// rows), the new English "processing budget", and the Dutch note phrase. The give-up class still wins
// over the budget class — the two need opposite fixes.
func TestThreadFailureHintMatchesEveryBudgetWording(t *testing.T) {
	hint := func(reason string) string {
		return threadFailureHint(&client.RunSummary{Outcome: "failed", DeclinedReason: reason})
	}
	const wantBudget = "the run hit its budget"
	for _, reason := range []string{
		"Run stopped: cost budget exceeded.",                      // historical rows
		"Could not complete within this run's processing budget.", // current English
		"Verwerkingsbudget bereikt.",                              // current Dutch
		"hit the wall-clock cap",
	} {
		if got := hint(reason); !strings.HasPrefix(got, wantBudget) {
			t.Errorf("hint(%q) = %q, want the budget hint", reason, got)
		}
	}
	// The give-up class is checked first and must not be swallowed by the budget catch-all.
	if got := hint("The model ended its turn without drafting."); !strings.Contains(got, "NOT a budget issue") {
		t.Errorf("give-up hint = %q, want the not-a-budget-issue hint", got)
	}
}
