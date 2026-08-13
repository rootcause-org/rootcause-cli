// Package contextexport locates a rootcause HOST checkout and builds the argv for the host's offline
// context renderer (`go run ./cmd/context-export`). The renderer imports the host's internal packages
// (agent, grounding, triage, brain, kbsync, db/generated), so it can never be forked into this module —
// `rc dev context-export` wraps it. Resolution lives here, apart from the exec, so host discovery and
// suite/output resolution stay unit-testable without compiling the host.
package contextexport

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RendererRelPath is the marker that identifies a directory as a rootcause host checkout.
const RendererRelPath = "cmd/context-export/main.go"

// suiteDir is where the host keeps its grounding-eval suites; a bare --suite name resolves under it.
const suiteDir = "eval/grounding"

// Options are the raw, unresolved inputs of one invocation: flags, the RC_HOST_REPO env, the working
// directory, and the brain marker context the CLI already resolved (empty outside a brain checkout).
type Options struct {
	HostRepo    string // --host-repo
	HostRepoEnv string // RC_HOST_REPO
	Cwd         string
	Suite       string // --suite: bare project name or a path
	Project     string // .rootcause.toml project, when cwd is inside a brain
	BrainDir    string // the brain marker's directory, when cwd is inside a brain
	Out         string // --out
	KB          string
	KBMount     string
	AgentsMD    string
}

// Plan is the resolved invocation. Suite is host-repo-relative when it resolved there (the renderer
// runs with Dir=HostRepo); OutDir and BrainDir are absolute so nothing lands in the host checkout.
type Plan struct {
	HostRepo string
	Suite    string
	OutDir   string
	BrainDir string
	Args     []string // `go` argv, starting at "run"
}

// Resolve turns Options into a runnable Plan, or an error naming how to point rc at a host checkout.
func Resolve(o Options) (Plan, error) {
	host, err := discoverHost(o)
	if err != nil {
		return Plan{}, err
	}
	suite, err := resolveSuite(host, o)
	if err != nil {
		return Plan{}, err
	}

	out := o.Out
	if out == "" {
		out = filepath.Join(".rootcause", "context-export")
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(o.Cwd, out)
	}

	plan := Plan{HostRepo: host, Suite: suite, OutDir: out, BrainDir: o.BrainDir}
	plan.Args = []string{"run", "./cmd/context-export", "-suite", suite, "-out", out}
	// No brain outside a brain checkout: the suite's own brain_dir default must stand.
	if plan.BrainDir != "" {
		plan.Args = append(plan.Args, "-brain", plan.BrainDir)
	}
	for _, kv := range []struct{ flag, value string }{
		{"-kb", o.KB}, {"-kb-mount", o.KBMount}, {"-agents-md", o.AgentsMD},
	} {
		if kv.value != "" {
			plan.Args = append(plan.Args, kv.flag, absFrom(o.Cwd, kv.value))
		}
	}
	return plan, nil
}

// discoverHost walks the explicit sources first, then guesses from the standard sibling layout
// (~/code/rootcause-org/{rootcause,rootcause-brain-*}) so the command works from any brain checkout.
func discoverHost(o Options) (string, error) {
	if o.HostRepo != "" {
		return verifyHost(absFrom(o.Cwd, o.HostRepo), "--host-repo")
	}
	if o.HostRepoEnv != "" {
		return verifyHost(absFrom(o.Cwd, o.HostRepoEnv), "RC_HOST_REPO")
	}
	for _, dir := range hostCandidates(o.Cwd) {
		if isHost(dir) {
			return dir, nil
		}
	}
	return "", fmt.Errorf("no rootcause host checkout found near %s — `rc dev context-export` is an operator command: it runs the host repo's offline context renderer (%s). Point at a checkout with --host-repo <path> or RC_HOST_REPO=<path>", o.Cwd, RendererRelPath)
}

// hostCandidates is cwd and its ancestors (running inside the host itself), then cwd's siblings with
// ../rootcause first — the conventional name in the standard side-by-side layout.
func hostCandidates(cwd string) []string {
	if cwd == "" {
		return nil
	}
	var candidates []string
	for dir := cwd; ; {
		candidates = append(candidates, dir)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	parent := filepath.Dir(cwd)
	candidates = append(candidates, filepath.Join(parent, "rootcause"))
	entries, err := os.ReadDir(parent)
	if err != nil {
		return candidates
	}
	var siblings []string
	for _, entry := range entries {
		if entry.IsDir() {
			siblings = append(siblings, filepath.Join(parent, entry.Name()))
		}
	}
	sort.Strings(siblings)
	return append(candidates, siblings...)
}

func verifyHost(dir, source string) (string, error) {
	if !isHost(dir) {
		return "", fmt.Errorf("%s points at %s, which is not a rootcause host checkout (no %s)", source, dir, RendererRelPath)
	}
	return dir, nil
}

func isHost(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(RendererRelPath)))
	return err == nil && !info.IsDir()
}

// resolveSuite accepts a bare project name (eval/grounding/<name>.yaml in the host) or a path, and
// falls back to the brain checkout's own project so the common case needs no flag at all.
func resolveSuite(host string, o Options) (string, error) {
	raw := strings.TrimSpace(o.Suite)
	if raw == "" {
		if o.Project == "" {
			return "", fmt.Errorf("no --suite given and no %s project context here — pass --suite <name> (available: %s)", ".rootcause.toml", suiteList(host))
		}
		raw = o.Project
	}

	if strings.ContainsRune(raw, filepath.Separator) || strings.ContainsRune(raw, '/') ||
		strings.HasSuffix(raw, ".yaml") || strings.HasSuffix(raw, ".yml") {
		for _, candidate := range suitePathCandidates(host, o.Cwd, raw) {
			if fileExists(candidate.path) {
				return candidate.pass, nil
			}
		}
		return "", fmt.Errorf("suite %q not found (looked relative to %s and %s; available: %s)", raw, o.Cwd, host, suiteList(host))
	}

	rel := filepath.Join(suiteDir, raw+".yaml")
	if fileExists(filepath.Join(host, rel)) {
		return rel, nil
	}
	return "", fmt.Errorf("no suite %q in %s/%s — available: %s", raw, host, suiteDir, suiteList(host))
}

// suitePathCandidate carries both the path we probe and the argument we pass: a host-relative suite
// stays relative (the renderer runs with Dir=host), anything else is made absolute.
type suitePathCandidate struct{ path, pass string }

func suitePathCandidates(host, cwd, raw string) []suitePathCandidate {
	if filepath.IsAbs(raw) {
		return []suitePathCandidate{{path: raw, pass: raw}}
	}
	return []suitePathCandidate{
		{path: filepath.Join(host, raw), pass: raw},
		{path: filepath.Join(cwd, raw), pass: filepath.Join(cwd, raw)},
	}
}

// suiteList names the host's suites by bare name, for the error that lists what the operator can pick.
func suiteList(host string) string {
	matches, err := filepath.Glob(filepath.Join(host, suiteDir, "*.yaml"))
	if err != nil || len(matches) == 0 {
		return "(none found)"
	}
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, strings.TrimSuffix(filepath.Base(m), ".yaml"))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func absFrom(cwd, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(cwd, path)
}
