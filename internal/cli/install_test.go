package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectRCInstallationsDetectsMiseShadowingHomebrew(t *testing.T) {
	root := t.TempDir()
	mise := executableFile(t, filepath.Join(root, "bin", "mise"))
	shim := symlinkFile(t, mise, filepath.Join(root, ".local", "share", "mise", "shims", "rc"))
	running := executableFile(t, filepath.Join(root, ".local", "share", "mise", "installs", "go", "1.25.9", "bin", "rc"))
	brewBinary := executableFile(t, filepath.Join(root, "homebrew", "Caskroom", "rc", "1.1.3", "rc"))
	brewLink := symlinkFile(t, brewBinary, filepath.Join(root, "homebrew", "bin", "rc"))

	pathValue := strings.Join([]string{filepath.Dir(shim), filepath.Dir(brewLink), filepath.Dir(running)}, string(os.PathListSeparator))
	inv := inspectRCInstallations(running, pathValue, "darwin")

	if inv.SelectedPath != shim {
		t.Fatalf("selected = %q, want shim %q", inv.SelectedPath, shim)
	}
	if inv.CanonicalPath != resolvePath(brewBinary) {
		t.Fatalf("canonical = %q, want Homebrew binary %q", inv.CanonicalPath, resolvePath(brewBinary))
	}
	if inv.Installations != 2 || !inv.HasDuplicates {
		t.Fatalf("installations = %d, duplicates = %v; want 2, true", inv.Installations, inv.HasDuplicates)
	}
	if !inv.RunningShadowed {
		t.Fatal("expected the mise-dispatched Go binary to be shadowing canonical Homebrew")
	}
	if got := inv.Paths[0].Kind; got != installMiseShim {
		t.Fatalf("selected kind = %q, want %q", got, installMiseShim)
	}
	if got := inv.Paths[1].Kind; got != installHomebrew || !inv.Paths[1].Shadowed {
		t.Fatalf("Homebrew path = %#v, want shadowed Homebrew", inv.Paths[1])
	}
	if got := inv.Paths[2].Kind; got != installGo || !inv.Paths[2].Running {
		t.Fatalf("running path = %#v, want running Go install", inv.Paths[2])
	}
}

func TestInspectRCInstallationsCleanStandalone(t *testing.T) {
	running := executableFile(t, filepath.Join(t.TempDir(), "bin", "rc"))
	inv := inspectRCInstallations(running, filepath.Dir(running), "linux")

	if inv.Installations != 1 || inv.HasDuplicates || inv.RunningShadowed {
		t.Fatalf("unexpected inventory: %#v", inv)
	}
	if inv.SelectedPath != running || inv.CanonicalPath != resolvePath(running) {
		t.Fatalf("selected/canonical = %q/%q, want %q/%q", inv.SelectedPath, inv.CanonicalPath, running, resolvePath(running))
	}
}

func TestInspectRCInstallationsDeduplicatesSymlinksToSameBinary(t *testing.T) {
	root := t.TempDir()
	running := executableFile(t, filepath.Join(root, "install", "rc"))
	first := symlinkFile(t, running, filepath.Join(root, "first", "rc"))
	second := symlinkFile(t, running, filepath.Join(root, "second", "rc"))
	pathValue := strings.Join([]string{filepath.Dir(first), filepath.Dir(second)}, string(os.PathListSeparator))

	inv := inspectRCInstallations(running, pathValue, "linux")
	if inv.SelectedPath != first {
		t.Fatalf("selected = %q, want first PATH entry %q", inv.SelectedPath, first)
	}
	if inv.Installations != 1 || inv.HasDuplicates {
		t.Fatalf("same resolved binary counted more than once: %#v", inv)
	}
	if inv.RunningShadowed {
		t.Fatal("selected symlink resolves to the running binary; must not report shadowing")
	}
}

func TestInspectRCInstallationsIncludesRunningBinaryOutsidePATH(t *testing.T) {
	root := t.TempDir()
	selected := executableFile(t, filepath.Join(root, "selected", "rc"))
	running := executableFile(t, filepath.Join(root, "running", "rc"))

	inv := inspectRCInstallations(running, filepath.Dir(selected), "linux")
	if len(inv.Paths) != 2 || !inv.Paths[1].Running {
		t.Fatalf("running binary missing from inventory: %#v", inv.Paths)
	}
	if !inv.RunningShadowed || !inv.HasDuplicates {
		t.Fatalf("absolute invocation of non-selected copy must be diagnosed: %#v", inv)
	}
}

func TestInspectRCInstallationsTreatsSelectedMiseShimAsShadowingAbsoluteHomebrew(t *testing.T) {
	root := t.TempDir()
	mise := executableFile(t, filepath.Join(root, "bin", "mise"))
	shim := symlinkFile(t, mise, filepath.Join(root, "mise", "shims", "rc"))
	brewBinary := executableFile(t, filepath.Join(root, "homebrew", "Caskroom", "rc", "1.1.3", "rc"))
	brewLink := symlinkFile(t, brewBinary, filepath.Join(root, "homebrew", "bin", "rc"))
	pathValue := strings.Join([]string{filepath.Dir(shim), filepath.Dir(brewLink)}, string(os.PathListSeparator))

	inv := inspectRCInstallations(brewBinary, pathValue, "darwin")
	if !inv.RunningShadowed {
		t.Fatalf("absolute Homebrew invocation must not prove selected shim dispatch: %#v", inv)
	}
}

func TestClassifyInstallPath(t *testing.T) {
	cases := []struct {
		path, resolved string
		want           installKind
	}{
		{"/opt/homebrew/bin/rc", "/opt/homebrew/Caskroom/rc/1.1.3/rc", installHomebrew},
		{"/Users/pj/.local/share/mise/shims/rc", "/opt/homebrew/bin/mise", installMiseShim},
		{"/Users/pj/.local/share/mise/installs/go/1.25.9/bin/rc", "/Users/pj/.local/share/mise/installs/go/1.25.9/bin/rc", installGo},
		{`C:\Users\pj\go\bin\rc.exe`, `C:\Users\pj\go\bin\rc.exe`, installGo},
		{"/Users/pj/.local/bin/rc", "/Users/pj/.local/bin/rc", installStandalone},
	}
	for _, tc := range cases {
		if got := classifyInstallPath(tc.path, tc.resolved); got != tc.want {
			t.Errorf("classifyInstallPath(%q, %q) = %q, want %q", tc.path, tc.resolved, got, tc.want)
		}
	}
}

func executableFile(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("test"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func symlinkFile(t *testing.T, target, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	return path
}
