// Wire contract for runs and traces (summaries, run/event detail, submit + scope preview, headers, brain diff).
// Field names and omitempty MUST match the server verbatim; the ground rules live in types.go.
package client

import "encoding/json"

// RunSummary is one row of GET /api/v1/runs. FinishedAt/DurationMs are absent on an unfinished run.
// Topic/DeclinedReason and the Health block are operator-tier extras the server attaches for a
// developer/admin bearer — `rc fleet runs` reads them for the digest's flags. They're absent (zero/nil)
// for a baseline bearer, so the digest degrades to the safe columns rather than erroring.
type RunSummary struct {
	RunID          string     `json:"run_id"`
	ThreadID       string     `json:"thread_id,omitempty"`
	SessionID      string     `json:"session_id,omitempty"`
	LocalThreadID  string     `json:"local_thread_id,omitempty"`
	TurnKey        string     `json:"turn_key,omitempty"`
	Kind           string     `json:"kind"`
	Source         string     `json:"source"`
	Status         string     `json:"status"`
	Outcome        string     `json:"outcome"`
	Category       string     `json:"category"`
	CreatedAt      string     `json:"created_at"`
	FinishedAt     string     `json:"finished_at,omitempty"`
	DurationMs     int64      `json:"duration_ms,omitempty"`
	HasDraft       bool       `json:"has_draft"`
	HasNote        bool       `json:"has_note"`
	DeclinedReason string     `json:"declined_reason,omitempty"`
	Topic          string     `json:"topic,omitempty"`
	Health         *RunHealth `json:"health,omitempty"`
	Learning       Learning   `json:"learning"`
	Review         *Review    `json:"review,omitempty"`
	// Attribution is present on `rc run thread`: stable ids joining one exact inbound turn to its run,
	// drafts, eventual sent messages, and human feedback. Ordinary run-index rows omit it.
	Attribution *RunAttribution `json:"attribution,omitempty"`
	raw         json.RawMessage
}

// Review is operator-tier human feedback attached when GET /runs is explicitly filtered with
// reviewed=true. It is distinct from Learning: held-out eval threads are reviewable but never training
// evidence, so their review rides here while Learning.Feedback remains false.
type Review struct {
	Score   int    `json:"score"`
	Comment string `json:"comment,omitempty"`
}

type RunAttribution struct {
	LocalThreadID     string                  `json:"local_thread_id,omitempty"`
	ThreadID          string                  `json:"thread_id"`
	SessionID         string                  `json:"session_id"`
	TurnKey           string                  `json:"turn_key"`
	RetryOf           string                  `json:"retry_of,omitempty"`
	ParentRunID       string                  `json:"parent_run_id,omitempty"`
	OriginRunID       string                  `json:"origin_run_id"`
	TriggerMessageIDs []string                `json:"trigger_message_ids"`
	Drafts            []RunAttributionDraft   `json:"drafts"`
	Feedback          *RunAttributionFeedback `json:"feedback,omitempty"`
}

type RunAttributionDraft struct {
	DraftID       string `json:"draft_id"`
	Status        string `json:"status"`
	SentMessageID string `json:"sent_message_id,omitempty"`
}

type RunAttributionFeedback struct {
	Score   *int16 `json:"score,omitempty"`
	Comment string `json:"comment,omitempty"`
}

func (r *RunSummary) UnmarshalJSON(data []byte) error {
	type wire RunSummary
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*r = RunSummary(decoded)
	r.raw = append(r.raw[:0], data...)
	return nil
}

func (r RunSummary) MarshalJSON() ([]byte, error) {
	if len(r.raw) > 0 {
		return append([]byte(nil), r.raw...), nil
	}
	type wire RunSummary
	return json.Marshal(wire(r))
}

