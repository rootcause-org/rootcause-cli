package cli

import (
	"runtime/debug"
	"strings"
)

// ResolveVersion returns the truthful version for the running binary. Release builds inject a
// version with ldflags; go install builds carry their module version in Go build info.
func ResolveVersion(injected string) string {
	bi, ok := debug.ReadBuildInfo()
	return resolveVersion(injected, bi, ok)
}

func resolveVersion(injected string, bi *debug.BuildInfo, ok bool) string {
	if injected != "" {
		return injected
	}
	if !ok || bi == nil {
		return "devel (unknown)"
	}
	if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version + " (go install)"
	}

	revision, dirty := "", false
	for _, setting := range bi.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			dirty = setting.Value == "true"
		}
	}
	if revision == "" {
		return "devel (unknown)"
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	parts := []string{revision}
	if dirty {
		parts = append(parts, "dirty")
	}
	return "devel (" + strings.Join(parts, ", ") + ")"
}
