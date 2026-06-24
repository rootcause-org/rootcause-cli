package cli

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// `rc upgrade` is self-update: the binary replaces itself with the latest GitHub release for the
// running OS/arch, so non-Homebrew installs (Linux/WSL/Windows, the install.sh / install.ps1 path) get
// the same one-command update as `brew upgrade rc` — no need to re-paste the install URL. When rc was
// installed via Homebrew it refuses and points at `brew update && brew upgrade rc`, so it never fights brew's
// bookkeeping (a self-overwrite would leave the cask's manifest pointing at a binary it no longer owns).
const (
	ghRepo      = "rootcause-org/rootcause-cli"
	ghLatestAPI = "https://api.github.com/repos/" + ghRepo + "/releases/latest"
	ghDownload  = "https://github.com/" + ghRepo + "/releases/download" // /<tag>/<asset>
)

func newUpgradeCmd(e *env, version string) *cobra.Command {
	var checkOnly bool
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Update rc to the latest release (self-update)",
		Long: "Update rc to the latest GitHub release for this OS/arch.\n\n" +
			"On Linux/WSL/Windows this replaces the running binary in place. On a Homebrew install it\n" +
			"defers to `brew upgrade rc` instead, so it doesn't fight Homebrew's bookkeeping.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runUpgrade(e, version, checkOnly)
		},
	}
	cmd.Flags().BoolVar(&checkOnly, "check", false, "only report whether a newer version exists; install nothing")
	return cmd
}

func runUpgrade(e *env, current string, checkOnly bool) error {
	latest, err := latestReleaseTag(e.ctx())
	if err != nil {
		return err
	}
	// Compare without the leading "v" — main.version is injected as "0.5.1", the tag is "v0.5.1".
	if compareVersions(current, latest) >= 0 {
		_, _ = fmt.Fprintf(e.out, "rc is already up to date (%s)\n", normVersion(current))
		return nil
	}

	if checkOnly {
		_, _ = fmt.Fprintf(e.out, "a newer rc is available: %s → %s\n  run: rc upgrade\n", normVersion(current), normVersion(latest))
		return nil
	}

	// Resolve the real binary path (follow the symlink Homebrew/installers may leave on PATH).
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot locate the running rc binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if isHomebrewManaged(exe) {
		// `brew update` FIRST, always: we already know (from the GitHub releases API) that a newer
		// version exists, so the only reason a bare `brew upgrade rc` would say "already latest" is a
		// stale local tap clone — Homebrew's auto-update refreshes the core JSON API but can skip a
		// git tap. Pairing the two means a stale tap can never mask a release we just detected.
		_, _ = fmt.Fprintf(e.out, "a newer rc is available: %s → %s\n"+
			"  this rc was installed with Homebrew — upgrade with: brew update && brew upgrade rc\n", normVersion(current), normVersion(latest))
		return nil
	}

	_, _ = fmt.Fprintf(e.err, "==> downloading rc %s for %s/%s\n", normVersion(latest), runtime.GOOS, runtime.GOARCH)
	bin, err := fetchReleaseBinary(e.ctx(), latest, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	if err := replaceBinary(exe, bin); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(e.out, "upgraded rc → %s (%s)\n", normVersion(latest), exe)
	return nil
}

// --- GitHub release resolution ----------------------------------------------------------------------

func httpGet(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "rootcause-cli")
	client := &http.Client{Timeout: 60 * time.Second}
	return client.Do(req)
}

func latestReleaseTag(ctx context.Context) (string, error) {
	resp, err := httpGet(ctx, ghLatestAPI)
	if err != nil {
		return "", fmt.Errorf("checking the latest release: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checking the latest release: GitHub API returned HTTP %d", resp.StatusCode)
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", fmt.Errorf("decoding the latest release: %w", err)
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("the latest release has no tag_name")
	}
	return rel.TagName, nil
}

