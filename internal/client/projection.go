package client

import (
	"encoding/json"
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

// PromptSection is one candidate block of the run's system prompt with the gate that decided it. Text
// is present only for sections that fired (the server stores none for the off ones).
type PromptSection struct {
	ID   string `json:"id"`
	Gate string `json:"gate"`
	On   bool   `json:"on"`
	Text string `json:"text,omitempty"`
}

// ManifestBlock is one block pasted into (or listed by) the bootstrap orientation turn: what reached
// the model and how much of it, without the body — the bodies are already in RunHeader.BootstrapTurn.
type ManifestBlock struct {
	Path          string `json:"path"`
	Gloss         string `json:"gloss"`
	Presence      string `json:"presence"` // pasted_full | directory_map
	Authoritative bool   `json:"authoritative"`
	Truncated     bool   `json:"truncated"`
	Chars         int    `json:"chars"`
}

// ContextCaptured reports whether the server served this run's persisted prompt context. False is the
// normal case for a run older than the capture or past its 7-day retention window — a renderer must say
// so out loud instead of drawing empty sections.
func (r *RunHeader) ContextCaptured() bool {
	return r != nil && (r.ContextSchemaVersion > 0 || len(r.PromptSections) > 0 ||
		r.BootstrapTurn != "" || r.PreselectedTurn != "")
}

// ParsePromptSections decodes RunHeader.PromptSections; a nil result means nothing was served.
func ParsePromptSections(raw json.RawMessage) ([]PromptSection, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var out []PromptSection
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ParseManifestBlocks decodes RunHeader.ManifestBlocks; a nil result means nothing was served.
func ParseManifestBlocks(raw json.RawMessage) ([]ManifestBlock, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var out []ManifestBlock
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
