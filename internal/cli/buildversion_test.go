package cli

import (
	"runtime/debug"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	tests := []struct {
		name     string
		injected string
		bi       *debug.BuildInfo
		ok       bool
		want     string
	}{
		{name: "injected wins", injected: "1.2.3", bi: &debug.BuildInfo{Main: debug.Module{Version: "v9.9.9"}}, ok: true, want: "1.2.3"},
		{name: "go install module", bi: &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}, ok: true, want: "v1.2.3 (go install)"},
		{name: "source revision", bi: &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abcdef1234567890"}}}, ok: true, want: "devel (abcdef123456)"},
		{name: "dirty source", bi: &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abcdef1234567890"}, {Key: "vcs.modified", Value: "true"}}}, ok: true, want: "devel (abcdef123456, dirty)"},
		{name: "missing build info", want: "devel (unknown)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveVersion(tt.injected, tt.bi, tt.ok); got != tt.want {
				t.Fatalf("resolveVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}
