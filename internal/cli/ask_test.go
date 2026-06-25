package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAskWaitsForTerminal: submit → poll → terminal. The default (wait) path submits, polls
// /runs/{id} until "done", then renders the run summary like `rc run <id>`.
func TestAskWaitsForTerminal(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "ask", "Do I have open invoices?"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Run:") || !strings.Contains(got, "11111111-1111-1111-1111-111111111111") {
		t.Errorf("expected run summary, got:\n%s", got)
	}
	if !strings.Contains(got, "Draft:") || !strings.Contains(got, "Note (anomaly):") {
		t.Errorf("expected email draft/note rendering, got:\n%s", got)
	}
}

func TestAskEmailTableGolden(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "ask", "email-rich"); err != nil {
		t.Fatalf("ask email: %v", err)
	}
	assertGolden(t, "ask_email.golden", out.String())
}

func TestAskRawTableGolden(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "ask", "show billing counts", "--scenario", "raw"); err != nil {
		t.Fatalf("ask raw: %v", err)
	}
	assertGolden(t, "ask_raw.golden", out.String())
}

// TestAskWaitJSON: the wait path in -o json emits the /runs/{id} body verbatim, so `jq -r .run_id`
// works on the result.
func TestAskWaitJSON(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "ask", "Do I have open invoices?"); err != nil {
		t.Fatalf("ask -o json: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(out.Bytes(), &obj); err != nil {
		t.Fatalf("output not a JSON object: %v\n%s", err, out.String())
	}
	if obj["run_id"] != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("run_id missing/wrong: %v", obj["run_id"])
	}
}

// TestAskNoWaitTable: --no-wait prints just the run_id on stdout (script-capturable), with the poll
// hint on stderr.
func TestAskNoWaitTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, errb := newTestEnv(t, srv, "table")
	if err := run(t, e, "ask", "hi", "--no-wait"); err != nil {
		t.Fatalf("ask --no-wait: %v", err)
	}
	if strings.TrimSpace(out.String()) != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("stdout should be the run_id alone, got: %q", out.String())
	}
	if !strings.Contains(errb.String(), "rc run 11111111") {
		t.Errorf("expected a poll hint on stderr, got: %q", errb.String())
	}
}

// TestAskNoWaitJSON: --no-wait -o json emits the full 202 body so `jq -r .run_id` works.
func TestAskNoWaitJSON(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "ask", "hi", "--no-wait"); err != nil {
		t.Fatalf("ask --no-wait -o json: %v", err)
	}
	var sub map[string]any
	if err := json.Unmarshal(out.Bytes(), &sub); err != nil {
		t.Fatalf("output not a JSON object: %v\n%s", err, out.String())
	}
	if sub["run_id"] != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("run_id missing/wrong: %v", sub["run_id"])
	}
}

// TestAskBrainRefForwarded asserts --brain-ref rides through as brain_ref in the POST body, verbatim.
func TestAskBrainRefForwarded(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(r.Body)
		gotBody = buf.String()
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"run_id":"r1","status":"done","status_url":"/api/v1/runs/r1","poll_after_ms":1}`))
	}))
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "ask", "q", "--brain-ref", "dev/refund-rework", "--no-wait"); err != nil {
		t.Fatalf("ask --brain-ref: %v", err)
	}
	if !strings.Contains(gotBody, `"brain_ref":"dev/refund-rework"`) {
		t.Errorf("brain_ref not forwarded; body=%s", gotBody)
	}
}

func TestAskDefaultScenarioEmailFieldsForwarded(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"run_id":"r1","status":"done","status_url":"/api/v1/runs/r1","poll_after_ms":1}`))
	}))
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "ask", "Do I have open invoices?\nPlease check.", "--no-wait"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	if got["scenario"] != "email" {
		t.Errorf("scenario = %v, want email", got["scenario"])
	}
	if got["sender"] != defaultAskFrom {
		t.Errorf("sender = %v, want %s", got["sender"], defaultAskFrom)
	}
	if got["subject"] != "Do I have open invoices?" {
		t.Errorf("subject = %v, want compact first line", got["subject"])
	}
}