// RunHealth is the per-run triage block on a run index row (run_health view). Spend, token counts and the
// SERVING MODEL IDENTITY are deliberately absent: naming the rung that answered is as
// cost-reverse-engineerable as naming the provider that served it, so both are host-only telemetry the
// server strips on EVERY tier (operator included) — heaviness is read off turns/bash/duration, and the
// fallback story off the content-free IsFallback boolean. Mirrors the server's runIndexHealth
// field-for-field.
type RunHealth struct {
	Turns          int64 `json:"turns"`
	GroundingTurns int64 `json:"grounding_turns"`
	BashTotal      int64 `json:"bash_total"`
	BashErrCount   int64 `json:"bash_err_count"`
	// BashErrRealCount / BashErrExploreCount split BashErrCount (real + explore = total): "explore" is
	// benign exploration noise (rg/grep exit-1 no-match, grounding pre-step probes), "real" a genuine
	// failure. Both zero on a pre-split server — render helpers fall back to BashErrCount as real.
	BashErrRealCount    int64 `json:"bash_err_real_count"`
	BashErrExploreCount int64 `json:"bash_err_explore_count"`
	BigStdoutCount      int64 `json:"big_stdout_count"`
	BlockedEgress       int64 `json:"blocked_egress"`
	GroundingDiscarded  bool  `json:"grounding_discarded"`
	NoJournal           bool  `json:"no_journal"`
	// IsFallback is the CONTENT-FREE half of the model-fallback signal (run_health.is_fallback): THAT
	// the loop swapped rungs, never between WHICH models. It is the only fallback fact any surface gets
	// — the model / model_fallback_from columns behind it stay host-side (superadmin SQL). The
	// empty-string-vs-NULL trap on runs.model_fallback_from is baked into the view, so the CLI never
	// recomputes it.
	IsFallback bool `json:"is_fallback"`
	// ErrorHead is the first 120 chars of the run's host-side error (run_health.error_head), '' when
	// none — the index-level class discriminator (DSN outage vs OAuth burst vs dead-letter) so a burst
	// doesn't cost one `rc run debug` drill per run. Same tier as the full error on the per-run rung.
	ErrorHead string `json:"error_head,omitempty"`
}

// Learning is the privacy-safe dream-cycle pointer attached to a run index row. It carries booleans
// plus the content-free shadow verdict; the evidence endpoint owns comments, bodies, and delta detail.
type Learning struct {
	Feedback         bool   `json:"feedback"`
	SentDelta        bool   `json:"sent_delta"`
	SentDeltaShadow  bool   `json:"sent_delta_shadow,omitempty"`
	SentDeltaVerdict string `json:"sent_delta_verdict,omitempty"`
	TriageSkipped    bool   `json:"triage_skipped"`
	TriageCorrected  bool   `json:"triage_corrected"`
}

// DreamEvidenceResponse is the heterogeneous JSON-only consolidation corpus returned by
// GET /api/v1/dream/evidence. Feedback and triage stay raw because each plane has its own shape; Deltas
// carries the stable sent-delta projection used by shadow consumers.
type DreamEvidenceResponse struct {
	Project  string               `json:"project"`
	Feedback []json.RawMessage    `json:"feedback,omitempty"`
	Deltas   []DreamDeltaEvidence `json:"deltas,omitempty"`
	Triage   []json.RawMessage    `json:"triage,omitempty"`
}

// DreamDeltaEvidence mirrors one deltas[] row. The shadow additions are optional for compatibility
// with older hosts and live-edit rows. ServedScore is a pointer because absent/null means unjudged or
// not_answerable, distinct from every valid 1..5 score.
type DreamDeltaEvidence struct {
	ID               string  `json:"id"`
	RelatedRunID     string  `json:"related_run_id,omitempty"`
	Shadow           bool    `json:"shadow,omitempty"`
	ShadowVerdict    string  `json:"shadow_verdict,omitempty"`
	ServedScore      *int    `json:"served_score,omitempty"`
	Topic            string  `json:"topic,omitempty"`
	QuestionExcerpt  string  `json:"question_excerpt,omitempty"`
	DeltaCategory    string  `json:"delta_category,omitempty"`
	DeltaDescription string  `json:"delta_description,omitempty"`
	Similarity       float64 `json:"similarity,omitempty"`
}

// SourceCount is the per-source tally inside the health summary.
type SourceCount struct {
	Total  int `json:"total"`
	Errors int `json:"errors"`
}

// SuccessRef / ErrorRef are the last-success / last-error pointers (nullable server-side).
type SuccessRef struct {
	RunID  string `json:"run_id"`
	Source string `json:"source"`
	At     string `json:"at"`
}

type ErrorRef struct {
	RunID    string `json:"run_id"`
	Source   string `json:"source"`
	Category string `json:"category"`
	At       string `json:"at"`
}

