package cli

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// `rc self update` replaces the binary with the latest GitHub release for the
// running OS/arch, so non-Homebrew installs (Linux/WSL/Windows, the install.sh / install.ps1 path) get
// the same one-command update as `brew upgrade rc` — no need to re-paste the install URL. When rc was
// installed via Homebrew it refuses and points at `brew update && brew upgrade rc`, so it never fights brew's
// bookkeeping (a self-overwrite would leave the cask's manifest pointing at a binary it no longer owns).
const (
	ghRepo      = "rootcause-org/rootcause-cli"
	ghLatestAPI = "https://api.github.com/repos/" + ghRepo + "/releases/latest"
	ghDownload  = "https://github.com/" + ghRepo + "/releases/download" // /<tag>/<asset>
)

func newSelfUpdateCmd(e *env, version string) *cobra.Command {
	var checkOnly bool
	var migrate bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update rc to the latest release (self-update)",
		Long: "Update rc to the latest GitHub release for this OS/arch.\n\n" +
			"On Linux/WSL/Windows this replaces one unambiguous running binary in place. On macOS it\n" +
			"updates the canonical Homebrew cask; use --migrate to canonicalize a mixed installation.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runSelfUpdate(e, version, checkOnly, migrate)
		},
	}
	cmd.Flags().BoolVar(&checkOnly, "check", false, "only report whether a newer version exists; install nothing")
	cmd.Flags().BoolVar(&migrate, "migrate", false, "on macOS, canonicalize onto Homebrew and remove verified legacy Go installs")
	return cmd
}

func runSelfUpdate(e *env, current string, checkOnly, migrate bool) error {
	running, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot locate the running rc binary: %w", err)
	}
	inv := inspectRCInstallations(running, os.Getenv("PATH"), runtime.GOOS)
	latest, err := latestReleaseTag(e.ctx())
	if err != nil {
		return err
	}

	if checkOnly {
		renderUpdateCheck(e, current, latest, inv)
		return nil
	}

	if migrate {
		if runtime.GOOS != "darwin" {
			return fmt.Errorf("--migrate is only for the canonical Homebrew installation on macOS")
		}
		return migrateToHomebrew(e, latest, inv)
	}

	if runtime.GOOS == "darwin" {
		runningInstall := findRunningInstall(inv.Paths)
		if runningInstall.Kind != installHomebrew || inv.HasDuplicates || inv.RunningShadowed {
			return fmt.Errorf("macOS uses one canonical Homebrew rc; run `rc self update --migrate` (inspect first with `rc self doctor`)")
		}
		if err := updateHomebrew(e); err != nil {
			return err
		}
		canonical, gotVersion, err := verifyHomebrewLatest(e.ctx(), latest)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(e.out, "Homebrew rc updated and verified (%s, %s); run `rc self doctor` to verify PATH\n", normVersion(gotVersion), canonical)
		return nil
	}

	if inv.HasDuplicates || inv.RunningShadowed {
		return fmt.Errorf("refusing to update a shadowed or duplicate binary; run `rc self doctor` and keep one rc on PATH")
	}
	if compareVersions(current, latest) >= 0 {
		_, _ = fmt.Fprintf(e.out, "rc is already up to date (%s)\n", normVersion(current))
		return nil
	}

	_, _ = fmt.Fprintf(e.err, "==> downloading rc %s for %s/%s\n", normVersion(latest), runtime.GOOS, runtime.GOARCH)
	bin, err := fetchReleaseBinary(e.ctx(), latest, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	if err := replaceBinary(inv.RunningPath, bin); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(e.out, "upgraded rc → %s (%s)\n", normVersion(latest), inv.RunningPath)
	return nil
}

func renderUpdateCheck(e *env, current, latest string, inv rcInstallInventory) {
	running := findRunningInstall(inv.Paths)
	_, _ = fmt.Fprintf(e.out, "running rc: %s (%s, %s)\n", inv.RunningPath, running.Kind, normVersion(current))
	if compareVersions(current, latest) < 0 {
		_, _ = fmt.Fprintf(e.out, "latest rc:  %s (update available)\n", normVersion(latest))
	} else {
		_, _ = fmt.Fprintf(e.out, "latest rc:  %s (up to date)\n", normVersion(latest))
	}
	if inv.HasDuplicates || inv.RunningShadowed {
		_, _ = fmt.Fprintf(e.out, "installation problem: %d distinct binaries; run `rc self doctor`\n", inv.Installations)
		if runtime.GOOS == "darwin" {
			_, _ = fmt.Fprintln(e.out, "migration: rc self update --migrate")
		}
		return
	}
	if runtime.GOOS == "darwin" && running.Kind != installHomebrew {
		_, _ = fmt.Fprintln(e.out, "installation problem: macOS canonical install is Homebrew")
		_, _ = fmt.Fprintln(e.out, "migration: rc self update --migrate")
	}
}

