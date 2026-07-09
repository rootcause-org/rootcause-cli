// Package outputspill keeps large CLI payloads out of stdout while preserving full bytes on disk.
package outputspill

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DefaultDir       = ".rootcause/output"
	DefaultThreshold = 6000
	DefaultInlineMax = 20000
	DefaultHeadBytes = 2000
	DefaultTailBytes = 1000
)

type Config struct {
	Dir       string
	Threshold int
	InlineMax int
	HeadBytes int
	TailBytes int
	NoPreview bool
	Raw       bool
}

type Preview struct {
	Head string `json:"head,omitempty"`
	Tail string `json:"tail,omitempty"`
}

type Artifact struct {
	Path            string   `json:"path"`
	Format          string   `json:"format"`
	Bytes           int      `json:"bytes"`
	Lines           int      `json:"lines,omitempty"`
	Preview         *Preview `json:"preview,omitempty"`
	Hints           []string `json:"hints,omitempty"`
	ServerTruncated bool     `json:"server_truncated,omitempty"`
}

type Manifest struct {
	Spilled     bool                `json:"spilled"`
	Path        string              `json:"path,omitempty"`
	Format      string              `json:"format,omitempty"`
	Bytes       int                 `json:"bytes,omitempty"`
	Lines       int                 `json:"lines,omitempty"`
	Preview     *Preview            `json:"preview,omitempty"`
	Artifacts   map[string]Artifact `json:"artifacts,omitempty"`
	Hints       []string            `json:"hints,omitempty"`
	RawModeHint string              `json:"raw_mode_hint,omitempty"`
}

func NewConfig(dir string, noPreview, raw bool) Config {
	if dir == "" {
		dir = os.Getenv("RC_OUTPUT_DIR")
	}
	if dir == "" {
		dir = DefaultDir
	}
	return Config{
		Dir:       dir,
		Threshold: envInt("RC_OUTPUT_SPILL_THRESHOLD", DefaultThreshold),
		InlineMax: envInt("RC_OUTPUT_INLINE_MAX", DefaultInlineMax),
		HeadBytes: DefaultHeadBytes,
		TailBytes: DefaultTailBytes,
		NoPreview: noPreview,
		Raw:       raw,
	}
}

func (c Config) WithRaw(raw bool) Config {
	c.Raw = raw
	return c
}

func (c Config) ShouldSpillBytes(b []byte) bool {
	return !c.Raw && len(b) > c.Threshold
}

func (c Config) ShouldSpillInline(b []byte) bool {
	return !c.Raw && len(b) > c.InlineMax
}

func (c Config) DirFor(label string) string {
	label = sanitize(label)
	if label == "" {
		label = "output"
	}
	if label == "output" {
		label = time.Now().UTC().Format("20060102T150405Z") + "-output"
	}
	return filepath.Join(c.Dir, label)
}

func WriteArtifact(c Config, dir, name string, b []byte, format string, serverTruncated bool) (Artifact, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Artifact{}, fmt.Errorf("create output dir %s: %w", dir, err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return Artifact{}, fmt.Errorf("write %s: %w", path, err)
	}
	art := Artifact{
		Path:            path,
		Format:          detectFormat(path, b, format),
		Bytes:           len(b),
		Lines:           lineCount(b),
		ServerTruncated: serverTruncated,
	}
	if !c.NoPreview && art.Format != "binary" {
		p := preview(b, c.HeadBytes, c.TailBytes)
		art.Preview = &p
	}
	art.Hints = Hints(art.Format, path)
	return art, nil
}

func ArtifactForFile(c Config, path, format string, serverTruncated bool) (Artifact, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Artifact{}, fmt.Errorf("read %s: %w", path, err)
	}
	art := Artifact{
		Path:            path,
		Format:          detectFormat(path, b, format),
		Bytes:           len(b),
		Lines:           lineCount(b),
		ServerTruncated: serverTruncated,
	}
	if !c.NoPreview && art.Format != "binary" {
		p := preview(b, c.HeadBytes, c.TailBytes)
		art.Preview = &p
	}
	art.Hints = Hints(art.Format, path)
	return art, nil
}

