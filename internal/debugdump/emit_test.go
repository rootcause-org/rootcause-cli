package debugdump

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

func TestHTMLFallbackDraftRendersInDebugDump(t *testing.T) {
	full := &client.FullResponse{
		Run: client.RunHeader{
			RunID:   "c446011c-7e78-4a41-8848-46d92b61152a",
			Project: "pj-mailbox",
			Status:  "done",
			Kind:    "email",
			Draft:   "<p>Visible HTML draft</p>",
		},
	}

	index := RenderIndex(full)
	if strings.Contains(index, "**Draft:** none") {
		t.Fatalf("index reported no draft:\n%s", index)
	}
	if !strings.Contains(index, "<p>Visible HTML draft</p>") {
		t.Fatalf("index missing HTML draft:\n%s", index)
	}

	var buf bytes.Buffer
	if err := EmitJSONL(&buf, full); err != nil {
		t.Fatalf("EmitJSONL: %v", err)
	}
	var header struct {
		Draft string `json:"draft"`
	}
	if err := json.Unmarshal(bytes.SplitN(buf.Bytes(), []byte("\n"), 2)[0], &header); err != nil {
		t.Fatalf("decode JSONL header: %v", err)
	}
	if header.Draft != "<p>Visible HTML draft</p>" {
		t.Fatalf("jsonl draft = %q, want HTML draft", header.Draft)
	}
}

func TestReplyAttachmentsRenderInDebugDump(t *testing.T) {
	full := &client.FullResponse{
		Run: client.RunHeader{RunID: "7ca34e0f-8a1e-418e-8818-c59543c735ff", Project: "kampadmin", Status: "done", Kind: "email"},
		Events: []client.EventItem{{
			Seq: 1, Tool: "reply", Status: "ok", Args: json.RawMessage(`{"has_draft":true,"attachments":[{"path":"/tmp/outbox/report.pdf","filename":"report.pdf","size_bytes":439296,"mime_type":"application/pdf","status":"shipped"},{"path":"/tmp/outbox/unsafe.bin","filename":"unsafe.bin","size_bytes":12,"status":"dropped","drop_reason":"sniff_rejected"}]}`),
		}},
	}

	index := RenderIndex(full)
	for _, want := range []string{"**Attachments:** 2 declared · 1 shipped · 1 dropped", "`report.pdf` · 439296 bytes · `application/pdf` · `/tmp/outbox/report.pdf` · shipped", "unsafe.bin", "dropped: sniff_rejected"} {
		if !strings.Contains(index, want) {
			t.Fatalf("index missing %q:\n%s", want, index)
		}
	}

	full.Events[0].Args = json.RawMessage(`{"has_draft":true}`)
	if index := RenderIndex(full); !strings.Contains(index, "**Attachments:** 0 declared · 0 shipped · 0 dropped") {
		t.Fatalf("zero attachments not visible:\n%s", index)
	}
}

func TestReplyLinksRenderInDebugDump(t *testing.T) {
	full := &client.FullResponse{
		Run: client.RunHeader{RunID: "7ca34e0f-8a1e-418e-8818-c59543c735ff", Project: "kampadmin", Status: "done", Kind: "email"},
		Events: []client.EventItem{{
			Seq: 1, Tool: "reply", Status: "ok", Args: json.RawMessage(`{"has_draft":true,"links":[{"url":"https://ok.example/login","status":403,"ms":12,"verdict":"pass","validator":"liveness"},{"url":"https://bad.example/x","status":404,"ms":21,"verdict":"fail","validator":"liveness"},{"url":"https://skip.example","status":0,"ms":0,"verdict":"skipped_allowlist","validator":"liveness"}]}`),
		}},
	}

	index := RenderIndex(full)
	for _, want := range []string{
		"**Links:** 2 checked · 1 passed · 1 removed · 1 untouched",
		"`https://ok.example/login` · pass · HTTP 403 · 12 ms",
		"`https://bad.example/x` · fail · HTTP 404 · 21 ms",
		"`https://skip.example` · skipped_allowlist · no HTTP status · 0 ms",
	} {
		if !strings.Contains(index, want) {
			t.Fatalf("index missing %q:\n%s", want, index)
		}
	}
}

