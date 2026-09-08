package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseShareTarget pins the whole link vocabulary a developer might paste. The failure mode being
// guarded is a silent misparse: a token dropped from a URL turns an account-less read into a confusing
// "run `rc auth login`".
func TestParseShareTarget(t *testing.T) {
	for _, tc := range []struct {
		name      string
		arg       string
		flagToken string
		wantRun   string
		wantToken string
		wantOrig  string
		wantErr   string
	}{
		{name: "BareID", arg: "11111111-1111-1111-1111-111111111111", wantRun: "11111111-1111-1111-1111-111111111111"},
		{name: "BareIDWithFlagToken", arg: "run-1", flagToken: "shr_abc", wantRun: "run-1", wantToken: "shr_abc"},
		{
			name: "FullURL", arg: "https://app.replypen.com/runs/run-1?t=shr_abc",
			wantRun: "run-1", wantToken: "shr_abc", wantOrig: "https://app.replypen.com",
		},
		{
			// Extra query params (a UTM tail from a pasted chat message) must not disturb the token.
			name: "URLWithExtraQuery", arg: "https://app.replypen.com/runs/run-1?utm_source=slack&t=shr_abc&ref=x",
			wantRun: "run-1", wantToken: "shr_abc", wantOrig: "https://app.replypen.com",
		},
		{
			// An explicit flag is the later, more deliberate statement: it wins over the embedded token.
			name: "FlagBeatsURLToken", arg: "https://app.replypen.com/runs/run-1?t=stale", flagToken: "shr_fresh",
			wantRun: "run-1", wantToken: "shr_fresh", wantOrig: "https://app.replypen.com",
		},
		{
			// A staging/dev host in the link is authoritative — the token is only valid there.
			name: "NonProdOrigin", arg: "http://localhost:8080/runs/run-1?t=shr_abc",
			wantRun: "run-1", wantToken: "shr_abc", wantOrig: "http://localhost:8080",
		},
		{name: "SessionURLRedirectsToSession", arg: "https://app.replypen.com/s/shr_tok", wantErr: "rc run session"},
		{name: "UnknownPath", arg: "https://app.replypen.com/inbox/123", wantErr: "not a run link"},
		{name: "Empty", arg: "   ", wantErr: "expected a run id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseRunTarget(tc.arg, tc.flagToken)
			assertTarget(t, got.runID, got.token, got.origin, err, tc.wantRun, tc.wantToken, tc.wantOrig, tc.wantErr)
		})
	}
}

func TestParseSessionTarget(t *testing.T) {
	for _, tc := range []struct {
		name      string
		arg       string
		flagToken string
		wantToken string
		wantOrig  string
		wantErr   string
	}{
		{name: "ShareURL", arg: "https://app.replypen.com/s/shr_tok", wantToken: "shr_tok", wantOrig: "https://app.replypen.com"},
		{name: "ShareURLTrailingSlash", arg: "https://app.replypen.com/s/shr_tok/", wantToken: "shr_tok", wantOrig: "https://app.replypen.com"},
		{name: "BareToken", arg: "shr_tok", wantToken: "shr_tok"},
		{name: "FlagToken", arg: "https://app.replypen.com/s/stale", flagToken: "shr_fresh", wantToken: "shr_fresh", wantOrig: "https://app.replypen.com"},
		{name: "RunURLRedirectsToDebug", arg: "https://app.replypen.com/runs/run-1?t=x", wantErr: "rc run debug"},
		{name: "Garbage", arg: "https://app.replypen.com/nope", wantErr: "not a share link"},
		{name: "Empty", arg: "", wantErr: "expected a share link"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSessionTarget(tc.arg, tc.flagToken)
			assertTarget(t, "", got.token, got.origin, err, "", tc.wantToken, tc.wantOrig, tc.wantErr)
		})
	}
}

func assertTarget(t *testing.T, runID, token, origin string, err error, wantRun, wantToken, wantOrigin, wantErr string) {
	t.Helper()
	if wantErr != "" {
		if err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Fatalf("error = %v, want it to contain %q", err, wantErr)
		}
		return
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runID != wantRun || token != wantToken || origin != wantOrigin {
		t.Errorf("got run=%q token=%q origin=%q, want run=%q token=%q origin=%q",
			runID, token, origin, wantRun, wantToken, wantOrigin)
	}
}

