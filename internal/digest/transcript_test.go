package digest

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

func TestIsFormAnswer(t *testing.T) {
	for _, tc := range []struct {
		name     string
		question string
		want     bool
	}{
		{"bare form submission", `User selected: plan=Pro`, true},
		{"leading blank line", "\n  User selected: regions=eu,us", true},
		{"prose first is a real question", "kan ik nog annuleren?\nUser selected: plan=Pro", false},
		{"ordinary question", "wanneer start de inschrijving?", false},
		{"empty", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsFormAnswer(tc.question); got != tc.want {
				t.Fatalf("IsFormAnswer(%q) = %v, want %v", tc.question, got, tc.want)
			}
		})
	}
}

func TestBuildTranscriptFoldsFormAnswers(t *testing.T) {
	runs := []client.RunSummary{
		{RunID: "r3", SessionID: "sess", CreatedAt: "2026-06-22T09:33:00Z", Outcome: "answered"},
		{RunID: "r1", SessionID: "sess", CreatedAt: "2026-06-22T09:30:00Z", Outcome: "answered"},
		{RunID: "r2", SessionID: "sess", CreatedAt: "2026-06-22T09:31:00Z", Outcome: "answered"},
	}
	sources := map[string]TranscriptSource{
		"r1": {Trace: trace("r1", "wanneer start de inschrijving?", "op 1 maart", `{"claims":{"admin_id":4212}}`)},
		"r2": {Trace: trace("r2", `User selected: kamp=Zomerkamp`, "voor Zomerkamp: 1 maart", "")},
		"r3": {Err: "run r3: 403 FORBIDDEN"},
	}

	got := BuildTranscript("sess", runs, sources)

	if got.Questions != 2 || got.FormAnswers != 1 {
		t.Fatalf("counts = %d questions / %d form answers, want 2/1", got.Questions, got.FormAnswers)
	}
	if ids := []string{got.Turns[0].RunID, got.Turns[1].RunID, got.Turns[2].RunID}; ids[0] != "r1" || ids[1] != "r2" || ids[2] != "r3" {
		t.Fatalf("turn order = %v, want r1 r2 r3 (created_at ascending)", ids)
	}
	// The form answer keeps the seq of the turn it answers, so "Turn 2" is the SECOND question.
	if got.Turns[1].Seq != 1 || !got.Turns[1].IsFormAnswer {
		t.Fatalf("folded turn = seq %d form=%v, want seq 1 form=true", got.Turns[1].Seq, got.Turns[1].IsFormAnswer)
	}
	if got.Turns[2].Seq != 2 || got.Turns[2].IsFormAnswer {
		t.Fatalf("turn 3 = seq %d form=%v, want seq 2 form=false", got.Turns[2].Seq, got.Turns[2].IsFormAnswer)
	}
	if got.Turns[0].PrincipalID != "4212" {
		t.Fatalf("principal = %q, want 4212 (a numeric claim must not become Go syntax)", got.Turns[0].PrincipalID)
	}
	// A failed header is a per-turn note, never a silent empty turn.
	if !strings.Contains(got.Turns[2].Unavailable, "FORBIDDEN") || got.Turns[2].Outcome != "answered" {
		t.Fatalf("unavailable turn = %+v, want the fetch error + the index-level outcome", got.Turns[2])
	}
}

// A first turn can never be a fold: there is no preceding turn for it to answer.
func TestBuildTranscriptFirstTurnIsNeverFolded(t *testing.T) {
	runs := []client.RunSummary{{RunID: "r1", CreatedAt: "2026-06-22T09:30:00Z"}}
	got := BuildTranscript("sess", runs, map[string]TranscriptSource{
		"r1": {Trace: trace("r1", "User selected: plan=Pro", "ok", "")},
	})
	if got.Turns[0].IsFormAnswer || got.Questions != 1 {
		t.Fatalf("first turn folded: %+v (questions=%d)", got.Turns[0], got.Questions)
	}
}

func TestTranscriptTruncateAnswers(t *testing.T) {
	tr := Transcript{Turns: []TranscriptTurn{{Answer: strings.Repeat("a", 50)}, {Answer: "short"}}}
	tr.TruncateAnswers(10)
	if !tr.AnswersTruncated || !strings.HasSuffix(tr.Turns[0].Answer, "[…]") {
		t.Fatalf("long answer not clipped: %q (flag=%v)", tr.Turns[0].Answer, tr.AnswersTruncated)
	}
	if tr.Turns[1].Answer != "short" {
		t.Fatalf("short answer changed: %q", tr.Turns[1].Answer)
	}
}

func TestPrincipalIDFallsBackToExternalID(t *testing.T) {
	for _, tc := range []struct{ name, scope, want string }{
		{"claims admin id wins", `{"external_id":"ext-9","claims":{"admin_id":"42","external_id":"ext-1"}}`, "42"},
		{"claims external id", `{"external_id":"ext-9","claims":{"external_id":"parent-77"}}`, "parent-77"},
		{"scope external id", `{"external_id":"ext-9","claims":{}}`, "ext-9"},
		{"no scope", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var g *client.GuardsView
			if tc.scope != "" {
				g = &client.GuardsView{PrincipalScope: json.RawMessage(tc.scope)}
			}
			if got := PrincipalID(g); got != tc.want {
				t.Fatalf("PrincipalID = %q, want %q", got, tc.want)
			}
		})
	}
}

func trace(runID, question, draft, principalScope string) *client.FullResponse {
	h := client.RunHeader{
		RunID: runID, Question: question, Draft: draft,
		Metadata: map[string]any{"outcome": "ok", "run_url": "https://app.replypen.com/runs/" + runID},
	}
	if principalScope != "" {
		h.Guards = &client.GuardsView{PrincipalScope: json.RawMessage(principalScope)}
	}
	return &client.FullResponse{Run: h}
}
