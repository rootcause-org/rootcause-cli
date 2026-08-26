package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

func TestActionsExposesCompleteOperatorFields(t *testing.T) {
	runID := "2db1"
	tenantID := "tenant-44"
	executedAt := "2026-07-22T08:58:14.006Z"
	runURL := "https://app.replypen.com/runs/2db1?t=full-secret-token"
	duration := int64(184)
	items := []client.ActionFeedItem{{
		ID:         "86fb",
		RunID:      &runID,
		TenantID:   &tenantID,
		ActionID:   "create_appointment",
		Status:     "succeeded",
		Params:     json.RawMessage(`{"agenda_id":42,"patient_id":"123","starts_at":"2026-07-22T09:00:00Z"}`),
		DurationMs: &duration,
		ProposedAt: "2026-07-22T08:58:11.123Z",
		ExecutedAt: &executedAt,
		RunURL:     &runURL,
	}}

	var out bytes.Buffer
	Actions(&out, items, "human")
	got := out.String()
	for _, want := range []string{
		"86fb", "2db1", tenantID, "create_appointment", "succeeded", "184ms",
		"2026-07-22T08:58:11.123Z", "2026-07-22T08:58:14.006Z",
		`{"agenda_id":42,"patient_id":"123","starts_at":"2026-07-22T09:00:00Z"}`,
		runURL,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestActionsRendersNullFieldsWithoutHidingEmptyParams(t *testing.T) {
	var out bytes.Buffer
	Actions(&out, []client.ActionFeedItem{{
		ID:         "direct-1",
		ActionID:   "sync",
		Status:     "proposed",
		Params:     json.RawMessage(`{}`),
		ProposedAt: "2026-07-22T08:00:00Z",
	}}, "human")
	got := out.String()
	if !strings.Contains(got, "Params: {}") || !strings.Contains(got, "Run URL: -") {
		t.Fatalf("null/empty fields rendered incorrectly:\n%s", got)
	}
}

func TestActionsAgentIsTokenLeanAndComplete(t *testing.T) {
	runID := "run-1"
	runURL := "https://app.replypen.com/runs/run-1?t=signed-token"
	duration := int64(17)
	var out bytes.Buffer
	Actions(&out, []client.ActionFeedItem{{
		ID:         "action-run-1",
		RunID:      &runID,
		ActionID:   "create_appointment",
		Status:     "succeeded",
		Params:     json.RawMessage(`{"patient_id":"p-1","starts_at":"2026-07-22T09:00:00+02:00"}`),
		DurationMs: &duration,
		ProposedAt: "2026-07-22T06:58:11Z",
		RunURL:     &runURL,
	}}, "agent")
	got := out.String()
	if strings.Count(got, "\n") != 1 {
		t.Fatalf("agent view should be one line per item:\n%s", got)
	}
	for _, want := range []string{
		`id=action-run-1`, `run_id="run-1"`, `tenant_id=null`,
		`action_id="create_appointment"`, `status="succeeded"`, `duration_ms=17`,
		`params={"patient_id":"p-1","starts_at":"2026-07-22T09:00:00+02:00"}`,
		`run_url="https://app.replypen.com/runs/run-1?t=signed-token"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("agent output missing %q:\n%s", want, got)
		}
	}
}

func TestActionsSurfacesSettledFailureClampedButClassed(t *testing.T) {
	long := "docker exec failed: fork/exec /usr/bin/docker: argument list too long\n(argv element 131414 bytes exceeds the 122880 byte limit)"
	items := []client.ActionFeedItem{{
		ID:           "86fb",
		ActionID:     "confirm_reschedule",
		Status:       "failed",
		Params:       json.RawMessage(`{}`),
		ProposedAt:   "2026-08-25T08:58:11.123Z",
		ErrorClass:   "executor_predispatch",
		ErrorMessage: long,
	}}

	var human, agent bytes.Buffer
	Actions(&human, items, "human")
	Actions(&agent, items, "agent")
	for name, got := range map[string]string{"human": human.String(), "agent": agent.String()} {
		if !strings.Contains(got, "executor_predispatch") {
			t.Fatalf("%s view lost the error class:\n%s", name, got)
		}
		if !strings.Contains(got, "docker exec failed") {
			t.Fatalf("%s view lost the error message:\n%s", name, got)
		}
		// Clamped to one readable line — the untruncated message stays in `-o json`.
		if strings.Contains(got, "122880") || strings.Contains(got, "\n(argv") {
			t.Fatalf("%s view did not clamp/flatten the error message:\n%s", name, got)
		}
	}
	if strings.Count(agent.String(), "\n") != 1 {
		t.Fatalf("agent view should stay one line per item:\n%s", agent.String())
	}
}
