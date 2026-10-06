package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const (
	attSession  = "5e550000-0000-4000-8000-000000000001"
	attSession2 = "5e550000-0000-4000-8000-000000000002"
	attRun      = "a1100000-0000-4000-8000-000000000001"
	attTicket   = "71c00000-0000-4000-8000-000000000001"
	attProject  = "/api/v1/projects/alpha"
)

type stubOriginal struct{ name, body string }

type stubAttachment struct {
	id, name, body string
	origin         string // "" = user
	gone           bool   // listed available, but the download answers 410 ATTACHMENT_EXPIRED
	unavailable    bool   // the list's live probe already says available:false
	headerSHA      string // overrides the X-Content-SHA256 header (tamper case)
	processedOnly  bool
	original       *stubOriginal
}

// listedSHA mirrors the server: a generated file's listed digest is of its stored (tokenized) bytes.
func listedSHA(f stubAttachment) string {
	if f.origin == "assistant" {
		return hexSHA("tokenized " + f.body)
	}
	return hexSHA(f.body)
}

func hexSHA(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

type attachmentLog struct {
	mu   sync.Mutex
	hits []string // request path + query of every attachment-plane call
}

func (l *attachmentLog) add(r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hits = append(l.hits, r.URL.RequestURI())
}

func (l *attachmentLog) downloads() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, h := range l.hits {
		if strings.Contains(h, "/attachments/") {
			out = append(out, h)
		}
	}
	return out
}

