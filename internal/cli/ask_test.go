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
// /runs/{id} until "done", then renders the run summary like `rc run show <id>`.
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
	if !strings.Contains(errb.String(), "rc run show 11111111") {
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

func TestAskRejectsUnknownScenario(t *testing.T) {
	e := &env{out: &bytes.Buffer{}, err: &bytes.Buffer{}}
	err := run(t, e, "ask", "q", "--scenario", "mcp", "--no-wait")
	if err == nil || !strings.Contains(err.Error(), `invalid --scenario "mcp" (want email or raw)`) {
		t.Fatalf("error = %v, want invalid scenario", err)
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

func TestAskAttachReadsLocalFiles(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "invoice.pdf"), []byte("%PDF-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "note.unknownext"), []byte("hello"), 0o600); err != nil {
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
	if err := run(t, e, "ask", "q", "--attach", "invoice.pdf", "--attach", filepath.Join(dir, "note.unknownext"), "--no-wait"); err != nil {
		t.Fatalf("ask --attach: %v", err)
	}
	atts, ok := got["attachments"].([]any)
	if !ok || len(atts) != 2 {
		t.Fatalf("attachments = %#v, want 2", got["attachments"])
	}
	first := atts[0].(map[string]any)
	if first["filename"] != "invoice.pdf" || first["mime_type"] != "application/pdf" || first["size_bytes"] != float64(7) || first["content_base64"] != "JVBERi0xCg==" {
		t.Fatalf("first attachment = %#v", first)
	}
	second := atts[1].(map[string]any)
	if second["filename"] != "note.unknownext" || second["mime_type"] != "text/plain; charset=utf-8" || second["content_base64"] != "aGVsbG8=" {
		t.Fatalf("second attachment = %#v", second)
	}
}

func TestAskUsesLocalTenantDefault(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, ".rootcause.toml"),
		[]byte("project = \"dentai\"\n"), 0o600); err != nil {
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

// TestAskPrincipalForwarded asserts the --principal-kind/--principal-id pair (plus optional
// --asserted-by/--assurance) rides through as a nested principal object in the POST body, verbatim.
func TestAskPrincipalForwarded(t *testing.T) {
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
	if err := run(t, e, "ask", "q",
		"--principal-kind", "kampadmin_person", "--principal-id", "p-123",
		"--asserted-by", "api", "--assurance", "bearer_operator", "--no-wait"); err != nil {
		t.Fatalf("ask --principal-*: %v", err)
	}
	p, ok := got["principal"].(map[string]any)
	if !ok {
		t.Fatalf("principal missing/not an object; body=%v", got)
	}
	if p["kind"] != "kampadmin_person" || p["external_id"] != "p-123" {
		t.Errorf("principal kind/external_id wrong: %v", p)
	}
	if p["asserted_by"] != "api" || p["assurance"] != "bearer_operator" {
		t.Errorf("principal asserted_by/assurance wrong: %v", p)
	}
}

// TestAskPrincipalMinimalPairOmitsDefaults: the bare kind+id pair sends no asserted_by/assurance keys
// (omitempty), so the server defaults them.
func TestAskPrincipalMinimalPairOmitsDefaults(t *testing.T) {
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
	if err := run(t, e, "ask", "q", "--principal-kind", "kampadmin_person", "--principal-id", "p-1", "--no-wait"); err != nil {
		t.Fatalf("ask minimal principal: %v", err)
	}
	p, ok := got["principal"].(map[string]any)
	if !ok {
		t.Fatalf("principal missing; body=%v", got)
	}
	if _, ok := p["asserted_by"]; ok {
		t.Errorf("asserted_by should be omitted, got %v", p)
	}
	if _, ok := p["assurance"]; ok {
		t.Errorf("assurance should be omitted, got %v", p)
	}
}

// TestAskNoPrincipalOmitsField: with no principal flags the POST body carries no principal at all
// (dormant default).
func TestAskNoPrincipalOmitsField(t *testing.T) {
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
	if err := run(t, e, "ask", "q", "--no-wait"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	if _, ok := got["principal"]; ok {
		t.Errorf("principal should be omitted when no flags given; body=%v", got)
	}
}

// TestAskPrincipalPairRequired: a lone half of the pair is a scoping mistake — error, don't submit.
func TestAskPrincipalPairRequired(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	cases := []struct {
		name string
		args []string
	}{
		{"kind-only", []string{"ask", "q", "--principal-kind", "kampadmin_person", "--no-wait"}},
		{"id-only", []string{"ask", "q", "--principal-id", "p-1", "--no-wait"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, _, _ := newTestEnv(t, srv, "table")
			err := run(t, e, tc.args...)
			if err == nil {
				t.Fatal("expected pair-required error, got nil")
			}
			if !strings.Contains(err.Error(), "must be provided together") {
				t.Errorf("expected pair-required error, got: %v", err)
			}
		})
	}
}

// TestAskAssertedByRequiresPair: --asserted-by/--assurance are meaningless without the identity pair.
func TestAskAssertedByRequiresPair(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	for _, flag := range [][]string{{"--asserted-by", "api"}, {"--assurance", "bearer_operator"}} {
		t.Run(flag[0], func(t *testing.T) {
			e, _, _ := newTestEnv(t, srv, "table")
			err := run(t, e, append([]string{"ask", "q"}, append(flag, "--no-wait")...)...)
			if err == nil {
				t.Fatal("expected require-pair error, got nil")
			}
			if !strings.Contains(err.Error(), "require --principal-kind and --principal-id") {
				t.Errorf("expected require-pair error, got: %v", err)
			}
		})
	}
}

// TestAskPrincipalNeverFallsBackToLegacy is the security guard: a principal-bearing submit that gets a
// BAD_BODY must surface the error, NEVER retry the bare {prompt,tenant} legacy body — a silently dropped
// principal is a silent under-scope.
func TestAskPrincipalNeverFallsBackToLegacy(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"BAD_BODY","message":"malformed request body"}}`))
	}))
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "json")
	err := run(t, e, "ask", "q", "--principal-kind", "kampadmin_person", "--principal-id", "p-1", "--no-wait")
	if err == nil {
		t.Fatal("expected BAD_BODY error, got nil")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (no legacy retry for a principal-bearing request)", attempts)
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
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"projects":[{"id":"11111111-1111-1111-1111-111111111111","name":"dentai"}]}`))
	})
	mux.HandleFunc("POST /api/v1/runs", func(w http.ResponseWriter, r *http.Request) {
		gotProject = r.URL.Query().Get("project")
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(r.Body)
		gotBody = buf.String()
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"run_id":"r1","status":"done","status_url":"/api/v1/runs/r1","poll_after_ms":1}`))
	})
	srv := httptest.NewServer(mux)
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
