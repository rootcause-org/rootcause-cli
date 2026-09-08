package cli

import (
	"os"
	"strings"
	"testing"
)

// TestKnowledgeArticleApplyFromStdin pins `--from -`: the block arrives on stdin (the suggestions
// pipeline pipes it) and must reach the server byte-identical to the file path.
func TestKnowledgeArticleApplyFromStdin(t *testing.T) {
	block, err := os.ReadFile("testdata/knowledge_article_block.md")
	if err != nil {
		t.Fatal(err)
	}
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	e.in = strings.NewReader(string(block))
	if err := run(t, e, "project", "knowledge", "article", "apply", "--from", "-", "--dry-run"); err != nil {
		t.Fatal(err)
	}
	assertGolden(t, "knowledge_article_apply_dry.golden", out.String())
}

// TestKnowledgeArticleApplyPublishedRefusal pins the 409 surfacing verbatim: without --publish a live
// article is refused, and the operator sees the server's own remedy sentence.
func TestKnowledgeArticleApplyPublishedRefusal(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	err := run(t, e, "project", "knowledge", "article", "apply", "--from", "testdata/knowledge_article_block.md")
	if err == nil || !strings.Contains(err.Error(), "re-run with --publish") {
		t.Fatalf("apply on a published article error = %v, want the --publish remedy", err)
	}
}

// TestKnowledgeArticleGetWritesFile pins --out: the file holds the block verbatim (it is the next
// apply's input) and stdout carries only the acknowledgement.
func TestKnowledgeArticleGetWritesFile(t *testing.T) {
	srv := stubServer(t)
	defer srv.Close()
	e, out, _ := newTestEnv(t, srv, "table")
	path := t.TempDir() + "/article.md"
	if err := run(t, e, "project", "knowledge", "article", "get", "--provider", "helpscout", "--id", "19", "--out", path); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "wrote "+path+"\n" {
		t.Errorf("stdout = %q, want the wrote line", got)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "replypen: helpcenter/v1") {
		t.Errorf("written block missing the marker:\n%s", written)
	}
}
