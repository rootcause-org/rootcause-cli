package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// update regenerates the golden files instead of comparing. Run: go test ./internal/cli -update
var update = flag.Bool("update", false, "update golden files")

// stubServer returns canned JSON per endpoint so the renderers and JSON passthrough can be pinned by
// golden tests without a live API. Each handler asserts the bearer header is present (auth wiring) and
// echoes a fixed fixture; the settings PATCH path returns an INVALID_SETTINGS envelope when asked to,
// driving the error-path test.
func stubServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/runs", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		// `rc fleet` pages this: with a cursor set, return the operator-tier (health-bearing) page that
		// ENDS the window (no next_before) so the paging loop terminates. Without a cursor it's the
		// existing single-page fixture (status/runs view).
		if r.URL.Query().Get("before") != "" {
			_, _ = w.Write(fixture(t, "fleet_runs_p2.json"))
			return
		}
		if r.URL.Query().Get("kind") == "fleet" { // `rc fleet` test path: drive the operator-tier digest fixtures
			_, _ = w.Write(fixture(t, "fleet_runs_p1.json"))
			return
		}
		_, _ = w.Write(fixture(t, "runs.json"))
	})

	// Fleet enumeration (rc projects + the --all fan-out seed). A "solo" project drives the --all
	// scoped-token error; the default returns the two-project fleet.
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("X-Test-Solo") != "" {
			_, _ = w.Write([]byte(`{"projects":[{"id":"11111111-1111-1111-1111-111111111111","name":"only-me"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"projects":[` +
			`{"id":"aaaaaaaa-0000-0000-0000-000000000001","name":"alpha"},` +
			`{"id":"bbbbbbbb-0000-0000-0000-000000000002","name":"bravo"}]}`))
	})

	// Observability feeds (rc fleet / patterns / health). The events feed is paged: page 1 carries a
	// next_before, page 2 (before set) is the last page — exercising the CLI's paging loop + accumulation.
	mux.HandleFunc("GET /api/v1/run-events", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("before") == "" {
			_, _ = w.Write(fixture(t, "events_feed_p1.json"))
			return
		}
		_, _ = w.Write(fixture(t, "events_feed_p2.json"))
	})
	mux.HandleFunc("GET /api/v1/egress-log", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "egress_feed.json"))
	})
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		// hours=999 simulates a clean (healthy) fleet; the default returns the unhealthy fixture.
		if r.URL.Query().Get("hours") == "999" {
			_, _ = w.Write(fixture(t, "health_clean.json"))
			return
		}
		_, _ = w.Write(fixture(t, "health.json"))
	})

	mux.HandleFunc("GET /api/v1/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		if r.PathValue("id") == "bad" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"UNKNOWN_RUN","message":"unknown run"}}`))
			return
		}
		// id "running" never reaches a terminal status — drives the `rc ask` timeout path.
		if r.PathValue("id") == "running" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"run_id":"running","status":"running","kind":"prompt","created_at":"2026-06-19T09:00:00Z","has_draft":false,"has_note":false,"attachments":[]}`))
			return
		}
		// id "405" simulates an older server whose endpoint returns a plain-text (non-JSON) 405 — the
		// no-envelope error path the friendly diagnostics are for.
		if r.PathValue("id") == "405" {
			w.Header().Set("Allow", "POST")
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = w.Write([]byte("Method Not Allowed\n"))
			return
		}
		// id "declined" carries the run-debug bundle (decline_reason + guardrail/forced/fallback flags +
		// recoverable retries) — exercises the index "why" line and the -o json passthrough of the new fields.
		if r.PathValue("id") == "declined" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(fixture(t, "run_declined.json"))
			return
		}
		if r.PathValue("id") == "email-rich" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(fixture(t, "ask_email_run.json"))
			return
		}
		if r.PathValue("id") == "raw-rich" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(fixture(t, "ask_raw_run.json"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "run.json"))
	})
	mux.HandleFunc("GET /api/v1/runs/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		// id "sentinel" returns the negative-sentinel seq block the real server emits, to exercise the
		// table renumbering (and confirm NDJSON keeps the raw seq).
		if r.PathValue("id") == "sentinel" {
			_, _ = w.Write([]byte(`{"run_id":"sentinel","events":[` +
				`{"seq":-1000000,"tool":"bash","status":"error","exit_code":64,"duration_ms":0,"at":"2026-06-19T08:10:06Z"},` +
				`{"seq":-999999,"tool":"bash","status":"ok","exit_code":0,"duration_ms":97,"at":"2026-06-19T08:10:10Z"}]}`))
			return
		}
		// id "declined" ends on a reply event that carries decline_reason (no draft/note) — exercises the
		// trace's terminal-decline rendering.
		if r.PathValue("id") == "declined" {
			_, _ = w.Write(fixture(t, "events_declined.json"))
			return
		}
		if r.PathValue("id") == "large-events" {
			_, _ = w.Write(largeEventsJSON())
			return
		}
		_, _ = w.Write(fixture(t, "events.json"))
	})
	mux.HandleFunc("GET /api/v1/runs/{id}/trace", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		// id "declined" carries the run.debug bundle, exercising the full header's debug rows + the
		// untruncated decline_reason block.
		if r.PathValue("id") == "declined" {
			_, _ = w.Write(fixture(t, "full_declined.json"))
			return
		}
		if r.PathValue("id") == "email-rich" {
			_, _ = w.Write(fixture(t, "ask_email_full.json"))
			return
		}
		_, _ = w.Write(fixture(t, "full.json"))
	})
	mux.HandleFunc("GET /api/v1/runs/{id}/brain-diff", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		// id "no-brain" wrote no journal commit — the explicit empty (found:false) result.
		if r.PathValue("id") == "no-brain" {
			_, _ = w.Write([]byte(`{"run_id":"no-brain","found":false}`))
			return
		}
		_, _ = w.Write(fixture(t, "brain_diff.json"))
	})
	mux.HandleFunc("GET /api/v1/threads/{id}/trace", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		switch r.PathValue("id") {
		case "session-fallback": // resolved via the session_id fallback path
			_, _ = w.Write(fixture(t, "thread_trace_session.json"))
		case "unknown": // an id matching nothing → clean empty (resolved_by:"none")
			_, _ = w.Write([]byte(`{"id":"unknown","resolved_by":"none","runs":[]}`))
		default:
			_, _ = w.Write(fixture(t, "thread_trace.json"))
		}
	})
	mux.HandleFunc("POST /api/v1/runs", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		// A rejected ref drives the BAD_BRAIN_REF path; seeing it in the body also proves --brain-ref
		// is forwarded verbatim.
		if strings.Contains(body, `"brain_ref":"bad/ref"`) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"BAD_BRAIN_REF","message":"brain ref rejected"}}`))
			return
		}
		// A prompt asking to hang resolves to the never-terminal "running" run (timeout test).
		runID := "11111111-1111-1111-1111-111111111111"
		if strings.Contains(body, "hang-please") {
			runID = "running"
		} else if strings.Contains(body, "email-rich") {
			runID = "email-rich"
		} else if strings.Contains(body, `"scenario":"raw"`) {
			runID = "raw-rich"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"run_id":"` + runID + `","status":"running","status_url":"/api/v1/runs/` + runID + `","poll_after_ms":1}`))
	})
	mux.HandleFunc("GET /api/v1/env", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		// A tenant query returns a tenant-merged shape (an extra key + a differing FEATURE_FLAG), so the
		// --tenant plumbing and the scope label are exercised.
		if r.URL.Query().Get("tenant") == "acme" {
			_, _ = w.Write([]byte(`{"project":"momentum-tools","tenant":"acme","keys":{"FEATURE_FLAG":"tenant","REGION":"eu","ACME_DSN":"postgres://acme@h/db"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"project":"momentum-tools","keys":{"FEATURE_FLAG":"project","REGION":"eu","STRIPE_KEY":"sk_live_SECRET"}}`))
	})
	mux.HandleFunc("POST /api/v1/console/bash/run", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		var req struct {
			Command  string `json:"command"`
			TimeoutS int    `json:"timeout_s"`
		}
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatalf("decode bash run body: %v\n%s", err, body)
		}
		if req.Command == "large-output" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(largeBashRunJSON())
			return
		}
		if req.Command == "small-truncated" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(smallTruncatedBashRunJSON())
			return
		}
		if req.Command != "printf hello && >&2 echo warn && exit 7" {
			t.Fatalf("bash command = %q", req.Command)
		}
		if req.TimeoutS != 45 {
			t.Fatalf("bash timeout_s = %d, want 45", req.TimeoutS)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "bash_run.json"))
	})
	mux.HandleFunc("GET /api/v1/console/bash", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "bash_list.json"))
	})
	mux.HandleFunc("GET /api/v1/console/capabilities", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "console_capabilities.json"))
	})
	mux.HandleFunc("GET /api/v1/whoami", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"email":"dev@example.test","project":{"id":"aaaaaaaa-0000-0000-0000-000000000001","name":"alpha","slug":"alpha"}}`))
	})
	mux.HandleFunc("GET /api/v1/settings", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "settings.json"))
	})
	mux.HandleFunc("PATCH /api/v1/settings", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		// "max_run_usd":"oops" (a non-number) drives the INVALID_SETTINGS error path.
		if strings.Contains(body, `"max_run_usd":"oops"`) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"INVALID_SETTINGS","message":"settings rejected","details":[{"field":"max_run_usd","message":"must be a number"}]}}`))
			return
		}
		// A pr.triggers set must arrive as a JSON ARRAY, not a comma string — the list-coercion contract.
		if strings.Contains(body, `"pr.triggers"`) {
			var got map[string]any
			if err := json.Unmarshal([]byte(body), &got); err != nil {
				t.Fatalf("decode set body: %v\n%s", err, body)
			}
			arr, ok := got["pr.triggers"].([]any)
			if !ok {
				t.Fatalf("pr.triggers not a JSON array: %T %v\nbody: %s", got["pr.triggers"], got["pr.triggers"], body)
			}
			if len(arr) != 2 || arr[0] != "inbound" || arr[1] != "mcp" {
				t.Fatalf("pr.triggers array = %v, want [inbound mcp]", arr)
			}
		}
		// An empty list value (egress.allowlist=) is the CLEAR gesture: it must arrive as an empty array,
		// not null or a string.
		if strings.Contains(body, `"egress.allowlist"`) {
			var got map[string]any
			_ = json.Unmarshal([]byte(body), &got)
			arr, ok := got["egress.allowlist"].([]any)
			if !ok || len(arr) != 0 {
				t.Fatalf("egress.allowlist = %T %v, want empty array\nbody: %s", got["egress.allowlist"], got["egress.allowlist"], body)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "settings.json"))
	})

	// kb bag (GET/PATCH /api/v1/kb): the generic bag shape, same as settings. PATCH echoes the fixture.
	mux.HandleFunc("GET /api/v1/kb", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "kb.json"))
	})
	mux.HandleFunc("PATCH /api/v1/kb", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		_ = readBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "kb.json"))
	})
	// action bag PATCH: asserts the bool-coercion contract — actions_enabled must arrive as a JSON bool,
	// not the string "true". Returns the kb fixture shape (the body is irrelevant to the assertion).
	mux.HandleFunc("PATCH /api/v1/action", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		if strings.Contains(body, "actions_enabled") {
			var got map[string]any
			if err := json.Unmarshal([]byte(body), &got); err != nil {
				t.Fatalf("decode action set body: %v\n%s", err, body)
			}
			if b, ok := got["actions_enabled"].(bool); !ok || !b {
				t.Fatalf("actions_enabled = %T %v, want JSON bool true\nbody: %s", got["actions_enabled"], got["actions_enabled"], body)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "kb.json"))
	})

	// Discovery layer: the config registry schema + token capabilities.
	mux.HandleFunc("GET /api/v1/meta/schema", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		if r.URL.Query().Get("resource") == "bogus" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"UNKNOWN_RESOURCE","message":"no such resource: bogus"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "meta_schema.json"))
	})
	mux.HandleFunc("GET /api/v1/meta/capabilities", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "meta_capabilities.json"))
	})
	mux.HandleFunc("GET /api/v1/meta/routes", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"routes":[{"method":"GET","path":"/api/v1/runs/{id}/trace","summary":"Read run trace bundle","auth":"bearer"},{"method":"GET","path":"/api/v1/runs/{id}/full","summary":"Read full run bundle","auth":"bearer","deprecated":true}]}`))
	})
	mux.HandleFunc("GET /api/v1/meta/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"openapi":"3.1.0","paths":{"/api/v1/runs/{id}/trace":{"get":{"summary":"Read run trace bundle"}}}}`))
	})

	// Hierarchy settings editing surface. The schema is served verbatim from the embedded legacy copy
	// for the compatibility schema command; GET returns a nested record; PATCH echoes the nested patch.
	mux.HandleFunc("GET /api/v1/tenants/settings/schema", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "tenant_schema.json"))
	})
	mux.HandleFunc("GET /api/v1/tenants/{slug}/settings", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "tenant_settings.json"))
	})
	mux.HandleFunc("PATCH /api/v1/tenants/{slug}/settings", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		if strings.Contains(body, `"unknown_key"`) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"validation_failed","field_errors":{"unknown_key":"is not allowed"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "tenant_settings.json"))
	})
	mux.HandleFunc("GET /api/v1/projects/{project}/settings", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "hierarchy_project_settings.json"))
	})
	mux.HandleFunc("PATCH /api/v1/projects/{project}/settings", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"scope":"project","project":"%s","settings":%s,"resolved":{"persona":{"tone":{"value":"project crisp","source":"project"}}}}`, r.PathValue("project"), body)
	})
	mux.HandleFunc("GET /api/v1/projects/{project}/tenants/{slug}/settings", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		// An unknown tenant is a uniform 404 (existence not leaked), like the real server.
		if r.PathValue("slug") == "ghost" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"NOT_FOUND","message":"tenant not found"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "hierarchy_tenant_settings.json"))
	})
	mux.HandleFunc("PATCH /api/v1/projects/{project}/tenants/{slug}/settings", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		// A bad enum value drives the 400 validation_failed / field_errors path (server is final word).
		if strings.Contains(body, `"tone":"nope"`) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"INVALID_SETTINGS","message":"1 setting failed validation","details":[{"field":"persona.tone","message":"bad tone"}]}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"scope":"tenant","project":"%s","tenant":"%s","settings":%s,"resolved":{"persona":{"tone":{"value":"tenant crisp","source":"tenant"},"language":{"value":"Dutch","source":"project"}},"channel":{"labeling_enabled":{"value":true,"source":"tenant"}}}}`, r.PathValue("project"), r.PathValue("slug"), body)
	})
	mux.HandleFunc("GET /api/v1/projects/{project}/mailboxes/{id}/settings", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "hierarchy_mailbox_settings.json"))
	})
	mux.HandleFunc("PATCH /api/v1/projects/{project}/mailboxes/{id}/settings", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"scope":"mailbox","project":"%s","tenant":"acme","mailbox":"%s","settings":%s,"resolved":{"persona":{"tone":{"value":"mailbox direct","source":"mailbox"}}}}`, r.PathValue("project"), r.PathValue("id"), body)
	})

	// Spam allow/block lists — path-scoped by project (/api/v1/projects/{project}/spam/…). GET each
	// list echoes a canned fixture; POST asserts the {pattern,reason} body and echoes back one rule with
	// the server-inferred match_type; DELETE 404s an unknown id on the block list so `rm` falls through
	// to the allow list (the try-both UX). The "created" field is present so the CREATED column shows.
	mux.HandleFunc("GET /api/v1/projects/{project}/spam/allows", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "spam_allows.json"))
	})
	mux.HandleFunc("GET /api/v1/projects/{project}/spam/blocks", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "spam_blocks.json"))
	})
	mux.HandleFunc("POST /api/v1/projects/{project}/spam/allows", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		var body map[string]any
		if err := json.Unmarshal([]byte(readBody(t, r)), &body); err != nil {
			t.Fatalf("decode spam create body: %v", err)
		}
		if _, ok := body["pattern"]; !ok {
			t.Fatalf("spam create body missing pattern: %v", body)
		}
		if _, ok := body["match_type"]; ok {
			t.Fatalf("spam create must not send match_type (server infers it): %v", body)
		}
		// A mailbox_id (when the caller passed --mailbox) rides in the BODY and echoes back as "mailbox".
		mailbox, _ := body["mailbox_id"].(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"a1111111-0000-0000-0000-000000000001","verdict":"allow","match_type":"sender_domain","pattern":%q,"reason":%q,"source":"operator","mailbox":%q,"created_at":"2026-07-01T10:00:00Z"}`,
			body["pattern"], reasonOrEmpty(body), mailbox)
	})
	mux.HandleFunc("POST /api/v1/projects/{project}/spam/blocks", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		var body map[string]any
		if err := json.Unmarshal([]byte(readBody(t, r)), &body); err != nil {
			t.Fatalf("decode spam create body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"b1111111-0000-0000-0000-000000000001","verdict":"block","match_type":"sender_address","pattern":%q,"reason":%q,"source":"operator","created_at":"2026-07-01T10:05:00Z"}`,
			body["pattern"], reasonOrEmpty(body))
	})
	mux.HandleFunc("DELETE /api/v1/projects/{project}/spam/allows/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /api/v1/projects/{project}/spam/blocks/{id}", func(w http.ResponseWriter, r *http.Request) {
		// id "allow-only" is not on the block list → 404, so `rm` falls through to the allow list.
		if r.PathValue("id") == "allow-only" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"UNKNOWN_SPAM_RULE","message":"unknown spam rule"}}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// Collection resources (repos / connections / members / tokens). Each list GET echoes a canned
	// fixture; create/patch echo a single item; the connection/token item-verbs return their canned
	// shapes. Handlers assert the auth header (via requireAuth) and, where load-bearing, the request body.

	// repos: full CRUD (id = repo name).
	mux.HandleFunc("GET /api/v1/repos", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "repos.json"))
	})
	// tenants collection (id = slug). GET lists; POST asserts the slug arrives and never leaks a secret.
	mux.HandleFunc("GET /api/v1/tenants", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "tenants.json"))
	})
	mux.HandleFunc("POST /api/v1/tenants", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		if !strings.Contains(body, `"slug":"acme"`) {
			t.Fatalf("tenant create body missing slug: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "tenant_item.json"))
	})
	mux.HandleFunc("GET /api/v1/tenants/{slug}", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		if r.PathValue("slug") != "acme" {
			t.Fatalf("tenant get slug = %q, want acme", r.PathValue("slug"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "tenant_item.json"))
	})
	mux.HandleFunc("POST /api/v1/repos", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		if !strings.Contains(body, `"id":"momentum-web"`) {
			t.Fatalf("repo create body missing id: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "repo_item.json"))
	})
	mux.HandleFunc("PATCH /api/v1/repos/{id}", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		if !strings.Contains(body, `"description":"Updated"`) {
			t.Fatalf("repo patch body missing field: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "repo_item.json"))
	})
	mux.HandleFunc("DELETE /api/v1/repos/{id}", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.WriteHeader(http.StatusNoContent)
	})

	// connections: list / create (echoes item, NO secret) / reveal / rotate / revoke / delete.
	mux.HandleFunc("GET /api/v1/connections", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "connections.json"))
	})
	mux.HandleFunc("POST /api/v1/connections", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "connection_item.json"))
	})
	mux.HandleFunc("POST /api/v1/connections/probe", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		if !strings.Contains(body, `"capability":"notion.write"`) {
			t.Fatalf("connection probe body missing capability: %s", body)
		}
		if strings.Contains(r.URL.RawQuery, "project=") && r.URL.Query().Get("project") == "" {
			t.Fatalf("bad project query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "connection_probe.json"))
	})
	mux.HandleFunc("POST /api/v1/connections/{id}/reveal", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"secret":"sk_live_REVEALED_ONCE"}`))
	})
	mux.HandleFunc("POST /api/v1/connections/{id}/rotate", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + r.PathValue("id") + `","status":"active","rotated":true}`))
	})
	mux.HandleFunc("POST /api/v1/connections/{id}/revoke", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /api/v1/connections/{id}", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.WriteHeader(http.StatusNoContent)
	})

	// members: list / create / delete (no read/update server-side).
	mux.HandleFunc("GET /api/v1/members", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "members.json"))
	})
	mux.HandleFunc("POST /api/v1/members", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		if !strings.Contains(body, `"id":"carol@acme.test"`) {
			t.Fatalf("member create body missing id: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"carol@acme.test","role":"editor"}`))
	})
	mux.HandleFunc("DELETE /api/v1/members/{id}", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.WriteHeader(http.StatusNoContent)
	})

	// tokens: list / mint (refresh_token+scope+status ONCE) / revoke.
	mux.HandleFunc("GET /api/v1/tokens", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "tokens.json"))
	})
	mux.HandleFunc("POST /api/v1/tokens", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"refresh_token":"rc_refresh_MINTED_ONCE","scope":"config:read","status":"active"}`))
	})
	mux.HandleFunc("DELETE /api/v1/tokens/{id}", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.WriteHeader(http.StatusNoContent)
	})

	registerConfigSurfaceStubs(t, mux)
	return httptest.NewServer(mux)
}

func largeBashRunJSON() []byte {
	stdout := "HEAD\n" + strings.Repeat("alpha line\n", 350) + "MIDDLE-SENTINEL\n" + strings.Repeat("omega line\n", 350) + "TAIL\n"
	body := map[string]any{
		"project":          "momentum-tools",
		"tenant":           "acme",
		"brain_resolved":   "HEAD @ 333333333333",
		"run_id":           "33333333-3333-3333-3333-333333333333",
		"seq":              9,
		"exit_code":        0,
		"stdout":           stdout,
		"stderr":           "small warning\n",
		"stdout_truncated": true,
		"stderr_truncated": false,
		"timed_out":        false,
		"duration_ms":      99,
		"egress_blocked":   false,
	}
	b, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return b
}

func smallTruncatedBashRunJSON() []byte {
	body := map[string]any{
		"project":          "momentum-tools",
		"tenant":           "acme",
		"brain_resolved":   "HEAD @ 444444444444",
		"run_id":           "44444444-4444-4444-4444-444444444444",
		"seq":              4,
		"exit_code":        0,
		"stdout":           "",
		"stderr":           "warn\n",
		"stdout_truncated": false,
		"stderr_truncated": true,
		"timed_out":        false,
		"duration_ms":      12,
		"egress_blocked":   false,
	}
	b, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return b
}

func largeEventsJSON() []byte {
	events := make([]map[string]any, 0, 260)
	for i := 0; i < 260; i++ {
		events = append(events, map[string]any{
			"seq":         i + 1,
			"tool":        "bash",
			"status":      "ok",
			"exit_code":   0,
			"duration_ms": 5,
			"at":          "2026-06-19T08:10:10Z",
			"command":     "printf event",
			"stdout":      strings.Repeat("event payload ", 8),
		})
	}
	b, err := json.Marshal(map[string]any{"run_id": "large-events", "events": events})
	if err != nil {
		panic(err)
	}
	return b
}

// registerConfigSurfaceStubs wires the config-surface endpoints (mailboxes / env per-key / databases +
// controls / openrouter-key / branding logo / github / brain / run feedback+retry / admin). Split into
// its own helper to keep stubServer readable; each handler asserts auth and (where load-bearing) the
// request body, then echoes a canned shape.
func registerConfigSurfaceStubs(t *testing.T, mux *http.ServeMux) {
	t.Helper()

	// watched mailboxes (the channel plane's inbox watch): list / pause / resume. resume on id
	// "needs-attn" returns a 200 with status:needs_attention + error_message (a subscribe failure surfaced,
	// NOT an error envelope).
	mux.HandleFunc("GET /api/v1/mailboxes/watched", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "watched_mailboxes.json"))
	})
	mux.HandleFunc("POST /api/v1/mailboxes/{id}/pause", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + r.PathValue("id") + `","provider":"google","email_address":"ops@momentum.test","status":"paused","has_sync_cursor":true}`))
	})
	mux.HandleFunc("POST /api/v1/mailboxes/{id}/resume", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		if r.PathValue("id") == "needs-attn" {
			_, _ = w.Write([]byte(`{"id":"needs-attn","provider":"microsoft","email_address":"help@acme.test","status":"needs_attention","has_sync_cursor":false,"error_message":"Subscribe failed: token expired"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"` + r.PathValue("id") + `","provider":"google","email_address":"ops@momentum.test","status":"active","has_sync_cursor":true}`))
	})

	// generic IMAP/SMTP connect (rc mailbox connect-imap): assert the password rode in the BODY (never
	// argv) and that the client applied the username→email / smtp-host→imap-host defaults, then echo a
	// created watched-mailbox item. A duplicate email ("dupe@acme.test") drives the 409 MAILBOX_IN_USE path.
	mux.HandleFunc("POST /api/v1/mailboxes/imap/connect", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		var got map[string]any
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatalf("decode imap connect body: %v\n%s", err, body)
		}
		if got["email_address"] == "dupe@acme.test" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":{"code":"MAILBOX_IN_USE","message":"that IMAP mailbox is already connected to another project or tenant"}}`))
			return
		}
		if got["password"] != "s3cr3t-from-env" {
			t.Fatalf("imap connect password = %v, want s3cr3t-from-env (from env, not argv)", got["password"])
		}
		if got["username"] != "info@acme.test" || got["smtp_host"] != "imap.acme.test" {
			t.Fatalf("imap connect defaults = %v, want username=info@acme.test smtp_host=imap.acme.test", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"mb-imap-1","provider":"imap","email_address":"info@acme.test","status":"connected","processing_enabled":false,"has_sync_cursor":false}`))
	})
	mux.HandleFunc("GET /api/v1/mailboxes/{id}/imap-env", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"mailbox_id":"` + r.PathValue("id") + `","email_address":"info@acme.test","env":{"RC_MAILBOX_ID":"` + r.PathValue("id") + `","RC_IMAP_EMAIL":"info@acme.test","RC_IMAP_USERNAME":"imap-user","RC_IMAP_PASSWORD":"imap-secret","RC_IMAP_HOST":"imap.acme.test","RC_IMAP_PORT":"993","RC_IMAP_TLS":"implicit","RC_SMTP_HOST":"smtp.acme.test","RC_SMTP_PORT":"587","RC_SMTP_TLS":"starttls","RC_SMTP_USERNAME":"smtp-user","RC_SMTP_PASSWORD":"smtp-secret","RC_UNEXPECTED_SECRET":"do-not-print-me"}}`))
	})

	// silent-onboarding processing gate (rc mailbox process on/off): echo the updated item with the
	// flag reflecting the verb path.
	mux.HandleFunc("POST /api/v1/mailboxes/{id}/processing/enable", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + r.PathValue("id") + `","provider":"google","email_address":"ops@momentum.test","status":"active","processing_enabled":true,"has_sync_cursor":true}`))
	})
	mux.HandleFunc("POST /api/v1/mailboxes/{id}/processing/disable", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + r.PathValue("id") + `","provider":"google","email_address":"ops@momentum.test","status":"active","processing_enabled":false,"has_sync_cursor":true}`))
	})

	// local-synthesis harvest/export (rc mailbox harvest, rc export ls/get/download). Harvest asserts the
	// clean/max_threads body shape; a mailbox id "busy" drives the 409 HARVEST_IN_PROGRESS path. The export
	// list/get echo canned fixtures; download returns raw Markdown (id "missing" → 404 BODY_UNAVAILABLE).
	// id "running-then-done" flips its export status once so the --wait poll loop terminates on the 2nd read.
	mux.HandleFunc("POST /api/v1/mailboxes/{id}/harvest", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		if r.PathValue("id") == "busy" {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":{"code":"HARVEST_IN_PROGRESS","message":"a harvest is already in progress for this mailbox"}}`))
			return
		}
		body := readBody(t, r)
		var got map[string]any
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatalf("decode harvest body: %v\n%s", err, body)
		}
		// clean omitted unless set; max_threads omitted at 0. When the wait id is asked, the body carries
		// max_threads:5 + clean:false to prove flag plumbing.
		exportID := "eeee1111-0000-0000-0000-000000000001"
		if r.PathValue("id") == "wait" {
			exportID = "wait-export"
			if got["max_threads"] != float64(5) || got["clean"] != false {
				t.Fatalf("harvest wait body = %v, want max_threads=5 clean=false", got)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"export_id":"` + exportID + `","status":"pending"}`))
	})
	// exportWaitCalls counts GETs for the wait-export so the first read is "running", the second "done".
	exportWaitCalls := 0
	mux.HandleFunc("GET /api/v1/exports", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "exports.json"))
	})
	mux.HandleFunc("GET /api/v1/exports/{id}", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		if r.PathValue("id") == "wait-export" {
			exportWaitCalls++
			status := "running"
			if exportWaitCalls >= 2 {
				status = "done"
			}
			_, _ = fmt.Fprintf(w, `{"id":"wait-export","kind":"harvest","status":%q,"mailbox_id":"wait","thread_count":3,"truncated":false,"created_at":"2026-07-06T10:00:00Z"}`, status)
			return
		}
		_, _ = w.Write(fixture(t, "export_item.json"))
	})
	mux.HandleFunc("GET /api/v1/exports/{id}/download", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		if r.PathValue("id") == "missing" {
			w.WriteHeader(http.StatusNotFound)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"error":{"code":"BODY_UNAVAILABLE","message":"export body not ready or evicted"}}`))
			return
		}
		if got := r.Header.Get("Accept"); got != "text/markdown" {
			t.Fatalf("download Accept = %q, want text/markdown", got)
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write(fixture(t, "harvest_corpus.md"))
	})

	// legacy routing table (rc mailbox route): list + create (upsert). Create asserts the email arrives.
	mux.HandleFunc("GET /api/v1/mailboxes", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "mailboxes.json"))
	})
	mux.HandleFunc("POST /api/v1/mailboxes", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		if !strings.Contains(body, `"mailbox":"support@acme.test"`) {
			t.Fatalf("mailbox create body missing mailbox: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "mailbox_item.json"))
	})

	// env per-key writes (grounding default; action plane). set asserts the value rode in the BODY
	// (never argv) and is never echoed; reveal returns {secret}; rm is a 204.
	mux.HandleFunc("POST /api/v1/env_grounding", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		var got map[string]any
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatalf("decode env set body: %v\n%s", err, body)
		}
		if got["key"] != "STRIPE_KEY" || got["value"] != "sk_live_FROM_STDIN" {
			t.Fatalf("env set body = %v, want key=STRIPE_KEY value=sk_live_FROM_STDIN", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key":"STRIPE_KEY"}`))
	})
	mux.HandleFunc("DELETE /api/v1/env_grounding/{key}", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/v1/env_grounding/{key}/reveal", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"secret":"sk_live_ENV_REVEALED"}`))
	})
	mux.HandleFunc("POST /api/v1/env_action", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		_ = readBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key":"PODIO_TOKEN"}`))
	})

	// databases: list / get / set + nested controls (GET/PATCH).
	mux.HandleFunc("GET /api/v1/databases", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "databases.json"))
	})
	mux.HandleFunc("GET /api/v1/databases/{dsn}", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		if r.PathValue("dsn") == "controls" {
			// guard: "controls" is a sub-path, never an id — this branch should never be hit by the tests.
			t.Fatalf("database get hit with dsn=controls (path collision)")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "database_item.json"))
	})
	mux.HandleFunc("PATCH /api/v1/databases/{dsn}", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		if !strings.Contains(body, `"description":"Primary OLTP"`) {
			t.Fatalf("database set body missing field: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "database_item.json"))
	})
	mux.HandleFunc("GET /api/v1/databases/{dsn}/controls", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "database_controls.json"))
	})
	mux.HandleFunc("PATCH /api/v1/databases/{dsn}/controls", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		var got map[string]any
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatalf("decode controls patch: %v\n%s", err, body)
		}
		if got["pii_masked"] != true {
			t.Fatalf("controls patch pii_masked = %v, want true (JSON bool from JSON arg)\nbody: %s", got["pii_masked"], body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "database_controls.json"))
	})

	// openrouter-key: PUT (asserts key in body, not URL) / DELETE / reveal.
	mux.HandleFunc("PUT /api/v1/settings/openrouter-key", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		if !strings.Contains(body, `"key":"sk-or-FROM_STDIN"`) {
			t.Fatalf("openrouter-key PUT body missing key: %s", body)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /api/v1/settings/openrouter-key", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/v1/settings/openrouter-key/reveal", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"secret":"sk-or-REVEALED_ONCE"}`))
	})

	// branding logo: PUT multipart (asserts the multipart file part + content type) / DELETE.
	mux.HandleFunc("PUT /api/v1/branding/logo", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "multipart/form-data") {
			t.Fatalf("logo PUT content-type = %q, want multipart/form-data", ct)
		}
		f, hdr, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("logo PUT missing file part: %v", err)
		}
		defer func() { _ = f.Close() }()
		if hdr.Filename != "logo.png" {
			t.Fatalf("logo filename = %q, want logo.png", hdr.Filename)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"logo":"stored","content_type":"image/png"}`))
	})
	mux.HandleFunc("DELETE /api/v1/branding/logo", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.WriteHeader(http.StatusNoContent)
	})

	// github status.
	mux.HandleFunc("GET /api/v1/github/status", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "github_status.json"))
	})

	// brain edit / consolidate — both return {queued, job_id}.
	mux.HandleFunc("GET /api/v1/brain/status", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "brain_status.json"))
	})
	mux.HandleFunc("POST /api/v1/brain/sync", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "brain_sync.json"))
	})
	mux.HandleFunc("POST /api/v1/brain/edit", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		if !strings.Contains(body, `"instruction":"add a runbook for refunds"`) {
			t.Fatalf("brain edit body missing instruction: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"queued":true,"job_id":"job_edit_001"}`))
	})
	mux.HandleFunc("POST /api/v1/brain/consolidate", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"queued":true,"job_id":"job_consolidate_001"}`))
	})

	// dream evidence: query-scoped public consolidation corpus.
	mux.HandleFunc("GET /api/v1/dream/evidence", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		if got := r.URL.Query().Get("limit"); got != "7" {
			t.Fatalf("dream evidence limit = %q, want 7", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"project":"acme","feedback":[{"run_id":"run1","score":2,"comment":"missed policy"}],"deltas":[{"id":"delta1","related_run_id":"run2","similarity":0.41,"changed_chars":120}]}`))
	})

	// triage policy/rules.
	mux.HandleFunc("GET /api/v1/triage/policy", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"scope":{"level":"project"},"guidance":"Draft customer questions only"}`))
	})
	mux.HandleFunc("PATCH /api/v1/triage/policy", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		if !strings.Contains(body, `"guidance":"Only answer support requests"`) {
			t.Fatalf("triage policy body = %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"scope":{"level":"project"},"guidance":"Only answer support requests"}`))
	})
	mux.HandleFunc("GET /api/v1/triage/rules", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rules":[{"id":"rule1","effect":"skip","match_kind":"subject_contains","pattern":"newsletter","enabled":true}]}`))
	})
	mux.HandleFunc("POST /api/v1/triage/rules", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		if !strings.Contains(body, `"effect":"skip"`) || !strings.Contains(body, `"priority":10`) || !strings.Contains(body, `"enabled":false`) {
			t.Fatalf("triage rule create body = %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"rule2","effect":"skip","match_kind":"subject_contains","pattern":"newsletter","priority":10,"enabled":false}`))
	})
	mux.HandleFunc("PATCH /api/v1/triage/rules/{id}", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		if r.PathValue("id") != "rule2" || !strings.Contains(body, `"enabled":true`) {
			t.Fatalf("triage rule patch id/body = %s/%s", r.PathValue("id"), body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"rule2","enabled":true}`))
	})
	mux.HandleFunc("DELETE /api/v1/triage/rules/{id}", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		if r.PathValue("id") != "rule2" {
			t.Fatalf("triage delete id = %q, want rule2", r.PathValue("id"))
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// run feedback / retry.
	mux.HandleFunc("POST /api/v1/runs/{id}/feedback", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		var got map[string]any
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatalf("decode feedback body: %v\n%s", err, body)
		}
		if got["score"] != float64(1) || got["comment"] != "great draft" {
			t.Fatalf("feedback body = %v, want score=1 comment=great draft", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"recorded":true}`))
	})
	mux.HandleFunc("POST /api/v1/runs/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		if !strings.Contains(body, `"tier":"pro"`) {
			t.Fatalf("retry body missing tier: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"run_id":"99999999-9999-9999-9999-999999999999","status":"queued"}`))
	})

	// admin: users / projects / catalog.
	mux.HandleFunc("GET /api/v1/admin/users", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "admin_users.json"))
	})
	mux.HandleFunc("POST /api/v1/admin/users", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		if !strings.Contains(body, `"email":"dana@acme.test"`) {
			t.Fatalf("admin user add body missing email: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"usr_dana","email":"dana@acme.test","admin":true}`))
	})
	mux.HandleFunc("PATCH /api/v1/admin/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		_ = readBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + r.PathValue("id") + `","admin":false}`))
	})
	mux.HandleFunc("GET /api/v1/admin/projects", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "admin_projects.json"))
	})
	mux.HandleFunc("POST /api/v1/admin/projects", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		if !strings.Contains(body, `"name":"momentum-tools"`) {
			t.Fatalf("admin project add body missing name: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"proj_new","name":"momentum-tools","webhook_secret":"whsec_SHOWN_ONCE"}`))
	})
	mux.HandleFunc("GET /api/v1/admin/catalog", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "admin_catalog.json"))
	})
	mux.HandleFunc("POST /api/v1/admin/catalog", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		body := readBody(t, r)
		if !strings.Contains(body, `"key":"podio"`) {
			t.Fatalf("admin catalog upsert body missing key: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key":"podio","kind":"api_key","status":"active"}`))
	})
}

// newTestEnv builds an env wired to the stub server with a fixed output mode, capturing stdout/stderr.
// It sets a static bearer (tokenOvr) so newClient resolves auth without the OAuth token store, and
// isolates XDG_CONFIG_HOME so a real ~/.config/rootcause is never read or written.
func newTestEnv(t *testing.T, srv *httptest.Server, output string) (*env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // isolate the token store + config
	t.Setenv("ROOTCAUSE_BASE_URL", "")       // a stray env must not override the test base URL
	var out, errb bytes.Buffer
	e := &env{
		profile:    "default",
		output:     output,
		baseURLOvr: srv.URL,
		tokenOvr:   "test-key",
		out:        &out,
		err:        &errb,
	}
	return e, &out, &errb
}

// run executes a command line against a fresh root built on the test env, returning the captured
// stdout, stderr, and the error from Execute (so the error-path test can assert non-nil).
func run(t *testing.T, e *env, args ...string) error {
	t.Helper()
	// Cobra resets the --output-bound field to its flag default during parsing, so force the mode via
	// an explicit -o arg (mirroring how a user would) rather than presetting e.output.
	if e.output != "" {
		args = append([]string{"-o", e.output}, args...)
	}
	root := newRootCmd(e, "0.1.0-test")
	root.SetArgs(args)
	root.SetOut(e.out)
	root.SetErr(e.err)
	return root.Execute()
}

func requireAuth(t *testing.T, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("missing/wrong auth header: %q", got)
	}
}

// reasonOrEmpty reads the optional "reason" from a decoded spam-create body ("" when absent) so the
// stub echo mirrors what the CLI sent.
func reasonOrEmpty(body map[string]any) string {
	if s, ok := body["reason"].(string); ok {
		return s
	}
	return ""
}

func readBody(t *testing.T, r *http.Request) string {
	t.Helper()
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(r.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	return buf.String()
}

// testdataDir is the absolute path to the fixtures dir, resolved once at package init so a test that
// os.Chdir's (e.g. the .rootcause default-split-dir test) doesn't break a stub handler's fixture read.
var testdataDir = func() string {
	abs, err := filepath.Abs("testdata")
	if err != nil {
		panic(err)
	}
	return abs
}()

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testdataDir, name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// assertGolden compares got against testdata/<name>, writing it when -update is set. Goldens are
// stable: fixtures use canned timestamps, never time.Now.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join(testdataDir, name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden %s: %v", name, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	if got != string(want) {
		t.Errorf("output mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

// assertJSONEqual checks that two JSON byte slices decode to the same value — the passthrough
// contract: -o json must round-trip the server's body (re-indentation aside), never reshape it.
func assertJSONEqual(t *testing.T, want, got []byte) {
	t.Helper()
	var wv, gv any
	if err := json.Unmarshal(want, &wv); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	if err := json.Unmarshal(got, &gv); err != nil {
		t.Fatalf("unmarshal got: %v\nraw: %s", err, got)
	}
	if !reflect.DeepEqual(wv, gv) {
		t.Errorf("JSON not equal\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