// attachmentServer serves the session-files plane over the shared stub (project "alpha" from whoami):
// per-session lists + downloads, run → session for attRun, and a resource link per ticket id.
func attachmentServer(t *testing.T, sessions map[string][]stubAttachment, links map[string][]string) (*httptest.Server, *attachmentLog) {
	t.Helper()
	base := stubServer(t)
	t.Cleanup(base.Close)
	log := &attachmentLog{}
	writeJSON := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	notFound := map[string]any{"error": map[string]any{"code": "NOT_FOUND", "message": "not found"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case p == "/api/v1/runs/"+attRun:
			requireAuth(t, r)
			writeJSON(w, http.StatusOK, map[string]any{"run_id": attRun, "session_id": attSession})
		case strings.HasPrefix(p, attProject+"/resources/support_ticket/"):
			requireAuth(t, r)
			log.add(r)
			id := strings.TrimSuffix(strings.TrimPrefix(p, attProject+"/resources/support_ticket/"), "/sessions")
			rows := []map[string]any{}
			for _, sid := range links[id] {
				rows = append(rows, map[string]any{"session_id": sid, "surface": "embed", "linked_at": "2026-10-02T09:00:00Z"})
			}
			writeJSON(w, http.StatusOK, map[string]any{"sessions": rows})
		case strings.HasPrefix(p, attProject+"/sessions/"):
			requireAuth(t, r)
			log.add(r)
			rest := strings.Split(strings.TrimPrefix(p, attProject+"/sessions/"), "/")
			files, ok := sessions[rest[0]]
			if !ok {
				writeJSON(w, http.StatusNotFound, notFound)
				return
			}
			if len(rest) == 2 {
				rows := make([]map[string]any, 0, len(files))
				for _, f := range files {
					origin := f.origin
					if origin == "" {
						origin = "user"
					}
					row := map[string]any{
						"attachment_id": f.id, "message_id": "m-" + f.id, "origin": origin, "filename": f.name,
						"mime_type": "application/octet-stream", "size_bytes": len(f.body), "sha256": listedSHA(f),
						"created_at": "2026-10-01T10:00:00Z", "available": !f.unavailable, "processed_only": f.processedOnly,
					}
					if o := f.original; o != nil {
						row["original"] = map[string]any{"filename": o.name, "size_bytes": len(o.body), "sha256": hexSHA(o.body), "available": true}
					}
					rows = append(rows, row)
				}
				writeJSON(w, http.StatusOK, map[string]any{"session_id": rest[0], "surface": "embed", "attachments": rows})
				return
			}
			for _, f := range files {
				if len(rest) != 3 || f.id != rest[2] {
					continue
				}
				if f.gone {
					writeJSON(w, http.StatusGone, map[string]any{"error": map[string]any{"code": "ATTACHMENT_EXPIRED", "message": "blob expired"}})
					return
				}
				body := f.body
				if r.URL.Query().Get("variant") == "original" {
					body = f.original.body
				}
				sha := hexSHA(body)
				if f.headerSHA != "" {
					sha = f.headerSHA
				}
				if f.origin != "assistant" { // generated files are detokenized on the way out: no digest header
					w.Header().Set("X-Content-SHA256", sha)
				}
				w.Header().Set("Content-Length", fmt.Sprint(len(body)))
				_, _ = w.Write([]byte(body))
				return
			}
			writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": "UNKNOWN_ATTACHMENT", "message": "unknown"}})
		default:
			base.Config.Handler.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, log
}

func TestRunAttachmentsList(t *testing.T) {
	srv, log := attachmentServer(t, map[string][]stubAttachment{attSession: {
		{id: "a7000000-0000-4000-8000-000000000001", name: "screen.mp4", body: strings.Repeat("x", 3<<20)},
		{id: "a7000000-0000-4000-8000-000000000002", name: "chart.png", body: "png", origin: "assistant"},
		{id: "a7000000-0000-4000-8000-000000000003", name: "old.jpg", body: "jpg", processedOnly: true},
		{id: "a7000000-0000-4000-8000-000000000004", name: "photo.jpg", body: "jpg", original: &stubOriginal{"photo.heic", "heic!"}},
	}}, nil)
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "run", "attachments", attSession); err != nil {
		t.Fatalf("rc run attachments: %v", err)
	}
	for _, want := range []string{"Session " + attSession + " (embed)", "screen.mp4", "3.0 MiB", "assistant",
		"processed (1536px JPEG)", "original photo.heic 5 B", "rc run attachments " + attSession + " --download"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("list output lacks %q:\n%s", want, out.String())
		}
	}

	// A run id falls back to its session after the session lookup 404s; JSON is the body verbatim.
	e, out, _ = newTestEnv(t, srv, "json")
	if err := run(t, e, "run", "attachments", attRun); err != nil {
		t.Fatalf("rc run attachments <run>: %v", err)
	}
	var doc struct {
		SessionID   string           `json:"session_id"`
		Attachments []map[string]any `json:"attachments"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil || doc.SessionID != attSession || len(doc.Attachments) != 4 {
		t.Fatalf("json passthrough = %v %+v\n%s", err, doc, out.String())
	}
	if len(log.downloads()) != 0 {
		t.Fatalf("listing downloaded %v", log.downloads())
	}
	if err := run(t, e, "run", "attachments", "not-an-id"); err == nil {
		t.Fatal("a non-UUID argument must be rejected locally")
	}
}

// TestRunAttachmentsDownloadCollisionAndPresent: a same-named file with other bytes gets the id-suffixed
// name; an identical file already on disk is present without a transfer; assistant files are skipped by
// default; an image downloads its original.
func TestRunAttachmentsDownloadCollisionAndPresent(t *testing.T) {
	report := stubAttachment{id: "b8000000-0000-4000-8000-000000000001", name: "../report.pdf", body: "the real report"}
	notes := stubAttachment{id: "b8000000-0000-4000-8000-000000000002", name: "notes.txt", body: "same notes"}
	chart := stubAttachment{id: "b8000000-0000-4000-8000-000000000003", name: "chart.png", body: "png", origin: "assistant"}
	photo := stubAttachment{id: "b8000000-0000-4000-8000-000000000004", name: "photo.jpg", body: "jpg", original: &stubOriginal{"photo.heic", "heic!"}}
	srv, log := attachmentServer(t, map[string][]stubAttachment{attSession: {report, notes, chart, photo}}, nil)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "report.pdf"), []byte("someone else's report"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte(notes.body), 0o600); err != nil {
		t.Fatal(err)
	}
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "run", "attachments", attSession, "--download", "--dir", dir); err != nil {
		t.Fatalf("download: %v\n%s", err, out.String())
	}
	var m struct {
		Files []struct {
			Path, Status, SHA256, Variant string
			Verified                      bool
			Bytes                         int64
		} `json:"files"`
	}
	if err := json.Unmarshal(out.Bytes(), &m); err != nil || len(m.Files) != 3 {
		t.Fatalf("manifest (want 3 user files): %v\n%s", err, out.String())
	}
	wantReport := filepath.Join(dir, "report-b8000000.pdf")
	if f := m.Files[0]; f.Status != "downloaded" || f.Path != wantReport || !f.Verified || f.SHA256 != hexSHA(report.body) || f.Bytes != int64(len(report.body)) {
		t.Fatalf("report entry = %+v", f)
	}
	if got, err := os.ReadFile(wantReport); err != nil || string(got) != report.body {
		t.Fatalf("report bytes = %q, %v", got, err)
	}
	if info, _ := os.Stat(wantReport); info.Mode().Perm() != 0o600 {
		t.Fatalf("report mode = %v, want 0600", info.Mode().Perm())
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "report.pdf")); string(got) != "someone else's report" {
		t.Fatal("the colliding existing file was overwritten")
	}
	if f := m.Files[1]; f.Status != "present" || f.Path != filepath.Join(dir, "notes.txt") || !f.Verified {
		t.Fatalf("notes entry = %+v", f)
	}
	if f := m.Files[2]; f.Status != "downloaded" || f.Variant != "original" || f.Path != filepath.Join(dir, "photo.heic") {
		t.Fatalf("photo entry = %+v", f)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "photo.heic")); string(got) != "heic!" {
		t.Fatalf("photo bytes = %q, want the original", got)
	}
	got := log.downloads()
	if len(got) != 2 || !strings.HasSuffix(got[0], report.id) || !strings.HasSuffix(got[1], photo.id+"?variant=original") {
		t.Fatalf("downloads = %v, want report + photo original only", got)
	}

	// --processed fetches the stored JPEG; --include-generated adds the assistant file.
	dir2 := t.TempDir()
	e, _, _ = newTestEnv(t, srv, "json")
	if err := run(t, e, "run", "attachments", attSession, "--id", photo.id, "--id", chart.id, "--processed", "--dir", dir2); err != nil {
		t.Fatalf("download --processed: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir2, "photo.jpg")); string(b) != "jpg" {
		t.Fatalf("processed photo = %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir2, "chart.png")); err != nil {
		t.Fatalf("--id must download an assistant file too: %v", err)
	}
}

// TestRunAttachmentsExpiredAndTampered: a 410 and an available:false file are expired (the latter never
// requested), a digest mismatch is failed (temp removed, nothing installed), the rest still lands, and
// the command exits non-zero.
func TestRunAttachmentsExpiredAndTampered(t *testing.T) {
	gone := stubAttachment{id: "c9000000-0000-4000-8000-000000000001", name: "old.mp4", body: "old", gone: true}
	bad := stubAttachment{id: "c9000000-0000-4000-8000-000000000002", name: "bad.bin", body: "bad", headerSHA: hexSHA("other")}
	ok := stubAttachment{id: "c9000000-0000-4000-8000-000000000003", name: "ok.txt", body: "ok"}
	probe := stubAttachment{id: "c9000000-0000-4000-8000-000000000004", name: "aged.pdf", body: "aged", unavailable: true}
	srv, log := attachmentServer(t, map[string][]stubAttachment{attSession: {gone, bad, ok, probe}}, nil)
	dir := t.TempDir()
	e, out, errb := newTestEnv(t, srv, "table")
	err := run(t, e, "run", "attachments", attSession, "--download", "--dir", dir)
	var ce *commandError
	if err == nil || exitCodeFor(err) == exitOK || !errors.As(err, &ce) || !ce.silent {
		t.Fatalf("want a silent non-zero commandError after the manifest, got %v", err)
	}
	body := out.String()
	for _, want := range []string{"expired", attachmentExpiredMessage, "failed", "sha256 mismatch", "downloaded"} {
		if !strings.Contains(body, want) {
			t.Fatalf("manifest lacks %q:\n%s", want, body)
		}
	}
	if !strings.Contains(errb.String(), "3 of 4 files not downloaded") {
		t.Fatalf("stderr = %q", errb.String())
	}
	for _, d := range log.downloads() {
		if strings.HasSuffix(d, probe.id) {
			t.Fatal("an available:false file must not be requested")
		}
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, en := range entries {
		names = append(names, en.Name())
	}
	if len(names) != 1 || names[0] != "ok.txt" {
		t.Fatalf("dir holds %v, want only ok.txt (no temp, no tampered file)", names)
	}
}

func TestRunAttachmentsByResource(t *testing.T) {
	one := []stubAttachment{{id: "e1000000-0000-4000-8000-000000000001", name: "a.txt", body: "a"}}
	two := []stubAttachment{{id: "e1000000-0000-4000-8000-000000000002", name: "b.txt", body: "b"}}
	srv, _ := attachmentServer(t, map[string][]stubAttachment{attSession: one, attSession2: two},
		map[string][]string{attTicket: {attSession}, "multi": {attSession, attSession2}})

	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "run", "attachments", "--resource", "support_ticket:"+attTicket); err != nil {
		t.Fatalf("--resource: %v", err)
	}
	if !strings.Contains(out.String(), "a.txt") {
		t.Fatalf("resource list:\n%s", out.String())
	}

	e, out, errb := newTestEnv(t, srv, "table")
	err := run(t, e, "run", "attachments", "--resource", "support_ticket:multi")
	if err == nil || exitCodeFor(err) == exitOK {
		t.Fatalf("several linked sessions must require a choice, got %v", err)
	}
	if !strings.Contains(out.String(), attSession2) || !strings.Contains(errb.String(), "--all-sessions") {
		t.Fatalf("ambiguous: stdout=%s stderr=%s", out.String(), errb.String())
	}

	e, out, _ = newTestEnv(t, srv, "table")
	if err := run(t, e, "run", "attachments", "--resource", "support_ticket:multi", "--all-sessions"); err != nil {
		t.Fatalf("--all-sessions: %v", err)
	}
	if !strings.Contains(out.String(), "a.txt") || !strings.Contains(out.String(), "b.txt") {
		t.Fatalf("all sessions:\n%s", out.String())
	}
}

func TestThreadTranscriptFilesSection(t *testing.T) {
	srv, _ := attachmentServer(t, map[string][]stubAttachment{"sess_chat42": {
		{id: "d1000000-0000-4000-8000-000000000001", name: "photo.jpg", body: "jpg"}}}, nil)
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "run", "thread", "chat-session", "--transcript"); err != nil {
		t.Fatalf("transcript: %v", err)
	}
	for _, want := range []string{"## Files", "- photo.jpg · 3 B · d1000000-0000-4000-8000-000000000001",
		"rc run attachments sess_chat42 --download"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("transcript lacks %q:\n%s", want, out.String())
		}
	}

	// The shared stub has no session-files route: a 404 drops the section without a word on stderr.
	plain := stubServer(t)
	defer plain.Close()
	e, out, errb := newTestEnv(t, plain, "table")
	if err := run(t, e, "run", "thread", "chat-session", "--transcript"); err != nil {
		t.Fatalf("transcript without files: %v", err)
	}
	if strings.Contains(out.String(), "## Files") || errb.Len() != 0 {
		t.Fatalf("404 must skip silently; Files=%v, stderr=%q", strings.Contains(out.String(), "## Files"), errb.String())
	}
}
