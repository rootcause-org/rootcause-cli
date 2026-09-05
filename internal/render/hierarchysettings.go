// The `rc config settings get` view: one hierarchy node's local overrides plus, when the server sent
// them, the resolved values with the level each one came from.

package render

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// HierarchySettings prints the scope header, the node's own overrides, and the resolved view.
func HierarchySettings(w io.Writer, hs *client.HierarchySettings) {
	_, _ = fmt.Fprintf(w, "scope: %s", hs.Scope)
	if hs.Project != "" {
		_, _ = fmt.Fprintf(w, "  project: %s", hs.Project)
	}
	if hs.Tenant != "" {
		_, _ = fmt.Fprintf(w, "  tenant: %s", hs.Tenant)
	}
	if hs.Mailbox != "" {
		_, _ = fmt.Fprintf(w, "  mailbox: %s", hs.Mailbox)
	}
	_, _ = fmt.Fprintln(w)

	local := nestedRaw(hs.Settings)
	if len(local) == 0 {
		_, _ = fmt.Fprintln(w, "\nLocal overrides:\n  (none)")
	} else {
		_, _ = fmt.Fprintln(w, "\nLocal overrides:")
		writeNestedRaw(w, local)
	}
	resolved := nestedRaw(hs.Resolved)
	if len(resolved) > 0 {
		_, _ = fmt.Fprintln(w, "\nResolved:")
		writeResolved(w, resolved)
	}
}

func nestedRaw(raw json.RawMessage) map[string]map[string]json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var out map[string]map[string]json.RawMessage
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}

func writeNestedRaw(w io.Writer, groups map[string]map[string]json.RawMessage) {
	for _, group := range sortedNestedGroups(groups) {
		_, _ = fmt.Fprintf(w, "  %s:\n", group)
		for _, field := range sortedRawKeys(groups[group]) {
			_, _ = fmt.Fprintf(w, "    %s = %s\n", field, settingValue(groups[group][field]))
		}
	}
}

func writeResolved(w io.Writer, groups map[string]map[string]json.RawMessage) {
	for _, group := range sortedNestedGroups(groups) {
		_, _ = fmt.Fprintf(w, "  %s:\n", group)
		for _, field := range sortedRawKeys(groups[group]) {
			_, _ = fmt.Fprintf(w, "    %s = %s\n", field, resolvedField(groups[group][field]))
		}
	}
}

// resolvedField renders one resolved entry as "value (source)" — the source is what makes the resolved
// view worth printing at all: it says which level of the hierarchy won.
func resolvedField(raw json.RawMessage) string {
	var f struct {
		Value  json.RawMessage `json:"value"`
		Source string          `json:"source"`
	}
	if json.Unmarshal(raw, &f) != nil {
		return settingValue(raw)
	}
	if f.Source == "" {
		return settingValue(f.Value)
	}
	return fmt.Sprintf("%s (%s)", settingValue(f.Value), f.Source)
}

func sortedNestedGroups(m map[string]map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