// AttentionItem flags a run needing a human look (the health summary's worklist).
type AttentionItem struct {
	Kind     string `json:"kind"`
	RunID    string `json:"run_id"`
	Source   string `json:"source"`
	Category string `json:"category"`
	Outcome  string `json:"outcome"`
	At       string `json:"at"`
}

// Summary is the health rollup that leads `rc status`. last_success/last_error are pointers because
// the server omits them entirely when there is no such run (omitempty) — either way they decode to a
// nil pointer, distinct from a zero-valued ref.
type Summary struct {
	Healthy        bool                   `json:"healthy"`
	CountsBySource map[string]SourceCount `json:"counts_by_source"`
	LastSuccess    *SuccessRef            `json:"last_success"`
	LastError      *ErrorRef              `json:"last_error"`
	Attention      []AttentionItem        `json:"attention"`
}

// RunsResponse is GET /api/v1/runs. NextBefore is the cursor for the next (older) page; absent/empty
// on the last page.
type RunsResponse struct {
	Runs       []RunSummary `json:"runs"`
	Summary    Summary      `json:"summary"`
	NextBefore string       `json:"next_before,omitempty"`
}

// RunDebug groups the run's debug/triage signals — the "why" a project-dev needs when a run did
// something surprising: why it declined (decline_reason), whether a loop guardrail tripped (guardrail
// sub-cause), whether the final answer was a FORCED submission under budget pressure (forced cause,
// e.g. "budget"/"timeout"), and how many recoverable (transient) errors were retried in-loop. Surfaced
// under a single optional "debug" object on GET /api/v1/runs/{id} and /trace's run (progressive
// disclosure) — the whole object is omitempty so a clean run carries nothing and the typed pointer
// stays nil. Field names match the server verbatim. No model identity rides here either: the rung a run
// fell back FROM is host-only telemetry like the rung that answered (run_health.is_fallback carries the
// content-free THAT).
type RunDebug struct {
	DeclineReason      string `json:"decline_reason,omitempty"`
	Guardrail          string `json:"guardrail,omitempty"`
	Forced             string `json:"forced,omitempty"`
	RecoverableRetries int    `json:"recoverable_retries,omitempty"`
}

// RunDetail is GET /api/v1/runs/{id} — it MUST mirror the server's statusResponse (internal/api/prompt.go)
// field-for-field: same json tags, same omitempty. Optional fields are omitempty server-side; Attachments
// is always present (always [] in v0). category/has_draft/has_note come from the shared row-builder;
// duration_ms/turns/bash_total are the run_health triage scalars.
type RunDetail struct {
	RunID           string           `json:"run_id"`
	ThreadID        string           `json:"thread_id,omitempty"`
	SessionID       string           `json:"session_id,omitempty"`
	LocalThreadID   string           `json:"local_thread_id,omitempty"`
	TurnKey         string           `json:"turn_key,omitempty"`
	Scenario        string           `json:"scenario,omitempty"`
	Status          string           `json:"status"`
	Kind            string           `json:"kind"`
	Category        string           `json:"category"`
	Outcome         string           `json:"outcome,omitempty"`
	CreatedAt       string           `json:"created_at"`
	FinishedAt      string           `json:"finished_at,omitempty"`
	DurationMs      int64            `json:"duration_ms,omitempty"`
	HasDraft        bool             `json:"has_draft"`
	HasNote         bool             `json:"has_note"`
	Turns           int64            `json:"turns,omitempty"`
	BashTotal       int64            `json:"bash_total,omitempty"`
	AnswerMarkdown  string           `json:"answer_markdown,omitempty"`
	DraftMarkdown   string           `json:"draft_markdown,omitempty"`
	Notes           []Note           `json:"notes,omitempty"`
	DeclineReason   string           `json:"decline_reason,omitempty"`
	ProposedActions []ProposedAction `json:"proposed_actions,omitempty"`
	SourcePR        *SourcePR        `json:"source_pr,omitempty"`
	RunURL          string           `json:"run_url,omitempty"`
	Attachments     []any            `json:"attachments"`
	Error           string           `json:"error,omitempty"`
	Debug           *RunDebug        `json:"debug,omitempty"`
	Metadata        map[string]any   `json:"metadata,omitempty"`
}

