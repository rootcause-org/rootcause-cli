package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/digest"
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

// TestTranscriptGolden pins the paths the CLI golden cannot reach: a clipped long conversation, a turn
// whose header could not be fetched, and a turn that placed nothing.
func TestTranscriptGolden(t *testing.T) {
	score := int16(2)
	tr := digest.Transcript{
		SessionID:        "sess_long",
		Questions:        2,
		FormAnswers:      1,
		AnswersTruncated: true,
		Turns: []digest.TranscriptTurn{
			{
				Seq: 1, RunID: "run-1", CreatedAt: "2026-06-22T09:30:00Z", PrincipalID: "4212",
				Question: "wanneer start de inschrijving?",
				Answer:   "regel een\nregel twee […]",
				Outcome:  "ok", RunURL: "https://app.replypen.com/runs/run-1",
				Feedback: &digest.TranscriptFeedback{Score: &score, Comment: "te vaag"},
			},
			{Seq: 2, RunID: "run-2", CreatedAt: "2026-06-22T09:32:00Z", Question: "en annuleren?", Outcome: "declined"},
			{Seq: 2, RunID: "run-3", CreatedAt: "2026-06-22T09:33:00Z", Unavailable: "run run-3: 403 FORBIDDEN", IsFormAnswer: true, Question: "User selected: kamp=Zomerkamp"},
		},
	}
	var out bytes.Buffer
	Transcript(&out, tr)
	assertGolden(t, "transcript.golden", out.String())
}
