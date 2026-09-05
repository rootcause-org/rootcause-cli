package cli

import (
	"os"
	"path/filepath"
	"strings"
)

type installKind string

const (
	installHomebrew   installKind = "Homebrew"
	installGo         installKind = "Go install"
	installStandalone installKind = "standalone"
	installMiseShim   installKind = "mise shim"
)

// rcInstallPath is one rc command reachable from PATH, or the actual running binary when a shim
// dispatches outside PATH. A shim is deliberately not an installation: mise's generic rc symlink is
// a selector, not another copy of the rc binary.
type rcInstallPath struct {
	Path         string
	ResolvedPath string
	Kind         installKind
	Selected     bool
	Running      bool
	Shadowed     bool
	Shim         bool
}

type rcInstallInventory struct {
	RunningPath     string
	SelectedPath    string
	CanonicalPath   string
	Paths           []rcInstallPath
	Installations   int
	HasDuplicates   bool
	RunningShadowed bool
}

// inspectRCInstallations inventories rc in PATH order and always includes the actual running binary.
// It takes all host-dependent inputs explicitly so tests can model shadowing without changing the
// developer's PATH or executing arbitrary candidate binaries.
func inspectRCInstallations(runningPath, pathValue, goos string) rcInstallInventory {
	runningPath = absoluteClean(runningPath)
	runningResolved := resolvePath(runningPath)
	inv := rcInstallInventory{RunningPath: runningResolved}

	for _, cand := range scanPathRC(pathValue, goos) {
		kind := classifyInstallPath(cand.Path, cand.Resolved)
		entry := rcInstallPath{
			Path:         cand.Path,
			ResolvedPath: cand.Resolved,
			Kind:         kind,
			Selected:     cand.Selected,
			Shadowed:     !cand.Selected,
			Shim:         kind == installMiseShim,
		}
		entry.Running = samePath(cand.Path, runningPath) || samePath(cand.Resolved, runningResolved)
		if entry.Selected {
			inv.SelectedPath = cand.Path
		}
		inv.Paths = append(inv.Paths, entry)
	}

	// os.Executable reports the dispatched Go binary when rc was entered through a mise shim. Include
	// it even when its bin directory is absent from PATH in the current project context.
	runningSeen := false
	for i := range inv.Paths {
		if inv.Paths[i].Running {
			runningSeen = true
			break
		}
	}
	if !runningSeen {
		inv.Paths = append(inv.Paths, rcInstallPath{
			Path:         runningPath,
			ResolvedPath: runningResolved,
			Kind:         classifyInstallPath(runningPath, runningResolved),
			Running:      true,
			Shadowed:     inv.SelectedPath != "",
		})
	}

	inv.Installations = countDistinctInstallations(inv.Paths)
	inv.HasDuplicates = inv.Installations > 1
	inv.CanonicalPath = chooseCanonicalInstall(inv.Paths, goos)
	inv.RunningShadowed = runningIsShadowed(inv, runningResolved)
	return inv
}

// pathRCCandidate is one executable named rc (rc.exe on Windows) reachable from PATH, in PATH order.
// Selected marks the first one — the copy a bare `rc` would run.
type pathRCCandidate struct {
	Path     string
	Resolved string
	Selected bool
}

// scanPathRC is the ONE PATH inventory in the self-update/doctor plane: it walks pathValue in order,
// keeps every executable rc, dedupes by absolute path and resolves symlinks. Deliberately it does not
// classify — the two consumers ask different questions of the same list. `rc self update`
// (inspectRCInstallations) classifies by path SHAPE (classifyInstallPath: homebrew/go/mise-shim/
// standalone) to decide what shadows what, without reading any binary. `rc self doctor`
// (scanPathBinaries) reads each binary's embedded build info to report PROVENANCE (classifyInstall:
// release/go-install/source/homebrew). Keep the walk shared; keep the two classifications apart.
func scanPathRC(pathValue, goos string) []pathRCCandidate {
	name := binaryName(goos)
	seen := make(map[string]bool)
	out := make([]pathRCCandidate, 0)
	for _, dir := range filepath.SplitList(pathValue) {
		if dir == "" {
			dir = "."
		}
		candidate := absoluteClean(filepath.Join(dir, name))
		if seen[candidate] || !isExecutableFile(candidate, goos) {
			continue
		}
		seen[candidate] = true
		out = append(out, pathRCCandidate{
			Path:     candidate,
			Resolved: resolvePath(candidate),
			Selected: len(out) == 0,
		})
	}
	return out
}

func absoluteClean(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Clean(path)
}

func resolvePath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return absoluteClean(resolved)
	}
	return absoluteClean(path)
}

func isExecutableFile(path, goos string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return goos == "windows" || info.Mode().Perm()&0o111 != 0
}

func classifyInstallPath(path, resolved string) installKind {
	if isMiseShim(path, resolved) {
		return installMiseShim
	}
	if isHomebrewManaged(resolved) || isHomebrewManaged(path) {
		return installHomebrew
	}
	if isGoInstallPath(resolved) || isGoInstallPath(path) || isRootcauseGoBinary(resolved) {
		return installGo
	}
	return installStandalone
}

func isMiseShim(path, resolved string) bool {
	clean := filepath.ToSlash(path)
	if strings.Contains(clean, "/mise/shims/") {
		return true
	}
	return filepath.Base(resolved) == "mise"
}

func isGoInstallPath(path string) bool {
	clean := strings.ReplaceAll(filepath.ToSlash(path), `\`, "/")
	isRC := strings.HasSuffix(clean, "/rc") || strings.HasSuffix(clean, "/rc.exe")
	miseGoBin := strings.Contains(clean, "/mise/installs/go/") && strings.Contains(clean, "/bin/")
	standardGoBin := strings.Contains(clean, "/go/bin/")
	return isRC && (miseGoBin || standardGoBin)
}

func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

func countDistinctInstallations(paths []rcInstallPath) int {
	seen := make(map[string]bool)
	for _, path := range paths {
		if path.Shim {
			continue
		}
		key := path.ResolvedPath
		if !seen[key] {
			seen[key] = true
		}
	}
	return len(seen)
}

func chooseCanonicalInstall(paths []rcInstallPath, goos string) string {
	if goos == "darwin" {
		for _, path := range paths {
			if !path.Shim && path.Kind == installHomebrew {
				return path.ResolvedPath
			}
		}
	}
	for _, path := range paths {
		if path.Selected && !path.Shim {
			return path.ResolvedPath
		}
	}
	for _, path := range paths {
		if path.Running && !path.Shim {
			return path.ResolvedPath
		}
	}
	return ""
}

func runningIsShadowed(inv rcInstallInventory, runningResolved string) bool {
	if inv.SelectedPath == "" {
		return true
	}
	for _, path := range inv.Paths {
		if !path.Selected {
			continue
		}
		if path.Shim {
			// os.Executable cannot prove whether this process came through the shim or an absolute path.
			// Stay conservative: a selected shim is healthy only after migration removes it and PATH selects
			// the canonical binary directly.
			return true
		}
		return !samePath(path.ResolvedPath, runningResolved)
	}
	return true
}

func findRunningInstall(paths []rcInstallPath) rcInstallPath {
	for _, path := range paths {
		if path.Running && !path.Shim {
			return path
		}
	}
	return rcInstallPath{Kind: installStandalone}
}

func findSelectedInstall(paths []rcInstallPath) *rcInstallPath {
	for i := range paths {
		if paths[i].Selected {
			return &paths[i]
		}
	}
	return nil
}