// fetchReleaseBinary downloads the archive for goos/goarch, verifies its sha256 against the release's
// checksums.txt, and returns the extracted rc (or rc.exe) bytes.
func fetchReleaseBinary(ctx context.Context, tag, goos, goarch string) ([]byte, error) {
	asset := assetName(normVersion(tag), goos, goarch)

	sums, err := downloadBytes(ctx, fmt.Sprintf("%s/%s/checksums.txt", ghDownload, tag))
	if err != nil {
		return nil, fmt.Errorf("downloading checksums: %w", err)
	}
	want, err := sha256FromChecksums(sums, asset)
	if err != nil {
		return nil, err
	}

	archive, err := downloadBytes(ctx, fmt.Sprintf("%s/%s/%s", ghDownload, tag, asset))
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", asset, err)
	}
	got := sha256.Sum256(archive)
	if hex.EncodeToString(got[:]) != want {
		return nil, fmt.Errorf("checksum mismatch for %s — refusing to install", asset)
	}

	bin, err := extractBinary(archive, goos)
	if err != nil {
		return nil, fmt.Errorf("extracting rc from %s: %w", asset, err)
	}
	return bin, nil
}

func downloadBytes(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpGet(ctx, url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

// --- pure helpers (unit-tested) ---------------------------------------------------------------------

// normVersion strips a single leading "v" so "v0.5.1" and "0.5.1" compare equal.
func normVersion(v string) string { return strings.TrimPrefix(v, "v") }

// assetName is the GoReleaser archive name: rc_<ver>_<os>_<arch>.tar.gz (zip on Windows).
func assetName(version, goos, goarch string) string {
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("rc_%s_%s_%s.%s", version, goos, goarch, ext)
}

// binaryName is what the archive holds for this OS.
func binaryName(goos string) string {
	if goos == "windows" {
		return "rc.exe"
	}
	return "rc"
}

// sha256FromChecksums finds the hex digest for asset in a `<sha>␠␠<name>` checksums.txt.
func sha256FromChecksums(data []byte, asset string) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == asset {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("no checksum for %s in checksums.txt", asset)
}

// isHomebrewManaged reports whether the resolved binary path lives inside a Homebrew prefix — a cask
// (Caskroom) or, for legacy/Linuxbrew, a formula (Cellar). Such installs must upgrade via brew.
func isHomebrewManaged(path string) bool {
	return strings.Contains(path, "/Caskroom/") || strings.Contains(path, "/Cellar/")
}

// compareVersions compares dotted numeric versions (leading "v" optional). Returns -1 if a<b, 0 if
// equal, 1 if a>b. Non-numeric or malformed parts make it conservative: it treats unequal strings as
// "a is older" (compare returns -1) so a dev build always sees an upgrade rather than wrongly skipping.
func compareVersions(a, b string) int {
	if normVersion(a) == normVersion(b) {
		return 0
	}
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	if !oka || !okb {
		return -1 // can't parse → assume an update is available
	}
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.SplitN(normVersion(v), ".", 3)
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		// Tolerate a pre-release/build suffix on the patch (e.g. "1-rc1") by cutting at the first non-digit.
		n, err := strconv.Atoi(leadingDigits(p))
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func leadingDigits(s string) string {
	for i, r := range s {
		if r < '0' || r > '9' {
			return s[:i]
		}
	}
	return s
}

// --- archive extraction + atomic replace ------------------------------------------------------------

func extractBinary(archive []byte, goos string) ([]byte, error) {
	want := binaryName(goos)
	if goos == "windows" {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if filepath.Base(f.Name) == want {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer func() { _ = rc.Close() }()
				return io.ReadAll(rc)
			}
		}
		return nil, fmt.Errorf("%s not found in archive", want)
	}

	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(hdr.Name) == want {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("%s not found in archive", want)
}

// replaceBinary writes data over the binary at path atomically: a temp file in the SAME directory (so
// the rename is atomic, not a cross-device copy), then rename into place. On Windows a running .exe
// can't be overwritten, so the current one is moved aside first.
func replaceBinary(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".rc-upgrade-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s (need write permission there; try sudo or RC_INSTALL_DIR): %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once renamed away; cleans up on any error path
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}

	if runtime.GOOS == "windows" {
		old := path + ".old"
		_ = os.Remove(old)
		if err := os.Rename(path, old); err != nil {
			return err
		}
		if err := os.Rename(tmpName, path); err != nil {
			_ = os.Rename(old, path) // best-effort rollback
			return err
		}
		_ = os.Remove(old) // fails while the old exe is still running; harmless to leave
		return nil
	}
	return os.Rename(tmpName, path)
}
