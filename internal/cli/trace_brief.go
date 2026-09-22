package cli

import (
	"encoding/json"
	"fmt"

	"github.com/rootcause-org/rootcause-cli/internal/digest"
)

// briefDroppedHeaderKeys are the trace-header fields `--brief` withholds: the run's INPUT CONTEXT.
// Measured on a real chat turn they are ~90% of the bundle (system_prompt 61%, the two tenant-settings
// blobs — byte-identical to each other on an undrifted tenant — 17%, the rest prompt scaffolding),
// and none of them answer "what was asked and what did we answer". They stay one `--raw-output` away.
//
// preselected_turn rides along with bootstrap_turn: same family (a verbatim pasted orientation turn),
// same reason.
var briefDroppedHeaderKeys = []string{
	"system_prompt",
	"prompt_sections",
	"manifest_blocks",
	"bootstrap_turn",
	"preselected_turn",
	"grounding_sources",
	"tenant_settings",
	"tenant_settings_current",
}

// briefTrace projects the /trace bundle down to the conversation-level facts, working on the RAW server
// bytes so every field we keep rides through verbatim (including ones the CLI does not model yet):
// question, prior_messages, draft/answer, notes, metadata, guards, egress, error. The events list is
// dropped wholesale — a timeline is debugging, not reading. tenant_settings_drift is COMPUTED from the
// two blobs before they go, so the one fact worth knowing about them survives their removal.
func briefTrace(raw json.RawMessage) (json.RawMessage, error) {
	var bundle struct {
		Run json.RawMessage `json:"run"`
	}
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return nil, fmt.Errorf("decode trace bundle: %w", err)
	}
	fields := map[string]json.RawMessage{}
	if len(bundle.Run) > 0 {
		if err := json.Unmarshal(bundle.Run, &fields); err != nil {
			return nil, fmt.Errorf("decode trace run header: %w", err)
		}
	}
	if drift := briefTenantDrift(fields); drift != nil {
		fields["tenant_settings_drift"] = drift
	}
	for _, k := range briefDroppedHeaderKeys {
		delete(fields, k)
	}
	return json.Marshal(map[string]any{"run": fields})
}

// briefTenantDrift computes the settings-drift list from the two snapshots the brief is about to drop.
// nil when there is no drift (or nothing to compare) — an empty list would read as a verified "no
// drift" on a run that never carried tenant settings at all.
func briefTenantDrift(fields map[string]json.RawMessage) json.RawMessage {
	if existing, ok := fields["tenant_settings_drift"]; ok {
		return existing // a server that already ships it wins; never recompute over the source of truth.
	}
	drift, err := digest.TenantSettingsDrift(jsonString(fields["tenant_settings"]), jsonString(fields["tenant_settings_current"]))
	if err != nil || len(drift) == 0 {
		return nil
	}
	out, err := json.Marshal(drift)
	if err != nil {
		return nil
	}
	return out
}

// jsonString unwraps a tenant-settings snapshot, which the server ships as a JSON-encoded STRING on
// some runs and as an object on others. Both reach digest.TenantSettingsDrift as text.
func jsonString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}
