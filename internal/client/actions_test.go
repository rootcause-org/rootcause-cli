package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
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
