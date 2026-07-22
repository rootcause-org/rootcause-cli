package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// Golden + contract tests for local synthesis (`rc project mailbox harvest` and
// `rc project corpus ls/get/download`) plus a pure corpus-splitter unit test. A stub server returns
// canned JSON/Markdown; tests pin rendered output or the on-disk split tree.

// --- rc project corpus ls / get ---

func TestExportListTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "corpus", "ls"); err != nil {
		t.Fatalf("project corpus ls: %v", err)
	}
	assertGolden(t, "export_ls.golden", out.String())
}

func TestExportListJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "project", "corpus", "ls"); err != nil {
		t.Fatalf("project corpus ls -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "exports.json"), out.Bytes())
}

func TestExportGetTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "corpus", "get", "eeee1111-0000-0000-0000-000000000001"); err != nil {
		t.Fatalf("project corpus get: %v", err)
	}
	assertGolden(t, "export_get.golden", out.String())
}

func TestExportGetJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "project", "corpus", "get", "eeee1111-0000-0000-0000-000000000001"); err != nil {
		t.Fatalf("project corpus get -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "export_item.json"), out.Bytes())
}

func TestExportGetSurveyShowsBlankFormat(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "corpus", "get", "survey"); err != nil {
		t.Fatalf("project corpus get survey: %v", err)
	}
	compact := strings.Join(strings.Fields(out.String()), " ")
	if !strings.Contains(compact, "kind: survey format: -") {
		t.Fatalf("survey human output missing blank format:\n%s", out.String())
	}
}

// --- rc project mailbox harvest ---

// TestMailboxHarvestAccepted: the no-wait path prints {export_id,status} and a poll hint on stderr.
func TestMailboxHarvestAccepted(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, errb := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "mailbox", "harvest", "11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatalf("project mailbox harvest: %v", err)
	}
	if !strings.Contains(out.String(), "export_id: eeee1111-0000-0000-0000-000000000001") {
		t.Errorf("missing export_id in stdout: %q", out.String())
	}
	if !strings.Contains(errb.String(), "rc project corpus get") {
		t.Errorf("missing poll hint on stderr: %q", errb.String())
	}
}

// TestMailboxHarvestConflict: a mailbox already harvesting returns 409 HARVEST_IN_PROGRESS, surfaced
// verbatim as a typed APIError.
func TestMailboxHarvestConflict(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "project", "mailbox", "harvest", "busy")
	var apiErr *client.APIError
	if !asAPIError(err, &apiErr) || apiErr.Code != "HARVEST_IN_PROGRESS" {
		t.Fatalf("expected HARVEST_IN_PROGRESS APIError, got %v", err)
	}
}

// TestMailboxHarvestWait: --wait polls the export to a terminal status (the stub flips running→done on
// the 2nd read) and prints the finished row. Also proves --clean=false / --max-threads ride in the body.
func TestMailboxHarvestWait(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "mailbox", "harvest", "wait", "--wait", "--clean=false", "--max-threads", "5"); err != nil {
		t.Fatalf("project mailbox harvest --wait: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "status:") || !strings.Contains(got, "done") {
		t.Errorf("expected finished export row with status done, got: %q", got)
	}
}

func TestMailboxIMAPEnvWritesSecretFile0600(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	tmp := t.TempDir()
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	e, out, errb := newTestEnv(t, srv, "table")
	id := "720c2741-dda4-4ecc-bcb3-5561a818e84b"
	if err := run(t, e, "project", "mailbox", "imap-env", id); err != nil {
		t.Fatalf("project mailbox imap-env: %v", err)
	}
	wantPath := filepath.Join(".rootcause", "imap", id+".env")
	if strings.TrimSpace(out.String()) != wantPath {
		t.Fatalf("stdout = %q, want path %q", out.String(), wantPath)
	}
	if strings.Contains(out.String(), "imap-secret") || strings.Contains(errb.String(), "imap-secret") ||
		strings.Contains(out.String(), "do-not-print-me") || strings.Contains(errb.String(), "do-not-print-me") {
		t.Fatalf("secret leaked in command output: stdout=%q stderr=%q", out.String(), errb.String())
	}
	body, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		"RC_MAILBOX_ID=" + id,
		"RC_IMAP_PASSWORD=imap-secret",
		"RC_SMTP_PASSWORD=smtp-secret",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("env file missing %q:\n%s", want, text)
		}
	}
	info, err := os.Stat(wantPath)
	if err != nil {
		t.Fatalf("stat env file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("env mode = %o, want 600", got)
	}
	gi, err := os.ReadFile(filepath.Join(".rootcause", ".gitignore"))
	if err != nil {
		t.Fatalf("read .rootcause/.gitignore: %v", err)
	}
	if strings.TrimSpace(string(gi)) != "*" {
		t.Fatalf(".rootcause/.gitignore = %q, want *", string(gi))
	}
}

