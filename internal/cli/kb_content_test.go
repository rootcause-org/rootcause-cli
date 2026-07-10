package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKBListTable(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "knowledge", "content", "list"); err != nil {
		t.Fatalf("project knowledge content list: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Provider: intercom") || !strings.Contains(got, "restore-amp-recovery\t2") {
		t.Fatalf("project knowledge content list output missing collection summary:\n%s", got)
	}
	if strings.Contains(got, "Choose Restore as new") {
		t.Fatalf("project knowledge content list leaked article body:\n%s", got)
	}
}

func TestKBSearchWritesProgressiveArtifacts(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	wd := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(wd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })

	e, out, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "project", "knowledge", "content", "search", "--out", ".rootcause/tmp/kb-searches/fixed-restore", "restore as new"); err != nil {
		t.Fatalf("project knowledge content search: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Found 1 articles, 2 matching lines") || !strings.Contains(got, "Artifacts: .rootcause/tmp/kb-searches/fixed-restore") {
		t.Fatalf("project knowledge content search summary missing expected handles:\n%s", got)
	}
	if strings.Contains(got, "Choose Restore as new") {
		t.Fatalf("project knowledge content search leaked article body:\n%s", got)
	}

	dir := filepath.Join(wd, ".rootcause/tmp/kb-searches/fixed-restore")
	for _, rel := range []string{"manifest.json", "hits.md", "articles/restore-amp-recovery/9286853-how-to-recover-deleted-data.md"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("expected artifact %s: %v", rel, err)
		}
	}
	body, err := os.ReadFile(filepath.Join(dir, "articles/restore-amp-recovery/9286853-how-to-recover-deleted-data.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Choose Restore as new to avoid overwriting live data.") {
		t.Fatalf("materialized article missing full body:\n%s", body)
	}
}

func TestKBSearchJSONIncludesArtifactPath(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	wd := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(wd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })

	e, out, _ := newTestEnv(t, srv, "json")
	if err := run(t, e, "project", "knowledge", "content", "search", "--out", ".rootcause/tmp/kb-searches/fixed-json", "restore as new"); err != nil {
		t.Fatalf("project knowledge content search json: %v", err)
	}
	var got kbCommandSummary
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode json: %v\n%s", err, out.String())
	}
	if got.ArtifactDir != ".rootcause/tmp/kb-searches/fixed-json" || got.ArticlesMatched != 1 || got.Hits != 2 {
		t.Fatalf("unexpected project knowledge content search json: %+v", got)
	}
}

func TestKBSearchRejectsProviderTraversal(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "project", "knowledge", "content", "search", "--provider", "../agent_internal", "restore")
	if err == nil || !strings.Contains(err.Error(), "invalid --provider") {
		t.Fatalf("project knowledge content search traversal err = %v, want invalid provider", err)
	}
}

func TestKBSearchDefaultDirsAreUniqueWithinSecond(t *testing.T) {
	wd := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(wd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })

	first := uniqueKBArtifactDir("restore as new")
	if err := os.MkdirAll(first, 0o755); err != nil {
		t.Fatal(err)
	}
	second := uniqueKBArtifactDir("restore as new")
	if first == second || !strings.HasSuffix(second, "-02") {
		t.Fatalf("uniqueKBArtifactDir collision handling = %q then %q", first, second)
	}
}
