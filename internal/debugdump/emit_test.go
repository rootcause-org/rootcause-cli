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
