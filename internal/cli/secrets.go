package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// readSecretStdin reads a secret VALUE from stdin so it never lands in argv / the process table /
// shell history — the hygiene the connection/openrouter-key reveal commands also honor. It reads the
// first line (trailing CR/LF trimmed); an entirely empty read is an error pointing the caller at the
// expected pipe. On an interactive TTY it prints a one-line prompt to stderr first (stdout stays
// reserved for machine output). `label` names what's being read (e.g. "OpenRouter key").
func readSecretStdin(e *env, label string) (string, error) {
	r := e.in
	if r == nil {
		r = os.Stdin
	}
	if f, ok := r.(*os.File); ok && render.IsTerminal(f) {
		_, _ = fmt.Fprintf(e.err, "Paste the %s, then press Enter: ", label)
	}
	br := bufio.NewReader(r)
	line, err := br.ReadString('\n')
	line = strings.TrimRight(line, "\r\n")
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read %s from stdin: %w", label, err)
	}
	if line == "" {
		return "", fmt.Errorf("no %s on stdin — pipe it in (e.g. `printf '%%s' \"$KEY\" | rc …`)", label)
	}
	return line, nil
}

// readAllStdin reads all of stdin (a non-secret multi-line body, e.g. a brain-edit instruction). Empty
// is allowed here — the caller decides whether that's an error.
func readAllStdin(e *env) (string, error) {
	r := e.in
	if r == nil {
		r = os.Stdin
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	return string(b), nil
}
