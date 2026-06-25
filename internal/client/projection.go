package client

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// TenantSettingsSnapshot is the reproducibility record stamped on templated runs:
// source/synced_at/version plus the canonical settings map the projection rendered from.
type TenantSettingsSnapshot struct {
	Source   string         `json:"source,omitempty"`
	SyncedAt string         `json:"synced_at,omitempty"`
	Version  string         `json:"version,omitempty"`
	Settings map[string]any `json:"settings,omitempty"`
}

// TenantSettingValue is one scalar tenant setting surfaced in concise human summaries.
type TenantSettingValue struct {
	Key   string
	Value string
}

// TenantSettingsDriftItem is one setting whose historical run value differs from the tenant's current
// record. Then is what the run saw; Now is what the tenant row holds today.
type TenantSettingsDriftItem struct {
	Key  string `json:"key"`
	Then string `json:"then"`
	Now  string `json:"now"`
}

// ParseTenantSettingsSnapshot decodes RunHeader.TenantSettings. The server currently sends this as a
// JSON string containing the snapshot object; an empty string means a flat or pre-stamp run.
func ParseTenantSettingsSnapshot(raw string) (*TenantSettingsSnapshot, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var snap TenantSettingsSnapshot
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

// BranchSelectorValues returns known projection branch selector values when they are present in the
// settings snapshot. If this is a future project with different selector names, fall back to likely
// selector-shaped scalar keys rather than dumping the whole settings record.
func BranchSelectorValues(settings map[string]any) []TenantSettingValue {
	if len(settings) == 0 {
		return nil
	}
	preferred := []string{
		"newpatient_method",
		"existingpatient_method",
		"reschedule_method",
		"booking_hygienist_dentist_interaction",
	}
	out := selectorValues(settings, preferred)
	if len(out) > 0 {
		return out
	}
	var keys []string
	for k := range settings {
		if strings.HasSuffix(k, "_method") || strings.HasSuffix(k, "_selector") || strings.HasSuffix(k, "_interaction") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return selectorValues(settings, keys)
}

// TenantSettingsDrift compares two tenant-settings snapshots and returns only settings whose values
// differ. Version/source changes with identical settings are deliberately ignored: the warning is for
// variables that could change model behavior.
func TenantSettingsDrift(snapshotRaw, currentRaw string) ([]TenantSettingsDriftItem, error) {
	snapshot, err := ParseTenantSettingsSnapshot(snapshotRaw)
	if err != nil {
		return nil, err
	}
	current, err := ParseTenantSettingsSnapshot(currentRaw)
	if err != nil {
		return nil, err
	}
	if snapshot == nil || current == nil {
		return nil, nil
	}
	keys := map[string]bool{}
	for k := range snapshot.Settings {
		keys[k] = true
	}
	for k := range current.Settings {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	out := make([]TenantSettingsDriftItem, 0)
	for _, k := range sorted {
		then, thenOK := snapshot.Settings[k]
		now, nowOK := current.Settings[k]
		if canonicalSettingValue(then, thenOK) == canonicalSettingValue(now, nowOK) {
			continue
		}
		out = append(out, TenantSettingsDriftItem{
			Key:  k,
			Then: displaySettingValue(then, thenOK),
			Now:  displaySettingValue(now, nowOK),
		})
	}
	return out, nil
}

func selectorValues(settings map[string]any, keys []string) []TenantSettingValue {
	out := make([]TenantSettingValue, 0, len(keys))
	for _, k := range keys {
		v, ok := settings[k]
		if !ok {
			continue
		}
		s, ok := scalarString(v)
		if !ok {
			continue
		}
		out = append(out, TenantSettingValue{Key: k, Value: s})
	}
	return out
}

func canonicalSettingValue(v any, ok bool) string {
	if !ok {
		return "<unset>"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%#v", v)
	}
	return string(b)
}

func displaySettingValue(v any, ok bool) string {
	if !ok {
		return "(unset)"
	}
	if s, ok := scalarString(v); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func scalarString(v any) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "null", true
	case string:
		return x, true
	case bool:
		if x {
			return "true", true
		}
		return "false", true
	case float64:
		return fmt.Sprintf("%g", x), true
	default:
		return "", false
	}
}
