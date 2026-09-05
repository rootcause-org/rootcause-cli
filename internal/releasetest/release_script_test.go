package releasetest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestReleasePublishesMainBeforeTag exercises the real release script against a local bare remote.
// gh/go are replaced only at their external boundaries, so the test proves an unpushed local main is
// accepted, published, remotely verified, and tagged at the same exact commit.
func TestReleasePublishesMainBeforeTag(t *testing.T) {
	if testing.Short() { // ~6 s of git/curl subprocesses; release.sh runs the full suite itself
		t.Skip("release script end-to-end run is skipped under -short")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	releaseScript := filepath.Join(repoRoot, "scripts", "release.sh")

	tmp := t.TempDir()
	origin := filepath.Join(tmp, "origin.git")
	checkout := filepath.Join(tmp, "checkout")
	run(t, tmp, "git", "init", "--bare", origin)
	run(t, tmp, "git", "init", "-b", "main", checkout)
	run(t, checkout, "git", "config", "user.name", "Release Test")
	run(t, checkout, "git", "config", "user.email", "release@example.test")
	write(t, filepath.Join(checkout, "README.md"), "initial\n", 0o644)

	// The release script shellchecks and byte-compares the real cloud bootstrap script, so the
	// sandbox checkout carries the repo's actual copy.
	cloudSetup, err := os.ReadFile(filepath.Join(repoRoot, "scripts", "cloud-setup.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(checkout, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(checkout, "scripts", "cloud-setup.sh"), string(cloudSetup), 0o755)

	// A local stand-in for the S3 release mirror the workflow publishes to (RC_RELEASE_MIRROR is the
	// same override scripts/cloud-setup.sh honours). Without it the script would curl the production
	// mirror for a tag that does not exist and get a 403.
	mirror := filepath.Join(tmp, "mirror")
	if err := os.MkdirAll(filepath.Join(mirror, "v9.9.9"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(mirror, "cloud-setup.sh"), string(cloudSetup), 0o644)
	write(t, filepath.Join(mirror, "v9.9.9", "cloud-setup.sh"), string(cloudSetup), 0o644)
	write(t, filepath.Join(mirror, "latest"), "v9.9.9\n", 0o644)
	write(t, filepath.Join(mirror, "v9.9.9", "checksums.txt"), "deadbeef  rc_9.9.9_linux_amd64.tar.gz\n", 0o644)

	run(t, checkout, "git", "add", "README.md", "scripts/cloud-setup.sh")
	run(t, checkout, "git", "commit", "-m", "initial")
	run(t, checkout, "git", "remote", "add", "origin", origin)
	run(t, checkout, "git", "push", "-u", "origin", "main")

	// This is the release-worthy commit: intentionally leave it ahead of origin/main.
	write(t, filepath.Join(checkout, "README.md"), "initial\nrelease change\n", 0o644)
	run(t, checkout, "git", "add", "README.md")
	run(t, checkout, "git", "commit", "-m", "release change")
	wantSHA := strings.TrimSpace(run(t, checkout, "git", "rev-parse", "HEAD"))
	beforeSHA := strings.TrimSpace(run(t, tmp, "git", "--git-dir", origin, "rev-parse", "refs/heads/main"))
	if beforeSHA == wantSHA {
		t.Fatal("test setup failed: release commit is already on origin/main")
	}

	fakeBin := filepath.Join(tmp, "bin")
	if err := os.Mkdir(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	// shellcheck and golangci-lint are shimmed for the same reason go and gh are: this sandbox is a
	// two-file checkout, so re-running them here proves nothing (ci.yml and the developer's own
	// `go test ./...` already cover them) while adding 1–2 s and an undeclared host dependency —
	// release.sh dies without shellcheck, so `go test ./...` would fail on a machine without it. We
	// still assert release.sh invoked both, which is the part this test is about.
	invoked := filepath.Join(tmp, "invoked")
	for _, linter := range []string{"shellcheck", "golangci-lint"} {
		write(t, filepath.Join(fakeBin, linter), "#!/usr/bin/env bash\nprintf '"+linter+"\\n' >> "+invoked+"\nexit 0\n", 0o755)
	}
	write(t, filepath.Join(fakeBin, "go"), `#!/usr/bin/env bash
if [[ "$*" == *"rootcause-org/rootcause-cli@latest"* ]]; then
  printf 'v9.9.9\n'
fi
exit 0
`, 0o755)
	write(t, filepath.Join(fakeBin, "gh"), `#!/usr/bin/env bash
if [[ "$1 $2" == "auth status" ]]; then
  exit 0
fi
if [[ "$1 $2" == "release view" && "$*" == *"--json assets"* ]]; then
  printf '7\n'
  exit 0
fi
if [[ "$1 $2" == "release view" && "$*" == *"--json url"* ]]; then
  printf 'https://example.test/releases/v9.9.9\n'
  exit 0
fi
if [[ "$1" == "api" && "$*" == *"rootcause-cli/releases/latest"* ]]; then
  printf 'v9.9.9\n'
  exit 0
fi
if [[ "$1" == "api" && "$*" == *"homebrew-tap/contents/Casks/rc.rb"* ]]; then
  printf '  version "9.9.9"\n'
  exit 0
fi
if [[ "$1 $2" == "run list" ]]; then
  printf 'completed\tsuccess\n'
  exit 0
fi
exit 0
`, 0o755)

	cmd := exec.Command("bash", releaseScript, "v9.9.9")
	cmd.Dir = checkout
	cmd.Env = append(
		withPath(os.Environ(), fakeBin+string(os.PathListSeparator)+os.Getenv("PATH")),
		"RC_RELEASE_MIRROR=file://"+mirror,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("release failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "origin/main verified") {
		t.Fatalf("release output omitted remote-main verification:\n%s", out)
	}
	if !strings.Contains(string(out), "Homebrew cask is 9.9.9") {
		t.Fatalf("release output omitted Homebrew verification:\n%s", out)
	}
	for _, key := range []string{
		"mirror v9.9.9/cloud-setup.sh", "mirror cloud-setup.sh",
		"mirror latest is v9.9.9", "mirror v9.9.9/checksums.txt",
	} {
		if !strings.Contains(string(out), key) {
			t.Fatalf("release output omitted release mirror verification (%s):\n%s", key, out)
		}
	}
	if got, err := os.ReadFile(invoked); err != nil || !strings.Contains(string(got), "shellcheck") || !strings.Contains(string(got), "golangci-lint") {
		t.Fatalf("release did not run both linters: %q (%v)", got, err)
	}

	gotMain := strings.TrimSpace(run(t, tmp, "git", "--git-dir", origin, "rev-parse", "refs/heads/main"))
	if gotMain != wantSHA {
		t.Fatalf("origin/main = %s, want tested HEAD %s", gotMain, wantSHA)
	}
	gotTag := strings.TrimSpace(run(t, tmp, "git", "--git-dir", origin, "rev-parse", "refs/tags/v9.9.9^{}"))
	if gotTag != wantSHA {
		t.Fatalf("release tag = %s, want tested HEAD %s", gotTag, wantSHA)
	}
}

func run(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

func write(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func withPath(environ []string, path string) []string {
	out := make([]string, 0, len(environ)+1)
	for _, item := range environ {
		if !strings.HasPrefix(item, "PATH=") {
			out = append(out, item)
		}
	}
	return append(out, "PATH="+path)
}
