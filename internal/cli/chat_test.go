package cli

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

func TestChatAPIErrorPrintsSharedDiagnosticLine(t *testing.T) {
	var out strings.Builder
	printError(&out, &client.APIError{Code: "ORIGIN_NOT_ALLOWED", Message: "origin rejected", Hint: "Add this exact origin.", Docs: errorDocsBase + "origin_not_allowed"})
	want := "[ReplyPen] ORIGIN_NOT_ALLOWED: Add this exact origin. — " + errorDocsBase + "origin_not_allowed\n"
	if out.String() != want {
		t.Fatalf("stderr = %q, want %q", out.String(), want)
	}
}

func TestChatSecretRotatePrintsSecretOnce(t *testing.T) {
	mux := http.NewServeMux()
	chatTestProjects(mux)
	mux.HandleFunc("POST /api/v1/projects/alpha/chat/secret/rotate", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		_, _ = w.Write([]byte(`{"secret":"chat-once","source":"dedicated"}`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { t.Fatalf("unexpected %s %s", r.Method, r.URL.String()) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "--project", "alpha", "project", "chat", "secret", "rotate"); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "chat-once\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestChatBriefPrintsServerMarkdownAndCarriesTenant(t *testing.T) {
	mux := http.NewServeMux()
	chatTestProjects(mux)
	mux.HandleFunc("GET /api/v1/projects/alpha/chat/brief", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("tenant"); got != "north" {
			t.Fatalf("tenant = %q", got)
		}
		if got := r.URL.Query().Get("target"); got != "page" {
			t.Fatalf("target = %q", got)
		}
		_, _ = w.Write([]byte("# Brief\n\n```yaml\nreplypen_brief: v1\n```\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "--project", "alpha", "--tenant", "north", "project", "chat", "brief", "--target", "page"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "replypen_brief: v1") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestPrincipalsSetAcceptsYAML(t *testing.T) {
	mux := http.NewServeMux()
	chatTestProjects(mux)
	mux.HandleFunc("PATCH /api/v1/projects/alpha/principals", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		kinds, ok := body["kinds"].(map[string]any)
		if !ok || kinds["app_user"] == nil {
			t.Fatalf("manifest = %#v", body)
		}
		_ = json.NewEncoder(w).Encode(body)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { t.Fatalf("unexpected %s %s", r.Method, r.URL.String()) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "principals.yaml")
	if err := os.WriteFile(path, []byte("kinds:\n  app_user:\n    claims:\n      user_id:\n        type: text\n        value: ':external_id'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "--project", "alpha", "project", "principals", "set", path); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "app_user") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestPrincipalsResolvePrintsExternalID(t *testing.T) {
	mux := http.NewServeMux()
	chatTestProjects(mux)
	mux.HandleFunc("POST /api/v1/projects/alpha/principals/resolve", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["kind"] != "app_user" || body["email"] != "person@example.com" || body["tenant"] != "north" {
			t.Fatalf("resolve body = %#v", body)
		}
		_, _ = w.Write([]byte(`{"kind":"app_user","external_id":"user-42","source":"email_lookup"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "--project", "alpha", "--tenant", "north", "project", "principals", "resolve", "--kind", "app_user", "--email", "person@example.com"); err != nil {
		t.Fatal(err)
	}
	if out.String() != "Scope: alpha / north\nuser-42\n" {
		t.Fatalf("output = %q", out.String())
	}
}

func TestChatDoctorBundleIsRedacted(t *testing.T) {
	mux := http.NewServeMux()
	chatTestProjects(mux)
	mux.HandleFunc("GET /api/v1/projects/alpha/chat", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"chat_enabled":{"effective":true},"chat_origins":{"effective":["https://app.example"]}}`))
	})
	mux.HandleFunc("GET /api/v1/projects/alpha/principals", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"kinds":{"app_user":{"claims":{"user_id":{"sql":"SELECT secret_schema.users"}}}},"email_lookup":{"sql":"SELECT internal_id"}}`))
	})
	mux.HandleFunc("GET /api/v1/projects/alpha/chat/secret", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"present":true,"source":"dedicated"}`))
	})
	mux.HandleFunc("GET /api/v1/branding", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"name":{"effective":"Example"}}`))
	})
	mux.HandleFunc("GET /api/v1/projects/alpha/chat/rejects", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"rejects":[{"code":"BAD_TOKEN","origin":"https://app.example","ip_prefix":"192.0.2.0/24","session_id":"11111111-1111-1111-1111-111111111111","timestamp":"2026-09-01T00:00:00Z"}]}`))
	})
	mux.HandleFunc("GET /chat/widget/v1/loader.js", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("/* loader */")) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { t.Fatalf("unexpected %s %s", r.Method, r.URL.String()) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "--project", "alpha", "project", "chat", "doctor", "--origin", "https://app.example", "--principal-kind", "app_user", "--bundle"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(out.String()), "secret\"") && strings.Contains(out.String(), "chat-once") {
		t.Fatal("bundle leaked secret")
	}
	for _, forbidden := range []string{"secret_schema", "internal_id", "192.0.2.0/24", "11111111-1111-1111-1111-111111111111"} {
		if strings.Contains(out.String(), forbidden) {
			t.Fatalf("bundle leaked %q: %s", forbidden, out.String())
		}
	}
	var bundle map[string]any
	if err := json.Unmarshal(out.Bytes(), &bundle); err != nil {
		t.Fatal(err)
	}
	if bundle["project"] != "alpha" {
		t.Fatalf("bundle = %#v", bundle)
	}
	findings, _ := bundle["findings"].([]any)
	first, _ := findings[0].(map[string]any)
	if first["code"] != "OK" || first["check"] != "CHAT_ENABLED" {
		t.Fatalf("success finding lost check identity: %#v", first)
	}
}

