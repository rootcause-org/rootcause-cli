package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestThreadTranscriptJSON pins the machine contract of `rc run thread <session> --transcript -o json`:
// the {session_id, turns[]} envelope, turns in created_at order, the folded form answer flagged (and
// carrying the seq of the turn it answers), and the per-turn feedback the thread trace attributes.
func TestThreadTranscriptJSON(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "run", "thread", "chat-session", "--transcript"); err != nil {
		t.Fatalf("rc run thread --transcript: %v", err)
	}
	var doc struct {
		SessionID string `json:"session_id"`
		Questions int    `json:"questions"`
		Forms     int    `json:"form_answers"`
		Turns     []struct {
			Seq          int    `json:"seq"`
			RunID        string `json:"run_id"`
			CreatedAt    string `json:"created_at"`
			PrincipalID  string `json:"principal_id"`
			Question     string `json:"question"`
			Answer       string `json:"answer"`
			Outcome      string `json:"outcome"`
			RunURL       string `json:"run_url"`
			IsFormAnswer bool   `json:"is_form_answer"`
			Feedback     *struct {
				Score   *int   `json:"score"`
				Comment string `json:"comment"`
			} `json:"feedback"`
		} `json:"turns"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("decode transcript: %v\n%s", err, out.String())
	}
	if doc.SessionID != "sess_chat42" || len(doc.Turns) != 3 {
		t.Fatalf("session %q with %d turns, want sess_chat42 with 3", doc.SessionID, len(doc.Turns))
	}
	if doc.Questions != 2 || doc.Forms != 1 {
		t.Fatalf("counts = %d questions / %d form answers, want 2/1", doc.Questions, doc.Forms)
	}
	first := doc.Turns[0]
	if first.RunID != "chat-turn-1" || first.Seq != 1 || first.PrincipalID != "4212" || first.Outcome != "ok" {
		t.Fatalf("first turn = %+v", first)
	}
	if first.Feedback == nil || first.Feedback.Score == nil || *first.Feedback.Score != 5 {
		t.Fatalf("first turn feedback = %+v, want score 5", first.Feedback)
	}
	if first.RunURL == "" || first.Question == "" || first.Answer == "" {
		t.Fatalf("first turn lost a body: %+v", first)
	}
	folded := doc.Turns[1]
	if !folded.IsFormAnswer || folded.Seq != 1 {
		t.Fatalf("second turn = %+v, want the folded form answer at seq 1", folded)
	}
	if doc.Turns[2].Seq != 2 || doc.Turns[2].IsFormAnswer {
		t.Fatalf("third turn = %+v, want question 2", doc.Turns[2])
	}
	// The whole point: the transcript must not carry the per-turn prompt bulk.
	if body := out.String(); strings.Contains(body, "system_prompt") || strings.Contains(body, "tenant_settings") {
		t.Fatal("transcript JSON leaked the trace header's prompt/tenant-settings bulk")
	}
}

// TestRunTraceBriefJSON pins the `--brief` filter over the RAW bundle: the input-context fields go, the
// conversation fields stay verbatim, the events list is gone, and the drift computed from the two
// tenant-settings blobs survives them.
func TestRunTraceBriefJSON(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "run", "trace", "chat-turn-1", "--brief"); err != nil {
		t.Fatalf("rc run trace --brief: %v", err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode brief: %v\n%s", err, out.String())
	}
	if _, ok := body["events"]; ok {
		t.Error("--brief kept the events list")
	}
	var runHeader map[string]json.RawMessage
	if err := json.Unmarshal(body["run"], &runHeader); err != nil {
		t.Fatalf("decode brief run header: %v", err)
	}
	for _, gone := range briefDroppedHeaderKeys {
		if _, ok := runHeader[gone]; ok {
			t.Errorf("--brief kept %s", gone)
		}
	}
	for _, kept := range []string{"run_id", "question", "draft", "metadata", "guards", "egress", "created_at"} {
		if _, ok := runHeader[kept]; !ok {
			t.Errorf("--brief dropped %s", kept)
		}
	}
	if _, ok := runHeader["tenant_settings_drift"]; !ok {
		t.Error("--brief dropped the tenant-settings drift it computed before removing the blobs")
	}
}

// TestRunTraceBriefIsSmallerThanRaw is the size promise in one assertion: the reading view of a turn
// must be a fraction of its forensic bundle, or the flag does not earn its place.
func TestRunTraceBriefIsSmallerThanRaw(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()

	eRaw, rawOut, _ := newTestEnv(t, srv, "json")
	if err := run(t, eRaw, "run", "trace", "chat-turn-1", "--stream"); err != nil {
		t.Fatalf("rc run trace: %v", err)
	}
	eBrief, briefOut, _ := newTestEnv(t, srv, "json")
	if err := run(t, eBrief, "run", "trace", "chat-turn-1", "--brief"); err != nil {
		t.Fatalf("rc run trace --brief: %v", err)
	}
	if briefOut.Len() >= rawOut.Len() {
		t.Fatalf("brief (%d bytes) is not smaller than the raw bundle (%d bytes)", briefOut.Len(), rawOut.Len())
	}
}

// A share link credentials ONE run, so it can never enumerate a conversation — the refusal must be
// explicit rather than a confusing empty transcript.
func TestThreadTranscriptRejectsShareToken(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "run", "thread", "11111111-1111-1111-1111-111111111111", "--transcript", "--share-token", "tok")
	if err == nil || !strings.Contains(err.Error(), "needs a login") {
		t.Fatalf("err = %v, want a login-required refusal", err)
	}
}
