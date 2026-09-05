// Views for the `rc self` family: the doctor report and the update check. Both are pure functions of
// a view model the CLI builds — every host-dependent decision (which install is running, whether this
// OS has a canonical Homebrew install, version comparison) is resolved in internal/cli and arrives
// here as plain fields, so the output is testable without touching PATH or the filesystem.

package render

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// DoctorBinary is one rc copy — the running one or a PATH entry.
type DoctorBinary struct {
	Path           string
	ResolvedPath   string
	Version        string
	ModuleVersion  string
	LDFlagsVersion string
	Install        string
	Active         bool
	Note           string
	Hint           string
	ReadError      string
}

// DoctorScope is the resolved profile/project/tenant/base-URL context, with where each came from.
type DoctorScope struct {
	Profile       string
	Project       string
	ProjectSource string
	Tenant        string
	TenantSource  string
	BaseURL       string
	BaseURLSource string
	LoginNote     string
}

// DoctorCapabilities pairs what this binary can parse with what the server writes today. Unsupported
// is decided by the CLI (it owns the format list) so this stays a dumb view.
type DoctorCapabilities struct {
	HarvestCorpusFormats []string
	ServerHarvestCorpus  string
	ServerNote           string
	Unsupported          bool
}

type DoctorUpdate struct {
	Current   string
	Latest    string
	Available bool
	Note      string
}

type DoctorFinding struct {
	Path    string
	Message string
	Hint    string
}

// DoctorReport is everything `rc self doctor` shows a human.
type DoctorReport struct {
	Binary       DoctorBinary
	Path         []DoctorBinary
	Scope        DoctorScope
	Capabilities DoctorCapabilities
	Update       DoctorUpdate
	Findings     []DoctorFinding
}

// Doctor writes the human doctor report. Columns are tab-aligned so the PATH scan lines up.
func Doctor(w io.Writer, report DoctorReport) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "This binary")
	_, _ = fmt.Fprintf(tw, "  path:\t%s\n", report.Binary.Path)
	if report.Binary.ResolvedPath != "" {
		_, _ = fmt.Fprintf(tw, "  resolved:\t%s\n", report.Binary.ResolvedPath)
	}
	_, _ = fmt.Fprintf(tw, "  version:\t%s\n", report.Binary.Version)
	_, _ = fmt.Fprintf(tw, "  module:\t%s\n", orDash(report.Binary.ModuleVersion, "—"))
	_, _ = fmt.Fprintf(tw, "  ldflags version:\t%s\n", orDash(report.Binary.LDFlagsVersion, "—"))
	_, _ = fmt.Fprintf(tw, "  install:\t%s\n\n", report.Binary.Install)

	_, _ = fmt.Fprintln(tw, "PATH scan")
	_, _ = fmt.Fprintln(tw, "  ACTIVE\tVERSION\tINSTALL\tPATH")
	for _, entry := range report.Path {
		active := ""
		if entry.Active {
			active = "yes"
		}
		_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", active, entry.Version, entry.Install, entry.Path)
		if entry.ResolvedPath != "" {
			_, _ = fmt.Fprintf(tw, "  \t\t\t  resolved: %s\n", entry.ResolvedPath)
		}
		if entry.Note != "" {
			_, _ = fmt.Fprintf(tw, "  \t\t\t  note: %s\n", entry.Note)
		}
		if entry.ReadError != "" {
			_, _ = fmt.Fprintf(tw, "  \t\t\t  read error: %s\n", entry.ReadError)
		}
		if entry.Hint != "" {
			_, _ = fmt.Fprintf(tw, "  \t\t\t  fix: %s\n", entry.Hint)
		}
	}
	_, _ = fmt.Fprintln(tw)

	_, _ = fmt.Fprintln(tw, "Scope")
	_, _ = fmt.Fprintf(tw, "  profile:\t%s\n", report.Scope.Profile)
	_, _ = fmt.Fprintf(tw, "  project:\t%s%s\n", orDash(report.Scope.Project, "—"), sourceSuffix(report.Scope.ProjectSource))
	_, _ = fmt.Fprintf(tw, "  tenant:\t%s%s\n", orDash(report.Scope.Tenant, "—"), sourceSuffix(report.Scope.TenantSource))
	_, _ = fmt.Fprintf(tw, "  base URL:\t%s (%s)\n", report.Scope.BaseURL, report.Scope.BaseURLSource)
	if report.Scope.LoginNote != "" {
		_, _ = fmt.Fprintf(tw, "  note:\t%s\n", report.Scope.LoginNote)
	}
	_, _ = fmt.Fprintln(tw, "  auth details:\trc auth status")
	_, _ = fmt.Fprintln(tw)

	_, _ = fmt.Fprintln(tw, "Capabilities")
	_, _ = fmt.Fprintf(tw, "  harvest corpus formats:\t%s\n", strings.Join(report.Capabilities.HarvestCorpusFormats, ", "))
	switch {
	case report.Capabilities.ServerNote != "":
		_, _ = fmt.Fprintf(tw, "  server writes:\t%s\n", report.Capabilities.ServerNote)
	case report.Capabilities.Unsupported:
		_, _ = fmt.Fprintf(tw, "  server writes:\t%s — this rc cannot split it; run: rc self update\n", report.Capabilities.ServerHarvestCorpus)
	case report.Capabilities.ServerHarvestCorpus != "":
		_, _ = fmt.Fprintf(tw, "  server writes:\t%s\n", report.Capabilities.ServerHarvestCorpus)
	}
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

// UpdateStatus is `rc self update --check`'s view model. Current/Latest arrive already normalised and
// the two "installation problem" flags are decided by the CLI (they depend on the host OS).
type UpdateStatus struct {
	Source           string
	RunningPath      string
	InstallKind      string
	Current          string
	Latest           string
	UpdateAvailable  bool
	InstallProblem   bool // duplicate or shadowed binaries
	Installations    int
	OfferMigrate     bool // this OS has a `rc self update --migrate` path
	NotCanonicalHome bool // this OS's canonical install is Homebrew but rc isn't on it
}

// UpdateCheck writes the `--check` report: where rc runs from, whether an update exists, and any
// installation problem that would make an in-place update unsafe.
func UpdateCheck(w io.Writer, s UpdateStatus) {
	_, _ = fmt.Fprintf(w, "source: %s\n", s.Source)
	_, _ = fmt.Fprintf(w, "running rc: %s (%s, %s)\n", s.RunningPath, s.InstallKind, s.Current)
	if s.UpdateAvailable {
		_, _ = fmt.Fprintf(w, "latest rc:  %s (update available)\n", s.Latest)
	} else {
		_, _ = fmt.Fprintf(w, "latest rc:  %s (up to date)\n", s.Latest)
	}
	if s.InstallProblem {
		_, _ = fmt.Fprintf(w, "installation problem: %d distinct binaries; run `rc self doctor`\n", s.Installations)
		if s.OfferMigrate {
			_, _ = fmt.Fprintln(w, "migration: rc self update --migrate")
		}
		return
	}
	if s.NotCanonicalHome {
		_, _ = fmt.Fprintln(w, "installation problem: macOS canonical install is Homebrew")
		_, _ = fmt.Fprintln(w, "migration: rc self update --migrate")
	}
}
