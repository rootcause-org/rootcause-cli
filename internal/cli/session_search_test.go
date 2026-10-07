package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func searchSession(id, at, kind, principal, question string) map[string]any {
	return map[string]any{"session_id": id, "created_at": at, "first_question": question,
		"principal": map[string]string{"kind": kind, "id": principal}, "future_field": "keep-me"}
}

func TestSessionSearchPagedContentAndScope(t *testing.T) {
	var pages, transcripts int
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"projects":[{"name":"kampadmin-support"}]}`)
	})
	mux.HandleFunc("GET /api/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		pages++
		q := r.URL.Query()
		if q.Get("project") != "kampadmin-support" || q.Get("tenant") != "heyo" || q.Get("limit") != "100" || q.Has("days") {
			t.Errorf("unexpected scope/window query: %s", r.URL)
		}
		var rows []map[string]any
		switch q.Get("before") {
		case "":
			rows = []map[string]any{
				searchSession("too-new", "2026-10-02T00:00:00Z", "kampadmin_admin", "jana", "match"),
				searchSession("other-kind", "2026-10-01T23:59:59Z", "member", "jana", "match"),
				searchSession("other-person", "2026-10-01T12:00:00Z", "kampadmin_admin", "jan", "match"),
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"sessions": rows, "next_before": "other-person"})
		case "other-person":
			rows = []map[string]any{
				searchSession("preview-hit", "2026-10-01T00:00:00Z", "kampadmin_admin", "jana", "MATCH in opening"),
				searchSession("tail-hit", "2026-10-01T00:00:00Z", "kampadmin_admin", "jana", "unrelated preview"),
				searchSession("too-old", "2026-09-30T23:59:59Z", "kampadmin_admin", "jana", "match"),
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"sessions": rows, "next_before": "too-old"})
		default:
			t.Errorf("unnecessary third page: %s", q.Get("before"))
		}
	})
	mux.HandleFunc("GET /api/v1/projects/kampadmin-support/tenants/heyo/sessions/{id}/transcript", func(w http.ResponseWriter, r *http.Request) {
		transcripts++
		if r.PathValue("id") != "tail-hit" {
			t.Errorf("unrelated transcript requested: %s", r.URL)
		}
		messages := make([]map[string]any, 41)
		for i := range messages {
			text := "ordinary answer"
			if i == 40 {
				text = strings.Repeat("long answer ", 100) + "Later MATCH in answer"
			}
			messages[i] = map[string]any{"id": fmt.Sprint(i), "run_id": "run-tail", "role": "assistant",
				"parts": []map[string]string{{"type": "text", "text": text}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"messages": messages})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	err := run(t, e, "run", "sessions", "search", "--project", "kampadmin-support", "--tenant", "heyo",
		"--principal-kind", "kampadmin_admin", "--principal-id", "jana", "--since", "2026-10-01", "--until", "2026-10-01", "--contains", "match")
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Sessions []map[string]any `json:"sessions"`
		Matches  []struct {
			RunID     string `json:"run_id"`
			MessageID string `json:"message_id"`
		} `json:"matches"`
		Coverage struct {
			Complete bool   `json:"complete"`
			Reason   string `json:"reason"`
			Scanned  int    `json:"scanned"`
		} `json:"coverage"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if pages != 2 || transcripts != 1 || len(result.Sessions) != 2 || result.Sessions[0]["future_field"] != "keep-me" || result.Matches[1].RunID != "run-tail" || result.Matches[1].MessageID != "40" || !result.Coverage.Complete || result.Coverage.Reason != "since" || result.Coverage.Scanned != 6 {
		t.Fatalf("search: pages=%d transcripts=%d result=%s", pages, transcripts, out.String())
	}
}

