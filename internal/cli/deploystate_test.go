package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// gitRepo builds a throwaway checkout with two commits and returns (dir, firstSHA).
func gitRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-b", "main")
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	write("a.txt", "one")
	run("add", ".")
	run("commit", "-m", "first")
	first := run("rev-parse", "HEAD")
	write("b.txt", "two")
	run("add", ".")
	run("commit", "-m", "second commit")
	return dir, first
}

// TestLocalUnpromotedListsCommitsAheadOfRelease: the deployed release is the first commit, so the
// second must show up as unpromoted.
func TestLocalUnpromotedListsCommitsAheadOfRelease(t *testing.T) {
	dir, first := gitRepo(t)

	u := localUnpromoted(dir, first[:8])
	if u.Error != "" {
		t.Fatalf("error = %q, want none", u.Error)
	}
	if u.Ref != "main" {
		t.Errorf("ref = %q, want main (no origin in a bare local checkout)", u.Ref)
	}
	if len(u.Commits) != 1 || !strings.Contains(u.Commits[0], "second commit") {
		t.Fatalf("commits = %v, want just the second commit", u.Commits)
	}

	// The tip being deployed means nothing is pending.
	head := u.Commits[0][:strings.Index(u.Commits[0], " ")]
	tip := localUnpromoted(dir, head)
	if tip.Error != "" || len(tip.Commits) != 0 {
		t.Fatalf("at tip: err=%q commits=%v, want clean and empty", tip.Error, tip.Commits)
	}
}

// TestLocalUnpromotedReportsUnusableInput: every failure must surface as an Error — an empty list would
// read as "everything is deployed", the exact wrong conclusion for an audit.
func TestLocalUnpromotedReportsUnusableInput(t *testing.T) {
	dir, first := gitRepo(t)
	cases := []struct {
		name, repo, release, want string
	}{
		{"no release", dir, "", "no release SHA"},
		{"not a sha", dir, "--upload-pack=evil", "non-SHA release"},
		{"unknown release", dir, "0123456789abcdef0123456789abcdef01234567", "not in"},
		{"not a checkout", t.TempDir(), first, "not a git checkout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := localUnpromoted(tc.repo, tc.release)
			if !strings.Contains(u.Error, tc.want) {
				t.Fatalf("error = %q, want it to mention %q", u.Error, tc.want)
			}
			if len(u.Commits) != 0 {
				t.Errorf("commits = %v, want none on an error", u.Commits)
			}
		})
	}
}

// TestWithUnpromotedSplicesIntoRawJSON: -o json must stay one object, server rows untouched.
func TestWithUnpromotedSplicesIntoRawJSON(t *testing.T) {
	raw := json.RawMessage(`{"project":"kampadmin","host":{"release":"abc1234"}}`)
	merged := withUnpromoted(raw, &render.HostUnpromoted{Repo: "/tmp/rc", Ref: "origin/main", Release: "abc1234", Commits: []string{"deadbee fix"}})

	var obj struct {
		Project    string `json:"project"`
		Unpromoted struct {
			Count   int      `json:"count"`
			Ref     string   `json:"ref"`
			Commits []string `json:"commits"`
		} `json:"unpromoted"`
	}
	if err := json.Unmarshal(merged, &obj); err != nil {
		t.Fatalf("decode merged: %v", err)
	}
	if obj.Project != "kampadmin" || obj.Unpromoted.Count != 1 || obj.Unpromoted.Ref != "origin/main" {
		t.Fatalf("merged = %s, want the server rows plus the local delta", merged)
	}

	if got := withUnpromoted(raw, nil); string(got) != string(raw) {
		t.Errorf("no local checkout must pass the server bytes through verbatim, got %s", got)
	}
}
