package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// Golden + contract tests for the local-synthesis harvest/export surface (rc mailbox harvest, rc export
// ls/get/download) plus a pure unit test of the corpus splitter. Mirrors config_surface_test.go: a stub
// server returns canned JSON/Markdown, the test pins the rendered output (or the on-disk split tree).

// --- rc export ls / get ---

func TestExportListTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "export", "ls"); err != nil {
		t.Fatalf("export ls: %v", err)
	}
	assertGolden(t, "export_ls.golden", out.String())
}

func TestExportListJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "export", "ls"); err != nil {
		t.Fatalf("export ls -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "exports.json"), out.Bytes())
}

func TestExportGetTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "export", "get", "eeee1111-0000-0000-0000-000000000001"); err != nil {
		t.Fatalf("export get: %v", err)
	}
	assertGolden(t, "export_get.golden", out.String())
}

func TestExportGetJSONPassthrough(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "export", "get", "eeee1111-0000-0000-0000-000000000001"); err != nil {
		t.Fatalf("export get -o json: %v", err)
	}
	assertJSONEqual(t, fixture(t, "export_item.json"), out.Bytes())
}

// --- rc mailbox harvest ---

// TestMailboxHarvestAccepted: the no-wait path prints {export_id,status} and a poll hint on stderr.
func TestMailboxHarvestAccepted(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, errb := newTestEnv(t, srv, "table")
	if err := run(t, e, "mailbox", "harvest", "11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatalf("mailbox harvest: %v", err)
	}
	if !strings.Contains(out.String(), "export_id: eeee1111-0000-0000-0000-000000000001") {
		t.Errorf("missing export_id in stdout: %q", out.String())
	}
	if !strings.Contains(errb.String(), "rc export get") {
		t.Errorf("missing poll hint on stderr: %q", errb.String())
	}
}

// TestMailboxHarvestConflict: a mailbox already harvesting returns 409 HARVEST_IN_PROGRESS, surfaced
// verbatim as a typed APIError.
func TestMailboxHarvestConflict(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "mailbox", "harvest", "busy")
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
	if err := run(t, e, "mailbox", "harvest", "wait", "--wait", "--clean=false", "--max-threads", "5"); err != nil {
		t.Fatalf("mailbox harvest --wait: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "status:") || !strings.Contains(got, "done") {
		t.Errorf("expected finished export row with status done, got: %q", got)
	}
}

// --- rc export download ---

func TestExportDownloadStdout(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "export", "download", "eeee1111-0000-0000-0000-000000000001"); err != nil {
		t.Fatalf("export download: %v", err)
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
	if err := run(t, e, "export", "download", "eeee1111-0000-0000-0000-000000000001", "--out", outFile); err != nil {
		t.Fatalf("export download --out: %v", err)
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read out file: %v", err)
	}
	if !strings.Contains(string(data), "harvest_format: v1") {
		t.Errorf("out file missing corpus: %q", string(data))
	}
}

// TestExportDownloadOutSplitExclusive: --out and --split can't combine (--out would be silently
// ignored otherwise).
func TestExportDownloadOutSplitExclusive(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "export", "download", "eeee1111-0000-0000-0000-000000000001", "--out", "x.md", "--split", "y")
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected a mutually-exclusive error, got %v", err)
	}
}

func TestExportDownloadBodyUnavailable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "export", "download", "missing")
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
	if err := run(t, e, "export", "download", id, "--split", dir); err != nil {
		t.Fatalf("export download --split: %v", err)
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
	if err := run(t, e, "export", "download", id, "--split", ""); err != nil {
		t.Fatalf("export download --split '': %v", err)
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