func TestAskScenarioMCPAliasIsRaw(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"run_id":"r1","status":"done","status_url":"/api/v1/runs/r1","poll_after_ms":1}`))
	}))
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "ask", "q", "--scenario", "mcp", "--no-wait"); err != nil {
		t.Fatalf("ask --scenario mcp: %v", err)
	}
	if got["scenario"] != "raw" {
		t.Errorf("scenario = %v, want raw", got["scenario"])
	}
	if _, ok := got["sender"]; ok {
		t.Errorf("raw default should omit sender, got body=%v", got)
	}
	if _, ok := got["subject"]; ok {
		t.Errorf("raw default should omit subject, got body=%v", got)
	}
}

func TestAskRawSubjectDoesNotSendDefaultSender(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"run_id":"r1","status":"done","status_url":"/api/v1/runs/r1","poll_after_ms":1}`))
	}))
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "ask", "q", "--scenario", "raw", "--subject", "operator note", "--no-wait"); err != nil {
		t.Fatalf("ask raw --subject: %v", err)
	}
	if got["subject"] != "operator note" {
		t.Errorf("subject = %v", got["subject"])
	}
	if _, ok := got["sender"]; ok {
		t.Errorf("raw --subject should not send default sender, got body=%v", got)
	}
}

func TestAskFromSubjectForwarded(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"run_id":"r1","status":"done","status_url":"/api/v1/runs/r1","poll_after_ms":1}`))
	}))
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "ask", "q", "--from", "sophie@example.test", "--subject", "Invoice question", "--no-wait"); err != nil {
		t.Fatalf("ask --from --subject: %v", err)
	}
	if got["sender"] != "sophie@example.test" {
		t.Errorf("sender = %v", got["sender"])
	}
	if got["subject"] != "Invoice question" {
		t.Errorf("subject = %v", got["subject"])
	}
}

func TestAskUsesLocalTenantDefault(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, ".rootcause.toml"),
		[]byte("project = \"dentai\"\nbase_url = \"https://rc.example\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".rootcause"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".rootcause", "local.toml"),
		[]byte("tenant = \"de-kies\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"run_id":"r1","status":"done","status_url":"/api/v1/runs/r1","poll_after_ms":1}`))
	}))
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "json")
	e.profile = "" // auto mode: discover the brain and its local tenant default.
	if err := run(t, e, "ask", "q", "--no-wait"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	if got["tenant"] != "de-kies" {
		t.Fatalf("tenant = %v, want de-kies; body=%v", got["tenant"], got)
	}
}

func TestAskRetriesLegacyPromptBodyOnMalformedSubmit(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		var got map[string]any
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if attempts == 1 {
			for _, key := range []string{"scenario", "sender", "subject", "tenant"} {
				if _, ok := got[key]; !ok {
					t.Fatalf("first rich submit missing %s: %v", key, got)
				}
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"BAD_BODY","message":"malformed request body"}}`))
			return
		}
		if attempts != 2 {
			t.Fatalf("unexpected attempt %d", attempts)
		}
		if got["prompt"] != "q" || got["tenant"] != "de-kies" {
			t.Fatalf("legacy submit should preserve prompt+tenant, got %v", got)
		}
		for _, key := range []string{"scenario", "sender", "subject"} {
			if _, ok := got[key]; ok {
				t.Fatalf("legacy submit should omit %s: %v", key, got)
			}
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"run_id":"r1","status":"done","status_url":"/api/v1/runs/r1","poll_after_ms":1}`))
	}))
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "--tenant", "de-kies", "ask", "q", "--no-wait"); err != nil {
		t.Fatalf("ask legacy fallback: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if !strings.Contains(out.String(), `"run_id": "r1"`) {
		t.Fatalf("missing fallback response on stdout: %s", out.String())
	}
}

