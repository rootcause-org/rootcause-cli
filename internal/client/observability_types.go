// Wire contract for the observability feeds (run events, egress, HTTP audit, action history/feed, thread trace).
// Field names and omitempty MUST match the server verbatim; the ground rules live in types.go.
package client

import (
	"encoding/json"
	"time"
)

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
	// SecurityBlock is the pre-agent security-block verdict — non-null only when this thread was blocked
	// before any agent run (status injection_blocked), so no run row exists. Content-free enum, mirrors
	// the server's threadSecurityBlock; the corpus-runner asserts on it via `-o json`.
	SecurityBlock *ThreadSecurityBlock `json:"security_block,omitempty"`
}

// ThreadSecurityBlock mirrors the server's content-free security-block enum (stage names the checkpoint,
// category is the injectionscan enum). No rationale text by design.
type ThreadSecurityBlock struct {
	Stage      string  `json:"stage"`
	Category   string  `json:"category,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

// ThreadTrace is GET /api/v1/threads/{id}/trace — the channel-pipeline outcome plus every run for one
// rootcause/provider thread (or session) id. Mirrors the server's threadTraceResponse field-for-field.
type ThreadTrace struct {
	ID         string              `json:"id"`
	ResolvedBy string              `json:"resolved_by"`
	Threads    []ThreadTraceThread `json:"threads"`
	Runs       []RunSummary        `json:"runs"`
}

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