func TestMailboxIMAPEnvJSONOmitsSecretValues(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	tmp := t.TempDir()
	outFile := filepath.Join(tmp, "imap.env")
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "project", "mailbox", "imap-env", "mb1", "--out", outFile); err != nil {
		t.Fatalf("project mailbox imap-env -o json: %v", err)
	}
	if strings.Contains(out.String(), "imap-secret") || strings.Contains(out.String(), "smtp-secret") || strings.Contains(out.String(), "do-not-print-me") {
		t.Fatalf("secret leaked in json output: %q", out.String())
	}
	if !strings.Contains(out.String(), `"path": "`+outFile+`"`) {
		t.Fatalf("json output missing path: %q", out.String())
	}
	if _, err := os.Stat(outFile); err != nil {
		t.Fatalf("env file was not written: %v", err)
	}
}

// --- rc project corpus download ---

func TestExportDownloadStdout(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "corpus", "download", "eeee1111-0000-0000-0000-000000000001"); err != nil {
		t.Fatalf("project corpus download: %v", err)
	}
	if !strings.HasPrefix(out.String(), "---\nharvest_format: v1") {
		t.Errorf("expected raw corpus on stdout, got: %q", out.String())
	}
}

func TestExportDownloadOut(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	dir := t.TempDir()
	outFile := filepath.Join(dir, "corpus.md")
	e, _, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "corpus", "download", "eeee1111-0000-0000-0000-000000000001", "--out", outFile); err != nil {
		t.Fatalf("project corpus download --out: %v", err)
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read out file: %v", err)
	}
	if !strings.Contains(string(data), "harvest_format: v1") {
		t.Errorf("out file missing corpus: %q", string(data))
	}
}

func TestExportDownloadLargeStdoutSpillsUnlessOutOrRaw(t *testing.T) {
	t.Setenv("RC_OUTPUT_SPILL_THRESHOLD", "80")
	srv := stubServer(t)
	defer srv.Close()

	outDir := t.TempDir()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "--out-dir", outDir, "--no-preview", "project", "corpus", "download", "eeee1111-0000-0000-0000-000000000001"); err != nil {
		t.Fatalf("project corpus download spill: %v", err)
	}
	if !strings.Contains(out.String(), "[output too large:") || !strings.Contains(out.String(), "body.md") {
		t.Fatalf("download did not print spill preview:\n%s", out.String())
	}
	if strings.Contains(out.String(), "Where is my invoice?") {
		t.Fatalf("download printed omitted corpus body:\n%s", out.String())
	}
	spilled := filepath.Join(outDir, "export-download-eeee1111-0000-0000-0000-000000000001", "body.md")
	b, err := os.ReadFile(spilled)
	if err != nil {
		t.Fatalf("read spilled export body: %v", err)
	}
	if !strings.Contains(string(b), "Where is my invoice?") {
		t.Fatalf("spilled export body missing full corpus")
	}

	fileDir := t.TempDir()
	outFile := filepath.Join(fileDir, "corpus.md")
	eFile, _, _ := newTestEnv(t, srv, "table")
	if err := run(t, eFile, "--out-dir", fileDir, "project", "corpus", "download", "eeee1111-0000-0000-0000-000000000001", "--out", outFile); err != nil {
		t.Fatalf("project corpus download --out: %v", err)
	}
	if _, err := os.Stat(outFile); err != nil {
		t.Fatalf("--out file missing: %v", err)
	}
	if entries, err := os.ReadDir(filepath.Join(fileDir, "export-download-eeee1111-0000-0000-0000-000000000001")); err == nil && len(entries) > 0 {
		t.Fatalf("--out wrote spill artifacts: %v", entries)
	}

	rawDir := t.TempDir()
	eRaw, rawOut, _ := newTestEnv(t, srv, "table")
	if err := run(t, eRaw, "--out-dir", rawDir, "--raw-output", "project", "corpus", "download", "eeee1111-0000-0000-0000-000000000001"); err != nil {
		t.Fatalf("project corpus download --raw-output: %v", err)
	}
	if !strings.Contains(rawOut.String(), "Where is my invoice?") || strings.Contains(rawOut.String(), "[output too large:") {
		t.Fatalf("raw export body not preserved:\n%s", rawOut.String())
	}
	if entries, err := os.ReadDir(rawDir); err != nil {
		t.Fatalf("read raw dir: %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("--raw-output wrote artifacts: %v", entries)
	}
}

