// Package render is the output layer: it turns wire structs into either a human table or the raw API
// JSON, and decides which by default. The INTENT is pipe-first ergonomics — a TTY gets a readable
// table, a pipe/redirect gets JSON so `| jq` always works — with a global -o/--output flag to force
// either. It only RENDERS what the server sent; JSON mode is a verbatim pretty-print (no reshaping),
// so the CLI can never invent or drop a field on the jq path.
package render

import (
	"encoding/json"
	"io"
	"os"
)

// Mode is the output format. ModeAuto resolves to table on a TTY, JSON otherwise.
type Mode string

const (
	ModeAuto  Mode = ""      // unset: detect from the destination
	ModeTable Mode = "table" // human, columnar
	ModeJSON  Mode = "json"  // raw API JSON, pretty-printed
)

// IsJSON resolves Mode against the destination: an explicit flag wins; otherwise JSON unless w is a
// character device (a terminal). We test ModeCharDevice on the *actual* writer when it's a file so
// tests can force a mode without a real TTY, and `> file` / `| cmd` correctly fall to JSON.
func IsJSON(mode Mode, w io.Writer) bool {
	switch mode {
	case ModeJSON:
		return true
	case ModeTable:
		return false
	}
	// Auto: table only when writing to a terminal.
	return !isTerminal(w)
}

// IsTerminal reports whether w is a real TTY. Used to gate transient progress output (e.g. `rc ask`'s
// live status line) so it never lands in a pipe, a redirect, or a test buffer.
func IsTerminal(w io.Writer) bool { return isTerminal(w) }

// isTerminal reports whether w is a character device (a TTY). A non-*os.File writer (e.g. a test
// buffer) is treated as "not a terminal" → JSON, which is the safe, scriptable default.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// JSON pretty-prints raw API bytes to w (2-space indent, trailing newline). The bytes are emitted as
// the server sent them — re-indenting is the only transform, so jq sees the true response shape.
func JSON(w io.Writer, raw json.RawMessage) error {
	var buf any
	if err := json.Unmarshal(raw, &buf); err != nil {
		// Not valid JSON to re-indent: emit verbatim so we never swallow the body.
		_, werr := w.Write(append([]byte(raw), '\n'))
		return werr
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(buf)
}
