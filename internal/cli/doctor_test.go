package cli

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

func TestAnalyzePathBinaries(t *testing.T) {
	tests := []struct {
		name   string
		copies []binaryInfo
		codes  []string
	}{
		{
			name: "divergent duplicate",
			copies: []binaryInfo{
				{Path: "/usr/local/bin/rc", Version: "1.2.3", LDFlagsVersion: "1.2.3", Install: "release", Active: true},
				{Path: "/home/me/go/bin/rc", Version: "v1.2.2 (go install)", ModuleVersion: "v1.2.2", Install: "go-install"},
			},
			codes: []string{"divergent_copy"},
		},
		{
			name: "same tag different provenance",
			copies: []binaryInfo{
				{Path: "/usr/local/bin/rc", Version: "1.2.3", LDFlagsVersion: "1.2.3", Install: "release", Active: true},
				{Path: "/home/me/go/bin/rc", Version: "v1.2.3 (go install)", ModuleVersion: "v1.2.3", Install: "go-install"},
			},
		},
		{name: "single release", copies: []binaryInfo{{Path: "/usr/local/bin/rc", Version: "1.2.3", Install: "release", Active: true}}},
		{name: "active go install", copies: []binaryInfo{{Path: "/home/me/go/bin/rc", Version: "v1.2.3 (go install)", Install: "go-install", Active: true}}, codes: []string{"active_non_release"}},
		{name: "single homebrew", copies: []binaryInfo{{Path: "/opt/homebrew/bin/rc", Version: "1.2.3", Install: "homebrew", Active: true}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := analyzePathBinaries(tt.copies)
			if len(findings) != len(tt.codes) {
				t.Fatalf("findings = %#v, want codes %v", findings, tt.codes)
			}
			for i, code := range tt.codes {
				if findings[i].Code != code {
					t.Fatalf("finding[%d].Code = %q, want %q", i, findings[i].Code, code)
				}
			}
		})
	}
}

func TestRenderDoctorHuman(t *testing.T) {
	report := doctorReport{
		Binary: binaryInfo{Path: "/usr/local/bin/rc", Version: "1.2.3", ModuleVersion: "v1.2.3", LDFlagsVersion: "1.2.3", Install: "release"},
		Path:   []binaryInfo{{Path: "/usr/local/bin/rc", Version: "1.2.3", Install: "release", Active: true}},
		Scope:  doctorScope{Profile: "default", Project: "acme", ProjectSource: ".rootcause.toml", Tenant: "-", BaseURL: "https://app.replypen.com", BaseURLSource: "built-in production"},
		Update: doctorUpdate{Current: "1.2.3", Latest: "v1.2.4", Available: true},
	}
	var out bytes.Buffer
	if err := renderDoctorHuman(&out, report); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"This binary", "PATH scan", "Scope", "auth details:", "Update", "1.2.3 → v1.2.4", "Findings: none"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("human report missing %q:\n%s", want, out.String())
		}
	}
}

func TestBuildInfoReadFileOnTestBinary(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := buildinfo.ReadFile(exe); err != nil {
		t.Fatalf("buildinfo.ReadFile(test binary): %v", err)
	}
}

func TestLDFlagsVersion(t *testing.T) {
	bi := &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "-ldflags", Value: "-s -w -X main.version=1.2.3"}}}
	if got := ldflagsVersion(bi); got != "1.2.3" {
		t.Fatalf("ldflagsVersion() = %q, want 1.2.3", got)
	}
}

