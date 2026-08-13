package contextexport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeLayout builds the standard side-by-side layout: <root>/rootcause (host, with suites) and
// <root>/rootcause-brain-acme (the brain a run of rc starts from).
func fakeLayout(t *testing.T, suites ...string) (host, brain string) {
	t.Helper()
	root := t.TempDir()
	host = filepath.Join(root, "rootcause")
	if err := os.MkdirAll(filepath.Join(host, "cmd", "context-export"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(host, "cmd", "context-export", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(host, suiteDir), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, s := range suites {
		if err := os.WriteFile(filepath.Join(host, suiteDir, s+".yaml"), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	brain = filepath.Join(root, "rootcause-brain-acme")
	if err := os.MkdirAll(brain, 0o755); err != nil {
		t.Fatal(err)
	}
	return host, brain
}

func argValue(t *testing.T, args []string, flag string) string {
	t.Helper()
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func TestResolveFromBrainSibling(t *testing.T) {
	host, brain := fakeLayout(t, "acme", "other")
	plan, err := Resolve(Options{Cwd: brain, Project: "acme", BrainDir: brain})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if plan.HostRepo != host {
		t.Fatalf("host = %s, want %s", plan.HostRepo, host)
	}
	if want := filepath.Join(suiteDir, "acme.yaml"); plan.Suite != want {
		t.Fatalf("suite = %s, want %s", plan.Suite, want)
	}
	// The default output must land under the CURRENT dir, never in the host checkout.
	if want := filepath.Join(brain, ".rootcause", "context-export"); plan.OutDir != want {
		t.Fatalf("out = %s, want %s", plan.OutDir, want)
	}
	if got := argValue(t, plan.Args, "-brain"); got != brain {
		t.Fatalf("-brain = %q, want %q", got, brain)
	}
	if got := argValue(t, plan.Args, "-out"); got != plan.OutDir {
		t.Fatalf("-out = %q, want %q", got, plan.OutDir)
	}
}

func TestResolveOutsideBrainKeepsSuiteBrainDefault(t *testing.T) {
	host, _ := fakeLayout(t, "acme")
	work := filepath.Join(filepath.Dir(host), "elsewhere")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err := Resolve(Options{Cwd: work, Suite: "acme"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for _, a := range plan.Args {
		if a == "-brain" {
			t.Fatal("-brain passed without a brain checkout; the suite default must stand")
		}
	}
}

func TestResolveExplicitHostRepoAndPaths(t *testing.T) {
	host, brain := fakeLayout(t, "acme")
	other := t.TempDir() // cwd far away from the layout, so only --host-repo can find the host
	plan, err := Resolve(Options{
		Cwd: other, HostRepo: host, Suite: filepath.Join(suiteDir, "acme.yaml"),
		Out: "dump", KB: "kb", KBMount: "/kb", AgentsMD: "AGENTS.md", BrainDir: brain,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if plan.Suite != filepath.Join(suiteDir, "acme.yaml") {
		t.Fatalf("suite = %s", plan.Suite)
	}
	if want := filepath.Join(other, "dump"); plan.OutDir != want {
		t.Fatalf("out = %s, want %s", plan.OutDir, want)
	}
	if got := argValue(t, plan.Args, "-kb"); got != filepath.Join(other, "kb") {
		t.Fatalf("-kb = %q", got)
	}
	if got := argValue(t, plan.Args, "-kb-mount"); got != "/kb" {
		t.Fatalf("-kb-mount = %q", got)
	}
}

func TestResolveHostNotFound(t *testing.T) {
	_, err := Resolve(Options{Cwd: t.TempDir(), Suite: "acme"})
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{"--host-repo", "RC_HOST_REPO", "operator"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}
}

func TestResolveBadHostRepoFlag(t *testing.T) {
	_, err := Resolve(Options{Cwd: t.TempDir(), HostRepo: t.TempDir(), Suite: "acme"})
	if err == nil || !strings.Contains(err.Error(), "--host-repo") {
		t.Fatalf("want a --host-repo error, got %v", err)
	}
}

func TestResolveUnknownSuiteListsAvailable(t *testing.T) {
	_, brain := fakeLayout(t, "acme", "other")
	_, err := Resolve(Options{Cwd: brain, Suite: "nope"})
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "acme, other") {
		t.Fatalf("error %q should list the available suites", err)
	}
}

func TestResolveNoSuiteNoProject(t *testing.T) {
	_, brain := fakeLayout(t, "acme")
	_, err := Resolve(Options{Cwd: brain})
	if err == nil || !strings.Contains(err.Error(), "--suite") {
		t.Fatalf("want a --suite hint, got %v", err)
	}
}