func MaybeSpillJSON(c Config, label string, raw json.RawMessage) (*Manifest, error) {
	if c.Raw {
		return nil, nil
	}
	compact, err := compactJSON(raw)
	if err != nil {
		compact = raw
	}
	spillWhole := c.ShouldSpillInline(compact)
	fields := largeJSONFields(compact, c.Threshold)
	if !spillWhole && len(fields) == 0 {
		return nil, nil
	}
	dir := c.DirFor(label)
	artifacts := map[string]Artifact{}
	response, err := WriteArtifact(c, dir, "response.json", raw, "json", false)
	if err != nil {
		return nil, err
	}
	artifacts["response"] = response
	for _, f := range fields {
		key := f.Name
		name := f.Name + extensionForFormat(f.Format)
		if _, exists := artifacts[f.Name]; exists {
			key = f.Name + "_field"
			name = f.Name + "-field" + extensionForFormat(f.Format)
		}
		art, err := WriteArtifact(c, dir, name, f.Bytes, f.Format, f.ServerTruncated)
		if err != nil {
			return nil, err
		}
		artifacts[key] = art
	}
	m := &Manifest{
		Spilled:     true,
		Artifacts:   artifacts,
		Hints:       manifestHints(artifacts),
		RawModeHint: "rerun with --raw-output to print the full payload to stdout",
	}
	if len(artifacts) == 1 {
		m.Path = response.Path
		m.Format = response.Format
		m.Bytes = response.Bytes
		m.Lines = response.Lines
		m.Preview = response.Preview
	}
	return m, nil
}

func ManifestForArtifact(art Artifact) Manifest {
	return Manifest{
		Spilled:     true,
		Path:        art.Path,
		Format:      art.Format,
		Bytes:       art.Bytes,
		Lines:       art.Lines,
		Preview:     art.Preview,
		Hints:       art.Hints,
		RawModeHint: "rerun with --raw-output to print the full payload to stdout",
	}
}

func WriteManifest(w interface{ Write([]byte) (int, error) }, m Manifest) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(m)
}

func Hints(format, path string) []string {
	switch format {
	case "json":
		return []string{
			"jq '.' " + shellQuote(path),
			"jq 'keys' " + shellQuote(path),
			"jq -r '.. | strings' " + shellQuote(path) + " | rg PATTERN",
		}
	case "jsonl", "ndjson":
		return []string{
			"sed -n '1,120p' " + shellQuote(path),
			"jq -r 'select(...)' " + shellQuote(path),
			"jq -r '.stdout? // empty' " + shellQuote(path),
		}
	case "csv", "tsv":
		return []string{
			"sed -n '1,20p' " + shellQuote(path),
			"rg PATTERN " + shellQuote(path),
			"tail -n 120 " + shellQuote(path),
		}
	default:
		return []string{
			"sed -n '1,120p' " + shellQuote(path),
			"rg PATTERN " + shellQuote(path),
			"tail -n 120 " + shellQuote(path),
		}
	}
}

func ShellQuote(s string) string {
	return shellQuote(s)
}

type jsonField struct {
	Name            string
	Bytes           []byte
	Format          string
	ServerTruncated bool
}

var spillFieldNames = map[string]bool{
	"stdout": true, "stderr": true, "body": true, "draft": true, "notes": true,
	"system_prompt": true, "events": true, "response": true,
}

