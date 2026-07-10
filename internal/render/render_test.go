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