func TestChatSendPrintsSSE(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"alpha","origin":"https://app.example"}`))
	token := "e30." + payload + ".sig"
	mux := http.NewServeMux()
	mux.HandleFunc("POST /chat/v1/session", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("X-RC-Embed-Origin") != "https://app.example" {
			t.Fatalf("bad embed headers")
		}
		_, _ = w.Write([]byte(`{"session_id":"11111111-1111-1111-1111-111111111111"}`))
	})
	mux.HandleFunc("POST /chat/v1/message", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"start\",\"messageId\":\"run-123\"}\n\ndata: {\"type\":\"text-delta\"}\n\ndata: [DONE]\n\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "chat", "send", "hello", "--token", token); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "[DONE]") {
		t.Fatalf("output = %q", out.String())
	}
	if !strings.HasSuffix(out.String(), "run_id: run-123\n") {
		t.Fatalf("missing final run ID: %q", out.String())
	}
}

func TestChatSendAnswersLatestQuestionInExistingSession(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"alpha","origin":"https://app.example"}`))
	token := "e30." + payload + ".sig"
	const sessionID = "11111111-1111-1111-1111-111111111111"
	mux := http.NewServeMux()
	mux.HandleFunc("GET /chat/v1/session/"+sessionID, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"messages":[{"role":"assistant","parts":[{"type":"data-questions","data":{"question_set_id":"qs-1","questions":[{"id":"area","kind":"single_select"},{"id":"detail","kind":"free_text"}]}}]}]}`))
	})
	mux.HandleFunc("POST /chat/v1/message", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Message struct {
				Parts []map[string]any `json:"parts"`
			} `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Message.Parts) != 1 || body.Message.Parts[0]["type"] != "data-answers" {
			t.Fatalf("parts = %#v", body.Message.Parts)
		}
		data := body.Message.Parts[0]["data"].(map[string]any)
		answers := data["answers"].(map[string]any)
		if answers["area"].(map[string]any)["value"] != "billing" || answers["detail"].(map[string]any)["text"] != "nightly" {
			t.Fatalf("answers = %#v", answers)
		}
		_, _ = w.Write([]byte("data: {\"type\":\"start\",\"messageId\":\"run-answer\"}\n\ndata: [DONE]\n\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "chat", "send", "--token", token, "--session", sessionID, "--answer", "area=billing", "--answer", "detail=nightly"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out.String(), "run_id: run-answer\n") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestChatAnswerPartDoesNotRepeatAnsweredQuestionSet(t *testing.T) {
	raw := json.RawMessage(`{"messages":[{"parts":[{"type":"data-questions","data":{"question_set_id":"qs-1","questions":[{"id":"area","kind":"single_select"}]}}]},{"parts":[{"type":"data-answers","data":{"question_set_id":"qs-1","answers":{"area":{"value":"billing"}}}}]}]}`)
	if _, err := chatAnswerPart(raw, []string{"area=billing"}); err == nil || !strings.Contains(err.Error(), "no unanswered") {
		t.Fatalf("error = %v", err)
	}
}

func TestChatSendSSEErrorIsNonZeroAndKeepsRunID(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"alpha","origin":"https://app.example"}`))
	token := "e30." + payload + ".sig"
	mux := http.NewServeMux()
	mux.HandleFunc("POST /chat/v1/message", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("data: {\"type\":\"start\",\"messageId\":\"run-error\"}\n\ndata: {\"type\":\"error\",\"errorText\":\"grounding failed\"}\n\ndata: [DONE]\n\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "project", "chat", "send", "hello", "--token", token, "--session", "11111111-1111-1111-1111-111111111111")
	if err == nil || !strings.Contains(err.Error(), "grounding failed") {
		t.Fatalf("error = %v", err)
	}
	if !strings.HasSuffix(out.String(), "run_id: run-error\n") {
		t.Fatalf("output = %q", out.String())
	}
}

func chatTestProjects(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"projects":[{"id":"11111111-1111-1111-1111-111111111111","name":"alpha"}]}`))
	})
}

