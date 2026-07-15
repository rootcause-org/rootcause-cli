package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.5.1", "0.5.1", 0},
		{"v0.5.1", "0.5.1", 0}, // leading v is ignored
		{"0.5.0", "0.5.1", -1},
		{"0.5.1", "0.5.0", 1},
		{"0.4.9", "0.5.0", -1},
		{"1.0.0", "0.9.9", 1},
		{"0.10.0", "0.9.0", 1},  // numeric, not lexical
		{"0.1.0", "v0.5.1", -1}, // default dev build sees an upgrade
		{"v1.1.3 (go install)", "v1.1.3", -1},
		{"devel (abcdef123456)", "v1.1.3", -1},
		{"weird", "0.5.1", -1}, // unparseable current → assume update available
		{"0.5.1-rc1", "0.5.1", -1},
		{"0.5.1", "0.5.1-rc1", 1},
		{"1.2.3-alpha.2", "1.2.3-alpha.10", -1},
		{"1.2.3+build.1", "1.2.3+build.2", 0},
		{"1.2.3.4", "1.2.4", -1}, // malformed current → assume update available
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestParseVersionRejectsMalformedValues(t *testing.T) {
	for _, version := range []string{"1.2", "1.2.3.4", "1.02.3", "1.2.x", "1.2.3-", "v"} {
		if _, ok := parseVersion(version); ok {
			t.Errorf("parseVersion(%q) succeeded, want rejection", version)
		}
	}
}

func TestAssetName(t *testing.T) {
	cases := []struct {
		goos, goarch, want string
	}{
		{"darwin", "arm64", "rc_0.5.1_darwin_arm64.tar.gz"},
		{"linux", "amd64", "rc_0.5.1_linux_amd64.tar.gz"},
		{"windows", "amd64", "rc_0.5.1_windows_amd64.zip"},
	}
	for _, c := range cases {
		if got := assetName("0.5.1", c.goos, c.goarch); got != c.want {
			t.Errorf("assetName(0.5.1,%q,%q) = %q, want %q", c.goos, c.goarch, got, c.want)
		}
	}
}

func TestBinaryName(t *testing.T) {
	if got := binaryName("windows"); got != "rc.exe" {
		t.Errorf("binaryName(windows) = %q, want rc.exe", got)
	}
	if got := binaryName("linux"); got != "rc" {
		t.Errorf("binaryName(linux) = %q, want rc", got)
	}
}

func TestSha256FromChecksums(t *testing.T) {
	data := []byte(
		"aaa111  rc_0.5.1_darwin_amd64.tar.gz\n" +
			"bbb222  rc_0.5.1_darwin_arm64.tar.gz\n" +
			"ccc333  rc_0.5.1_linux_amd64.tar.gz\n")
	got, err := sha256FromChecksums(data, "rc_0.5.1_darwin_arm64.tar.gz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "bbb222" {
		t.Errorf("got %q, want bbb222", got)
	}
	if _, err := sha256FromChecksums(data, "rc_0.5.1_windows_arm64.zip"); err == nil {
		t.Error("expected an error for a missing asset, got nil")
	}
}

func TestIsHomebrewManaged(t *testing.T) {
	managed := []string{
		"/opt/homebrew/Caskroom/rc/0.5.1/rc",
		"/usr/local/Cellar/rc/0.5.0/bin/rc",
		"/home/linuxbrew/.linuxbrew/Cellar/rc/0.5.0/bin/rc",
	}
	for _, p := range managed {
		if !isHomebrewManaged(p) {
			t.Errorf("isHomebrewManaged(%q) = false, want true", p)
		}
	}
	unmanaged := []string{
		"/usr/local/bin/rc",
		"/home/pj/.local/bin/rc",
		"C:\\Users\\pj\\AppData\\Local\\Programs\\rc\\rc.exe",
	}
	for _, p := range unmanaged {
		if isHomebrewManaged(p) {
			t.Errorf("isHomebrewManaged(%q) = true, want false", p)
		}
	}
}