func updateHomebrew(e *env) error {
	brew, err := exec.LookPath("brew")
	if err != nil {
		return fmt.Errorf("Homebrew is required for the canonical macOS rc: %w", err)
	}
	if err := runUpgradeCommand(e, brew, "update"); err != nil {
		return err
	}
	installed := exec.CommandContext(e.ctx(), brew, "list", "--cask", "rc").Run() == nil
	if installed {
		if err := runUpgradeCommand(e, brew, "upgrade", "--cask", "rc"); err != nil {
			return err
		}
	} else if err := runUpgradeCommand(e, brew, "install", "--cask", "rootcause-org/tap/rc"); err != nil {
		return err
	}
	return nil
}

func runUpgradeCommand(e *env, name string, args ...string) error {
	_, _ = fmt.Fprintf(e.err, "==> %s %s\n", filepath.Base(name), strings.Join(args, " "))
	cmd := exec.CommandContext(e.ctx(), name, args...)
	cmd.Stdout = e.out
	cmd.Stderr = e.err
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", filepath.Base(name), strings.Join(args, " "), err)
	}
	return nil
}

func migrateToHomebrew(e *env, latest string, inv rcInstallInventory) error {
	if err := updateHomebrew(e); err != nil {
		return err
	}
	canonical, gotVersion, err := verifyHomebrewLatest(e.ctx(), latest)
	if err != nil {
		return err
	}
	brew, err := exec.LookPath("brew")
	if err != nil {
		return err
	}
	prefixCmd := exec.CommandContext(e.ctx(), brew, "--prefix")
	prefix, err := prefixCmd.Output()
	if err != nil {
		return fmt.Errorf("brew --prefix: %w", err)
	}
	canonicalLink := filepath.Join(strings.TrimSpace(string(prefix)), "bin", binaryName(runtime.GOOS))

	removed, err := removeVerifiedLegacyGoBinaries(legacyGoBinaryCandidates(inv), canonical, isRootcauseGoBinary, os.Remove)
	if err != nil {
		return err
	}
	for _, path := range removed {
		_, _ = fmt.Fprintf(e.out, "removed legacy Go rc: %s\n", path)
	}
	if len(removed) > 0 {
		if mise, lookErr := exec.LookPath("mise"); lookErr == nil {
			if err := runUpgradeCommand(e, mise, "reshim"); err != nil {
				return err
			}
		}
	}

	after := inspectRCInstallations(canonical, os.Getenv("PATH"), runtime.GOOS)
	selected := findSelectedInstall(after.Paths)
	if selected == nil || selected.Shim || !samePath(selected.ResolvedPath, canonical) || after.Installations != 1 {
		return fmt.Errorf("Homebrew rc %s is verified, but another rc still shadows or duplicates it; run `%s self doctor` and remove only the reported unknown install", canonical, canonicalLink)
	}
	_, _ = fmt.Fprintf(e.out, "rc is canonical on Homebrew (%s, %s)\n", normVersion(gotVersion), canonical)
	return nil
}

func verifyHomebrewLatest(ctx context.Context, latest string) (string, string, error) {
	brew, err := exec.LookPath("brew")
	if err != nil {
		return "", "", err
	}
	prefixCmd := exec.CommandContext(ctx, brew, "--prefix")
	prefix, err := prefixCmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("brew --prefix: %w", err)
	}
	canonicalLink := filepath.Join(strings.TrimSpace(string(prefix)), "bin", binaryName(runtime.GOOS))
	canonical := resolvePath(canonicalLink)
	gotVersion, err := installedBinaryVersion(ctx, canonicalLink)
	if err != nil {
		return "", "", fmt.Errorf("verifying Homebrew rc at %s: %w", canonicalLink, err)
	}
	if compareVersions(gotVersion, latest) != 0 {
		return "", "", fmt.Errorf("Homebrew rc is %s after upgrade, but GitHub latest is %s; refusing to continue", normVersion(gotVersion), normVersion(latest))
	}
	return canonical, gotVersion, nil
}

func installedBinaryVersion(ctx context.Context, path string) (string, error) {
	cmd := exec.CommandContext(ctx, path, "--version")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", fmt.Errorf("empty --version output")
	}
	return fields[len(fields)-1], nil
}

