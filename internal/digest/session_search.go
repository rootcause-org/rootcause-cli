package digest

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

type SessionSearchFilter struct {
	Principal client.SessionPrincipal
	Since     time.Time
	Until     time.Time
	Contains  string
}

// SessionSearchResult retains the original session rows, including unknown server fields.
type SessionSearchResult struct {
	Project  string                  `json:"project"`
	Tenant   string                  `json:"tenant,omitempty"`
	Sessions []json.RawMessage       `json:"sessions"`
	Matches  []SessionSearchMatch    `json:"matches"`
	Coverage SessionSearchCoverage   `json:"coverage"`
	Rows     []client.SessionSummary `json:"-"`
}

type SessionSearchMatch struct {
	SessionID string `json:"session_id"`
	Source    string `json:"source"`
	Snippet   string `json:"snippet,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	Role      string `json:"role,omitempty"`
	RunID     string `json:"run_id,omitempty"`
}

type SessionSearchCoverage struct {
	Complete    bool   `json:"complete"`
	Reason      string `json:"reason"`
	Pages       int    `json:"pages"`
	Scanned     int    `json:"scanned"`
	Transcripts int    `json:"transcripts"`
	NextBefore  string `json:"next_before,omitempty"`
}

func SessionSearchWindow(since, until string) (time.Time, time.Time, error) {
	start, err := sessionSearchTime(since, false)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid --since: %w", err)
	}
	end, err := sessionSearchTime(until, true)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid --until: %w", err)
	}
	if !start.IsZero() && !end.IsZero() && !start.Before(end) {
		return time.Time{}, time.Time{}, fmt.Errorf("--since must precede --until")
	}
	return start, end, nil
}

func sessionSearchTime(value string, inclusiveDate bool) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if at, err := time.Parse("2006-01-02", value); err == nil {
		if inclusiveDate {
			at = at.AddDate(0, 0, 1)
		}
		return at, nil
	}
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("use YYYY-MM-DD (UTC) or RFC3339")
	}
	return at, nil
}

// Candidate returns pastSince separately: the index is ordered by session creation, so it can stop.
func (f SessionSearchFilter) Candidate(s client.SessionSummary) (match, pastSince bool, err error) {
	at, err := time.Parse(time.RFC3339, s.CreatedAt)
	if err != nil {
		return false, false, fmt.Errorf("session %s has invalid created_at", s.SessionID)
	}
	if !f.Since.IsZero() && at.Before(f.Since) {
		return false, true, nil
	}
	if !f.Until.IsZero() && !at.Before(f.Until) {
		return false, false, nil
	}
	return f.Principal.Kind == "" || s.Principal == f.Principal, false, nil
}

func SessionPreviewMatch(s client.SessionSummary, query string) (SessionSearchMatch, bool) {
	if query == "" {
		return SessionSearchMatch{SessionID: s.SessionID, Source: "session"}, true
	}
	for _, field := range []struct{ source, text string }{{"title", s.Title}, {"first_question", s.FirstQuestion}} {
		if snippet, ok := sessionSearchSnippet(field.text, query); ok {
			return SessionSearchMatch{SessionID: s.SessionID, Source: field.source, Snippet: snippet}, true
		}
	}
	return SessionSearchMatch{}, false
}

func SessionTranscriptMatch(sessionID, query string, messages []client.TranscriptMessage) (SessionSearchMatch, bool) {
	for _, m := range messages {
		var texts []string
		for _, p := range m.Parts {
			if p.Type == "text" {
				texts = append(texts, p.Text)
			}
		}
		if snippet, ok := sessionSearchSnippet(strings.Join(texts, "\n"), query); ok {
			return SessionSearchMatch{SessionID: sessionID, Source: "message", Snippet: snippet,
				MessageID: m.ID, Role: m.Role, RunID: m.RunID}, true
		}
	}
	return SessionSearchMatch{}, false
}

func sessionSearchSnippet(text, query string) (string, bool) {
	text = strings.Join(strings.Fields(text), " ")
	query = strings.Join(strings.Fields(query), " ")
	index := strings.Index(strings.ToLower(text), strings.ToLower(query))
	if index < 0 {
		return "", false
	}
	start := max(0, utf8.RuneCountInString(strings.ToLower(text)[:index])-50)
	runes := []rune(text)
	end := min(len(runes), start+160)
	snippet := string(runes[start:end])
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(runes) {
		snippet += "…"
	}
	return snippet, true
}
