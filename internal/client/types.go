// This file is the wire contract: the exact JSON shapes the rootcause API returns. Field names
// and omitempty here MUST match the server verbatim — the CLI only RENDERS these; it never invents or
// reshapes data. Anything the server omits stays a zero value (a pointer where "absent" must be
// distinguishable from "zero", e.g. last_success / kb_enrich_model).
package client

import (
	"encoding/json"
	"time"
)

type ConsoleDBInfo struct {
	Name        string `json:"name"`
	Env         string `json:"env"`
	Description string `json:"description,omitempty"`
	Scoped      bool   `json:"scoped"`
	PIIMasked   bool   `json:"pii_masked"`
}

type ConsoleScriptInfo struct {
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	Purpose     string   `json:"purpose,omitempty"`
	Args        string   `json:"args,omitempty"`
	RequiredEnv []string `json:"required_env,omitempty"`
}

type ConsoleActionSummary struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
	Risk        string `json:"risk,omitempty"`
	Preflight   bool   `json:"preflight"`

	// Enriched catalog fields (additive; absent from the legacy /capabilities projection).
	HasPreflight bool                    `json:"has_preflight"`
	HasPolicy    bool                    `json:"has_policy"`
	Autonomy     ActionAutonomyGauge     `json:"autonomy"`
	Connections  []ActionConnectionState `json:"connections,omitempty"`
	Params       []ActionParamSpec       `json:"params,omitempty"`
	Stats        ActionStats             `json:"stats"`
	Digest       string                  `json:"digest,omitempty"`
}

type ActionAutonomyGauge struct {
	Manifest  string   `json:"manifest"`
	Cap       string   `json:"cap"`
	Effective string   `json:"effective"`
	Floors    []string `json:"floors,omitempty"`
}

type ActionConnectionState struct {
	Key       string `json:"key"`
	Satisfied bool   `json:"satisfied"`
}

type ActionParamSpec struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
}

type ActionStats struct {
	Total          int64      `json:"total"`
	Succeeded      int64      `json:"succeeded"`
	Failed         int64      `json:"failed"`
	Proposed       int64      `json:"proposed"`
	Executing      int64      `json:"executing"`
	Canceled       int64      `json:"canceled"`
	SuccessRate    *float64   `json:"success_rate,omitempty"`
	P50DurationMs  float64    `json:"p50_duration_ms"`
	LastProposedAt *time.Time `json:"last_proposed_at,omitempty"`
	LastExecutedAt *time.Time `json:"last_executed_at,omitempty"`
}

type BrainStatus struct {
	Available bool   `json:"available"`
	Dir       string `json:"dir,omitempty"`
	Ref       string `json:"ref"`
	LocalSHA  string `json:"local_sha,omitempty"`
	RemoteSHA string `json:"remote_sha,omitempty"`
	Ahead     int    `json:"ahead"`
	Behind    int    `json:"behind"`
	Dirty     bool   `json:"dirty"`
	Stale     bool   `json:"stale"`
	State     string `json:"state"`
	SyncedAt  string `json:"synced_at,omitempty"`
	Message   string `json:"message,omitempty"`
}

type BrainStatusResponse struct {
	Project string      `json:"project"`
	Status  BrainStatus `json:"status"`
}

type BrainSyncResult struct {
	Before              BrainStatus `json:"before"`
	After               BrainStatus `json:"after"`
	Fetched             bool        `json:"fetched"`
	FastForwarded       bool        `json:"fast_forwarded"`
	ManualReconcile     bool        `json:"manual_reconcile"`
	RefreshedWorkspaces int         `json:"refreshed_workspaces,omitempty"`
	Message             string      `json:"message,omitempty"`
}

type BrainSyncResponse struct {
	Project string          `json:"project"`
	Sync    BrainSyncResult `json:"sync"`
}

