// Wire contract for the per-run guard rails (injection scan, egress, output guard/judge, final verdict).
// Field names and omitempty MUST match the server verbatim; the ground rules live in types.go.
package client

import "encoding/json"

// GuardsView is the run header's `guards` object: every security checkpoint's verdict, gathered by the
// server on the detail/project-admin trace tier. Absent fields mean the checkpoint did not apply (see
// each field's note). Mirrors the server contract field-for-field; do not rename.
type GuardsView struct {
	// PrincipalScope is runs.principal_scope passed through UNMASKED (kind, external_id, asserted_by,
	// assurance, resolution, claims, table_grants). Absent if the run carried no principal scope.
	PrincipalScope json.RawMessage `json:"principal_scope,omitempty"`
	// InjectionScan is the inbound prompt-injection scan. Absent for chat/prompt runs (no channel thread).
	InjectionScan *GuardsInjection `json:"injection_scan,omitempty"`
	// Egress is the outbound host allow/block tally. Always present.
	Egress *GuardsEgress `json:"egress,omitempty"`
	// OutputGuard is the deterministic output guard. Absent for non-chat runs.
	OutputGuard *GuardsOutputGuard `json:"output_guard,omitempty"`
	// OutputJudge is the semantic output judge. Absent unless the judge ran; a set Error means fail-open
	// (the turn shipped UNJUDGED).
	OutputJudge *GuardsOutputJudge `json:"output_judge,omitempty"`
	// Final is the run's terminal disposition. Always present.
	Final *GuardsFinal `json:"final,omitempty"`
}

// GuardsInjection is the inbound prompt-injection scan verdict.
type GuardsInjection struct {
	Ran        bool    `json:"ran"`
	Category   string  `json:"category"`
	Confidence float64 `json:"confidence"`
	Blocked    bool    `json:"blocked"`
	Rationale  string  `json:"rationale"`
}

// GuardsEgress is the outbound host allow/block tally with the per-host breakdown.
type GuardsEgress struct {
	Allowed int                `json:"allowed"`
	Blocked int                `json:"blocked"`
	Hosts   []GuardsEgressHost `json:"hosts"`
}

// GuardsEgressHost is one host's outbound decision + attempt count.
type GuardsEgressHost struct {
	Host     string `json:"host"`
	Decision string `json:"decision"`
	Count    int    `json:"count"`
}

// GuardsOutputGuard is the deterministic output-guard verdict.
type GuardsOutputGuard struct {
	Evaluated      bool     `json:"evaluated"`
	SurfaceGuarded bool     `json:"surface_guarded"`
	Violated       bool     `json:"violated"`
	Rules          []string `json:"rules"`
	SourceHits     int      `json:"source_hits"`
	SourceScore    int      `json:"source_score"`
}

// GuardsOutputJudge is the semantic output-judge verdict. A non-empty Error is fail-open: the turn
// shipped UNJUDGED.
type GuardsOutputJudge struct {
	Fired      bool     `json:"fired"`
	Reasons    []string `json:"reasons"`
	Violation  bool     `json:"violation"`
	Blocked    bool     `json:"blocked"`
	Category   string   `json:"category"`
	Confidence float64  `json:"confidence"`
	Error      string   `json:"error"`
}

// GuardsFinal is the run's terminal disposition.
type GuardsFinal struct {
	Outcome       string `json:"outcome"`
	HasDraft      bool   `json:"has_draft"`
	HasNote       bool   `json:"has_note"`
	DeclineReason string `json:"decline_reason"`
	Guardrail     string `json:"guardrail"`
}
