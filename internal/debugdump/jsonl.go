package debugdump

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// EmitJSONL writes the drill-down event log: a {"type":"run"} header line (run metadata + full
// draft/note bodies + the untrimmed system prompt + egress) followed by one {"type":"event"} line per
// tool call, every field FULL and untruncated, keyed by `disp`. Header rollups are `run_`-prefixed so
// event-space jq queries (`select(.duration_ms > 60000)`) never match the header.
func EmitJSONL(w io.Writer, full *client.FullResponse) error {
	events := decorate(full.Events)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	r := full.Run
	header := map[string]any{
		"type":                    "run",
		"run_id":                  r.RunID,
		"project":                 r.Project,
		"tenant":                  emptyNil(r.Tenant),
		"status":                  r.Status,
		"kind":                    r.Kind,
		"trigger":                 emptyNil(r.Trigger),
		"brain_ref":               emptyNil(r.BrainRef),
		"brain_resolved":          emptyNil(r.BrainResolved),
		"tenant_settings":         tenantSettingsJSON(r.TenantSettings),
		"tenant_settings_current": tenantSettingsJSON(r.TenantSettingsCurrent),
		"error":                   emptyNil(r.Error),
		"thread_id":               emptyNil(r.ThreadID),
		"session_id":              emptyNil(r.SessionID),
		"topic":                   emptyNil(r.Topic),
		"question":                emptyNil(r.Question),
		"warm_start_digest":       emptyNil(r.WarmStartDigest),
		"grounding_seed":          emptyNil(r.GroundingSeed),
		"system_prompt":           emptyNil(r.SystemPrompt),
		"created_at":              emptyNil(r.CreatedAt),
		"finished_at":             emptyNil(r.FinishedAt),
		"draft":                   emptyNil(r.Draft),
		"notes":                   notesJSON(r.Notes),
		"metadata":                metadataJSON(r.Metadata),
		"egress":                  egressJSON(r.Egress),
	}
	// A redacted bundle still gets its JSONL (whatever the server sent) — but the header must carry the
	// marker so a jq consumer can tell "nothing happened" from "nothing was served".
	if full.Redacted() {
		header["detail_redacted"] = true
	}
	if len(r.GroundingSourcesRaw) > 0 {
		header["grounding_sources"] = json.RawMessage(r.GroundingSourcesRaw)
		header["grounding_source_drift_count"] = client.GroundingSourceDriftCount(r.GroundingSources)
	} else if r.GroundingSources != nil {
		header["grounding_sources"] = r.GroundingSources
		header["grounding_source_drift_count"] = client.GroundingSourceDriftCount(r.GroundingSources)
	}
	if len(r.ProposedActions) > 0 {
		header["proposed_actions"] = r.ProposedActions
	}
	// The persisted prompt context, under the server's own field names so the rc-debug jq recipes read
	// the same keys the API documents. context_schema_version is written even when 0: that zero IS the
	// "this run predates the capture / aged out" signal, and a jq consumer needs it present to test it.
	header["context_schema_version"] = r.ContextSchemaVersion
	if len(r.PromptSections) > 0 {
		header["prompt_sections"] = json.RawMessage(r.PromptSections)
	}
	if len(r.ManifestBlocks) > 0 {
		header["manifest_blocks"] = json.RawMessage(r.ManifestBlocks)
	}
	if r.BootstrapTurn != "" {
		header["bootstrap_turn"] = r.BootstrapTurn
	}
	if r.PreselectedTurn != "" {
		header["preselected_turn"] = r.PreselectedTurn
	}
	if drift, err := client.TenantSettingsDrift(r.TenantSettings, r.TenantSettingsCurrent); err == nil && len(drift) > 0 {
		header["tenant_settings_drift"] = drift
	}
	if err := enc.Encode(header); err != nil {
		return err
	}
	for _, e := range events {
		line := map[string]any{
			"type":        "event",
			"disp":        e.disp,
			"seq":         e.src.Seq,
			"grounding":   e.grounding,
			"tool":        e.src.Tool,
			"label":       e.label,
			"command":     e.command,
			"stdout":      emptyNil(e.src.Stdout),
			"stderr":      emptyNil(e.src.Stderr),
			"exit_code":   e.src.ExitCode,
			"status":      e.src.Status,
			"duration_ms": e.src.DurationMs,
			"at":          emptyNil(e.src.At),
			"reasoning":   emptyNil(e.src.Reasoning),
		}
		// Bash's full input is `command`; other tools carry their structured input in `args`.
		if e.src.Tool != "bash" {
			if len(e.src.Args) > 0 {
				line["args"] = json.RawMessage(e.src.Args)
			} else {
				line["args"] = map[string]any{}
			}
		}
		if err := enc.Encode(line); err != nil {
			return err
		}
	}
	return nil
}

// --- JSON shapers (header fields that need a null or typed shape, not a bare string) ------------------

func emptyNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func notesJSON(notes []client.Note) []map[string]any {
	out := make([]map[string]any, 0, len(notes))
	for _, n := range notes {
		out = append(out, map[string]any{"key": n.Key, "body": n.Body})
	}
	return out
}

// metadataJSON passes the run's freeform metadata into the JSONL header, minus the host-only keys an
// older server may still emit (spend/tokens, serving model identity) — the dump is a read surface like
// any other.
func metadataJSON(m map[string]any) any {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if client.HostOnlyMetadataKey(k) {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func tenantSettingsJSON(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if json.Valid([]byte(raw)) {
		return json.RawMessage(raw)
	}
	return raw
}

func egressJSON(egress []client.EgressItem) []map[string]any {
	out := make([]map[string]any, 0, len(egress))
	for _, g := range egress {
		out = append(out, map[string]any{"host": g.Host, "count": g.Count, "blocked": g.Blocked})
	}
	return out
}
