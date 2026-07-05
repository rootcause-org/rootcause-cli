package render

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// This file holds the GENERIC collection renderers — one list view, one item view — shared by every
// noun command (repo / connection / member / token). They hold no per-resource knowledge: they render
// whatever flat keys the server returned, in sorted order, so a new server-side field shows up with no
// CLI change (the same thin-client invariant as the settings bag).

// ItemList renders a collection as a table: one column per field, "id" pinned first, the rest sorted.
// Empty → "(none)". A pure function of the wire items so a golden pins it.
func ItemList(w io.Writer, l *client.ListResponse) {
	if l == nil || len(l.Items) == 0 {
		_, _ = fmt.Fprintln(w, "(none)")
		return
	}
	cols := itemColumns(l.Items)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	header := make([]string, len(cols))
	for i, c := range cols {
		header[i] = strings.ToUpper(c)
	}
	_, _ = fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, it := range l.Items {
		row := make([]string, len(cols))
		for i, c := range cols {
			row[i] = cellValue(it[c])
		}
		_, _ = fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	_ = tw.Flush()
}

// Item renders one collection element as a key: value block (sorted, "id" first). Used by add/set and
// the verb echoes that return an item.
func Item(w io.Writer, it client.Item) {
	if len(it) == 0 {
		_, _ = fmt.Fprintln(w, "(no fields returned)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, k := range itemKeysIDFirst(it) {
		_, _ = fmt.Fprintf(tw, "%s:\t%s\n", k, cellValue(it[k]))
	}
	_ = tw.Flush()
}

// Secret renders a reveal/mint response: it prints the secret VALUE alone (so a pipe captures just the
// token), preferring a "secret" then "refresh_token" key. Any other fields are shown as a key: value
// block beneath. Never quotes the value. With no recognizable secret key it falls back to the full item.
func Secret(w io.Writer, it client.Item) {
	for _, k := range []string{"secret", "refresh_token", "token"} {
		if raw, ok := it[k]; ok {
			_, _ = fmt.Fprintln(w, cellValue(raw))
			return
		}
	}
	Item(w, it)
}

// ConnectionProbe renders `rc connection probe`: concise diagnostics, while -o json keeps the full raw
// response for scripts.
func ConnectionProbe(w io.Writer, p *client.ConnectionProbeResult) {
	if p == nil {
		_, _ = fmt.Fprintln(w, "(no probe result)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "ok:\t%s\n", yesNo(p.OK))
	_, _ = fmt.Fprintf(tw, "capability:\t%s (%s/%s)\n", p.Capability.Key, p.Capability.Platform, p.Capability.Tier)
	_, _ = fmt.Fprintf(tw, "grant:\t%s", yesNo(p.Grant.OK))
	if p.Grant.Label != "" {
		_, _ = fmt.Fprintf(tw, " label=%s", p.Grant.Label)
	}
	if p.Grant.Error != "" {
		_, _ = fmt.Fprintf(tw, " error=%s", p.Grant.Error)
	}
	_, _ = fmt.Fprintln(tw)
	_, _ = fmt.Fprintf(tw, "action plane:\t%s mode=%s runner=%t reverse_secret=%t\n",
		p.Action.Status, p.Action.Mode, p.Action.RunnerURLConfigured, p.Action.ReverseSecretConfigured)
	if p.Provider != nil {
		_, _ = fmt.Fprintf(tw, "provider:\t%s write=%t read_back=%t cleanup=%t", p.Provider.Name, p.Provider.Write, p.Provider.ReadBack, p.Provider.Cleanup)
		if p.Provider.ObjectID != "" {
			_, _ = fmt.Fprintf(tw, " object=%s", p.Provider.ObjectID)
		}
		if p.Provider.Error != "" {
			_, _ = fmt.Fprintf(tw, " error=%s", p.Provider.Error)
		}
		_, _ = fmt.Fprintln(tw)
	}
	for _, step := range p.Steps {
		_, _ = fmt.Fprintf(tw, "step:\t%s %s", step.Name, yesNo(step.OK))
		if step.Detail != "" {
			_, _ = fmt.Fprintf(tw, " %s", step.Detail)
		}
		_, _ = fmt.Fprintln(tw)
	}
	for _, warning := range p.Warnings {
		_, _ = fmt.Fprintf(tw, "warning:\t%s\n", warning)
	}
	_ = tw.Flush()
}

// itemColumns is the union of all field keys across the items, "id" pinned first then the rest sorted —
// a stable column order so the table is deterministic regardless of map iteration.
func itemColumns(items []client.Item) []string {
	seen := map[string]bool{}
	for _, it := range items {
		for k := range it {
			seen[k] = true
		}
	}
	return keysIDFirst(seen)
}

func itemKeysIDFirst(it client.Item) []string {
	seen := make(map[string]bool, len(it))
	for k := range it {
		seen[k] = true
	}
	return keysIDFirst(seen)
}

// keysIDFirst returns the key set with "id" first (when present) and the rest alphabetically sorted.
func keysIDFirst(seen map[string]bool) []string {
	rest := make([]string, 0, len(seen))
	for k := range seen {
		if k != "id" {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	if seen["id"] {
		return append([]string{"id"}, rest...)
	}
	return rest
}

// cellValue renders a flat field's JSON value for a table cell: a string unquoted, a scalar as written,
// a list/array joined with commas, anything else compact JSON. Absent → "".
func cellValue(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
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
	case []any:
		parts := make([]string, len(t))
		for i, e := range t {
			parts[i] = fmt.Sprintf("%v", e)
		}
		return strings.Join(parts, ",")
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}
