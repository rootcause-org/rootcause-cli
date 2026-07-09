package outputspill

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaybeSpillJSONLargeFields(t *testing.T) {
	dir := t.TempDir()
	cfg := NewConfig(dir, false, false)
	cfg.Threshold = 20
	cfg.InlineMax = 1000
	raw := json.RawMessage(`{"run_id":"abc","stdout":"HEAD` + strings.Repeat("x", 80) + `TAIL","stdout_truncated":true}`)

	m, err := MaybeSpillJSON(cfg, "bash-run-abc-seq-1", raw)
	if err != nil {
		t.Fatalf("spill json: %v", err)
	}
	if m == nil || !m.Spilled {
		t.Fatalf("expected spill manifest, got %#v", m)
	}
	stdout := m.Artifacts["stdout"]
	if stdout.Path == "" || stdout.Format != "text" || !stdout.ServerTruncated {
		t.Fatalf("stdout artifact = %#v", stdout)
	}
	if len(stdout.Hints) < 3 || !strings.Contains(stdout.Hints[0], "sed -n") || !strings.Contains(stdout.Hints[1], "rg PATTERN") {
		t.Fatalf("text hints = %#v", stdout.Hints)
	}
	if _, err := os.Stat(filepath.Join(dir, "bash-run-abc-seq-1", "response.json")); err != nil {
		t.Fatalf("response artifact missing: %v", err)
	}
	if got, err := os.ReadFile(stdout.Path); err != nil || !strings.Contains(string(got), "TAIL") {
		t.Fatalf("stdout artifact content = %q err=%v", got, err)
	}
}

func TestMaybeSpillJSONRawMode(t *testing.T) {
	cfg := NewConfig(t.TempDir(), false, true)
	cfg.Threshold = 1
	m, err := MaybeSpillJSON(cfg, "raw", []byte(`{"stdout":"large"}`))
	if err != nil {
		t.Fatalf("raw spill: %v", err)
	}
	if m != nil {
		t.Fatalf("raw mode should not spill: %#v", m)
	}
}

func TestNewConfigUsesRCOutputDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RC_OUTPUT_DIR", dir)
	cfg := NewConfig("", false, false)
	if cfg.Dir != dir {
		t.Fatalf("Dir = %q, want %q", cfg.Dir, dir)
	}
}

func TestMaybeSpillJSONResponseFieldDoesNotClobberRawResponse(t *testing.T) {
	cfg := NewConfig(t.TempDir(), true, false)
	cfg.Threshold = 5
	cfg.InlineMax = 1000
	raw := json.RawMessage(`{"response":"` + strings.Repeat("x", 40) + `"}`)
	m, err := MaybeSpillJSON(cfg, "response-field", raw)
	if err != nil {
		t.Fatalf("spill response field: %v", err)
	}
	if m.Artifacts["response"].Path == "" || !strings.HasSuffix(m.Artifacts["response"].Path, "response.json") {
		t.Fatalf("raw response artifact was clobbered: %#v", m.Artifacts["response"])
	}
	if m.Artifacts["response_field"].Path == "" || !strings.HasSuffix(m.Artifacts["response_field"].Path, "response-field.txt") {
		t.Fatalf("response field artifact missing: %#v", m.Artifacts)
	}
}