// Event is one tool-call in a run's trace (GET /api/v1/runs/{id}/events). Command is bash-only;
// HasDraft/HasNote are reply-only; stdout/stderr are omitempty.
type Event struct {
	Seq        int32  `json:"seq"`
	Tool       string `json:"tool"`
	Status     string `json:"status"`
	ExitCode   int32  `json:"exit_code"`
	DurationMs int64  `json:"duration_ms"`
	At         string `json:"at"`
	Command    string `json:"command,omitempty"`
	HasDraft   bool   `json:"has_draft,omitempty"`
	HasNote    bool   `json:"has_note,omitempty"`
	// DeclineReason is reply-only: the reasoned "why nothing" on a terminal reply event that DECLINED
	// (neither a draft nor a note was placed). omitempty, so a normal reply event carries it as "".
	DeclineReason string `json:"decline_reason,omitempty"`
	Stdout        string `json:"stdout,omitempty"`
	Stderr        string `json:"stderr,omitempty"`
}

// EventsResponse is GET /api/v1/runs/{id}/events. DetailRedacted is the server's "you are not a
// project-level admin" marker: the call still 200s with the same envelope, but Events comes back EMPTY.
// Renderers MUST say "withheld" — an empty list here is NOT a clean bill of health. Absent on older
// servers, which is indistinguishable from "full detail served" (the pre-redaction contract).
type EventsResponse struct {
	RunID          string  `json:"run_id"`
	Events         []Event `json:"events"`
	DetailRedacted bool    `json:"detail_redacted,omitempty"`
}

// SubmitRequest is the rich POST /api/v1/runs body plus optional URL scope. Scenario is explicit even
// for the default email simulation; sender/subject shape the synthetic inbound email for that scenario.
// Project is the ?project= selector for all-projects admin tokens, never JSON.
type SubmitRequest struct {
	Prompt          string       `json:"prompt"`
	Scenario        string       `json:"scenario"`
	SessionID       string       `json:"session_id,omitempty"`
	Tenant          string       `json:"tenant,omitempty"`
	BrainRef        string       `json:"brain_ref,omitempty"`
	ReasoningEffort string       `json:"reasoning_effort,omitempty"`
	Sender          string       `json:"sender,omitempty"`
	Subject         string       `json:"subject,omitempty"`
	Principal       *Principal   `json:"principal,omitempty"`
	Attachments     []Attachment `json:"attachments,omitempty"`
	// DryScope resolves and returns the principal scope this run WOULD get, then stops — no agent loop, no
	// LLM spend, no run row. The response is a ScopePreview, not a SubmitResponse.
	DryScope bool   `json:"dry_scope,omitempty"`
	Project  string `json:"-"`
}

// ScopePreview is the 200 body of a POST /api/v1/runs {dry_scope:true} request: the resolved principal
// scope a real run would get (Resolved + Scope), or the server's fail-closed refusal reason (Error).
type ScopePreview struct {
	DryScope bool         `json:"dry_scope"`
	Resolved bool         `json:"resolved"`
	Error    string       `json:"error"`
	Scope    *ScopeRecord `json:"scope"`
}

// ScopeRecord mirrors the server's principalScopeRecord verbatim: the operator-visible identity resolution
// (IDs, never secrets). Fields are empty on an unresolved (resolution:none) record.
type ScopeRecord struct {
	Kind        string            `json:"kind,omitempty"`
	ExternalID  string            `json:"external_id,omitempty"`
	AssertedBy  string            `json:"asserted_by,omitempty"`
	Assurance   string            `json:"assurance,omitempty"`
	Resolution  string            `json:"resolution"`
	Claims      map[string]any    `json:"claims,omitempty"`
	TableGrants *ScopeTableGrants `json:"table_grants,omitempty"`
}

// ScopeTableGrants is the per-principal readable-table decision: the full-grant flag, else the granted names.
type ScopeTableGrants struct {
	All     bool     `json:"all,omitempty"`
	Granted []string `json:"granted,omitempty"`
}

// Attachment is one local file uploaded by rc ask for a synthetic Prompt API run.
type Attachment struct {
	Filename      string `json:"filename"`
	MimeType      string `json:"mime_type,omitempty"`
	SizeBytes     int64  `json:"size_bytes"`
	ContentBase64 string `json:"content_base64"`
}

