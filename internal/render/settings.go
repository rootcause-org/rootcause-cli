// The config-registry views: effective settings (`rc project settings runtime get`), the whole schema
// (`rc schema`), and one field (`rc explain`).
// The renderers are pure functions of the wire structs (no I/O beyond the passed writer, no clock) so
// golden tests pin them exactly. Timestamps are shown as the server sent them, keeping goldens stable.

package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// Settings renders the effective settings table: value (what's set, blank if unset), effective, and
// default per key. kb_enrich_model is shown only when the server included it.
// Settings renders the generic settings bag (`rc project settings runtime get`): one row per key, in stable key order,
// with the override / effective / default / source. The CLI holds no per-key knowledge — it renders
// whatever keys the server sends, so a new server-side knob appears with no CLI change.
func Settings(w io.Writer, s *client.Settings) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "KEY\tVALUE\tEFFECTIVE\tDEFAULT\tSOURCE")
	for _, key := range settingKeys(*s) {
		f := (*s)[key]
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			key, jsonScalarOrBlank(f.Value), jsonScalar(f.Effective), jsonScalar(f.Default), f.Source)
	}
	_ = tw.Flush()
}

// Schema renders the config registry (`rc schema`): each resource, then a row per field with its type,
// enum (if any), write scopes, default, and help.
func Schema(w io.Writer, resp *client.SchemaResponse) {
	names := make([]string, 0, len(resp.Resources))
	for n := range resp.Resources {
		names = append(names, n)
	}
	sort.Strings(names)
	for i, n := range names {
		if i > 0 {
			_, _ = fmt.Fprintln(w)
		}
		_, _ = fmt.Fprintf(w, "%s\n", n)
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "  KEY\tTYPE\tENUM\tSCOPES\tDEFAULT\tHELP")
		for _, f := range resp.Resources[n].Fields {
			_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\n",
				f.Key, f.Type, strings.Join(f.Enum, "|"), strings.Join(f.Scopes, ","),
				jsonScalarOrBlank(f.Default), f.Help)
			// An object field's members are shown as indented ".member" sub-rows: they share the key's
			// scopes/default (the whole object is written at once) so those columns stay blank.
			for _, m := range f.Members {
				_, _ = fmt.Fprintf(tw, "    .%s\t%s\t%s\t\t\t%s\n",
					m.Key, m.Type, strings.Join(m.Enum, "|"), m.Help)
			}
		}
		_ = tw.Flush()
	}
}

// ExplainField renders one field's full schema (`rc explain <key>`) as a key: value block — the
// human-readable twin of /meta/schema for a single knob.
func ExplainField(w io.Writer, resource string, f client.FieldSchema) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "key:\t%s\n", f.Key)
	_, _ = fmt.Fprintf(tw, "resource:\t%s\n", resource)
	_, _ = fmt.Fprintf(tw, "type:\t%s\n", f.Type)
	if len(f.Enum) > 0 {
		_, _ = fmt.Fprintf(tw, "enum:\t%s\n", strings.Join(f.Enum, ", "))
	}
	_, _ = fmt.Fprintf(tw, "scope:\t%s\n", f.Scope)
	_, _ = fmt.Fprintf(tw, "group:\t%s\n", f.Group)
	if len(f.Scopes) > 0 {
		_, _ = fmt.Fprintf(tw, "write scopes:\t%s\n", strings.Join(f.Scopes, ", "))
	}
	if f.Sensitive {
		_, _ = fmt.Fprintf(tw, "sensitive:\ttrue\n")
	}
	for i, m := range f.Members {
		label := ""
		if i == 0 {
			label = "members:"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\n", label, memberLine(m))
	}
	if v := jsonScalarOrBlank(f.Default); v != "" {
		_, _ = fmt.Fprintf(tw, "default:\t%s\n", v)
	}
	_, _ = fmt.Fprintf(tw, "help:\t%s\n", f.Help)
	_ = tw.Flush()
}

// memberLine renders one member of an object field as ".key (type[: a|b|c]) — help".
func memberLine(m client.FieldSchema) string {
	typ := m.Type
	if len(m.Enum) > 0 {
		typ += ": " + strings.Join(m.Enum, "|")
	}
	line := fmt.Sprintf(".%s (%s)", m.Key, typ)
	if m.Help != "" {
		line += " — " + m.Help
	}
	return line
}

// sortedKeys returns a settings map's keys in stable order for deterministic table output.
func settingKeys(s client.Settings) []string {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// scalar renders a JSON scalar (string/number/bool/null) for a table cell: a string unquoted, a
// number/bool as written, null/empty as "". Keeps the generic bag rendering type-agnostic.
func jsonScalar(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return strings.TrimSpace(string(raw))
	}
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return num(t)
	case bool:
		return fmt.Sprintf("%t", t)
	default:
		// Objects/arrays (e.g. a models.* record): compact, so a server that pretty-prints can't break
		// the table with embedded newlines.
		var buf bytes.Buffer
		if err := json.Compact(&buf, raw); err != nil {
			return strings.TrimSpace(string(raw))
		}
		return buf.String()
	}
}

// scalarOrBlank is scalar but renders the zero value (empty string, 0) as "" so an unset override reads
// blank in the table.
func jsonScalarOrBlank(raw json.RawMessage) string {
	s := jsonScalar(raw)
	if s == "0" {
		return ""
	}
	return s
}
