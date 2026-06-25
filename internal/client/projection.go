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