// Principal is the optional structured identity assertion on a triggered run (data-scoping), mirroring
// the server's webhook.ProjectPrincipal contract verbatim. Dormant unless the project declares
// scope_claims; kind+external_id are the required pair, asserted_by/assurance are server-defaulted when
// omitted. NO tenant_hint — tenant binding is the explicit --tenant slug, not part of the principal.
type Principal struct {
	Kind       string `json:"kind"`
	ExternalID string `json:"external_id"`
	AssertedBy string `json:"asserted_by,omitempty"`
	Assurance  string `json:"assurance,omitempty"`
}

// ScopePreviewReport mirrors the server's manifestcheck.PreviewReport verbatim: the scoped view a real run
// of (tenant, principal) would see — per-table counts + sample rows + the compiled predicate, plus the
// resolved claim summary and tenant binding.
type ScopePreviewReport struct {
	Project         string              `json:"project"`
	DSNEnv          string              `json:"dsn_env"`
	Tenant          string              `json:"tenant,omitempty"`
	TenantPredicate bool                `json:"tenant_predicate"`
	ScopeValue      string              `json:"scope_value,omitempty"`
	Principal       *Principal          `json:"principal,omitempty"`
	Claims          map[string]string   `json:"claims,omitempty"`
	Tables          []ScopePreviewTable `json:"tables"`
}

// ScopePreviewTable is one scoped view's evidence: the row count under the scoped predicate, up to a few
// sample rows, and the compiled WHERE the view enforces.
type ScopePreviewTable struct {
	Name      string           `json:"name"`
	Count     int64            `json:"count"`
	Predicate string           `json:"predicate,omitempty"`
	Rows      []map[string]any `json:"rows"`
}

// SubmitResponse is the 202 body from POST /api/v1/runs: the run id + where/when to poll. PollAfterMs
// is the server's hint for the poll interval (ms); 0 → the caller picks a default.
type SubmitResponse struct {
	RunID       string `json:"run_id"`
	Status      string `json:"status"`
	StatusURL   string `json:"status_url"`
	PollAfterMs int    `json:"poll_after_ms"`
}

// Note is one named note body on a run, returned in full by /trace (vs. the has_note boolean on the
// lean run detail).
type Note struct {
	Key          string       `json:"key,omitempty"`
	Body         string       `json:"body,omitempty"`
	BodyMarkdown string       `json:"body_markdown,omitempty"`
	BodyHTML     string       `json:"body_html,omitempty"`
	BodyText     string       `json:"body_text,omitempty"`
	Actions      []NoteAction `json:"actions,omitempty"`
}

// NoteAction is the email-plane button shape nested under notes[].actions.
type NoteAction struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url"`
	Color       string `json:"color,omitempty"`
}

// ProposedAction is the canonical pull-plane action proposal shape from rootcause.
type ProposedAction struct {
	ID          string `json:"id"`
	Slug        string `json:"slug,omitempty"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url,omitempty"`
	Color       string `json:"color,omitempty"`
}

