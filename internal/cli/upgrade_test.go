package cli

import "testing"

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
		{"weird", "0.5.1", -1},    // unparseable current → assume update available
		{"0.5.1", "0.5.1-rc1", 0}, // pre-release suffix tolerated; same numeric patch → no downgrade
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
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