func TestAskDoesNotDropBrainRefForLegacyFallback(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"BAD_BODY","message":"malformed request body"}}`))
	}))
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "json")
	err := run(t, e, "ask", "q", "--brain-ref", "dev/refund-rework", "--no-wait")
	if err == nil {
		t.Fatal("expected BAD_BODY error, got nil")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

// TestAskEffortForwarded asserts --effort rides through as reasoning_effort in the POST body.
func TestAskEffortForwarded(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(r.Body)
		gotBody = buf.String()
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"run_id":"r1","status":"done","status_url":"/api/v1/runs/r1","poll_after_ms":1}`))
	}))
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "ask", "q", "--effort", "max", "--no-wait"); err != nil {
		t.Fatalf("ask --effort: %v", err)
	}
	if !strings.Contains(gotBody, `"reasoning_effort":"max"`) {
		t.Errorf("reasoning_effort not forwarded; body=%s", gotBody)
	}
}

func TestAskBadEffort(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	for _, bad := range []string{"high", ""} {
		t.Run("invalid "+bad, func(t *testing.T) {
			e, _, _ := newTestEnv(t, srv, "table")
			err := run(t, e, "ask", "q", "--effort", bad, "--no-wait")
			if err == nil {
				t.Fatal("expected invalid --effort error, got nil")
			}
			if !strings.Contains(err.Error(), "invalid --effort") {
				t.Errorf("expected invalid --effort error, got: %v", err)
			}
		})
	}
}

func TestAskBadScenario(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "ask", "q", "--scenario", "support", "--no-wait")
	if err == nil {
		t.Fatal("expected invalid --scenario error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid --scenario") {
		t.Errorf("expected invalid --scenario error, got: %v", err)
	}
}

// TestAskProjectForwarded asserts global --project rides as ?project= on the submit request, not in
// the JSON body. That lets an all-projects token trigger a selected project's prompt run.
func TestAskProjectForwarded(t *testing.T) {
	var gotProject, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotProject = r.URL.Query().Get("project")
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(r.Body)
		gotBody = buf.String()
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"run_id":"r1","status":"done","status_url":"/api/v1/runs/r1","poll_after_ms":1}`))
	}))
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "--project", "dentai", "ask", "q", "--no-wait"); err != nil {
		t.Fatalf("ask --project: %v", err)
	}
	if gotProject != "dentai" {
		t.Errorf("project query = %q, want dentai", gotProject)
	}
	if strings.Contains(gotBody, "project") {
		t.Errorf("project should not be sent in JSON body; body=%s", gotBody)
	}
}

// TestAskBadBrainRef: a 400 BAD_BRAIN_REF is surfaced verbatim with the push-the-ref hint.
func TestAskBadBrainRef(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, errb := newTestEnv(t, srv, "table")
	err := run(t, e, "ask", "q", "--brain-ref", "bad/ref", "--no-wait")
	if err == nil {
		t.Fatal("expected BAD_BRAIN_REF error, got nil")
	}
	printError(errb, err)
	got := errb.String()
	if !strings.Contains(got, "BAD_BRAIN_REF: brain ref rejected") {
		t.Errorf("missing verbatim code/message: %q", got)
	}
	if !strings.Contains(got, "git push origin <ref>") {
		t.Errorf("missing push hint: %q", got)
	}
}

// TestAskPollsUntilTerminal proves the poll loop iterates: the run is "running" on the first GET and
// "done" on the second, so the command must poll at least twice before rendering the summary.
func TestAskPollsUntilTerminal(t *testing.T) {
	var gets int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"run_id":"r1","status":"running","status_url":"/api/v1/runs/r1","poll_after_ms":1}`))
			return
		}
		gets++
		if gets < 2 {
			_, _ = w.Write([]byte(`{"run_id":"r1","status":"running","kind":"prompt","created_at":"2026-06-19T09:00:00Z","has_draft":false,"has_note":false,"attachments":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"run_id":"r1","status":"done","kind":"prompt","created_at":"2026-06-19T09:00:00Z","has_draft":true,"has_note":false,"attachments":[],"answer_markdown":"done now"}`))
	}))
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "ask", "q"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	if gets < 2 {
		t.Errorf("expected the poll loop to iterate (>=2 GETs), got %d", gets)
	}
	if !strings.Contains(out.String(), "done now") {
		t.Errorf("expected the terminal summary, got:\n%s", out.String())
	}
}

// TestAskTimeout: a run that never terminates trips --timeout into a clear error.
func TestAskTimeout(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "ask", "hang-please", "--timeout", "30ms")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}
