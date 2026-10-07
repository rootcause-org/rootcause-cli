package digest

import (
	"strings"
	"testing"
	"time"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

func TestSessionSearchWindowAndCandidate(t *testing.T) {
	start, end, err := SessionSearchWindow("2026-10-01", "2026-10-01")
	if err != nil || end.Sub(start) != 24*time.Hour {
		t.Fatalf("window: %v %v %v", start, end, err)
	}
	f := SessionSearchFilter{Since: start, Until: end}
	for _, tc := range []struct {
		at          string
		match, past bool
	}{
		{"2026-10-01T02:00:00+02:00", true, false},
		{"2026-10-02T02:00:00+02:00", false, false},
		{"2026-10-01T01:59:59+02:00", false, true},
	} {
		match, past, err := f.Candidate(client.SessionSummary{CreatedAt: tc.at})
		if err != nil || match != tc.match || past != tc.past {
			t.Errorf("%s = %t,%t,%v", tc.at, match, past, err)
		}
	}
	if _, _, err := f.Candidate(client.SessionSummary{CreatedAt: "bad"}); err == nil {
		t.Fatal("invalid server date accepted")
	}
	_, end, err = SessionSearchWindow("", "2026-10-01T12:00:00Z")
	if err != nil || end.Hour() != 12 {
		t.Fatalf("RFC3339 until changed: %v %v", end, err)
	}
}

func TestSessionSearchProseAndUnicodeSnippet(t *testing.T) {
	text := strings.Repeat("é", 200) + "  Café\nMATCH   text " + strings.Repeat("後", 200)
	match, ok := SessionTranscriptMatch("session", "café match text", []client.TranscriptMessage{{ID: "m", Parts: []client.TranscriptPart{{Type: "text", Text: text}}}})
	if !ok || !strings.Contains(match.Snippet, "Café MATCH text") || len([]rune(match.Snippet)) > 162 {
		t.Fatalf("match=%+v ok=%t", match, ok)
	}
	_, ok = SessionTranscriptMatch("session", "hidden", []client.TranscriptMessage{{Parts: []client.TranscriptPart{{Type: "data-card", Text: "hidden"}, {Type: "file", Filename: "hidden"}}}})
	if ok {
		t.Fatal("searched cards/files instead of prose")
	}
}
