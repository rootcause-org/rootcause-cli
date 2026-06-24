package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	if !strings.Contains(got, "missing index") {
		t.Errorf("expected the answer body in the summary, got:\n%s", got)
	}
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
