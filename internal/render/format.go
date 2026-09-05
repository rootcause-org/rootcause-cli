// Shared cell formatters for every render view. They live here (not next to one view) so the whole
// package clips IDs, renders durations and marks empty cells the same way; the deliberate differences
// between views are passed as arguments rather than forked into per-view copies.

package render

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// runDetailDuration prefers the server's duration_ms, falling back to finished−created (a run_health
// miss leaves duration_ms zero). Blank when neither yields a positive span.
func runDetailDuration(r *client.RunDetail) string {
	if r.DurationMs > 0 {
		return duration(r.DurationMs)
	}
	return runDuration(r.CreatedAt, r.FinishedAt)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// truncate clamps s to at most max runes, appending an ellipsis when it had to cut — keeping the index
// "why" line skimmable. Newlines are collapsed first so the one-liner stays on one line.
func truncate(s string, max int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", "")
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimRight(string(r[:max]), " ") + "…"
}

// joinOrDash joins a list with ", " or renders "-" when empty.
func joinOrDash(ss []string) string {
	if len(ss) == 0 {
		return "-"
	}
	return strings.Join(ss, ", ")
}

// duration renders ms as a compact human string; blank when absent (unfinished run / no duration).
func duration(ms int64) string {
	if ms <= 0 {
		return "-"
	}
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	secs := float64(ms) / 1000.0
	if secs < 60 {
		return fmt.Sprintf("%.1fs", secs)
	}
	mins := int(secs) / 60
	rem := int(secs) % 60
	return fmt.Sprintf("%dm%ds", mins, rem)
}

// runDuration computes a human duration from the run's created/finished timestamps (RFC3339). Blank
// when either is missing/unparseable or the span is non-positive — the server doesn't send a
// duration_ms on the run detail, so this is the only way to show it.
func runDuration(created, finished string) string {
	if created == "" || finished == "" {
		return ""
	}
	start, err1 := time.Parse(time.RFC3339, created)
	end, err2 := time.Parse(time.RFC3339, finished)
	if err1 != nil || err2 != nil {
		return ""
	}
	ms := end.Sub(start).Milliseconds()
	if ms <= 0 {
		return ""
	}
	return duration(ms)
}

// metaString reads a string value from the freeform run metadata map; "" if absent or not a string.
func metaString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

func num(f float64) string { return fmt.Sprintf("%g", f) }

// indentBlock collapses a multi-line stdout/stderr into a single readable line for the trace, keeping
// the table compact (full payloads remain available via -o json).
func indentBlock(s string) string {
	s = strings.TrimRight(s, "\n")
	return strings.ReplaceAll(s, "\n", " ")
}

// clipID clips an id or commit sha to its n-character prefix; a shorter value is returned as-is. Views
// deliberately differ in width (8 for run/thread ids, 12 for shas), so the width is an argument.
func clipID(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// orDash renders an empty cell as placeholder. The placeholder is explicit because the tables use "-"
// and the console views use an em dash.
func orDash(s, placeholder string) string {
	if s == "" {
		return placeholder
	}
	return s
}

// orDashTrimmed is orDash for cells where a whitespace-only value also counts as empty.
func orDashTrimmed(s, placeholder string) string {
	if strings.TrimSpace(s) == "" {
		return placeholder
	}
	return s
}

// clipStr is truncate's harder sibling for the pattern/deploy digests: it also folds "|" (which would
// break a pipe-delimited digest line) and appends no ellipsis, so the clipped text stays machine-greppable.
func clipStr(s string, limit int) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "|", "/"), "\n", " "))
	if len([]rune(s)) <= limit {
		return s
	}
	return string([]rune(s)[:limit])
}

// nullableString / nullableDuration render the action feed's nullable columns; nil and "" both read
// as an empty cell there (the JSON-shaped `null` spelling lives in actions.go's agent format).
func nullableString(value *string) string {
	if value == nil || *value == "" {
		return "-"
	}
	return *value
}

func nullableDuration(value *int64) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprintf("%dms", *value)
}

// settingValue renders one stored settings value compactly: a JSON string as its bare text, null as
// the literal "null" (an explicitly-unconfigured field), anything else as its compact JSON. It differs
// from jsonScalar on purpose — the settings views must show "null" rather than an empty cell, because
// "unset" is a meaningful state there.
func settingValue(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "null" || trimmed == "" {
		return "null"
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return trimmed
}

// sortedRawKeys returns a raw-JSON map's keys, sorted — the settings views print in key order.
func sortedRawKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