func TestScopeSummaryRendersSplitKBCounts(t *testing.T) {
	summary := scopeSummary(map[string]any{
		"mode":            "tenant",
		"tenant":          "yes_events",
		"project_total":   220,
		"project_visible": 206,
		"project_hidden":  14,
		"tenant_total":    8,
		"total_visible":   214,
		"hidden":          14,
		"scoped":          true,
	})
	want := "mode=tenant tenant=yes_events project_total=220 project_visible=206 project_hidden=14 tenant_total=8 total_visible=214 hidden=14 scoped=true"
	if summary != want {
		t.Fatalf("scopeSummary() = %q, want %q", summary, want)
	}
}

// TestFilesReadCoversAllMountsAndClauses locks the two production command shapes that used to lose
// paths: a `for f in …; do … done` loop and a `;`-chained cat/echo/cat. Every mount the sandbox exposes
// must be recognized — a missing mount renders an index that lies about what the run opened.
func TestFilesReadCoversAllMountsAndClauses(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    []string
	}{
		{
			name:    "for-loop over tenant and brain files",
			command: `for f in /tenant/skills/cases/betaling-annulatie.md /brain/skills/cases/financial-discrepancy.md; do echo "=== $f"; sed -n '1,180p' "$f"; done`,
			want: []string{
				"/brain/skills/cases/financial-discrepancy.md",
				"/tenant/skills/cases/betaling-annulatie.md",
			},
		},
		{
			name:    "semicolon-chained cat clauses",
			command: `cat /tenant/triage.md 2>/dev/null | head -40; echo "---"; cat /tenant/skills/cases/betaling-annulatie.md 2>/dev/null | head -60`,
			want: []string{
				"/tenant/skills/cases/betaling-annulatie.md",
				"/tenant/triage.md",
			},
		},
		{
			name:    "&&-chained reads across every mount",
			command: `cat /brain/a.md && cat /tenant/b.md && cat /kb/tenant/intercom/c.md && cat /mirrors/app/d.py && cat /tmp/rc-context/e.json`,
			want: []string{
				"/brain/a.md",
				"/kb/tenant/intercom/c.md",
				"/mirrors/app/d.py",
				"/tenant/b.md",
				"/tmp/rc-context/e.json",
			},
		},
		{
			name:    "glued paths and directories are not files",
			command: `cat foo/tenant/not-a-mount.md; ls /tenant/skills`,
			want:    nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filesRead(decorate([]client.EventItem{{Tool: "bash", Command: tc.command}}))
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Fatalf("filesRead() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestNotesRenderFullBody: the index must show the whole note body — the 👀/📊 reviewer lines live past
// the first sentence, and hiding them cost two reviewer round-trips in the 2026-08-12 audit.
func TestNotesRenderFullBody(t *testing.T) {
	body := "Antwoord verzonden. Tweede zin met detail.\n👀 reviewer: check the refund window\n📊 confidence 0.62"
	full := &client.FullResponse{
		Run: client.RunHeader{
			RunID:   "c446011c-7e78-4a41-8848-46d92b61152a",
			Project: "kampadmin",
			Status:  "done",
			Kind:    "email",
			Notes:   []client.Note{{Key: "summary", Body: body}},
		},
	}
	index := RenderIndex(full)
	for _, want := range []string{"👀 reviewer: check the refund window", "📊 confidence 0.62", "Tweede zin met detail."} {
		if !strings.Contains(index, want) {
			t.Fatalf("index dropped note line %q:\n%s", want, index)
		}
	}
}

// TestRedactedIndexLeadsWithWithheld: a bundle served without detail (non-project-admin) must announce
// that in the first lines and drop the sections that would otherwise render as "nothing happened".
func TestRedactedIndexLeadsWithWithheld(t *testing.T) {
	full := &client.FullResponse{
		Run: client.RunHeader{
			RunID:          "c446011c-7e78-4a41-8848-46d92b61152a",
			Project:        "pj-mailbox",
			Status:         "done",
			Kind:           "email",
			DetailRedacted: true,
		},
	}

	index := RenderIndex(full)
	lead := strings.Join(strings.SplitN(index, "\n", 4)[:3], "\n")
	if !strings.Contains(lead, "detail withheld (project-admin required)") {
		t.Fatalf("withheld notice not in the lead block:\n%s", index)
	}
	for _, section := range []string{"## Timeline", "## Flags"} {
		if strings.Contains(index, section) {
			t.Fatalf("index kept %s on a redacted bundle:\n%s", section, index)
		}
	}

	var buf bytes.Buffer
	if err := EmitJSONL(&buf, full); err != nil {
		t.Fatalf("EmitJSONL: %v", err)
	}
	var header map[string]any
	if err := json.Unmarshal(bytes.SplitN(buf.Bytes(), []byte("\n"), 2)[0], &header); err != nil {
		t.Fatalf("decode JSONL header: %v", err)
	}
	if header["detail_redacted"] != true {
		t.Fatalf("jsonl header detail_redacted = %v, want true", header["detail_redacted"])
	}
}

// A run older than the server's context capture (or past its 7-day window) must SAY the context is
// gone. Silence would read as "the model was handed nothing" — a different, false fact.
func TestPromptContextAbsenceIsStated(t *testing.T) {
	full := &client.FullResponse{
		Run: client.RunHeader{
			RunID:        "c446011c-7e78-4a41-8848-46d92b61152a",
			Project:      "pj-mailbox",
			Status:       "done",
			Kind:         "email",
			SystemPrompt: "You are rootcause.",
		},
	}

	index := RenderIndex(full)
	if !strings.Contains(index, "## Prompt context") || !strings.Contains(index, "**Not captured**") {
		t.Fatalf("absent prompt context not called out:\n%s", index)
	}
	for _, section := range []string{"### System-prompt sections", "### Bootstrap blocks", "## Draft cleanup"} {
		if strings.Contains(index, section) {
			t.Fatalf("index drew empty section %s:\n%s", section, index)
		}
	}

	var buf bytes.Buffer
	if err := EmitJSONL(&buf, full); err != nil {
		t.Fatalf("EmitJSONL: %v", err)
	}
	var header map[string]any
	if err := json.Unmarshal(bytes.SplitN(buf.Bytes(), []byte("\n"), 2)[0], &header); err != nil {
		t.Fatalf("decode JSONL header: %v", err)
	}
	// The zero version must be PRESENT: it is the machine-readable absence signal a jq consumer tests.
	if v, ok := header["context_schema_version"]; !ok || v != float64(0) {
		t.Fatalf("context_schema_version = %v (present=%v), want 0", v, ok)
	}
	for _, k := range []string{"prompt_sections", "manifest_blocks", "bootstrap_turn", "preselected_turn"} {
		if _, ok := header[k]; ok {
			t.Fatalf("header carried %q on an uncaptured run: %v", k, header[k])
		}
	}
}

// Polish rows live in their own band: they must not be counted as agent steps, flagged as failed turns
// (their status is "draft-cleanup", never "ok"), or interleaved into the timeline.
func TestDraftCleanupRendersInItsOwnSection(t *testing.T) {
	full := &client.FullResponse{
		Run: client.RunHeader{
			RunID: "c446011c-7e78-4a41-8848-46d92b61152a", Project: "pj-mailbox", Status: "done", Kind: "email",
			Draft: "You have 2 open invoices, totalling $480.",
		},
		Events: []client.EventItem{
			{Seq: 1, Tool: "bash", Status: "ok", Command: "psql -c 'select 1'"},
			{Seq: 4_000_000, Tool: "host", Status: "draft-cleanup", Args: json.RawMessage(
				`{"pass":"em_dash","status":"rewritten","called":true,"changed":true,"before":"a — b","after":"a, b"}`)},
		},
	}

	index := RenderIndex(full)
	if !strings.Contains(index, "## Draft cleanup") || !strings.Contains(index, "| C1 | em_dash | rewritten | yes |") {
		t.Fatalf("draft cleanup pass not rendered:\n%s", index)
	}
	if !strings.Contains(index, "**Steps:** 1 main") {
		t.Fatalf("polish row counted as an agent step:\n%s", index)
	}
	if strings.Contains(index, "[C1] draft-cleanup") {
		t.Fatalf("polish row flagged as a failed turn:\n%s", index)
	}
}