type CapabilitiesResponse struct {
	Project    string                 `json:"project"`
	Tenant     string                 `json:"tenant,omitempty"`
	Brain      BrainStatus            `json:"brain"`
	Databases  []ConsoleDBInfo        `json:"databases"`
	Scripts    []ConsoleScriptInfo    `json:"scripts"`
	Actions    []ConsoleActionSummary `json:"actions"`
	EgressMode string                 `json:"egress_mode"`
	Planes     map[string]string      `json:"planes"`
}

type DBListResponse struct {
	Project   string          `json:"project"`
	Tenant    string          `json:"tenant,omitempty"`
	Databases []ConsoleDBInfo `json:"databases"`
}

type DBSchemaResponse struct {
	Project string          `json:"project"`
	Tenant  string          `json:"tenant,omitempty"`
	DB      string          `json:"db"`
	Tables  []DBSchemaTable `json:"tables"`
}

type DBSchemaTable struct {
	Schema  string           `json:"schema"`
	Name    string           `json:"name"`
	Columns []DBSchemaColumn `json:"columns"`
}

type DBSchemaColumn struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
}

type DBQueryRequest struct {
	SQL   string `json:"sql"`
	Limit int    `json:"limit,omitempty"`
}

type DBQueryResponse struct {
	Project    string           `json:"project"`
	Tenant     string           `json:"tenant,omitempty"`
	DB         string           `json:"db"`
	RunID      string           `json:"run_id"`
	Columns    []string         `json:"columns"`
	Rows       []map[string]any `json:"rows"`
	RowCount   int              `json:"row_count"`
	Truncated  bool             `json:"truncated"`
	DurationMs int64            `json:"duration_ms"`
}

type BashListResponse struct {
	Project string              `json:"project"`
	Tenant  string              `json:"tenant,omitempty"`
	Brain   BrainStatus         `json:"brain"`
	Scripts []ConsoleScriptInfo `json:"scripts"`
}

type BashRunRequest struct {
	Command  string `json:"command"`
	TimeoutS int    `json:"timeout_s,omitempty"`
}

type BashRunResponse struct {
	Project         string `json:"project"`
	Tenant          string `json:"tenant,omitempty"`
	BrainResolved   string `json:"brain_resolved,omitempty"`
	RunID           string `json:"run_id"`
	Seq             int32  `json:"seq"`
	ExitCode        int    `json:"exit_code"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	StdoutTruncated bool   `json:"stdout_truncated"`
	StderrTruncated bool   `json:"stderr_truncated"`
	TimedOut        bool   `json:"timed_out"`
	DurationMs      int64  `json:"duration_ms"`
	EgressBlocked   bool   `json:"egress_blocked"`
}

type ActionListResponse struct {
	Project string                 `json:"project"`
	Tenant  string                 `json:"tenant,omitempty"`
	Actions []ConsoleActionSummary `json:"actions"`
}

type ActionShowResponse struct {
	Project   string               `json:"project"`
	Tenant    string               `json:"tenant,omitempty"`
	ID        string               `json:"id"`
	Manifest  json.RawMessage      `json:"manifest"`
	Digest    string               `json:"digest"`
	Preflight bool                 `json:"preflight"`
	Catalog   ConsoleActionSummary `json:"catalog"`
}

type ActionExecRequest struct {
	Params map[string]any `json:"params"`
}

type ActionExecResponse struct {
	Project    string          `json:"project"`
	Tenant     string          `json:"tenant,omitempty"`
	ID         string          `json:"id"`
	Status     string          `json:"status"`
	DryRun     bool            `json:"dry_run"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      json.RawMessage `json:"error,omitempty"`
	DurationMs int64           `json:"duration_ms"`
}

// RunSummary is one row of GET /api/v1/runs. FinishedAt/DurationMs are absent on an unfinished run.
// Topic/DeclinedReason and the Health block are operator-tier extras the server attaches for a
// developer/admin bearer — `rc fleet` reads them for the digest's flags + cost. They're absent (zero/nil)
// for a baseline bearer, so the digest degrades to the safe columns rather than erroring.
type RunSummary struct {
	RunID          string     `json:"run_id"`
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
}