func TestClassifyInstall(t *testing.T) {
	goInstall := &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}
	source := &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}
	for _, tt := range []struct {
		name, path, injected, want string
		bi                         *debug.BuildInfo
	}{
		{name: "release", path: "/usr/local/bin/rc", injected: "1.2.3", bi: source, want: "release"},
		{name: "go install", path: "/home/me/go/bin/rc", bi: goInstall, want: "go-install"},
		{name: "source", path: "/tmp/rc", bi: source, want: "source"},
		{name: "homebrew", path: "/opt/homebrew/Caskroom/rc/1.2.3/rc", injected: "1.2.3", bi: source, want: "homebrew"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyInstall(tt.path, tt.injected, tt.bi); got != tt.want {
				t.Fatalf("classifyInstall() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestScanPathMiseDispatcherUsesRunningRC(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink mode semantics differ on Windows")
	}
	root := t.TempDir()
	shimDir := filepath.Join(root, ".local", "share", "mise", "shims")
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mise := filepath.Join(root, "mise")
	if err := os.WriteFile(mise, []byte("not a Go binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(shimDir, "rc")
	if err := os.Symlink(mise, shim); err != nil {
		t.Fatal(err)
	}
	current := binaryInfo{Path: "/home/me/.local/share/mise/installs/go/1.25.9/bin/rc", Version: "v1.2.3 (go install)", ModuleVersion: "v1.2.3", Install: "go-install"}

	copies := scanPathBinaries(shimDir, current, runtime.GOOS)
	if len(copies) != 1 {
		t.Fatalf("copies = %#v, want one", copies)
	}
	got := copies[0]
	if !got.Active || !got.Dispatcher || got.Version != current.Version || got.ModuleVersion != current.ModuleVersion {
		t.Fatalf("dispatcher info = %#v, want running rc info", got)
	}
	if strings.Contains(got.ReadError, "not a rootcause") {
		t.Fatalf("mise dispatcher was parsed as rc: %#v", got)
	}
}

func TestDoctorUpdateCheckAndOfflineAreInformational(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("ROOTCAUSE_BASE_URL", "")

	t.Run("fake release server", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"tag_name":"v9.9.9"}`))
		}))
		defer srv.Close()
		var out, errOut bytes.Buffer
		e := &env{profile: "default", output: "json", out: &out, err: &errOut,
			latestRelease: func(ctx context.Context) (string, error) { return latestReleaseTagAt(ctx, srv.URL) }}
		if err := run(t, e, "self", "doctor"); err != nil {
			t.Fatalf("doctor with fake release server: %v", err)
		}
		var report doctorReport
		if err := json.Unmarshal(out.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		if report.Update.Latest != "v9.9.9" || !report.Update.Available {
			t.Fatalf("update = %#v", report.Update)
		}
	})

	t.Run("offline", func(t *testing.T) {
		var out, errOut bytes.Buffer
		e := &env{profile: "default", output: "json", out: &out, err: &errOut,
			latestRelease: func(context.Context) (string, error) { return "", errors.New("offline") }}
		if err := run(t, e, "self", "doctor"); err != nil {
			t.Fatalf("offline update check changed exit behavior: %v", err)
		}
		var report doctorReport
		if err := json.Unmarshal(out.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		if report.Update.Note != "update check skipped: offline" || len(report.Findings) != 0 {
			t.Fatalf("offline report = %#v", report)
		}
	})
}

func TestDoctorFindingsReturnNonzero(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink mode semantics differ on Windows")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	if err := os.Symlink(exe, filepath.Join(binDir, "rc")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("ROOTCAUSE_BASE_URL", "")
	var out, errOut bytes.Buffer
	e := &env{profile: "default", output: "json", out: &out, err: &errOut,
		latestRelease: func(context.Context) (string, error) { return "", errors.New("offline") }}
	err = run(t, e, "self", "doctor")
	var findingErr doctorFindingsError
	if !errors.As(err, &findingErr) || findingErr.count == 0 {
		t.Fatalf("doctor error = %v, want doctorFindingsError", err)
	}
}

func TestDoctorScopeSelectorsStayLocal(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("ROOTCAUSE_BASE_URL", "")

	e := &env{profile: "default", tenant: "acme", scope: scopeSelectorProject}
	resolved, err := e.resolveScope(false)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Tenant != "" || resolved.TenantSource != "--scope project" {
		t.Fatalf("project scope tenant/source = %q/%q", resolved.Tenant, resolved.TenantSource)
	}

	e = &env{profile: "default", scope: scopeSelectorTenant}
	resolved, err = e.resolveScope(false)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Tenant != "" || resolved.TenantSource != "--scope tenant (unresolved)" {
		t.Fatalf("unresolved tenant scope = %q/%q", resolved.Tenant, resolved.TenantSource)
	}
}