// On format drift, --out + --split writes the raw bytes before parsing and reports both the rescue
// path and the server's post-consume re-download window.
func TestExportDownloadSplitFailurePreservesOut(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	dir := t.TempDir()
	rawPath := filepath.Join(dir, "raw.md")
	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "project", "corpus", "download", "unsupported", "--out", rawPath, "--split", filepath.Join(dir, "split"))
	if err == nil || !strings.Contains(err.Error(), "unsupported harvest_format") ||
		!strings.Contains(err.Error(), "raw download preserved at "+rawPath) || !strings.Contains(err.Error(), "~48h") {
		t.Fatalf("split rescue error = %v", err)
	}
	raw, readErr := os.ReadFile(rawPath)
	if readErr != nil {
		t.Fatalf("read rescued raw corpus: %v", readErr)
	}
	if !strings.Contains(string(raw), "harvest_format: v3") || !strings.Contains(string(raw), "**Occurrences:** 2") {
		t.Fatalf("rescued corpus does not match download:\n%s", raw)
	}
}

func TestExportDownloadSplitFailureOffersOutRescue(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "project", "corpus", "download", "unsupported", "--split", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "rc project corpus download unsupported --out <file>") ||
		!strings.Contains(err.Error(), "~48h") || !strings.Contains(err.Error(), "server may evict") {
		t.Fatalf("split rescue offer = %v", err)
	}
}

func TestExportDownloadMalformedKnownFormatUsesOutRescue(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	dir := t.TempDir()
	rawPath := filepath.Join(dir, "malformed.md")
	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "project", "corpus", "download", "malformed", "--out", rawPath, "--split", filepath.Join(dir, "split"))
	if err == nil || !strings.Contains(err.Error(), "declares 3 threads but 2 valid thread sections were found") ||
		!strings.Contains(err.Error(), "raw download preserved at "+rawPath) || !strings.Contains(err.Error(), "~48h") {
		t.Fatalf("malformed split rescue error = %v", err)
	}
	if raw, readErr := os.ReadFile(rawPath); readErr != nil {
		t.Fatalf("read malformed rescue: %v", readErr)
	} else if !strings.Contains(string(raw), "unique_content: 3") || !strings.Contains(string(raw), "## Steps") {
		t.Fatalf("malformed raw download was not preserved:\n%s", raw)
	}
}

func TestExportDownloadBodyUnavailable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "project", "corpus", "download", "missing")
	var apiErr *client.APIError
	if !asAPIError(err, &apiErr) || apiErr.Code != "BODY_UNAVAILABLE" {
		t.Fatalf("expected BODY_UNAVAILABLE APIError, got %v", err)
	}
}

