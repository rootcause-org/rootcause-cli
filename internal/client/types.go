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
	// Writable is true when the project has sealed a <X>_WRITE_DSN for this database in .env.action —
	// the presence of write-plane credentials that `query --write` (scope console:db:write) commits to.
	Writable bool `json:"writable,omitempty"`
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
	Available bool                 `json:"available"`
	Dir       string               `json:"dir,omitempty"`
	Ref       string               `json:"ref"`
	LocalSHA  string               `json:"local_sha,omitempty"`
	RemoteSHA string               `json:"remote_sha,omitempty"`
	Ahead     int                  `json:"ahead"`
	Behind    int                  `json:"behind"`
	Dirty     bool                 `json:"dirty"`
	Stale     bool                 `json:"stale"`
	State     string               `json:"state"`
	SyncedAt  string               `json:"synced_at,omitempty"`
	Message   string               `json:"message,omitempty"`
	Channels  []BrainChannelStatus `json:"channels,omitempty"`
}

type BrainChannelStatus struct {
	Channel       string `json:"channel"`
	ResolvedSHA   string `json:"resolved_sha,omitempty"`
	OriginSHA     string `json:"origin_sha,omitempty"`
	MainSHA       string `json:"main_sha,omitempty"`
	MatchesOrigin bool   `json:"matches_origin"`
	MatchesMain   bool   `json:"matches_main"`
	State         string `json:"state"`
	Provenance    string `json:"provenance,omitempty"`
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

type BrainPromoteRequest struct {
	Channel string `json:"channel"`
	SHA     string `json:"sha"`
}

type BrainPromoteResponse struct {
	Project    string `json:"project"`
	Channel    string `json:"channel"`
	OldSHA     string `json:"old_sha"`
	NewSHA     string `json:"new_sha"`
	Changed    bool   `json:"changed"`
	Idempotent bool   `json:"idempotent"`
}

// BrainPreflightRequest dry-runs a promotion: which candidate commit, onto which managed channel. The
// server never resolves a ref for us — SHA is the exact 40-character commit, as for a real promote.
type BrainPreflightRequest struct {
	Channel string `json:"channel"`
	SHA     string `json:"sha"`
}

type BrainPreflightResponse struct {
	Project string      `json:"project"`
	Canary  BrainCanary `json:"canary"`
}

// BrainCanary is the server's verdict on a candidate: would it degrade or break any tenant pinned to the
// channel? Keys and reasons only — a tenant VALUE never crosses this wire.
type BrainCanary struct {
	Channel string `json:"channel"`
	SHA     string `json:"sha"`
	OK      bool   `json:"ok"`
	// Templated is false when the candidate carries no projection.yaml (nothing to break).
	Templated bool `json:"templated"`
	Checked   int  `json:"checked"`
	// Skipped counts tenants excluded from this channel's set: on the other channel, frozen at an exact
	// SHA, or not active.
	Skipped int                 `json:"skipped"`
	Tenants []BrainCanaryTenant `json:"tenants"`
	// Error is a candidate-wide failure no single tenant owns (an unparseable projection.yaml).
	Error string `json:"error"`
	// Note explains a trivially-passing verdict (untemplated brain, or nobody pinned to the channel).
	Note string `json:"note"`
}

type BrainCanaryTenant struct {
	Tenant string `json:"tenant"`
	// Status is ok | degraded | failed; anything but ok would block a promotion.
	Status       string                   `json:"status"`
	Degraded     []BrainCanaryDegradation `json:"degraded"`
	FilesDropped int                      `json:"files_dropped"`
	Error        string                   `json:"error"`
}

type BrainCanaryDegradation struct {
	Key    string `json:"key"`
	File   string `json:"file"`
	Reason string `json:"reason"`
}

// BrainRenderRequest compiles ONE tenant's projection of a brain commit, in memory — the on-box cache is
// never touched. Sha and Channel are mutually exclusive; omitting both means the tenant's current channel.
// Paths are brain-relative paths or globs (ignored when All).
type BrainRenderRequest struct {
	Tenant  string   `json:"tenant"`
	SHA     string   `json:"sha,omitempty"`
	Channel string   `json:"channel,omitempty"`
	Paths   []string `json:"paths,omitempty"`
	All     bool     `json:"all,omitempty"`
}

// BrainRenderFile is one compiled file exactly as /brain would mount it for this tenant.
type BrainRenderFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type BrainRenderStats struct {
	FilesRendered      int `json:"files_rendered"`
	FilesCopied        int `json:"files_copied"`
	FilesDropped       int `json:"files_dropped"`
	PlaceholdersFilled int `json:"placeholders_filled"`
	BranchesCollapsed  int `json:"branches_collapsed"`
}

// BrainRenderResponse carries the compiled files plus the same key/file/reason degradation shape the
// promote-time canary reports, so a degraded projection reads identically in preflight and render.
type BrainRenderResponse struct {
	Project      string                   `json:"project"`
	Tenant       string                   `json:"tenant"`
	SHA          string                   `json:"sha"`
	Channel      string                   `json:"channel"`
	Files        []BrainRenderFile        `json:"files"`
	Stats        BrainRenderStats         `json:"stats"`
	Degradations []BrainCanaryDegradation `json:"degradations"`
}

type MirrorRefreshRequest struct {
	Repo        string `json:"repo"`
	ExpectedSHA string `json:"expected_sha"`
}

type MirrorRefreshResponse struct {
	Project             string `json:"project"`
	Repo                string `json:"repo"`
	Branch              string `json:"branch"`
	ExpectedSHA         string `json:"expected_sha"`
	ActualSHA           string `json:"actual_sha"`
	Verified            bool   `json:"verified"`
	JobID               int64  `json:"job_id"`
	RefreshedWorkspaces int    `json:"refreshed_workspaces"`
}

// BrainDeveloperInvitationRequest grants one GitHub user access to one tenant brain repository.
// The server owns GitHub App credentials; rc only forwards the handle in the JSON body.
type BrainDeveloperInvitationRequest struct {
	GitHubHandle string `json:"github_handle"`
}

// BrainDeveloperInvitation is the idempotent access receipt returned by the tenant-brain endpoint.
// InvitationURL is empty once the developer already has active repository access.
type BrainDeveloperInvitation struct {
	Project       string `json:"project"`
	Tenant        string `json:"tenant"`
	Repository    string `json:"repository"`
	GitHubHandle  string `json:"github_handle"`
	Permission    string `json:"permission"`
	State         string `json:"state"`
	InvitationURL string `json:"invitation_url,omitempty"`
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
	// Write routes the statement to the project's sealed write-plane DSN (scope console:db:write) and
	// commits unless DryRun is set; omitempty so a plain read never carries it.
	Write bool `json:"write,omitempty"`
	// DryRun preserves the write plane and authorization but rolls the transaction back.
	DryRun bool `json:"dry_run,omitempty"`
}

type DBQueryResponse struct {
	Project string           `json:"project"`
	Tenant  string           `json:"tenant,omitempty"`
	DB      string           `json:"db"`
	RunID   string           `json:"run_id"`
	Columns []string         `json:"columns"`
	Rows    []map[string]any `json:"rows"`
	// RowsAffected is the write's CommandTag row count; a pointer because it is present only on a write
	// response (absent on a read), distinct from a real 0-row write. Write echoes that the statement ran
	// on the write plane.
	RowsAffected *int64 `json:"rows_affected,omitempty"`
	Write        bool   `json:"write,omitempty"`
	DryRun       bool   `json:"dry_run,omitempty"`
	RowCount     int    `json:"row_count"`
	Truncated    bool   `json:"truncated"`
	DurationMs   int64  `json:"duration_ms"`
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
// developer/admin bearer — `rc fleet runs` reads them for the digest's flags. They're absent (zero/nil)
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

// Learning is the privacy-safe dream-cycle pointer attached to a run index row. It carries only
// booleans; the evidence endpoint owns comments, bodies, deltas, and triage detail.
type Learning struct {
	Feedback        bool `json:"feedback"`
	SentDelta       bool `json:"sent_delta"`
	TriageSkipped   bool `json:"triage_skipped"`
	TriageCorrected bool `json:"triage_corrected"`
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

// hostOnlyMetadataKeys are the freeform run-metadata keys that carry host-only telemetry: LLM spend /
// token counts (unit economics) and the SERVING MODEL IDENTITY (route attribution — the rung that
// answered and the rung it fell back from are as cost-reverse-engineerable as the provider slug). The
// server strips all of them on projection and the typed DTOs dropped their fields, but `metadata` is a
// passthrough map: an older server still emitting them would otherwise reach a rendered surface or a
// debug dump. Every metadata passthrough filters on this. Mirrors the server's own hostOnlyMetadataKeys.
var hostOnlyMetadataKeys = map[string]bool{
	"cost_usd": true, "total_cost_usd": true, "run_cost_usd": true, "max_run_usd_spent": true,
	"tokens": true, "total_tokens": true, "run_total_tokens": true, "peak_context_tokens": true,
	"input_tokens": true, "output_tokens": true,
	"model": true, "model_fallback_from": true,
}

// HostOnlyMetadataKey reports whether a run-metadata key must never be rendered (spend / token counts /
// serving model identity).
func HostOnlyMetadataKey(k string) bool { return hostOnlyMetadataKeys[k] }

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

// ThreadTraceThread is the provider-neutral channel row resolved before a run exists. It deliberately
// carries outcome metadata and counts, not message bodies; the Inbox detail API owns body disclosure.
type ThreadTraceThread struct {
	LocalThreadID     string          `json:"local_thread_id"`
	ExternalThreadID  string          `json:"external_thread_id"`
	Provider          string          `json:"provider"`
	FeedbackLevel     string          `json:"feedback_level"`
	Tenant            string          `json:"tenant,omitempty"`
	Status            string          `json:"status"`
	Outcome           string          `json:"outcome"`
	TriageExplanation string          `json:"triage_explanation,omitempty"`
	DeclineReason     string          `json:"decline_reason,omitempty"`
	ProcessorFailure  json.RawMessage `json:"processor_failure,omitempty"`
	MessageCount      int             `json:"message_count"`
	DraftCount        int             `json:"draft_count"`
	NoteCount         int             `json:"note_count"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

// ThreadTrace is GET /api/v1/threads/{id}/trace — the channel-pipeline outcome plus every run for one
// rootcause/provider thread (or session) id. Mirrors the server's threadTraceResponse field-for-field.
type ThreadTrace struct {
	ID         string              `json:"id"`
	ResolvedBy string              `json:"resolved_by"`
	Threads    []ThreadTraceThread `json:"threads"`
	Runs       []RunSummary        `json:"runs"`
}

// WatchedMailbox is one row of GET /api/v1/projects/{project}/mailboxes — a connection-backed mailbox the channel
// plane actively watches. Field
// names mirror the server verbatim. Tenant/SubscriptionExpiresAt/ErrorMessage are omitempty: absent for
// a non-tenant mailbox / a provider without a renewable subscription / a healthy mailbox.
type WatchedMailbox struct {
	ID           string `json:"id"`
	Provider     string `json:"provider"`
	EmailAddress string `json:"email_address"`
	// awaiting_credential = seeded without a password; the customer still has to open its password link.
	Status                string `json:"status"` // active|paused|connected|needs_attention|awaiting_credential
	Tenant                string `json:"tenant,omitempty"`
	ProcessingEnabled     bool   `json:"processing_enabled"` // false = silent onboarding: polled, not processed
	HasSyncCursor         bool   `json:"has_sync_cursor"`
	SubscriptionExpiresAt string `json:"subscription_expires_at,omitempty"`
	ErrorMessage          string `json:"error_message,omitempty"`
	// Ingest liveness — the only health signal a POLL mailbox (IMAP) has, since it never carries a
	// subscription expiry. Empty LastSuccessfulSyncAt means it has not completed a sync yet.
	LastSuccessfulSyncAt    string `json:"last_successful_sync_at,omitempty"`
	ConsecutiveSyncFailures int    `json:"consecutive_sync_failures"`
	// Probe rides along on the connect + probe responses only, never on a list row.
	Probe *IMAPProbe `json:"probe,omitempty"`
	// PasswordLink rides along on the seed + password-link responses only. It is a no-login URL that can
	// only SET this mailbox's password, never read it — safe to forward to whoever holds the credential.
	PasswordLink string `json:"password_link,omitempty"`
}

// IMAPProbeStep is one stage of the live IMAP/SMTP connection check. Detail is a caller-safe hint that
// names the fix, not the transport error.
type IMAPProbeStep struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok | failed | warning | skipped
	Detail string `json:"detail,omitempty"`
}

// IMAPProbe is the connection checklist. OK is false only when a REQUIRED stage failed — a warning
// (typically: drafts cannot be placed) reports a limitation without blocking the mailbox.
type IMAPProbe struct {
	OK    bool            `json:"ok"`
	Steps []IMAPProbeStep `json:"steps"`
}

// WatchedMailboxList is GET /api/v1/projects/{project}/mailboxes — the watched-mailbox set under its envelope key.
type WatchedMailboxList struct {
	Mailboxes []WatchedMailbox `json:"mailboxes"`
}

// HarvestAccepted is the 202 body of POST /api/v1/projects/{project}/mailboxes/{id}/harvest — the queued export handle.
type HarvestAccepted struct {
	ExportID string `json:"export_id"`
	Status   string `json:"status"`
}

// ExportItem is one row of GET /api/v1/exports (and the whole of GET /api/v1/exports/{id}) — a
// local-synthesis corpus export (a harvest or a survey). Field names mirror the server verbatim; most
// counts/timestamps are omitempty (absent until the export runs/completes/is consumed). Truncated is
// always present (a harvest either hit its thread cap or didn't).
type ExportItem struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`             // harvest|survey|templates
	Format        string `json:"format,omitempty"` // retained artifact version; absent for surveys/unfinished legacy rows
	Status        string `json:"status"`           // pending|running|done|error|failed
	MailboxID     string `json:"mailbox_id"`
	Tenant        string `json:"tenant,omitempty"`
	Cleaned       *bool  `json:"cleaned,omitempty"`
	ThreadCount   *int   `json:"thread_count,omitempty"`
	TemplateCount *int   `json:"template_count,omitempty"`
	Truncated     bool   `json:"truncated"`
	CreatedAt     string `json:"created_at,omitempty"`
	CompletedAt   string `json:"completed_at,omitempty"`
	ConsumedAt    string `json:"consumed_at,omitempty"`
	Error         string `json:"error,omitempty"`
}

// Export aliases ExportItem for the single-item GET, matching the WatchedMailbox naming split.
type Export = ExportItem

// ExportList is GET /api/v1/exports — the exports (newest-first) under their envelope key.
type ExportList struct {
	Exports []ExportItem `json:"exports"`
}

// Project is one row of GET /api/v1/projects — a fleet handle (id + name). It's what `rc project list`
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

// RunEvent is one raw run_events row from GET /api/v1/run-events — the bulk feed `rc fleet patterns`
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
	raw          json.RawMessage
}

func (e *RunEvent) UnmarshalJSON(data []byte) error {
	type wire RunEvent
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*e = RunEvent(decoded)
	e.raw = append(e.raw[:0], data...)
	return nil
}

func (e RunEvent) MarshalJSON() ([]byte, error) {
	if len(e.raw) > 0 {
		return append([]byte(nil), e.raw...), nil
	}
	type wire RunEvent
	return json.Marshal(wire(e))
}

// RunEventsResponse is one page of GET /api/v1/run-events. NextBefore is the cursor to the next
// (older) page; empty on the last page.
// DetailRedacted marks a page whose rows kept their skeleton but lost reasoning/stdout/stderr/args/
// command — the caller is not a project-level admin. Pattern mining over such rows is blind, not clean.
type RunEventsResponse struct {
	Events         []RunEvent `json:"events"`
	NextBefore     string     `json:"next_before,omitempty"`
	DetailRedacted bool       `json:"detail_redacted,omitempty"`
}

// EgressRow is one raw egress_log row from GET /api/v1/egress-log — the bulk feed `rc fleet patterns`
// clusters into blocked-host signatures. Decision is "block" for a blocked attempt.
type EgressRow struct {
	ID           string `json:"id,omitempty"`
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
	raw          json.RawMessage
}

func (e *EgressRow) UnmarshalJSON(data []byte) error {
	type wire EgressRow
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*e = EgressRow(decoded)
	e.raw = append(e.raw[:0], data...)
	return nil
}

func (e EgressRow) MarshalJSON() ([]byte, error) {
	if len(e.raw) > 0 {
		return append([]byte(nil), e.raw...), nil
	}
	type wire EgressRow
	return json.Marshal(wire(e))
}

// EgressResponse is one page of GET /api/v1/egress-log. DetailRedacted: see RunEventsResponse.
type EgressResponse struct {
	Egress         []EgressRow `json:"egress"`
	NextBefore     string      `json:"next_before,omitempty"`
	DetailRedacted bool        `json:"detail_redacted,omitempty"`
}

// HTTPAuditRow is one client-cooperative or broker HTTP attempt from api_log. RequestBody is already
// redacted in-container; the host overwrites all correlation fields before persistence.
type HTTPAuditRow struct {
	ID             string          `json:"id"`
	RunID          string          `json:"run_id,omitempty"`
	ActionRunID    string          `json:"action_run_id,omitempty"`
	Source         string          `json:"source"`
	Method         string          `json:"method"`
	Endpoint       string          `json:"endpoint"`
	Path           string          `json:"path"`
	Host           string          `json:"host,omitempty"`
	StatusCode     int32           `json:"status_code"`
	Decision       string          `json:"decision"`
	PayloadSHA256  string          `json:"payload_sha256,omitempty"`
	RequestBody    json.RawMessage `json:"request_body,omitempty"`
	RequestBytes   int64           `json:"request_bytes"`
	ResponseBytes  int64           `json:"response_bytes"`
	DurationMs     int64           `json:"duration_ms"`
	Attempt        int32           `json:"attempt"`
	Reason         string          `json:"reason,omitempty"`
	RequestID      string          `json:"request_id,omitempty"`
	IntegrationKey string          `json:"integration_key,omitempty"`
	At             string          `json:"at"`
	raw            json.RawMessage
}

func (h *HTTPAuditRow) UnmarshalJSON(data []byte) error {
	type wire HTTPAuditRow
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*h = HTTPAuditRow(decoded)
	h.raw = append(h.raw[:0], data...)
	return nil
}

func (h HTTPAuditRow) MarshalJSON() ([]byte, error) {
	if len(h.raw) > 0 {
		return append([]byte(nil), h.raw...), nil
	}
	type wire HTTPAuditRow
	return json.Marshal(wire(h))
}

// HTTPAuditResponse is one page of GET /api/v1/api-log. DetailRedacted: see RunEventsResponse (here it
// strips request_body).
type HTTPAuditResponse struct {
	Items          []HTTPAuditRow `json:"items"`
	NextCursor     string         `json:"next_cursor,omitempty"`
	DetailRedacted bool           `json:"detail_redacted,omitempty"`
}

type RunEgressResponse struct {
	RunID          string         `json:"run_id"`
	Egress         []EgressRow    `json:"egress"`
	HTTP           []HTTPAuditRow `json:"http"`
	HTTPNextCursor string         `json:"http_next_cursor,omitempty"`
	HTTPTruncated  bool           `json:"http_truncated,omitempty"`
}

// ActionHistoryRow is the customer-safe action lifecycle projection. Grounded params, raw result,
// and errors are intentionally absent from the wire contract.
type ActionHistoryRow struct {
	ID          string `json:"id"`
	RunID       string `json:"run_id,omitempty"`
	TenantID    string `json:"tenant_id,omitempty"`
	ActionID    string `json:"action_id"`
	Status      string `json:"status"`
	Digest      string `json:"digest"`
	ParamsHash  string `json:"params_hash"`
	DurationMs  *int64 `json:"duration_ms,omitempty"`
	CreatedAt   string `json:"created_at"`
	CompletedAt string `json:"completed_at,omitempty"`
	// ErrorClass is the customer-safe, host-whitelisted failure class (executor_predispatch,
	// executor_error, no_executor, no_runner_url, attachment_fetch); anything action-authored
	// collapses to action_error server-side. The message stays off this endpoint.
	ErrorClass string `json:"error_class,omitempty"`
	raw        json.RawMessage
}

func (a *ActionHistoryRow) UnmarshalJSON(data []byte) error {
	type wire ActionHistoryRow
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*a = ActionHistoryRow(decoded)
	a.raw = append(a.raw[:0], data...)
	return nil
}

func (a ActionHistoryRow) MarshalJSON() ([]byte, error) {
	if len(a.raw) > 0 {
		return append([]byte(nil), a.raw...), nil
	}
	type wire ActionHistoryRow
	return json.Marshal(wire(a))
}

type ActionHistoryResponse struct {
	Items      []ActionHistoryRow `json:"items"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

// ActionFeedItem is one operator-only cross-run action row from GET /api/v1/actions. Unlike the
// customer-safe per-run lifecycle projection above, this feed intentionally includes the exact
// grounded params and a freshly minted tokenized run URL. Nullable wire fields use pointers so a
// synthesized value still marshals as JSON null. raw preserves future server fields through paging.
type ActionFeedItem struct {
	ID         string          `json:"id"`
	RunID      *string         `json:"run_id"`
	TenantID   *string         `json:"tenant_id"`
	ActionID   string          `json:"action_id"`
	Status     string          `json:"status"`
	Params     json.RawMessage `json:"params"`
	DurationMs *int64          `json:"duration_ms"`
	ProposedAt string          `json:"proposed_at"`
	ExecutedAt *string         `json:"executed_at"`
	RunURL     *string         `json:"run_url"`
	// ErrorClass/ErrorMessage are the settled failure verbatim (operator-only endpoint): the class
	// tells an infra fault (executor_predispatch = provably nothing ran, executor_error) from a
	// domain refusal stamped by the action itself.
	ErrorClass   string `json:"error_class,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	raw          json.RawMessage
}

func (a *ActionFeedItem) UnmarshalJSON(data []byte) error {
	type wire ActionFeedItem
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*a = ActionFeedItem(decoded)
	a.raw = append(a.raw[:0], data...)
	return nil
}

func (a ActionFeedItem) MarshalJSON() ([]byte, error) {
	if len(a.raw) > 0 {
		return append([]byte(nil), a.raw...), nil
	}
	type wire ActionFeedItem
	return json.Marshal(wire(a))
}

// ActionFeedResponse is one keyset-paginated page from GET /api/v1/actions.
type ActionFeedResponse struct {
	Items      []ActionFeedItem `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

// HealthMirror is one raw mirror_health row from GET /api/v1/health — the input `rc fleet health` applies its
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
	// Ingest liveness. HoursSinceSync is nil when the mailbox never completed a sync — "no signal yet",
	// which is not the same as stale.
	LastSuccessfulSyncAt    string   `json:"last_successful_sync_at,omitempty"`
	HoursSinceSync          *float64 `json:"hours_since_sync"`
	ConsecutiveSyncFailures int      `json:"consecutive_sync_failures"`
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

// DeployStateResponse is GET /api/v1/deploy-state — the live SHA per moving plane plus the timelines
// the server actually stores. There is no host deploy history server-side (the box only knows what it
// runs now), so `rc fleet deploy-state` derives "what is not deployed yet" from a local rootcause
// checkout instead.
type DeployStateResponse struct {
	Project       string              `json:"project"`
	GeneratedAt   string              `json:"generated_at"`
	HistoryLimit  int                 `json:"history_limit"`
	Host          DeployHost          `json:"host"`
	Brain         DeployBrain         `json:"brain"`
	Mirrors       []DeployMirror      `json:"mirrors"`
	MirrorHistory []DeployMirrorEvent `json:"mirror_history"`
}

// DeployHost is the running host build: Release is the short git SHA the container was built from
// (empty when the box never exported RELEASE).
type DeployHost struct {
	Release     string  `json:"release"`
	StartedAt   string  `json:"started_at,omitempty"`
	UptimeHours float64 `json:"uptime_hours"`
}

// DeployBrain is the project brain plane: the box's clone of main plus the channel pointers a run
// resolves, with the recorded promotion timeline.
type DeployBrain struct {
	Dir        string                 `json:"dir,omitempty"`
	State      string                 `json:"state"`
	MainSHA    string                 `json:"main_sha,omitempty"`
	OriginSHA  string                 `json:"origin_sha,omitempty"`
	SyncedAt   string                 `json:"synced_at,omitempty"`
	Channels   []DeployBrainChannel   `json:"channels"`
	Promotions []DeployBrainPromotion `json:"promotions"`
}

// DeployBrainChannel is one managed channel pointer (stable|edge) on the box.
type DeployBrainChannel struct {
	Channel       string `json:"channel"`
	ResolvedSHA   string `json:"resolved_sha,omitempty"`
	OriginSHA     string `json:"origin_sha,omitempty"`
	MainSHA       string `json:"main_sha,omitempty"`
	MatchesOrigin bool   `json:"matches_origin"`
	MatchesMain   bool   `json:"matches_main"`
	State         string `json:"state"`
}

// DeployBrainPromotion is one recorded channel promotion. Outcome "started" means the attempt never
// finished — its requested SHA is not proof of what went live.
type DeployBrainPromotion struct {
	Channel      string `json:"channel"`
	OldSHA       string `json:"old_sha,omitempty"`
	RequestedSHA string `json:"requested_sha,omitempty"`
	NewSHA       string `json:"new_sha,omitempty"`
	Outcome      string `json:"outcome"`
	Detail       string `json:"detail,omitempty"`
	Actor        string `json:"actor,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
	FinishedAt   string `json:"finished_at,omitempty"`
}

// DeployMirror is one mirror's currently checked-out source commit.
type DeployMirror struct {
	Repo        string `json:"repo"`
	Tenant      string `json:"tenant,omitempty"`
	State       string `json:"state"`
	SHA         string `json:"sha,omitempty"`
	Subject     string `json:"subject,omitempty"`
	CommittedAt string `json:"committed_at,omitempty"`
	LastOkAt    string `json:"last_ok_at,omitempty"`
}

// DeployMirrorEvent is one observed SHA change of a mirror — the refresh timeline.
type DeployMirrorEvent struct {
	Repo        string `json:"repo"`
	Tenant      string `json:"tenant,omitempty"`
	SHA         string `json:"sha"`
	Subject     string `json:"subject,omitempty"`
	CommittedAt string `json:"committed_at,omitempty"`
	RefreshedAt string `json:"refreshed_at"`
}

// EnvResponse is GET /api/v1/env — the resolved grounding env. Keys holds live secret VALUES (the
// whole point: `rc project env pull` writes them to ./.env). The CLI NEVER prints a value: it renders key
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
	// Members describes an object-typed field's CLOSED set of scalar members (e.g. models.agent →
	// tier/model/effort/engine); such a key is written as one JSON object, never member-by-member.
	Members []FieldSchema `json:"members,omitempty"`
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
	Formats      AccessFormats  `json:"formats"`
}

// AccessFormats is the wire-format versions THAT box currently writes (token-independent). It is the
// server's own answer to "can this rc still parse what you produce?" — read it instead of re-pinning the
// server's current corpus version here. Empty against a server older than the field.
type AccessFormats struct {
	HarvestCorpus string `json:"harvest_corpus"`
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

// TenantSettings is GET /api/v1/tenants/{slug}/profile (and the echoed body of a PATCH), the
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

// TenantSettingsPatchRequest is the PATCH /api/v1/tenants/{slug}/profile body:
// { "settings": { …partial… }, "source"?: "…" }. Settings is a raw key→value map so an explicit JSON
// null (the "unconfigure" gesture) rides through verbatim, distinct from an omitted key. Source is the
// provenance label ("cli"); omitempty so a blank source isn't sent (the server defaults it to "cli").
type TenantSettingsPatchRequest struct {
	Settings map[string]any `json:"settings"`
	Source   string         `json:"source,omitempty"`
}
