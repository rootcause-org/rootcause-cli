package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileTreeSanitisesAndWipes(t *testing.T) {
	dir := t.TempDir()

	// A stale file from an earlier render must not survive the next one.
	stale := filepath.Join(dir, "tree", "playbooks", "gone.md")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	root, written, skipped, err := writeFileTree(dir, []treeFile{
		{Path: "AGENTS.md", Content: "agents"},
		{Path: "playbooks/annulatie_beleid.md", Content: "beleid"},
		{Path: "../escape.md", Content: "nope"},
		{Path: "playbooks/../../escape2.md", Content: "nope"},
		{Path: "/etc/passwd", Content: "nope"},
		{Path: "  ", Content: "nope"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "tree"); root != want {
		t.Errorf("root = %q, want %q", root, want)
	}
	if written != 2 {
		t.Errorf("written = %d, want 2", written)
	}
	if len(skipped) != 4 {
		t.Errorf("skipped = %v, want 4 entries", skipped)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale file survived the wipe: %v", err)
	}
	for path, want := range map[string]string{
		filepath.Join(root, "AGENTS.md"):                        "agents",
		filepath.Join(root, "playbooks", "annulatie_beleid.md"): "beleid",
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	for _, escaped := range []string{filepath.Join(dir, "escape.md"), filepath.Join(dir, "escape2.md"), filepath.Join(filepath.Dir(dir), "escape.md")} {
		if _, err := os.Stat(escaped); !os.IsNotExist(err) {
			t.Errorf("path escaped the tree: %s", escaped)
		}
	}
}
