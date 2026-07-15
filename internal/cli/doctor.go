package cli

import (
	"context"
	debugbuildinfo "debug/buildinfo"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

const rcCommandPath = "github.com/rootcause-org/rootcause-cli/cmd/rc"

type binaryInfo struct {
	Path           string `json:"path"`
	ResolvedPath   string `json:"resolved_path,omitempty"`
	Version        string `json:"version"`
	ModuleVersion  string `json:"module_version,omitempty"`
	LDFlagsVersion string `json:"ldflags_version,omitempty"`
	Install        string `json:"install"`
	Active         bool   `json:"active"`
	Dispatcher     bool   `json:"dispatcher,omitempty"`
	Note           string `json:"note,omitempty"`
	Hint           string `json:"remediation,omitempty"`
	ReadError      string `json:"read_error,omitempty"`
}

type doctorFinding struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
	Hint    string `json:"remediation,omitempty"`
}

type doctorScope struct {
	Profile       string `json:"profile"`
	Project       string `json:"project"`
	ProjectSource string `json:"project_source,omitempty"`
	Tenant        string `json:"tenant"`
	TenantSource  string `json:"tenant_source,omitempty"`
	BaseURL       string `json:"base_url"`
	BaseURLSource string `json:"base_url_source"`
	LoginNote     string `json:"login_scope_note,omitempty"`
}

type doctorUpdate struct {
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
	Available bool   `json:"available"`
	Note      string `json:"note,omitempty"`
}

type doctorReport struct {
	Binary   binaryInfo      `json:"binary"`
	Path     []binaryInfo    `json:"path"`
	Scope    doctorScope     `json:"scope"`
	Update   doctorUpdate    `json:"update"`
	Findings []doctorFinding `json:"findings"`
}

type doctorFindingsError struct{ count int }

func (e doctorFindingsError) Error() string {
	return fmt.Sprintf("doctor found %d PATH issue(s)", e.count)
}

func newSelfDoctorCmd(e *env, version string) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose the active rc install, PATH copies, scope, and updates",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			report, err := collectDoctorReport(e, version)
			if err != nil {
				return err
			}
			if e.jsonOut() {
				err = writeJSON(e, report)
			} else {
				err = renderDoctorHuman(e.out, report)
			}
			if err != nil {
				return err
			}
			if len(report.Findings) > 0 {
				return doctorFindingsError{count: len(report.Findings)}
			}
			return nil
		},
	}
}

func collectDoctorReport(e *env, version string) (doctorReport, error) {
	current, err := currentBinaryInfo(version)
	if err != nil {
		return doctorReport{}, err
	}
	pathCopies := scanPathBinaries(os.Getenv("PATH"), current, runtime.GOOS)
	findings := analyzePathBinaries(pathCopies)

	scope, err := e.resolveScope(false)
	if err != nil {
		return doctorReport{}, err
	}
	doctorScope := doctorScope{
		Profile: scope.Profile, Project: scope.Project, ProjectSource: scope.ProjectSource,
		Tenant: scope.Tenant, TenantSource: scope.TenantSource,
		BaseURL: scope.BaseURL, BaseURLSource: scope.BaseURLSource,
	}
	if scope.LoginScopeError != "" {
		doctorScope.LoginNote = "login scope unavailable: " + scope.LoginScopeError
	}

	update := doctorUpdate{Current: current.Version}
	ctx, cancel := context.WithTimeout(e.ctx(), 5*time.Second)
	defer cancel()
	latestFn := latestReleaseTag
	if e.latestRelease != nil {
		latestFn = e.latestRelease
	}
	latest, updateErr := latestFn(ctx)
	if updateErr != nil {
		update.Note = "update check skipped: " + updateErr.Error()
	} else {
		update.Latest = latest
		update.Available = compareVersions(current.Version, latest) < 0
	}

	return doctorReport{Binary: current, Path: pathCopies, Scope: doctorScope, Update: update, Findings: findings}, nil
}

func currentBinaryInfo(version string) (binaryInfo, error) {
	exe, err := os.Executable()
	if err != nil {
		return binaryInfo{}, fmt.Errorf("cannot locate the running rc binary: %w", err)
	}
	resolved := exe
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		resolved = real
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		bi = nil
	}
	info := binaryInfoFromBuildInfo(exe, resolved, bi)
	info.Version = version
	info.Hint = remediationHint(info)
	return info, nil
}

func binaryInfoFromBuildInfo(path, resolved string, bi *debug.BuildInfo) binaryInfo {
	info := binaryInfo{Path: path, Version: "unknown", Install: "unknown"}
	if resolved != "" && resolved != path {
		info.ResolvedPath = resolved
	}
	if bi == nil {
		return info
	}
	info.ModuleVersion = bi.Main.Version
	info.LDFlagsVersion = ldflagsVersion(bi)
	info.Version = resolveVersion(info.LDFlagsVersion, bi, true)
	info.Install = classifyInstall(resolved, info.LDFlagsVersion, bi)
	return info
}