func TestSessionSearchResultLimitResumesWithinPage(t *testing.T) {
	var cursors []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sessions" {
			_, _ = fmt.Fprint(w, `{"project":{"name":"test"}}`)
			return
		}
		before := r.URL.Query().Get("before")
		cursors = append(cursors, before)
		rows := []map[string]any{searchSession("first", "2026-10-01T00:00:00Z", "member", "1", "first"), searchSession("second", "2026-10-01T00:00:00Z", "member", "1", "second")}
		if before == "first" {
			rows = rows[1:]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sessions": rows})
	}))
	defer srv.Close()
	for _, before := range []string{"", "first"} {
		e, out, _ := newTestEnv(t, srv, "json")
		if err := run(t, e, "run", "sessions", "search", "--limit", "1", "--before", before); err != nil {
			t.Fatal(err)
		}
		if before == "" && (!strings.Contains(out.String(), `"next_before": "first"`) || !strings.Contains(out.String(), `"complete": false`)) {
			t.Fatalf("missing within-page continuation: %s", out.String())
		}
		if before == "first" && (!strings.Contains(out.String(), `"complete": true`) || !strings.Contains(out.String(), `"session_id": "second"`)) {
			t.Fatalf("bad resumed search: %s", out.String())
		}
	}
	if len(cursors) != 2 || cursors[0] != "" || cursors[1] != "first" {
		t.Fatalf("cursors = %v", cursors)
	}
}

func TestSessionSearchPageCapAndRepeatedCursor(t *testing.T) {
	for _, repeat := range []bool{false, true} {
		t.Run(fmt.Sprint(repeat), func(t *testing.T) {
			pages := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/sessions" {
					_, _ = fmt.Fprint(w, `{"project":{"name":"test"}}`)
					return
				}
				pages++
				id := fmt.Sprintf("cursor-%d", pages)
				if repeat {
					id = "same"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"sessions": []map[string]any{searchSession(id, "2026-01-01T00:00:00Z", "member", "other", "none")}, "next_before": id})
			}))
			defer srv.Close()
			e, out, _ := newTestEnv(t, srv, "json")
			err := run(t, e, "run", "sessions", "search", "--principal-kind", "member", "--principal-id", "missing")
			if repeat {
				if err == nil || !strings.Contains(err.Error(), "repeated cursor") || pages != 2 {
					t.Fatalf("pages=%d err=%v", pages, err)
				}
			} else if err != nil || pages != sessionSearchMaxPages || !strings.Contains(out.String(), `"reason": "page_limit"`) || !strings.Contains(out.String(), `"next_before": "cursor-20"`) || !strings.Contains(out.String(), `"complete": false`) {
				t.Fatalf("pages=%d err=%v result=%s", pages, err, out.String())
			}
		})
	}
}

func TestSessionSearchTranscriptFailuresAreLoud(t *testing.T) {
	for _, code := range []int{403, 404, 500} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/whoami":
					_, _ = fmt.Fprint(w, `{"project":{"name":"test"}}`)
				case "/api/v1/sessions":
					_ = json.NewEncoder(w).Encode(map[string]any{"sessions": []map[string]any{searchSession("unreadable", "2026-01-01T00:00:00Z", "member", "1", "none")}})
				default:
					w.WriteHeader(code)
					_, _ = fmt.Fprint(w, `{"error":{"code":"UNKNOWN_SESSION","message":"unavailable"}}`)
				}
			}))
			defer srv.Close()
			e, out, _ := newTestEnv(t, srv, "json")
			err := run(t, e, "run", "sessions", "search", "--contains", "find")
			if err == nil || !strings.Contains(err.Error(), "session unreadable transcript") || out.Len() != 0 {
				t.Fatalf("err=%v out=%s", err, out.String())
			}
		})
	}
}

func TestSessionSearchRejectsBeforeRequest(t *testing.T) {
	for _, flags := range [][]string{
		{"--principal-id", "1"}, {"--principal-kind", "member"}, {"--since", "yesterday"}, {"--until", "2026-02-30"},
		{"--since", "2026-02-02", "--until", "2026-02-01"}, {"--limit", "0"}, {"--contains", "  "}, {"--surface", "bad"},
	} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { calls++ }))
			defer srv.Close()
			e, _, _ := newTestEnv(t, srv, "json")
			err := run(t, e, append([]string{"run", "sessions", "search"}, flags...)...)
			if err == nil || calls != 0 {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
		})
	}
}
