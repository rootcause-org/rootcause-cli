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
