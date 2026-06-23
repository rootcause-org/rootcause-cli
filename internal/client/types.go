// This file is the wire contract: the exact JSON shapes the rootcause API returns. Field names
// and omitempty here MUST match the server verbatim — the CLI only RENDERS these; it never invents or
// reshapes data. Anything the server omits stays a zero value (a pointer where "absent" must be
// distinguishable from "zero", e.g. last_success / kb_enrich_model).
package client

import "encoding/json"

// RunSummary is one row of GET /api/v1/runs. FinishedAt/DurationMs are absent on an unfinished run.
type RunSummary struct {
	RunID      string `json:"run_id"`
	Kind       string `json:"kind"`
	Source     string `json:"source"`
	Status     string `json:"status"`
	Outcome    string `json:"outcome"`
	Category   string `json:"category"`
	CreatedAt  string `json:"created_at"`
	FinishedAt string `json:"finished_at,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	HasDraft   bool   `json:"has_draft"`
	HasNote    bool   `json:"has_note"`
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
// under a single optional "debug" object on GET /api/v1/runs/{id} and /full's run (progressive
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
	RunID          string         `json:"run_id"`
	Status         string         `json:"status"`
	Kind           string         `json:"kind"`
	Category       string         `json:"category"`
	CreatedAt      string         `json:"created_at"`
	FinishedAt     string         `json:"finished_at,omitempty"`
	DurationMs     int64          `json:"duration_ms,omitempty"`
	HasDraft       bool           `json:"has_draft"`
	HasNote        bool           `json:"has_note"`
	Turns          int64          `json:"turns,omitempty"`
	BashTotal      int64          `json:"bash_total,omitempty"`
	CostUSD        float64        `json:"cost_usd,omitempty"`
	AnswerMarkdown string         `json:"answer_markdown,omitempty"`
	RunURL         string         `json:"run_url,omitempty"`
	Attachments    []any          `json:"attachments"`
	Error          string         `json:"error,omitempty"`
	Debug          *RunDebug      `json:"debug,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
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

// SubmitRequest is the POST /api/v1/runs body. session_id/tenant/brain_ref are omitempty so a bare
// `rc ask "<q>"` sends just {prompt}; brain_ref names a non-main brain ref (a dev/* branch) for a test
// run. The project is resolved server-side from the bearer key — there is no project field.
type SubmitRequest struct {
	Prompt    string `json:"prompt"`
	SessionID string `json:"session_id,omitempty"`
	Tenant    string `json:"tenant,omitempty"`
	BrainRef  string `json:"brain_ref,omitempty"`
}

// SubmitResponse is the 202 body from POST /api/v1/runs: the run id + where/when to poll. PollAfterMs
// is the server's hint for the poll interval (ms); 0 → the caller picks a default.
type SubmitResponse struct {
	RunID       string `json:"run_id"`
	Status      string `json:"status"`
	StatusURL   string `json:"status_url"`
	PollAfterMs int    `json:"poll_after_ms"`
}

// Note is one named note body on a run, returned in full by /full (vs. the has_note boolean on the
// lean run detail).
type Note struct {
	Key  string `json:"key"`
	Body string `json:"body"`
}

// EgressItem is one host the run reached out to (the egress_log rollup): how many times, and whether
// the egress proxy blocked it.
type EgressItem struct {
	Host    string `json:"host"`
	Count   int    `json:"count"`
	Blocked bool   `json:"blocked"`
}

// RunHeader is the run-level half of GET /api/v1/runs/{id}/full — the superset of RunDetail the
// brain-renderer's JSONL run-header line needs: full draft/notes bodies (not booleans), the untrimmed
// system_prompt, warm inputs (warm_start_digest/grounding_seed), run-level cost/tokens, egress, and
// metadata.trace_url. Mirrors the server's `run` object field-for-field.
type RunHeader struct {
	RunID           string         `json:"run_id"`
	Project         string         `json:"project,omitempty"`
	Tenant          string         `json:"tenant,omitempty"` // run's tenant SLUG ('' for a flat/cross-tenant run)
	Status          string         `json:"status"`
	Kind            string         `json:"kind"`
	Trigger         string         `json:"trigger,omitempty"`
	BrainRef        string         `json:"brain_ref,omitempty"`
	Error           string         `json:"error,omitempty"`
	ThreadID        string         `json:"thread_id,omitempty"`
	SessionID       string         `json:"session_id,omitempty"`
	Topic           string         `json:"topic,omitempty"`
	Question        string         `json:"question,omitempty"`
	WarmStartDigest string         `json:"warm_start_digest,omitempty"`
	GroundingSeed   string         `json:"grounding_seed,omitempty"`
	SystemPrompt    string         `json:"system_prompt,omitempty"`
	CreatedAt       string         `json:"created_at"`
	FinishedAt      string         `json:"finished_at,omitempty"`
	Model           string         `json:"model,omitempty"`
	RunCostUSD      float64        `json:"run_cost_usd,omitempty"`
	RunTotalTokens  int64          `json:"run_total_tokens,omitempty"`
	Draft           string         `json:"draft,omitempty"`
	Notes           []Note         `json:"notes,omitempty"`
	Debug           *RunDebug      `json:"debug,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	Egress          []EgressItem   `json:"egress,omitempty"`
}

// EventItem is one event in the /full bundle — the superset of Event: it adds the ai_usage join
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

// FullResponse is GET /api/v1/runs/{id}/full — the whole bundle. The CLI decomposes it for
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

// EnvResponse is GET /api/v1/env — the resolved grounding env. Keys holds live secret VALUES (the
// whole point: `rc env pull` writes them to ./.env). The CLI NEVER prints a value: it renders key
// NAMES only and writes values solely to the 0600 file.
type EnvResponse struct {
	Project string            `json:"project"`
	Tenant  string            `json:"tenant,omitempty"`
	Keys    map[string]string `json:"keys"`
}

// NumberSetting / StringSetting are one settings field: value (what's set, "" / 0 if unset), effective
// (value-or-default), default. max_run_usd is numeric; the rest are strings.
type NumberSetting struct {
	Value     float64 `json:"value"`
	Effective float64 `json:"effective"`
	Default   float64 `json:"default"`
}

type StringSetting struct {
	Value     string `json:"value"`
	Effective string `json:"effective"`
	Default   string `json:"default"`
}

// Settings is GET /api/v1/settings (PATCH returns the same shape). KBEnrichModel is a pointer: the
// server omits it entirely when the project has no KB sync.
type Settings struct {
	MaxRunUSD     NumberSetting  `json:"max_run_usd"`
	DefaultTier   StringSetting  `json:"default_tier"`
	ImageModel    StringSetting  `json:"image_model"`
	KBEnrichModel *StringSetting `json:"kb_enrich_model,omitempty"`
}

// TenantSettings is GET /api/v1/tenants/{slug}/settings (and the echoed body of a PATCH). It mirrors
// the server's tenantSettingsGetResponse / tenantSettingsPatchResponse field-for-field. Settings is
// the RAW stored object (kept as json.RawMessage so the CLI renders/echoes the exact keys+values the
// server holds — never reshaped; `{}` for a tenant that has never been written). The PATCH response
// drops nothing the GET carries, so one struct serves both.
type TenantSettings struct {
	TenantID  string          `json:"tenant_id"`
	Settings  json.RawMessage `json:"settings"`
	Version   string          `json:"version"`
	AppliedAt string          `json:"applied_at"`
}

// TenantSettingsPatchRequest is the PATCH /api/v1/tenants/{slug}/settings body:
// { "settings": { …partial… }, "source"?: "…" }. Settings is a raw key→value map so an explicit JSON
// null (the "unconfigure" gesture) rides through verbatim, distinct from an omitted key. Source is the
// provenance label ("cli"); omitempty so a blank source isn't sent (the server defaults it to "cli").
type TenantSettingsPatchRequest struct {
	Settings map[string]any `json:"settings"`
	Source   string         `json:"source,omitempty"`
}
