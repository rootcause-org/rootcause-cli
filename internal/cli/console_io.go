package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rootcause-org/rootcause-cli/internal/outputspill"
)

const maxConsoleInputBytes = 256 << 10

func consoleInput(e *env, arg string) (string, error) {
	switch {
	case arg == "-":
		r := e.in
		if r == nil {
			r = os.Stdin
		}
		return readConsoleInput(r, "stdin")
	case strings.HasPrefix(arg, "@"):
		path := strings.TrimPrefix(arg, "@")
		if path == "" {
			return "", fmt.Errorf("@file input requires a path")
		}
		f, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("open console input %s: %w", path, err)
		}
		defer func() { _ = f.Close() }()
		return readConsoleInput(f, path)
	default:
		return arg, nil
	}
}

func readConsoleInput(r io.Reader, source string) (string, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxConsoleInputBytes+1))
	if err != nil {
		return "", fmt.Errorf("read console input from %s: %w", source, err)
	}
	if len(b) > maxConsoleInputBytes {
		return "", fmt.Errorf("console input from %s exceeds %d bytes", source, maxConsoleInputBytes)
	}
	return string(b), nil
}

func parseConsoleParams(values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	params := make(map[string]string, len(values))
	for _, raw := range values {
		key, value, ok := strings.Cut(raw, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("--param must be k=v (got %q)", raw)
		}
		if _, exists := params[key]; exists {
			return nil, fmt.Errorf("duplicate --param %q", key)
		}
		params[key] = value
	}
	return params, nil
}

type outputTarget struct {
	w          io.Writer
	file       *os.File
	tempPath   string
	finalPath  string
	format     string
	bytes      int
	lines      int
	lastWasNew bool
}

func openOutputTarget(e *env, out, autoName, format string) (*outputTarget, error) {
	if out == "" || out == "-" {
		return &outputTarget{w: e.out, format: format}, nil
	}
	path := out
	if out == "auto" {
		path = filepath.Join(e.spillConfig().Dir, autoName)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create output directory for %s: %w", path, err)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".rc-output-*")
	if err != nil {
		return nil, fmt.Errorf("create output file for %s: %w", path, err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return nil, fmt.Errorf("secure output file %s: %w", path, err)
	}
	t := &outputTarget{file: f, tempPath: f.Name(), finalPath: path, format: format}
	t.w = &countingWriter{target: t, w: bufio.NewWriter(f)}
	return t, nil
}

type countingWriter struct {
	target *outputTarget
	w      *bufio.Writer
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	w.target.bytes += n
	w.target.lines += strings.Count(string(p[:n]), "\n")
	if n > 0 {
		w.target.lastWasNew = p[n-1] == '\n'
	}
	return n, err
}

func (t *outputTarget) Writer() io.Writer { return t.w }

func (t *outputTarget) abort() {
	if t.file == nil {
		return
	}
	_ = t.file.Close()
	_ = os.Remove(t.tempPath)
}

func (t *outputTarget) finish(e *env) error {
	if t.file == nil {
		return nil
	}
	if cw, ok := t.w.(*countingWriter); ok {
		if err := cw.w.Flush(); err != nil {
			t.abort()
			return fmt.Errorf("flush %s: %w", t.finalPath, err)
		}
	}
	if err := t.file.Sync(); err != nil {
		t.abort()
		return fmt.Errorf("sync %s: %w", t.finalPath, err)
	}
	if err := t.file.Close(); err != nil {
		t.abort()
		return fmt.Errorf("close %s: %w", t.finalPath, err)
	}
	if err := os.Rename(t.tempPath, t.finalPath); err != nil {
		_ = os.Remove(t.tempPath)
		return fmt.Errorf("install %s: %w", t.finalPath, err)
	}
	lines := t.lines
	if t.bytes > 0 && !t.lastWasNew {
		lines++
	}
	art := outputspill.Artifact{Path: t.finalPath, Format: t.format, Bytes: t.bytes, Lines: lines,
		Hints: outputspill.Hints(t.format, t.finalPath)}
	return outputspill.WriteManifest(e.out, outputspill.ManifestForArtifact(art))
}

func shortRunID(id string) string {
	id = strings.ReplaceAll(id, "-", "")
	if len(id) > 8 {
		return id[:8]
	}
	if id == "" {
		return "unknown"
	}
	return id
}
