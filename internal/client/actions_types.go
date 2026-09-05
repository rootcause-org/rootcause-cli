// Wire contract for the actions plane (catalogue, params/stats, exec, probe, Embassy health).
// Field names and omitempty MUST match the server verbatim; the ground rules live in types.go.
package client

import (
	"encoding/json"
	"time"
)

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

// ActionProbeResponse is the secret-free result of the host-originated Embassy mount and health probe.
type ActionProbeResponse struct {
	Reachable   bool                 `json:"reachable"`
	Status      int                  `json:"status"`
	AllowHeader string               `json:"allow_header,omitempty"`
	Health      *ActionEmbassyHealth `json:"health"`
	LatencyMs   int64                `json:"latency_ms"`
	Code        string               `json:"code,omitempty"`
	Hint        string               `json:"hint,omitempty"`
	Docs        string               `json:"docs,omitempty"`
}

type ActionEmbassyHealth struct {
	OK           bool     `json:"ok"`
	Embassy      string   `json:"embassy"`
	Version      string   `json:"version"`
	Protocol     int      `json:"protocol"`
	Capabilities []string `json:"capabilities"`
}
