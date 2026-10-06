package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunAttachmentsNeverOverwritesSuffixedFile(t *testing.T) {
	file := stubAttachment{id: "b8000000-0000-4000-8000-000000000001", name: "report.pdf", body: "new upload"}
	srv, _ := attachmentServer(t, map[string][]stubAttachment{attSession: {file}}, nil)
	dir := t.TempDir()
	for _, name := range []string{"report.pdf", "report-b8000000.pdf"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("existing local data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	e, _, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "run", "attachments", attSession, "--download", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "report-b8000000.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "existing local data" {
		t.Fatalf("existing file overwritten: got %q", got)
	}
	next, err := os.ReadFile(filepath.Join(dir, "report-b8000000-2.pdf"))
	if err != nil || string(next) != "new upload" {
		t.Fatalf("upload not installed under next free name: %q %v", next, err)
	}
	left, _ := filepath.Glob(filepath.Join(dir, ".rc-attachment-*"))
	if len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

func TestRunAttachmentsGeneratedFileSkipsListedDigest(t *testing.T) {
	file := stubAttachment{id: "c9000000-0000-4000-8000-000000000001", name: "export.csv", body: "naam\nAnna", origin: "assistant"}
	srv, _ := attachmentServer(t, map[string][]stubAttachment{attSession: {file}}, nil)
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		e, _, _ := newTestEnv(t, srv, "json")
		if err := run(t, e, "run", "attachments", attSession, "--id", file.id, "--dir", dir); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	names, _ := filepath.Glob(filepath.Join(dir, "*"))
	if len(names) != 1 {
		t.Fatalf("generated file re-downloaded under a new name: %v", names)
	}
}