func classifyInstall(path, injected string, bi *debug.BuildInfo) string {
	if isHomebrewManaged(path) {
		return "homebrew"
	}
	if injected != "" {
		return "release"
	}
	if bi != nil && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return "go-install"
	}
	if bi != nil {
		return "source"
	}
	return "unknown"
}

func ldflagsVersion(bi *debug.BuildInfo) string {
	if bi == nil {
		return ""
	}
	for _, setting := range bi.Settings {
		if setting.Key != "-ldflags" {
			continue
		}
		fields := strings.Fields(setting.Value)
		for i, field := range fields {
			candidate := strings.TrimPrefix(field, "-X=")
			if field == "-X" && i+1 < len(fields) {
				candidate = fields[i+1]
			}
			if value, ok := strings.CutPrefix(candidate, "main.version="); ok {
				return strings.Trim(value, "\"'")
			}
		}
	}
	return ""
}

func scanPathBinaries(pathEnv string, current binaryInfo, goos string) []binaryInfo {
	name := binaryName(goos)
	seen := make(map[string]bool)
	copies := make([]binaryInfo, 0)
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			dir = "."
		}
		candidate := filepath.Clean(filepath.Join(dir, name))
		if abs, err := filepath.Abs(candidate); err == nil {
			candidate = abs
		}
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		stat, err := os.Stat(candidate)
		if err != nil || stat.IsDir() || (goos != "windows" && stat.Mode().Perm()&0o111 == 0) {
			continue
		}

		active := len(copies) == 0
		if active && isMiseDispatcher(candidate) {
			if isMiseInstalledRC(current.Path) {
				info := current
				info.Path = candidate
				info.ResolvedPath = current.Path
				info.Active = true
				info.Dispatcher = true
				info.Note = "mise dispatcher; version is from the running rc executable"
				info.Hint = remediationHint(info)
				copies = append(copies, info)
				continue
			}
			info := readPathBinary(candidate)
			info.Active = true
			info.Dispatcher = true
			info.Note = "mise dispatcher; selected rc cannot be resolved safely from this invocation"
			copies = append(copies, info)
			continue
		}

		info := readPathBinary(candidate)
		info.Active = active
		info.Hint = remediationHint(info)
		copies = append(copies, info)
	}
	return copies
}

func readPathBinary(path string) binaryInfo {
	resolved := path
	if real, err := filepath.EvalSymlinks(path); err == nil {
		resolved = real
	}
	bi, err := debugbuildinfo.ReadFile(path)
	if err != nil {
		return binaryInfo{Path: path, ResolvedPath: differentPath(path, resolved), Version: "unknown", Install: "unknown", ReadError: err.Error()}
	}
	if bi.Path != rcCommandPath && bi.Main.Path != "github.com/rootcause-org/rootcause-cli" {
		return binaryInfo{Path: path, ResolvedPath: differentPath(path, resolved), Version: "unknown", Install: "unknown", ReadError: fmt.Sprintf("not a rootcause rc binary (build path %s)", bi.Path)}
	}
	return binaryInfoFromBuildInfo(path, resolved, bi)
}

func differentPath(path, resolved string) string {
	if path != resolved {
		return resolved
	}
	return ""
}