// TestShareLinkNeedsNoProfile is the promise of the whole feature: a developer with NO login, NO stored
// token and a --profile that doesn't exist still gets the two debug artifacts. The env deliberately
// carries no tokenSource, so any fall-through to the credential ladder fails the test.
func TestShareLinkNeedsNoProfile(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	e.tokenSource = nil
	e.profile = "does-not-exist"
	t.Setenv("HOME", t.TempDir())
	outDir := t.TempDir()

	link := srv.URL + "/runs/11111111-1111-1111-1111-111111111111?t=shr_abc"
	if err := run(t, e, "run", "debug", link, "--out-dir", outDir); err != nil {
		t.Fatalf("rc run debug <link>: %v", err)
	}
	// Identical file names to the logged-in path — the artifacts are the deliverable, not the credential.
	for _, name := range []string{"11111111-coca-cola.md", "11111111-coca-cola.jsonl"} {
		if _, err := os.Stat(filepath.Join(outDir, name)); err != nil {
			t.Fatalf("expected %s: %v", name, err)
		}
	}
	if got := out.String(); !strings.Contains(got, "11111111-coca-cola.jsonl") {
		t.Errorf("stdout = %q, want the two artifact paths", got)
	}
}

// TestShareTokenFlagWithBareID pins the second entry point: the id and the token given separately, with
// the base URL coming from config/env rather than a pasted origin.
func TestShareTokenFlagWithBareID(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	e.tokenSource = nil
	outDir := t.TempDir()
	if err := run(t, e, "run", "debug", "11111111-1111-1111-1111-111111111111", "--share-token", "shr_abc", "--out-dir", outDir); err != nil {
		t.Fatalf("rc run debug --share-token: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "11111111-coca-cola.jsonl")); err != nil {
		t.Fatalf("expected the jsonl artifact: %v", err)
	}
}

// TestSharedThreadTraceRendersIdentically: a shared thread trace renders byte-for-byte like the bearer
// one (the extra share_url rides through -o json, it is not a second view).
func TestSharedThreadTraceRendersIdentically(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	e.tokenSource = nil
	if err := run(t, e, "run", "thread", srv.URL+"/runs/aaaaaaaa-1111-1111-1111-111111111111?t=shr_abc"); err != nil {
		t.Fatalf("rc run thread <link>: %v", err)
	}
	assertGolden(t, "thread_trace.golden", out.String())
}

// TestRunSessionWritesMarkdown pins the one artifact `rc run session` produces, including the per-run
// drill command that is the whole point of the runs table.
func TestRunSessionWritesMarkdown(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, errb := newTestEnv(t, srv, "table")
	e.tokenSource = nil
	outDir := t.TempDir()
	if err := run(t, e, "run", "session", srv.URL+"/s/shr_tok", "--out-dir", outDir); err != nil {
		t.Fatalf("rc run session <link>: %v", err)
	}
	path := filepath.Join(outDir, "session-session-9f8e.md")
	if got := strings.TrimSpace(out.String()); got != path {
		t.Errorf("stdout = %q, want the artifact path %q", got, path)
	}
	if !strings.Contains(errb.String(), "3 message(s) · 2 run(s)") {
		t.Errorf("stderr summary = %q", errb.String())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	assertGolden(t, "session_shared.golden", string(body))
}

// TestRunSessionJSONPassthrough: -o json hands back BOTH server bodies verbatim under named keys.
func TestRunSessionJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	e.tokenSource = nil
	if err := run(t, e, "run", "session", "shr_tok"); err != nil {
		t.Fatalf("rc run session: %v", err)
	}
	var got struct {
		Transcript json.RawMessage `json:"transcript"`
		Runs       json.RawMessage `json:"runs"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\nraw: %s", err, out.String())
	}
	assertJSONEqual(t, fixture(t, "shared_session_transcript.json"), got.Transcript)
	assertJSONEqual(t, fixture(t, "shared_session_runs.json"), got.Runs)
}

// TestShareRefusalSurfacesVerbatim: a 404 refusal is the API error the server sent, not a login prompt —
// the reader has no login to fix.
func TestShareRefusalSurfacesVerbatim(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"UnknownRun", []string{"run", "debug", srv.URL + "/runs/gone?t=shr_abc"}, "no such run"},
		{"UnknownSession", []string{"run", "session", srv.URL + "/s/expired"}, "no longer valid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _, _ := newTestEnv(t, srv, "table")
			e.tokenSource = nil
			err := run(t, e, append(tc.args, "--out-dir", t.TempDir())...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}