// TestExportDownloadSplit: --split materializes INDEX.md + per-thread files under an explicit dir, each
// thread file carrying the export_id/thread front-matter.
func TestExportDownloadSplit(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	dir := filepath.Join(t.TempDir(), "split")
	e, out, _ := newTestEnv(t, srv, "table")
	id := "eeee1111-0000-0000-0000-000000000001"
	if err := run(t, e, "project", "corpus", "download", id, "--split", dir); err != nil {
		t.Fatalf("project corpus download --split: %v", err)
	}
	if strings.TrimSpace(out.String()) != dir {
		t.Errorf("split stdout = %q, want the dir %q", out.String(), dir)
	}

	idx, err := os.ReadFile(filepath.Join(dir, "INDEX.md"))
	if err != nil {
		t.Fatalf("read INDEX.md: %v", err)
	}
	assertGolden(t, "export_split_index.golden", string(idx))

	entries, err := os.ReadDir(filepath.Join(dir, "threads"))
	if err != nil {
		t.Fatalf("read threads dir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, en := range entries {
		names = append(names, en.Name())
	}
	wantNames := []string{"2025-02--re-invoice-42-question--1.md", "2025-03--another-subject--2.md"}
	if strings.Join(names, ",") != strings.Join(wantNames, ",") {
		t.Errorf("thread files = %v, want %v", names, wantNames)
	}

	first, err := os.ReadFile(filepath.Join(dir, "threads", wantNames[0]))
	if err != nil {
		t.Fatalf("read first thread: %v", err)
	}
	fc := string(first)
	if !strings.Contains(fc, "export_id: "+id) {
		t.Errorf("thread file missing export_id front-matter: %q", fc)
	}
	if !strings.Contains(fc, `thread: "`+id+`#1"`) {
		t.Errorf("thread file missing thread handle front-matter: %q", fc)
	}
	if !strings.Contains(fc, "## Re: Invoice #42 question — #1") {
		t.Errorf("thread file missing original section body: %q", fc)
	}
	if !strings.Contains(fc, "## Steps") || !strings.Contains(fc, "corrected document") {
		t.Errorf("thread file lost embedded H2 body: %q", fc)
	}
}

// V2 is the current server download shape: no mailbox/participants/span metadata, role-based message
// headers, and an Occurrences field. The splitter derives the file month from the first message date.
func TestExportDownloadSplitV2(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	dir := filepath.Join(t.TempDir(), "split")
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "corpus", "download", "v2", "--split", dir); err != nil {
		t.Fatalf("project corpus download v2 --split: %v", err)
	}
	if strings.TrimSpace(out.String()) != dir {
		t.Errorf("v2 split stdout = %q, want %q", out.String(), dir)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "threads"))
	if err != nil {
		t.Fatalf("read v2 threads: %v", err)
	}
	wantNames := []string{"2025-02--re-invoice-42-question--1.md", "2025-03--another-subject--2.md"}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if strings.Join(names, ",") != strings.Join(wantNames, ",") {
		t.Errorf("v2 thread files = %v, want %v", names, wantNames)
	}
	index, err := os.ReadFile(filepath.Join(dir, "INDEX.md"))
	if err != nil {
		t.Fatalf("read v2 INDEX.md: %v", err)
	}
	for _, want := range []string{"harvested_at: 2026-07-19T10:00:00Z", "| 2025-02-10 | - | Re: Invoice #42 question | 2 |"} {
		if !strings.Contains(string(index), want) {
			t.Errorf("v2 index missing %q:\n%s", want, index)
		}
	}
	first, err := os.ReadFile(filepath.Join(dir, "threads", wantNames[0]))
	if err != nil {
		t.Fatalf("read v2 first thread: %v", err)
	}
	if !strings.Contains(string(first), "**Occurrences:** 2") || !strings.Contains(string(first), "**mailbox (2025-02-18):**") ||
		!strings.Contains(string(first), "## Steps") {
		t.Errorf("v2 thread body lost current-render fields:\n%s", first)
	}
}

// TestExportDownloadSplitDefaultDir: --split with an empty value defaults to .rootcause/exports/<id>/
// and seeds a .rootcause/.gitignore with "*" so the harvested corpus can't be committed.
func TestExportDownloadSplitDefaultDir(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	tmp := t.TempDir()
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	e, out, _ := newTestEnv(t, srv, "table")
	id := "eeee1111-0000-0000-0000-000000000001"
	if err := run(t, e, "project", "corpus", "download", id, "--split", ""); err != nil {
		t.Fatalf("project corpus download --split '': %v", err)
	}
	wantDir := filepath.Join(".rootcause", "exports", id)
	if strings.TrimSpace(out.String()) != wantDir {
		t.Errorf("default split dir = %q, want %q", strings.TrimSpace(out.String()), wantDir)
	}
	gi, err := os.ReadFile(filepath.Join(".rootcause", ".gitignore"))
	if err != nil {
		t.Fatalf("read .rootcause/.gitignore: %v", err)
	}
	if strings.TrimSpace(string(gi)) != "*" {
		t.Errorf(".rootcause/.gitignore = %q, want *", string(gi))
	}
}