func TestRemoveVerifiedLegacyGoBinariesIsSafeAndIdempotent(t *testing.T) {
	root := t.TempDir()
	verified := filepath.Join(root, "go", "bin", "rc")
	unknown := filepath.Join(root, "other", "rc")
	for _, path := range []string{verified, unknown} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("binary"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	verifiedResolved := resolvePath(verified)
	isVerified := func(path string) bool { return path == verifiedResolved }

	removed, err := removeVerifiedLegacyGoBinaries([]string{verified, verified, unknown}, "", isVerified, os.Remove)
	if err != nil {
		t.Fatal(err)
	}
	verifiedCandidate := absoluteClean(verified)
	if len(removed) != 1 || removed[0] != verifiedCandidate {
		t.Fatalf("removed = %v, want [%s]", removed, verifiedCandidate)
	}
	if _, err := os.Stat(unknown); err != nil {
		t.Fatalf("unknown binary was touched: %v", err)
	}
	removed, err = removeVerifiedLegacyGoBinaries([]string{verified, unknown}, "", isVerified, os.Remove)
	if err != nil || len(removed) != 0 {
		t.Fatalf("second migration = %v, %v; want idempotent no-op", removed, err)
	}
}

func TestLegacyGoBinaryCandidatesIncludesCustomInstallPath(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "custom-bin", "rc")
	inv := rcInstallInventory{Paths: []rcInstallPath{{Path: custom, ResolvedPath: custom, Kind: installStandalone}}}
	candidates := legacyGoBinaryCandidates(inv)
	found := false
	for _, candidate := range candidates {
		if candidate == custom {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("custom install missing from migration candidates: %v", candidates)
	}
}

func TestRemoveVerifiedLegacyGoBinariesSurfacesRemovalFailure(t *testing.T) {
	want := errors.New("denied")
	path := executableFile(t, filepath.Join(t.TempDir(), "go", "bin", "rc"))
	_, err := removeVerifiedLegacyGoBinaries([]string{path}, "", func(string) bool { return true }, func(string) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want wrapped removal failure", err)
	}
}

func TestRemoveVerifiedLegacyGoBinariesNeverFollowsSymlinkOrRemovesProtectedBinary(t *testing.T) {
	root := t.TempDir()
	canonical := executableFile(t, filepath.Join(root, "Caskroom", "rc", "1.1.3", "rc"))
	link := symlinkFile(t, canonical, filepath.Join(root, "go", "bin", "rc"))

	removed, err := removeVerifiedLegacyGoBinaries([]string{link, canonical}, canonical, func(string) bool { return true }, os.Remove)
	if err != nil || len(removed) != 0 {
		t.Fatalf("removed protected/symlink candidate: %v, %v", removed, err)
	}
	if _, err := os.Stat(canonical); err != nil {
		t.Fatalf("canonical binary was removed: %v", err)
	}
}

func TestRenderUpdateCheckReportsAlreadyLatestDuplicates(t *testing.T) {
	running := executableFile(t, filepath.Join(t.TempDir(), "go", "bin", "rc"))
	other := executableFile(t, filepath.Join(t.TempDir(), "bin", "rc"))
	inv := inspectRCInstallations(running, strings.Join([]string{filepath.Dir(running), filepath.Dir(other)}, string(os.PathListSeparator)), "linux")
	var out bytes.Buffer
	renderUpdateCheck(&env{out: &out}, "1.1.3", "v1.1.3", inv)
	got := out.String()
	if !strings.Contains(got, "up to date") || !strings.Contains(got, "installation problem: 2 distinct binaries") {
		t.Fatalf("check hid duplicate at latest version:\n%s", got)
	}
}

func TestVerifyHomebrewLatestChecksCanonicalVersion(t *testing.T) {
	root := t.TempDir()
	brew := filepath.Join(root, "bin", "brew")
	rc := filepath.Join(root, "bin", binaryName("darwin"))
	if err := os.MkdirAll(filepath.Dir(brew), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(brew, []byte("#!/bin/sh\nprefix=${0%/bin/brew}\n[ \"$1\" = --prefix ] && printf '%s\\n' \"$prefix\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rc, []byte("#!/bin/sh\nprintf 'rc version 1.1.4\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(brew))
	canonical, version, err := verifyHomebrewLatest(context.Background(), "v1.1.4")
	if err != nil || version != "1.1.4" || canonical != resolvePath(rc) {
		t.Fatalf("verify = %q, %q, %v", canonical, version, err)
	}
	if _, _, err := verifyHomebrewLatest(context.Background(), "v1.1.5"); err == nil {
		t.Fatal("expected stale Homebrew version to fail verification")
	}
}
