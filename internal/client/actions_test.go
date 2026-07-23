package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestActionsPathIncludesRepeatableFiltersAndScope(t *testing.T) {
	path := ActionsPath(ActionFeedParams{
		Days:     14,
		Actions:  []string{"create_appointment", "update_appointment"},
		Statuses: []string{"succeeded", "failed"},
		Limit:    2000,
		Cursor:   "opaque+/=",
		Project:  "dentai",
		Tenant:   "de-kies",
	})
	request, err := http.NewRequest(http.MethodGet, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	query := request.URL.Query()
	if got, want := request.URL.Path, "/api/v1/actions"; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	if got, want := query["action"], []string{"create_appointment", "update_appointment"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("action = %#v, want %#v", got, want)
	}
	if got, want := query["status"], []string{"succeeded", "failed"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("status = %#v, want %#v", got, want)
	}
	for key, want := range map[string]string{
		"days": "14", "limit": "2000", "cursor": "opaque+/=", "project": "dentai", "tenant": "de-kies",
	} {
		if got := query.Get(key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestAllActionsPagesAndPreservesRawRows(t *testing.T) {
	var cursors []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/actions" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		cursors = append(cursors, r.URL.Query().Get("cursor"))
		w.Header().Set("Content-Type", "application/json")
		if len(cursors) == 1 {
			_, _ = w.Write([]byte(`{"items":[{"id":"ar-1","run_id":"run-1","tenant_id":null,"action_id":"create_appointment","status":"succeeded","params":{"patient_id":"123","agenda_id":42},"duration_ms":184,"proposed_at":"2026-07-22T08:58:11.123Z","executed_at":"2026-07-22T08:58:14.006Z","run_url":"https://app.replypen.com/runs/run-1?t=secret","future_field":{"kept":true}}],"next_cursor":"next opaque"}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"ar-2","run_id":null,"tenant_id":"tenant-1","action_id":"update_appointment","status":"proposed","params":{},"duration_ms":null,"proposed_at":"2026-07-21T08:00:00Z","executed_at":null,"run_url":null}]}`))
	}))
	defer server.Close()

	c := New(server.URL, StaticToken("test"))
	items, capped, err := c.AllActions(context.Background(), ActionFeedParams{Days: 14, Cursor: "ignored"})
	if err != nil {
		t.Fatal(err)
	}
	if capped {
		t.Fatal("unexpected page cap")
	}
	if got, want := cursors, []string{"", "next opaque"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("cursors = %#v, want %#v", got, want)
	}
	if len(items) != 2 || items[0].ID != "ar-1" || items[1].ID != "ar-2" {
		t.Fatalf("items = %#v", items)
	}
	got, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"future_field":{"kept":true}`) {
		t.Fatalf("unknown server field dropped after paging: %s", got)
	}
	if !strings.Contains(string(got), `"params":{"patient_id":"123","agenda_id":42}`) {
		t.Fatalf("exact params dropped after paging: %s", got)
	}
}

func TestAllActionsReportsPageCap(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"items":[],"next_cursor":"still-more"}`))
	}))
	defer server.Close()

	c := New(server.URL, StaticToken("test"))
	items, capped, err := c.AllActions(context.Background(), ActionFeedParams{})
	if err != nil {
		t.Fatal(err)
	}
	if !capped || len(items) != 0 || requests != maxFeedPages {
		t.Fatalf("items=%d capped=%t requests=%d, want 0/true/%d", len(items), capped, requests, maxFeedPages)
	}
}