// RunHealth is the per-run triage block on a run index row (run_health view). The COUNT/flag fields are
// safe for any bearer; the spend fields (CostUSD/TotalTokens/PeakContextTokens) + Model are operator-tier
// pointers — nil for a baseline bearer, so the digest's cost/$!/CTX columns simply blank out. Mirrors the
// server's runIndexHealth field-for-field.
type RunHealth struct {
	Turns              int64 `json:"turns"`
	GroundingTurns     int64 `json:"grounding_turns"`
	BashTotal          int64 `json:"bash_total"`
	BashErrCount       int64 `json:"bash_err_count"`
	BigStdoutCount     int64 `json:"big_stdout_count"`
	BlockedEgress      int64 `json:"blocked_egress"`
	GroundingDiscarded bool  `json:"grounding_discarded"`
	NoJournal          bool  `json:"no_journal"`
	// IsFallback is the CLEAN model-fallback signal (run_health.is_fallback): the loop swapped the
	// planned model for a different one that answered. SAFE (a boolean) so it rides for any bearer —
	// it drives the digest's fallback flag (FB) + the model×cost×fallback breakdown. The empty-string-
	// vs-NULL trap on runs.model_fallback_from is baked into the view, so the CLI never recomputes it.
	IsFallback        bool     `json:"is_fallback"`
	CostUSD           *float64 `json:"cost_usd,omitempty"`
	TotalTokens       *int64   `json:"total_tokens,omitempty"`
	PeakContextTokens *int64   `json:"peak_context_tokens,omitempty"`
	Model             string   `json:"model,omitempty"`
	// PlannedModel is the model the loop planned but that failed (run_health.model_fallback_from), set
	// only when IsFallback. Operator-tier like Model — omitted for a baseline bearer.
	PlannedModel string `json:"planned_model,omitempty"`
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
// e.g. "budget"/"timeout"), whether the model fell back to a cheaper cascade rung (fallback_from = the
// model it fell back FROM), and how many recoverable (transient) errors were retried in-loop. Surfaced
// under a single optional "debug" object on GET /api/v1/runs/{id} and /trace's run (progressive
// disclosure) — the whole object is omitempty so a clean run carries nothing and the typed pointer
// stays nil. Field names match the server verbatim.
type RunDebug struct {
	DeclineReason      string `json:"decline_reason,omitempty"`
	Guardrail          string `json:"guardrail,omitempty"`
	Forced             string `json:"forced,omitempty"`
	FallbackFrom       string `json:"fallback_from,omitempty"`
	RecoverableRetries int    `json:"recoverable_retries,omitempty"`
}

// RunDetail is GET /api/v1/runs/{id} — it MUST mirror the server's statusResponse (internal/api/prompt.go)
// field-for-field: same json tags, same omitempty. Optional fields are omitempty server-side; Attachments
// is always present (always [] in v0). category/has_draft/has_note come from the shared row-builder;
// duration_ms/cost_usd/turns/bash_total are the run_health triage scalars (cost is the run's TOTAL spend).
type RunDetail struct {
	RunID           string           `json:"run_id"`
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
	CostUSD         float64          `json:"cost_usd,omitempty"`
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

// EventsResponse is GET /api/v1/runs/{id}/events.
type EventsResponse struct {
	RunID  string  `json:"run_id"`
	Events []Event `json:"events"`
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
	Project         string       `json:"-"`
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
// system_prompt, warm inputs (warm_start_digest/grounding_seed), run-level cost/tokens, egress, and
// metadata.trace_url. Mirrors the server's `run` object field-for-field.
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
	Model                 string            `json:"model,omitempty"`
	RunCostUSD            float64           `json:"run_cost_usd,omitempty"`
	RunTotalTokens        int64             `json:"run_total_tokens,omitempty"`
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

// EventItem is one event in the /trace bundle — the superset of Event: it adds the ai_usage join
// (cost_usd/total_tokens/model), non-bash tool args, the agent's reasoning, and a human label, all of
// which today's lean /events omits. Args is carried as raw JSON because its shape is tool-specific.
type EventItem struct {
	Seq         int32           `json:"seq"`
	Tool        string          `json:"tool"`
	Label       string          `json:"label,omitempty"`
	Status      string          `json:"status"`
	ExitCode    int32           `json:"exit_code"`
	DurationMs  int64           `json:"duration_ms"`
	At          string          `json:"at"`
	Command     string          `json:"command,omitempty"`
	Args        json.RawMessage `json:"args,omitempty"`
	Stdout      string          `json:"stdout,omitempty"`
	Stderr      string          `json:"stderr,omitempty"`
	Reasoning   string          `json:"reasoning,omitempty"`
	HasDraft    bool            `json:"has_draft,omitempty"`
	HasNote     bool            `json:"has_note,omitempty"`
	CostUSD     float64         `json:"cost_usd,omitempty"`
	TotalTokens int64           `json:"total_tokens,omitempty"`
	Model       string          `json:"model,omitempty"`
}

// FullResponse is GET /api/v1/runs/{id}/trace — the whole bundle. The CLI decomposes it for
// progressive disclosure (a header block + timeline in table mode; a JSONL stream in -o json).
type FullResponse struct {
	Run    RunHeader   `json:"run"`
	Events []EventItem `json:"events"`
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

// ThreadTrace is GET /api/v1/threads/{id}/trace — every run for one thread (or session) id, newest-first,
// each a full RunSummary (status + safe health), so `rc thread` can answer "why did this thread get no
// draft". ResolvedBy is "thread" | "session" | "none". Mirrors the server's threadTraceResponse
// field-for-field.
type ThreadTrace struct {
	ID         string       `json:"id"`
	ResolvedBy string       `json:"resolved_by"`
	Runs       []RunSummary `json:"runs"`
}

// WatchedMailbox is one row of GET /api/v1/mailboxes/watched — a connection-backed mailbox the channel
// plane actively watches (NOT the legacy email-keyed routing table behind `rc mailbox route`). Field
// names mirror the server verbatim. Tenant/SubscriptionExpiresAt/ErrorMessage are omitempty: absent for
// a non-tenant mailbox / a provider without a renewable subscription / a healthy mailbox.
type WatchedMailbox struct {
	ID                    string `json:"id"`
	Provider              string `json:"provider"`
	EmailAddress          string `json:"email_address"`
	Status                string `json:"status"` // active|paused|connected|needs_attention
	Tenant                string `json:"tenant,omitempty"`
	ProcessingEnabled     bool   `json:"processing_enabled"` // false = silent onboarding: polled, not processed
	HasSyncCursor         bool   `json:"has_sync_cursor"`
	SubscriptionExpiresAt string `json:"subscription_expires_at,omitempty"`
	ErrorMessage          string `json:"error_message,omitempty"`
}

// WatchedMailboxList is GET /api/v1/mailboxes/watched — the watched-mailbox set under its envelope key.
type WatchedMailboxList struct {
	Mailboxes []WatchedMailbox `json:"mailboxes"`
}

// HarvestAccepted is the 202 body of POST /api/v1/mailboxes/{id}/harvest — the queued export handle.
type HarvestAccepted struct {
	ExportID string `json:"export_id"`
	Status   string `json:"status"`
}

// ExportItem is one row of GET /api/v1/exports (and the whole of GET /api/v1/exports/{id}) — a
// local-synthesis corpus export (a harvest or a survey). Field names mirror the server verbatim; most
// counts/timestamps are omitempty (absent until the export runs/completes/is consumed). Truncated is
// always present (a harvest either hit its thread cap or didn't).
type ExportItem struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`   // harvest|survey
	Status      string `json:"status"` // pending|running|done|error|failed
	MailboxID   string `json:"mailbox_id"`
	Tenant      string `json:"tenant,omitempty"`
	Cleaned     *bool  `json:"cleaned,omitempty"`
	ThreadCount *int   `json:"thread_count,omitempty"`
	Truncated   bool   `json:"truncated"`
	CreatedAt   string `json:"created_at,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
	ConsumedAt  string `json:"consumed_at,omitempty"`
	Error       string `json:"error,omitempty"`
}

// Export aliases ExportItem for the single-item GET, matching the WatchedMailbox naming split.
type Export = ExportItem

// ExportList is GET /api/v1/exports — the exports (newest-first) under their envelope key.
type ExportList struct {
	Exports []ExportItem `json:"exports"`
}

// Project is one row of GET /api/v1/projects — a fleet handle (id + name). It's what `rc projects`
// renders and the seed the `--all` fan-out lists before hitting each project's read surface with
// ?project=<id>. Mirrors the server's projectItem field-for-field.
type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ProjectsResponse is GET /api/v1/projects — the projects the bearer may see (every one for an
// all-projects admin token; just its own for a project-pinned token).
type ProjectsResponse struct {
	Projects []Project `json:"projects"`
}

// ProjectRenameRequest is PATCH /api/v1/projects/{project}/rename.
type ProjectRenameRequest struct {
	Name string `json:"name"`
}

// ProjectRenameResponse is PATCH /api/v1/projects/{project}/rename — the server-side project slug,
// brain repo, GitHub repo, and deployed local-dir rename result.
type ProjectRenameResponse struct {
	ID                string `json:"id"`
	PreviousName      string `json:"previous_name"`
	Name              string `json:"name"`
	PreviousBrainRepo string `json:"previous_brain_repo"`
	BrainRepo         string `json:"brain_repo"`
	GitHubRenamed     bool   `json:"github_renamed"`
	LocalDirRenamed   bool   `json:"local_dir_renamed"`
	URL               string `json:"url"`
}

type WhoamiScope struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	Slug string `json:"slug,omitempty"`
}

type WhoamiResponse struct {
	Email       string       `json:"email,omitempty"`
	AllProjects bool         `json:"all_projects"`
	Project     *WhoamiScope `json:"project,omitempty"`
	Tenant      *WhoamiScope `json:"tenant,omitempty"`
}

// --- observability feeds (rc fleet / patterns / health) ---

// RunEvent is one raw run_events row from GET /api/v1/run-events — the bulk feed `rc patterns`
// clusters locally (bash-failure themes, recurring error signatures). Args is raw JSON (the bash
// command lives at args.command). RunKind/RunCreatedAt are the parent run's, carried for the keyset
// page + per-kind grouping. Field names match the server verbatim.
type RunEvent struct {
	RunID        string          `json:"run_id"`
	RunKind      string          `json:"run_kind"`
	RunCreatedAt string          `json:"run_created_at"`
	Seq          int32           `json:"seq"`
	Tool         string          `json:"tool"`
	Args         json.RawMessage `json:"args,omitempty"`
	Stdout       string          `json:"stdout,omitempty"`
	Stderr       string          `json:"stderr,omitempty"`
	ExitCode     int32           `json:"exit_code"`
	Status       string          `json:"status"`
	DurationMs   int64           `json:"duration_ms"`
	At           string          `json:"at"`
	Reasoning    string          `json:"reasoning,omitempty"`
}

// RunEventsResponse is one page of GET /api/v1/run-events. NextBefore is the cursor to the next
// (older) page; empty on the last page.
type RunEventsResponse struct {
	Events     []RunEvent `json:"events"`
	NextBefore string     `json:"next_before,omitempty"`
}

// EgressRow is one raw egress_log row from GET /api/v1/egress-log — the bulk feed `rc patterns`
// clusters into blocked-host signatures. Decision is "block" for a blocked attempt.
type EgressRow struct {
	RunID        string `json:"run_id"`
	RunKind      string `json:"run_kind"`
	RunCreatedAt string `json:"run_created_at"`
	Host         string `json:"host"`
	Port         int32  `json:"port"`
	Scheme       string `json:"scheme"`
	URL          string `json:"url"`
	BytesOut     int64  `json:"bytes_out"`
	Decision     string `json:"decision"`
	At           string `json:"at"`
}

// EgressResponse is one page of GET /api/v1/egress-log.
type EgressResponse struct {
	Egress     []EgressRow `json:"egress"`
	NextBefore string      `json:"next_before,omitempty"`
}

// HealthMirror is one raw mirror_health row from GET /api/v1/health — the input `rc health` applies its
// staleness/state rule to. HoursSinceOK is nil when the mirror never succeeded (the CLI renders "never").
type HealthMirror struct {
	Repo                string   `json:"repo"`
	State               string   `json:"state"`
	ConsecutiveFailures int32    `json:"consecutive_failures"`
	LastOkAt            string   `json:"last_ok_at,omitempty"`
	LastError           string   `json:"last_error,omitempty"`
	HoursSinceOK        *float64 `json:"hours_since_ok"`
}

// HealthMailbox is one watched mailbox row from GET /api/v1/health whose watch needs attention.
type HealthMailbox struct {
	ID                        string `json:"id"`
	Provider                  string `json:"provider"`
	EmailAddress              string `json:"email_address"`
	Status                    string `json:"status"`
	Tenant                    string `json:"tenant,omitempty"`
	SubscriptionExpiresAt     string `json:"subscription_expires_at,omitempty"`
	SpamSubscriptionExpiresAt string `json:"spam_subscription_expires_at,omitempty"`
	ErrorMessage              string `json:"error_message,omitempty"`
	UpdatedAt                 string `json:"updated_at,omitempty"`
}

// HealthDeadLetter is one terminally dead-lettered run from GET /api/v1/health.
type HealthDeadLetter struct {
	RunID      string `json:"run_id"`
	Kind       string `json:"kind"`
	Error      string `json:"error"`
	FinishedAt string `json:"finished_at,omitempty"`
}

// HealthResponse is GET /api/v1/health — the RAW health inputs; the CLI decides healthy/unhealthy.
type HealthResponse struct {
	WindowHours  int                `json:"window_hours"`
	Mirrors      []HealthMirror     `json:"mirrors"`
	Mailboxes    []HealthMailbox    `json:"mailboxes"`
	DeadLettered []HealthDeadLetter `json:"dead_lettered"`
}

// EnvResponse is GET /api/v1/env — the resolved grounding env. Keys holds live secret VALUES (the
// whole point: `rc env pull` writes them to ./.env). The CLI NEVER prints a value: it renders key
// NAMES only and writes values solely to the 0600 file.
type EnvResponse struct {
	Project string            `json:"project"`
	Tenant  string            `json:"tenant,omitempty"`
	Keys    map[string]string `json:"keys"`
}

// SettingField is one field of the server's generic settings bag: the stored override (value),
// effective (value-or-default), default, the provenance of the effective value ("override"|"default"),
// and — only with GET ?include=schema — the field's registry schema. Scalars are kept as
// json.RawMessage so the CLI renders the exact type the server holds (number for max_run_usd, string
// otherwise) without a typed-per-key shape.
type SettingField struct {
	Value     json.RawMessage `json:"value"`
	Effective json.RawMessage `json:"effective"`
	Default   json.RawMessage `json:"default"`
	Source    string          `json:"source"`
	Schema    json.RawMessage `json:"schema,omitempty"`
}

// Settings is GET /api/v1/settings (PATCH returns the same shape): a generic key→field map, mirroring
// the server's registry-driven bag. A field absent from the map (e.g. kb_enrich_model when KB sync is
// off) is simply unset for this project. The CLI holds no per-key knowledge — it renders whatever keys
// the server sends, so a new server-side knob shows up with no CLI change.
type Settings map[string]SettingField

// SchemaResponse is GET /api/v1/meta/schema: the declarative config registry as JSON, keyed by
// resource name. The self-describing surface `rc schema`/`rc explain` render.
type SchemaResponse struct {
	Resources map[string]BagSchema `json:"resources"`
}

// BagSchema is one resource's schema: its name + every field descriptor.
type BagSchema struct {
	Name   string        `json:"name"`
	Fields []FieldSchema `json:"fields"`
}

// FieldSchema is one settable field's public description — everything a human or agent needs to write
// it without out-of-band docs.
type FieldSchema struct {
	Key       string          `json:"key"`
	Scope     string          `json:"scope"`
	Group     string          `json:"group"`
	Type      string          `json:"type"`
	Enum      []string        `json:"enum,omitempty"`
	Scopes    []string        `json:"scopes,omitempty"`
	Sensitive bool            `json:"sensitive,omitempty"`
	Help      string          `json:"help"`
	Default   json.RawMessage `json:"default,omitempty"`
}

// Access is GET /api/v1/meta/capabilities: what THIS token may do (effective scopes, writable keys,
// reachable resources, console reach). The agent/operator pre-flight. Named Access to avoid confusion
// with the console CapabilitiesResponse (which lists DB/script/action primitives, not token authority).
type Access struct {
	Email        string         `json:"email,omitempty"`
	AllProjects  bool           `json:"all_projects"`
	Project      *ScopeItem     `json:"project,omitempty"`
	Tenant       *ScopeItem     `json:"tenant,omitempty"`
	Scopes       []string       `json:"scopes"`
	WritableKeys []string       `json:"writable_keys"`
	Resources    []string       `json:"resources"`
	Console      ConsoleCapsSum `json:"console"`
}

// HierarchySettings is GET/PATCH /api/v1/projects/{project}/settings and its tenant/mailbox children.
// Settings is the scope-local nested override bag ({persona:{...},channel:{...}}); Resolved is present
// only when ?resolved=true and carries effective values plus provenance per field.
type HierarchySettings struct {
	Scope    string          `json:"scope"`
	Project  string          `json:"project,omitempty"`
	Tenant   string          `json:"tenant,omitempty"`
	Mailbox  string          `json:"mailbox,omitempty"`
	Settings json.RawMessage `json:"settings"`
	Resolved json.RawMessage `json:"resolved,omitempty"`
}

// ScopeItem is a project/tenant identity in a capabilities response.
type ScopeItem struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	Slug string `json:"slug,omitempty"`
}

// ConsoleCapsSum is the dev-console reach broken out as booleans.
type ConsoleCapsSum struct {
	DB     bool `json:"db"`
	Bash   bool `json:"bash"`
	Action bool `json:"action"`
}

// TenantSettings is GET /api/v1/tenants/{slug}/settings (and the echoed body of a PATCH), the legacy
// tenant profile/projection record. It mirrors the server's tenantSettingsGetResponse /
// tenantSettingsPatchResponse field-for-field. Settings is the RAW stored object (kept as
// json.RawMessage so the CLI renders/echoes the exact keys+values the server holds — never reshaped;
// `{}` for a tenant that has never been written). The PATCH response drops nothing the GET carries, so
// one struct serves both.
type TenantSettings struct {
	TenantID  string          `json:"tenant_id"`
	Settings  json.RawMessage `json:"settings"`
	Version   string          `json:"version"`
	AppliedAt string          `json:"applied_at"`
}

// TenantSettingsPatchRequest is the PATCH /api/v1/tenants/{slug}/settings profile body:
// { "settings": { …partial… }, "source"?: "…" }. Settings is a raw key→value map so an explicit JSON
// null (the "unconfigure" gesture) rides through verbatim, distinct from an omitted key. Source is the
// provenance label ("cli"); omitempty so a blank source isn't sent (the server defaults it to "cli").
type TenantSettingsPatchRequest struct {
	Settings map[string]any `json:"settings"`
	Source   string         `json:"source,omitempty"`
}
