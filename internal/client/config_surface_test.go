package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBrainPromoteUsesCanonicalProjectRouteAndPreservesRawResult(t *testing.T) {
	const sha = "d2f9de784ab7cded001f2b6ac86892795f58a8ce"
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects/{project}/brain/promote", func(w http.ResponseWriter, r *http.Request) {
		if got := r.PathValue("project"); got != "dentai/shared" {
			t.Fatalf("project path = %q, want escaped project selector", got)
		}
		var req BrainPromoteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Channel != "stable" || req.SHA != sha {
			t.Fatalf("request = %+v", req)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"project":"dentai/shared","channel":"stable","old_sha":"79822d6309aa0000000000000000000000000000","new_sha":"` + sha + `","changed":true,"idempotent":false,"audit_id":"kept"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, StaticToken("test"))
	resp, raw, err := c.BrainPromote(context.Background(), "dentai/shared", BrainPromoteRequest{Channel: "stable", SHA: sha})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Project != "dentai/shared" || resp.NewSHA != sha || !resp.Changed || resp.Idempotent {
		t.Fatalf("response = %+v", resp)
	}
	var verbatim map[string]any
	if err := json.Unmarshal(raw, &verbatim); err != nil {
		t.Fatal(err)
	}
	if verbatim["audit_id"] != "kept" {
		t.Fatalf("raw response lost additive field: %s", raw)
	}
}

func TestBrainPromoteRequiresProjectWithoutRequest(t *testing.T) {
	c := New("http://127.0.0.1:1", StaticToken("test"))
	_, _, err := c.BrainPromote(context.Background(), "", BrainPromoteRequest{Channel: "stable", SHA: "d2f9de784ab7cded001f2b6ac86892795f58a8ce"})
	if err == nil {
		t.Fatal("expected project-required error")
	}
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Code != "PROJECT_REQUIRED" {
		t.Fatalf("error = %#v, want PROJECT_REQUIRED", err)
	}
}
