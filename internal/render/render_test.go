package render

import (
	"io"
	"os"
	"testing"
)

type wrappedWriter struct{ io.Writer }

func (w wrappedWriter) UnwrapWriter() io.Writer { return w.Writer }

func TestAutoModePreservedThroughWriterDecorator(t *testing.T) {
	f, err := os.OpenFile("/dev/null", os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if IsJSON(ModeAuto, f) != IsJSON(ModeAuto, wrappedWriter{Writer: f}) {
		t.Fatal("writer decorator changed auto output mode")
	}
}

// A server still emitting spend/token metadata must not leak it through the freeform passthrough.
func TestMetadataPassthroughDropsSpendKeys(t *testing.T) {
	md := map[string]any{
		"total_cost_usd": 1.23, "cost_usd": 0.4, "tokens": 900, "peak_context_tokens": 50000,
		"outcome": "answered", "run_url": "https://x", "channel": "email",
	}
	got := sortedMetadataKeys(md)
	if len(got) != 1 || got[0] != "channel" {
		t.Fatalf("metadata keys = %v, want only [channel]", got)
	}
}