func TestChatSecretRevealPrintsRotationAttribution(t *testing.T) {
	mux := http.NewServeMux()
	chatTestProjects(mux)
	mux.HandleFunc("POST /api/v1/projects/alpha/chat/secret/reveal", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"secret":"shown-once","rotated_by":"admin@example.com","rotated_at":"2026-09-01T10:00:00Z"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, out, errOut := newTestEnv(t, srv, "table")
	if err := run(t, e, "--project", "alpha", "project", "chat", "secret", "reveal"); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "shown-once" {
		t.Fatalf("stdout = %q", out.String())
	}
	if !strings.Contains(errOut.String(), "Rotated by admin@example.com at 2026-09-01T10:00:00Z") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

// The doctor names the ONE run-time principal failure an integrator can act on: the dominant code, and
// the TOTAL count across both, so "last N turns failed principal verification" is honest.
func TestRecentPrincipalRejects(t *testing.T) {
	code, kind, n := recentPrincipalRejects([]doctorReject{
		{Code: "ORIGIN_NOT_ALLOWED"},
		{Code: "PRINCIPAL_LOOKUP_FAILED", Kind: "account"},
		{Code: "PRINCIPAL_UNVERIFIED", Kind: "app_user"},
		{Code: "PRINCIPAL_UNVERIFIED", Kind: "ignored", Stale: true},
		{Code: "PRINCIPAL_UNVERIFIED", Kind: "app_user"},
	})
	if code != "PRINCIPAL_UNVERIFIED" || kind != "app_user" || n != 2 {
		t.Fatalf("recentPrincipalRejects = %q/%q/%d, want PRINCIPAL_UNVERIFIED/app_user/2", code, kind, n)
	}
	if code, kind, n := recentPrincipalRejects([]doctorReject{{Code: "BAD_TOKEN"}}); code != "" || kind != "" || n != 0 {
		t.Fatalf("recentPrincipalRejects = %q/%q/%d, want no finding", code, kind, n)
	}
}

func TestChatDoctorSinceMarksOldRejectStale(t *testing.T) {
	now := time.Now().UTC()
	mux := http.NewServeMux()
	chatTestProjects(mux)
	mux.HandleFunc("GET /api/v1/projects/alpha/chat", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"chat_enabled":{"effective":true},"chat_origins":{"effective":["https://app.example"]}}`))
	})
	mux.HandleFunc("GET /api/v1/projects/alpha/principals", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"kinds":{}}`)) })
	mux.HandleFunc("GET /api/v1/projects/alpha/chat/secret", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"source":"dedicated"}`)) })
	mux.HandleFunc("GET /api/v1/branding", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"name":{"effective":"Example"}}`))
	})
	mux.HandleFunc("GET /api/v1/projects/alpha/chat/rejects", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"rejects":[{"code":"BAD_TOKEN","timestamp":%q},{"code":"PRINCIPAL_UNVERIFIED","timestamp":%q}]}`, now.Add(-2*time.Hour).Format(time.RFC3339), now.Format(time.RFC3339))
	})
	mux.HandleFunc("GET /chat/widget/v1/loader.js", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "--project", "alpha", "project", "chat", "doctor", "--since", "1h"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "stale   BAD_TOKEN") || !strings.Contains(out.String(), "recent  PRINCIPAL_UNVERIFIED") {
		t.Fatalf("output = %q", out.String())
	}
}

// A doctor that exits 0 when piped is a false clean bill of health: the verdict must not depend on the
// output mode, and the bundle must still be printed alongside the non-zero exit.
func TestChatDoctorJSONStillFailsWhenACheckFails(t *testing.T) {
	mux := http.NewServeMux()
	chatTestProjects(mux)
	mux.HandleFunc("GET /api/v1/projects/alpha/chat", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"chat_enabled":{"effective":false},"chat_origins":{"effective":[]}}`))
	})
	mux.HandleFunc("GET /api/v1/projects/alpha/principals", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"kinds":{}}`)) })
	mux.HandleFunc("GET /api/v1/projects/alpha/chat/secret", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"source":"dedicated"}`)) })
	mux.HandleFunc("GET /api/v1/branding", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"name":{"effective":"Example"}}`))
	})
	mux.HandleFunc("GET /api/v1/projects/alpha/chat/rejects", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"rejects":[]}`)) })
	mux.HandleFunc("GET /chat/widget/v1/loader.js", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	err := run(t, e, "--project", "alpha", "project", "chat", "doctor")
	var cmdErr *commandError
	if !errors.As(err, &cmdErr) || cmdErr.name != "CHAT_DOCTOR_FAILED" || cmdErr.code == 0 {
		t.Fatalf("err = %v, want a non-zero CHAT_DOCTOR_FAILED", err)
	}
	var bundle map[string]any
	if jsonErr := json.Unmarshal(out.Bytes(), &bundle); jsonErr != nil {
		t.Fatalf("bundle not printed: %v (%q)", jsonErr, out.String())
	}
	if bundle["project"] != "alpha" {
		t.Fatalf("bundle = %#v", bundle)
	}
}
