// The `rc tenant settings get` view: a tenant's stored settings, grouped and labelled by the enriched
// schema when the server gave us one, flat key=value when it didn't.

package render

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// SettingsGroup is one schema section header, in display order.
type SettingsGroup struct {
	Key   string
	Label string
}

// SettingsField is what rendering needs to know about one schema property: which group it belongs to,
// where it sorts inside that group, and its human label.
type SettingsField struct {
	Group string
	Order int
	Label string
}

// SettingsSchema is the render-only projection of the enriched tenant schema (the CLI's parsed schema
// also carries type/enum info used for coercion, which rendering never touches). A nil schema means
// "no schema" and falls back to a flat, sorted list.
type SettingsSchema struct {
	Groups []SettingsGroup
	Fields map[string]SettingsField
}

// TenantSettings prints a tenant's settings for a human. With a schema it groups fields by the schema
// groups (in order), labels them, and orders within a group by the field order — only fields the
// record actually carries are shown. The header line carries the tenant + version so an operator can
// confirm what they just wrote.
func TenantSettings(w io.Writer, ts *client.TenantSettings, schema *SettingsSchema) {
	values := map[string]json.RawMessage{}
	if len(ts.Settings) > 0 {
		_ = json.Unmarshal(ts.Settings, &values)
	}
	_, _ = fmt.Fprintf(w, "tenant: %s", ts.TenantID)
	if ts.Version != "" {
		_, _ = fmt.Fprintf(w, "  version: %s", ts.Version)
	}
	if ts.AppliedAt != "" {
		_, _ = fmt.Fprintf(w, "  applied: %s", ts.AppliedAt)
	}
	_, _ = fmt.Fprintln(w)

	if len(values) == 0 {
		_, _ = fmt.Fprintln(w, "(no settings configured)")
		return
	}

	if schema == nil {
		// Plain key=value, sorted — acceptable fallback per the spec.
		for _, k := range sortedRawKeys(values) {
			_, _ = fmt.Fprintf(w, "  %s = %s\n", k, settingValue(values[k]))
		}
		return
	}

	rendered := map[string]bool{}
	for _, g := range schema.Groups {
		// Collect this group's present fields, ordered by field order then name.
		var keys []string
		for k := range values {
			if f, ok := schema.Fields[k]; ok && f.Group == g.Key {
				keys = append(keys, k)
			}
		}
		if len(keys) == 0 {
			continue
		}
		sort.SliceStable(keys, func(i, j int) bool {
			fi, fj := schema.Fields[keys[i]], schema.Fields[keys[j]]
			if fi.Order != fj.Order {
				return fi.Order < fj.Order
			}
			return keys[i] < keys[j]
		})
		_, _ = fmt.Fprintf(w, "\n%s:\n", g.Label)
		for _, k := range keys {
			rendered[k] = true
			_, _ = fmt.Fprintf(w, "  %s = %s\n", settingLabel(schema, k), settingValue(values[k]))
		}
	}
	// Any field not placed by the schema (unknown key on the stored record) — show it so nothing is
	// silently hidden.
	var orphans []string
	for k := range values {
		if !rendered[k] {
			orphans = append(orphans, k)
		}
	}
	if len(orphans) > 0 {
		sort.Strings(orphans)
		_, _ = fmt.Fprintln(w, "\nOther:")
		for _, k := range orphans {
			_, _ = fmt.Fprintf(w, "  %s = %s\n", k, settingValue(values[k]))
		}
	}
}

// settingLabel returns "key (label)" when the schema has a human label, else just the key — so the raw
// key (what `set` takes) is always visible alongside the label.
func settingLabel(schema *SettingsSchema, key string) string {
	if f, ok := schema.Fields[key]; ok && f.Label != "" {
		return fmt.Sprintf("%s (%s)", key, f.Label)
	}
	return key
}