func isMiseDispatcher(path string) bool {
	if !strings.Contains(filepath.ToSlash(path), "/mise/shims/") {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	return err == nil && filepath.Base(resolved) == "mise"
}

func isMiseInstalledRC(path string) bool {
	return strings.Contains(filepath.ToSlash(path), "/mise/installs/go/")
}

// analyzePathBinaries is pure so findings and exit behavior are testable without touching PATH.
func analyzePathBinaries(copies []binaryInfo) []doctorFinding {
	var active *binaryInfo
	for i := range copies {
		if copies[i].Active {
			active = &copies[i]
			break
		}
	}
	findings := make([]doctorFinding, 0)
	if active == nil {
		return findings
	}
	if active.Install != "release" && active.Install != "homebrew" {
		findings = append(findings, doctorFinding{
			Code: "active_non_release", Path: active.Path,
			Message: fmt.Sprintf("active PATH copy is %s, not a release/homebrew build", active.Install), Hint: active.Hint,
		})
	}
	activeVersion := comparableBinaryVersion(*active)
	for _, copy := range copies {
		if copy.Active || comparableBinaryVersion(copy) == activeVersion {
			continue
		}
		findings = append(findings, doctorFinding{
			Code: "divergent_copy", Path: copy.Path,
			Message: fmt.Sprintf("shadowed copy reports %s; active reports %s", copy.Version, active.Version), Hint: copy.Hint,
		})
	}
	return findings
}

func comparableBinaryVersion(info binaryInfo) string {
	version := info.LDFlagsVersion
	if version == "" && info.ModuleVersion != "" && info.ModuleVersion != "(devel)" {
		version = info.ModuleVersion
	}
	if version == "" {
		version = info.Version
	}
	return normVersion(version)
}

func remediationHint(info binaryInfo) string {
	path := info.Path
	if info.Dispatcher && info.ResolvedPath != "" {
		path = info.ResolvedPath
	}
	slash := filepath.ToSlash(path)
	if strings.Contains(slash, "/mise/installs/go/") {
		return "rm " + strconv.Quote(path) + " && mise reshim"
	}
	if isGoBin(filepath.Dir(path)) {
		return "rm " + strconv.Quote(path)
	}
	if info.Install == "homebrew" && !info.Active {
		return "remove earlier stale PATH copies to activate this Homebrew install"
	}
	return ""
}

func isGoBin(dir string) bool {
	dir = filepath.Clean(dir)
	if gobin := os.Getenv("GOBIN"); gobin != "" && dir == filepath.Clean(gobin) {
		return true
	}
	for _, gopath := range filepath.SplitList(os.Getenv("GOPATH")) {
		if gopath != "" && dir == filepath.Join(filepath.Clean(gopath), "bin") {
			return true
		}
	}
	home, err := os.UserHomeDir()
	return err == nil && dir == filepath.Join(home, "go", "bin")
}

func renderDoctorHuman(w interface{ Write([]byte) (int, error) }, report doctorReport) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "This binary")
	_, _ = fmt.Fprintf(tw, "  path:\t%s\n", report.Binary.Path)
	if report.Binary.ResolvedPath != "" {
		_, _ = fmt.Fprintf(tw, "  resolved:\t%s\n", report.Binary.ResolvedPath)
	}
	_, _ = fmt.Fprintf(tw, "  version:\t%s\n", report.Binary.Version)
	_, _ = fmt.Fprintf(tw, "  module:\t%s\n", emptyDash(report.Binary.ModuleVersion))
	_, _ = fmt.Fprintf(tw, "  ldflags version:\t%s\n", emptyDash(report.Binary.LDFlagsVersion))
	_, _ = fmt.Fprintf(tw, "  install:\t%s\n\n", report.Binary.Install)

	_, _ = fmt.Fprintln(tw, "PATH scan")
	_, _ = fmt.Fprintln(tw, "  ACTIVE\tVERSION\tINSTALL\tPATH")
	for _, copy := range report.Path {
		active := ""
		if copy.Active {
			active = "yes"
		}
		_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", active, copy.Version, copy.Install, copy.Path)
		if copy.ResolvedPath != "" {
			_, _ = fmt.Fprintf(tw, "  \t\t\t  resolved: %s\n", copy.ResolvedPath)
		}
		if copy.Note != "" {
			_, _ = fmt.Fprintf(tw, "  \t\t\t  note: %s\n", copy.Note)
		}
		if copy.ReadError != "" {
			_, _ = fmt.Fprintf(tw, "  \t\t\t  read error: %s\n", copy.ReadError)
		}
		if copy.Hint != "" {
			_, _ = fmt.Fprintf(tw, "  \t\t\t  fix: %s\n", copy.Hint)
		}
	}
	_, _ = fmt.Fprintln(tw)

	_, _ = fmt.Fprintln(tw, "Scope")
	_, _ = fmt.Fprintf(tw, "  profile:\t%s\n", report.Scope.Profile)
	_, _ = fmt.Fprintf(tw, "  project:\t%s%s\n", emptyDash(report.Scope.Project), sourceSuffix(report.Scope.ProjectSource))
	_, _ = fmt.Fprintf(tw, "  tenant:\t%s%s\n", emptyDash(report.Scope.Tenant), sourceSuffix(report.Scope.TenantSource))
	_, _ = fmt.Fprintf(tw, "  base URL:\t%s (%s)\n", report.Scope.BaseURL, report.Scope.BaseURLSource)
	if report.Scope.LoginNote != "" {
		_, _ = fmt.Fprintf(tw, "  note:\t%s\n", report.Scope.LoginNote)
	}
	_, _ = fmt.Fprintln(tw, "  auth details:\trc auth status")
	_, _ = fmt.Fprintln(tw)

	_, _ = fmt.Fprintln(tw, "Update")
	if report.Update.Note != "" {
		_, _ = fmt.Fprintf(tw, "  %s\n", report.Update.Note)
	} else if report.Update.Available {
		_, _ = fmt.Fprintf(tw, "  available:\t%s → %s (run: rc self update)\n", report.Update.Current, report.Update.Latest)
	} else {
		_, _ = fmt.Fprintf(tw, "  status:\tup to date (%s)\n", report.Update.Current)
	}
	_, _ = fmt.Fprintln(tw)

	if len(report.Findings) == 0 {
		_, _ = fmt.Fprintln(tw, "Findings: none")
	} else {
		_, _ = fmt.Fprintf(tw, "Findings: %d (exit 1)\n", len(report.Findings))
		for _, finding := range report.Findings {
			_, _ = fmt.Fprintf(tw, "  - %s: %s\n", finding.Path, finding.Message)
			if finding.Hint != "" {
				_, _ = fmt.Fprintf(tw, "    fix: %s\n", finding.Hint)
			}
		}
	}
	return tw.Flush()
}

func sourceSuffix(source string) string {
	if source == "" {
		return ""
	}
	return " (" + source + ")"
}