func largeJSONFields(raw []byte, threshold int) []jsonField {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	var out []jsonField
	seen := map[string]int{}
	var walk func(any, string, map[string]any)
	walk = func(x any, key string, parent map[string]any) {
		switch t := x.(type) {
		case map[string]any:
			for k, v := range t {
				walk(v, k, t)
			}
		case []any:
			if spillFieldNames[key] {
				if b, err := json.Marshal(t); err == nil && len(b) > threshold {
					out = append(out, jsonField{Name: uniqueName(key, seen), Bytes: b, Format: "json", ServerTruncated: truncated(parent, key)})
				} else if err == nil && truncated(parent, key) {
					out = append(out, jsonField{Name: uniqueName(key, seen), Bytes: b, Format: "json", ServerTruncated: true})
				}
				return
			}
			for _, v := range t {
				walk(v, "", parent)
			}
		case string:
			if len(t) > threshold || (spillFieldNames[key] && truncated(parent, key)) {
				out = append(out, jsonField{Name: uniqueName(key, seen), Bytes: []byte(t), Format: "text", ServerTruncated: truncated(parent, key)})
			}
		}
	}
	walk(v, "", nil)
	return out
}

func truncated(parent map[string]any, key string) bool {
	if parent == nil || key == "" {
		return false
	}
	v, _ := parent[key+"_truncated"].(bool)
	return v
}

func uniqueName(base string, seen map[string]int) string {
	if base == "" {
		base = "field"
	}
	base = sanitize(base)
	seen[base]++
	if seen[base] == 1 {
		return base
	}
	return base + "-" + strconv.Itoa(seen[base])
}

func compactJSON(raw []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func preview(b []byte, head, tail int) Preview {
	if len(b) <= head+tail {
		s := string(bytes.ToValidUTF8(b, []byte("\uFFFD")))
		return Preview{Head: s}
	}
	h := string(bytes.ToValidUTF8(b[:head], []byte("\uFFFD")))
	t := string(bytes.ToValidUTF8(b[len(b)-tail:], []byte("\uFFFD")))
	return Preview{Head: h, Tail: t}
}

func lineCount(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	n := bytes.Count(b, []byte{'\n'})
	if !bytes.HasSuffix(b, []byte{'\n'}) {
		n++
	}
	return n
}

func detectFormat(path string, b []byte, hint string) string {
	if hint != "" {
		return hint
	}
	if !utf8.Valid(b) {
		return "binary"
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return "json"
	case ".jsonl", ".ndjson":
		return "jsonl"
	case ".csv":
		return "csv"
	case ".tsv":
		return "tsv"
	default:
		return "text"
	}
}

func extensionForFormat(format string) string {
	switch format {
	case "json":
		return ".json"
	case "jsonl", "ndjson":
		return ".jsonl"
	default:
		return ".txt"
	}
}

func manifestHints(artifacts map[string]Artifact) []string {
	var hints []string
	if a, ok := artifacts["response"]; ok {
		hints = append(hints, "jq '.' "+shellQuote(a.Path))
	}
	for name, art := range artifacts {
		if name == "response" {
			continue
		}
		switch art.Format {
		case "json":
			hints = append(hints, "jq '.' "+shellQuote(art.Path))
		default:
			hints = append(hints, "sed -n '1,120p' "+shellQuote(art.Path))
		}
	}
	if len(hints) < 3 {
		for _, art := range artifacts {
			for _, h := range art.Hints {
				if !contains(hints, h) {
					hints = append(hints, h)
				}
				if len(hints) >= 3 {
					return hints
				}
			}
		}
	}
	return hints
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func sanitize(s string) string {
	s = strings.Trim(s, " ._-")
	s = unsafeName.ReplaceAllString(s, "-")
	if len(s) > 80 {
		s = s[:80]
	}
	return strings.Trim(s, "-")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.IndexFunc(s, func(r rune) bool {
		return !(r == '/' || r == '.' || r == '_' || r == '-' || r == ':' || r == '+' || r == '=' ||
			(r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'))
	}) == -1 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func envInt(name string, def int) int {
	s := strings.TrimSpace(os.Getenv(name))
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	return n
}