// SourcePR is a proposed source change. The host may return either the original proposal fields or the
// opened PR URL; all fields are optional so older/newer servers decode cleanly.
type SourcePR struct {
	Repo  string `json:"repo,omitempty"`
	Base  string `json:"base,omitempty"`
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`
	Diff  string `json:"diff,omitempty"`
	URL   string `json:"url,omitempty"`
}

// EgressItem is one host the run reached out to (the egress_log rollup): how many times, and whether
// the egress proxy blocked it.
type EgressItem struct {
	Host    string `json:"host"`
	Count   int    `json:"count"`
	Blocked bool   `json:"blocked"`
}

// GroundingSources is the reproducibility stamp for what the run saw: the historical snapshot
// mounted into the workspace plus the current sync state so the CLI can surface stale or missing
// grounding without recomputing anything. Old runs return captured:false with a reason.
type GroundingSources struct {
	Captured         bool              `json:"captured"`
	Reason           string            `json:"reason,omitempty"`
	CapturedAt       string            `json:"captured_at,omitempty"`
	CurrentCheckedAt string            `json:"current_checked_at,omitempty"`
	Sources          []GroundingSource `json:"sources,omitempty"`
}

// GroundingSource is one mounted grounding input (mirror, kb, or a future kind). Details stays
// freeform because each kind owns its provider/scope payload.
type GroundingSource struct {
	Kind          string                  `json:"kind"`
	Name          string                  `json:"name"`
	MountPath     string                  `json:"mount_path,omitempty"`
	Configured    bool                    `json:"configured"`
	Available     bool                    `json:"available"`
	Mounted       bool                    `json:"mounted"`
	Ref           string                  `json:"ref,omitempty"`
	CommitSHA     string                  `json:"commit_sha,omitempty"`
	CommittedAt   string                  `json:"committed_at,omitempty"`
	LastOKAt      string                  `json:"last_ok_at,omitempty"`
	LastAttemptAt string                  `json:"last_attempt_at,omitempty"`
	State         string                  `json:"state,omitempty"`
	Details       map[string]any          `json:"details,omitempty"`
	Current       *GroundingSourceCurrent `json:"current,omitempty"`
	Drift         []string                `json:"drift,omitempty"`
}

// GroundingSourceCurrent is the current sync state for a historical grounding source.
type GroundingSourceCurrent struct {
	Ref       string `json:"ref,omitempty"`
	CommitSHA string `json:"commit_sha,omitempty"`
	LastOKAt  string `json:"last_ok_at,omitempty"`
	State     string `json:"state,omitempty"`
}

// RunHeader is the run-level half of GET /api/v1/runs/{id}/trace — the superset of RunDetail the
// brain-renderer's JSONL run-header line needs: full draft/notes bodies (not booleans), the untrimmed
// system_prompt, warm inputs (warm_start_digest/grounding_seed), egress, and metadata.trace_url.
// Mirrors the server's `run` object field-for-field.
type RunHeader struct {
	RunID                 string            `json:"run_id"`
	Scenario              string            `json:"scenario,omitempty"`
	Project               string            `json:"project,omitempty"`
	Tenant                string            `json:"tenant,omitempty"` // run's tenant SLUG ('' for a flat/cross-tenant run)
	Status                string            `json:"status"`
	Kind                  string            `json:"kind"`
	Trigger               string            `json:"trigger,omitempty"`
	BrainRef              string            `json:"brain_ref,omitempty"`
	BrainResolved         string            `json:"brain_resolved,omitempty"`
	TenantSettings        string            `json:"tenant_settings,omitempty"`
	TenantSettingsCurrent string            `json:"tenant_settings_current,omitempty"`
	Error                 string            `json:"error,omitempty"`
	ThreadID              string            `json:"thread_id,omitempty"`
	SessionID             string            `json:"session_id,omitempty"`
	Topic                 string            `json:"topic,omitempty"`
	Question              string            `json:"question,omitempty"`
	WarmStartDigest       string            `json:"warm_start_digest,omitempty"`
	GroundingSeed         string            `json:"grounding_seed,omitempty"`
	SystemPrompt          string            `json:"system_prompt,omitempty"`
	CreatedAt             string            `json:"created_at"`
	FinishedAt            string            `json:"finished_at,omitempty"`
	Draft                 string            `json:"draft,omitempty"`
	DraftMarkdown         string            `json:"draft_markdown,omitempty"`
	AnswerMarkdown        string            `json:"answer_markdown,omitempty"`
	Notes                 []Note            `json:"notes,omitempty"`
	Decline               string            `json:"decline,omitempty"`
	DeclineReason         string            `json:"decline_reason,omitempty"`
	ProposedActions       []ProposedAction  `json:"proposed_actions,omitempty"`
	SourcePR              *SourcePR         `json:"source_pr,omitempty"`
	Debug                 *RunDebug         `json:"debug,omitempty"`
	Metadata              map[string]any    `json:"metadata,omitempty"`
	Egress                []EgressItem      `json:"egress,omitempty"`
	GroundingSources      *GroundingSources `json:"grounding_sources,omitempty"`
	GroundingSourcesRaw   json.RawMessage   `json:"-"`
	// The run's FULL prompt context (server table `run_contexts`, detail tier, 7-day window). SystemPrompt
	// above is the joined string; PromptSections is that same prompt decomposed — [{id, gate, on, text?}] —
	// so a debugger sees WHICH gate turned a paragraph on. BootstrapTurn/PreselectedTurn are the verbatim
	// orientation user turns; ManifestBlocks indexes what BootstrapTurn pastes
	// ([{path, gloss, presence, authoritative, truncated, chars}]). ALL absent on a run predating the
	// capture or past its retention window — ContextSchemaVersion == 0 is that absence signal, and a
	// renderer must SAY the context is gone rather than draw empty sections. Cross-repo contract with the
	// server: do not rename.
	PromptSections       json.RawMessage `json:"prompt_sections,omitempty"`
	BootstrapTurn        string          `json:"bootstrap_turn,omitempty"`
	PreselectedTurn      string          `json:"preselected_turn,omitempty"`
	ManifestBlocks       json.RawMessage `json:"manifest_blocks,omitempty"`
	ContextSchemaVersion int             `json:"context_schema_version,omitempty"`
	// DetailRedacted marks a trace served WITHOUT its detail: the caller is not a project-level admin, so
	// the server omits the sensitive header fields (system_prompt, grounding_*, warm_start_digest, prior
	// messages/notes, tenant settings, egress) and ships an empty events list. Every renderer must call
	// that out — the sparse bundle reads like a clean run otherwise.
	DetailRedacted bool `json:"detail_redacted,omitempty"`
	// Guards is the security-checkpoint roll-up: every guardrail verdict for the run in one object
	// (`rc run guards`). Additive and detail-tier only — absent on a redacted trace or an older server.
	Guards *GuardsView `json:"guards,omitempty"`
}

// UnmarshalJSON keeps the exact grounding_sources object for debug JSONL while still exposing typed
// fields to human renderers.
func (r *RunHeader) UnmarshalJSON(data []byte) error {
	type runHeaderAlias RunHeader
	var out runHeaderAlias
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	*r = RunHeader(out)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err == nil {
		r.GroundingSourcesRaw = raw["grounding_sources"]
	}
	return nil
}

// EventItem is one event in the /trace bundle — the superset of Event: it adds non-bash tool args, the
// agent's reasoning, and a human label, all of which today's lean /events omits. The answering model is
// NOT among them: per-turn model identity is host-only telemetry, stripped server-side on every tier.
// Args is carried as raw JSON because its shape is tool-specific.
type EventItem struct {
	Seq        int32           `json:"seq"`
	Tool       string          `json:"tool"`
	Label      string          `json:"label,omitempty"`
	Status     string          `json:"status"`
	ExitCode   int32           `json:"exit_code"`
	DurationMs int64           `json:"duration_ms"`
	At         string          `json:"at"`
	Command    string          `json:"command,omitempty"`
	Args       json.RawMessage `json:"args,omitempty"`
	Stdout     string          `json:"stdout,omitempty"`
	Stderr     string          `json:"stderr,omitempty"`
	Reasoning  string          `json:"reasoning,omitempty"`
	HasDraft   bool            `json:"has_draft,omitempty"`
	HasNote    bool            `json:"has_note,omitempty"`
}

// FullResponse is GET /api/v1/runs/{id}/trace — the whole bundle. The CLI decomposes it for
// progressive disclosure (a header block + timeline in table mode; a JSONL stream in -o json).
type FullResponse struct {
	Run    RunHeader   `json:"run"`
	Events []EventItem `json:"events"`
	// DetailRedacted is accepted at the envelope level too, so a server that marks the bundle rather than
	// the run header still reads as "withheld" here. Ask Redacted(), never this field.
	DetailRedacted bool `json:"detail_redacted,omitempty"`
}

// Redacted reports whether this trace was served without its detail (non-project-admin caller), from
// either the run header or the envelope.
func (f *FullResponse) Redacted() bool {
	return f != nil && (f.DetailRedacted || f.Run.DetailRedacted)
}

// BrainDiffFile is one path the run's journal commit touched, with its line churn. Additions is -1 for
// a binary file (the server's numstat "-" → -1, distinct from a real 0).
type BrainDiffFile struct {
	Path      string `json:"path"`
	Status    string `json:"status"` // git name-status letter: A/M/D/R… ("" when unknown)
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// BrainDiff is GET /api/v1/runs/{id}/brain-diff — the ONE journal commit a run wrote to its brain.
// Found=false means the run wrote no journal commit (declined / swallowed); every other field is then
// empty. Mirrors the server's brainDiffResponse field-for-field.
type BrainDiff struct {
	RunID         string          `json:"run_id"`
	Found         bool            `json:"found"`
	SHA           string          `json:"sha,omitempty"`
	Message       string          `json:"message,omitempty"`
	Author        string          `json:"author,omitempty"`
	CommittedAt   string          `json:"committed_at,omitempty"`
	Files         []BrainDiffFile `json:"files,omitempty"`
	Diff          string          `json:"diff,omitempty"`
	DiffTruncated bool            `json:"diff_truncated,omitempty"`
}