func legacyGoBinaryCandidates(inv rcInstallInventory) []string {
	paths := make([]string, 0)
	for _, candidate := range inv.Paths {
		// Build metadata is the authority. Include every non-Homebrew physical candidate here so a
		// custom GOBIN (for example ~/bin) is migratable; removeVerifiedLegacyGoBinaries filters out
		// symlinks, the protected cask, and anything not proven to be rootcause-cli.
		if candidate.Kind != installHomebrew && !candidate.Shim {
			paths = append(paths, candidate.ResolvedPath)
		}
	}
	home, _ := os.UserHomeDir()
	miseData := os.Getenv("MISE_DATA_DIR")
	if miseData == "" && home != "" {
		miseData = filepath.Join(home, ".local", "share", "mise")
	}
	if miseData != "" {
		matches, _ := filepath.Glob(filepath.Join(miseData, "installs", "go", "*", "bin", binaryName(runtime.GOOS)))
		paths = append(paths, matches...)
	}
	if goBin := os.Getenv("GOBIN"); goBin != "" {
		paths = append(paths, filepath.Join(goBin, binaryName(runtime.GOOS)))
	}
	goPath := os.Getenv("GOPATH")
	if goPath == "" && home != "" {
		goPath = filepath.Join(home, "go")
	}
	for _, root := range filepath.SplitList(goPath) {
		if root != "" {
			paths = append(paths, filepath.Join(root, "bin", binaryName(runtime.GOOS)))
		}
	}
	return paths
}

func isRootcauseGoBinary(path string) bool {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return false
	}
	return info.Path == "github.com/rootcause-org/rootcause-cli/cmd/rc" ||
		info.Main.Path == "github.com/rootcause-org/rootcause-cli"
}

func removeVerifiedLegacyGoBinaries(paths []string, protected string, verified func(string) bool, remove func(string) error) ([]string, error) {
	seen := make(map[string]bool)
	removed := make([]string, 0)
	protected = resolvePath(protected)
	for _, candidate := range paths {
		candidate = absoluteClean(candidate)
		info, err := os.Lstat(candidate)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}
		resolved := resolvePath(candidate)
		if samePath(resolved, protected) || seen[resolved] || !verified(resolved) {
			continue
		}
		seen[resolved] = true
		if err := remove(candidate); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return removed, fmt.Errorf("removing verified legacy Go rc %s: %w", candidate, err)
		}
		removed = append(removed, candidate)
	}
	return removed, nil
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
	return latestReleaseTagAt(ctx, ghLatestAPI)
}

func latestReleaseTagAt(ctx context.Context, url string) (string, error) {
	resp, err := httpGet(ctx, url)
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
	parsed, ok := parseVersion(rel.TagName)
	if !ok || parsed.prerelease != "" || rel.TagName != fmt.Sprintf("v%d.%d.%d", parsed.major, parsed.minor, parsed.patch) {
		return "", fmt.Errorf("the latest release tag %q is not a stable vX.Y.Z version", rel.TagName)
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

// compareVersions compares semantic versions (leading "v" optional). Malformed current versions are
// conservatively older; latestReleaseTag separately rejects malformed published tags.
func compareVersions(a, b string) int {
	if normVersion(a) == normVersion(b) {
		return 0
	}
	if strings.Contains(a, " (go install)") || strings.HasPrefix(a, "devel (") {
		return -1
	}
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	if !oka || !okb {
		return -1 // can't parse → assume an update is available
	}
	av := []int{pa.major, pa.minor, pa.patch}
	bv := []int{pb.major, pb.minor, pb.patch}
	for i := range av {
		if av[i] != bv[i] {
			if av[i] < bv[i] {
				return -1
			}
			return 1
		}
	}
	return comparePrerelease(pa.prerelease, pb.prerelease)
}

type semanticVersion struct {
	major, minor, patch int
	prerelease          string
}

func parseVersion(v string) (semanticVersion, bool) {
	var out semanticVersion
	v = normVersion(v)
	if strings.Count(v, "+") > 1 {
		return out, false
	}
	v, _, _ = strings.Cut(v, "+")
	core, prerelease, hasPrerelease := strings.Cut(v, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return out, false
	}
	values := []*int{&out.major, &out.minor, &out.patch}
	for i, p := range parts {
		if p == "" || len(p) > 1 && p[0] == '0' {
			return out, false
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		*values[i] = n
	}
	if hasPrerelease {
		if prerelease == "" {
			return out, false
		}
		for _, id := range strings.Split(prerelease, ".") {
			if id == "" || !validSemverIdentifier(id) {
				return out, false
			}
		}
		out.prerelease = prerelease
	}
	return out, true
}

func validSemverIdentifier(s string) bool {
	for _, r := range s {
		if r != '-' && (r < '0' || r > '9') && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
			return false
		}
	}
	return true
}

func comparePrerelease(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return 1
	}
	if b == "" {
		return -1
	}
	aa, bb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(aa) && i < len(bb); i++ {
		if aa[i] == bb[i] {
			continue
		}
		an, aerr := strconv.Atoi(aa[i])
		bn, berr := strconv.Atoi(bb[i])
		switch {
		case aerr == nil && berr == nil:
			if an < bn {
				return -1
			}
			return 1
		case aerr == nil:
			return -1
		case berr == nil:
			return 1
		case aa[i] < bb[i]:
			return -1
		default:
			return 1
		}
	}
	if len(aa) < len(bb) {
		return -1
	}
	return 1
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
