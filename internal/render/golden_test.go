package render

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// Golden tests for the views the CLI hands a view model. They exist because these renderers used to
// live in internal/cli behind a full Cobra run and had no coverage at all; here they are pure
// functions, so pinning them costs one fixture each. Regenerate with:
//
//	go test ./internal/render -update
var update = flag.Bool("update", false, "rewrite testdata/*.golden")

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
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

func TestDoctorGolden(t *testing.T) {
	report := DoctorReport{
		Binary: DoctorBinary{
			Path: "/opt/homebrew/bin/rc", ResolvedPath: "/opt/homebrew/Caskroom/rc/1.2.3/rc",
			Version: "1.2.3", ModuleVersion: "v1.2.3", Install: "Homebrew",
		},
		Path: []DoctorBinary{
			{Path: "/opt/homebrew/bin/rc", Version: "1.2.3", Install: "Homebrew", Active: true},
			{Path: "/Users/me/go/bin/rc", Version: "1.1.0", Install: "Go install",
				Note: "shadowed by the Homebrew copy", Hint: "rc self update --migrate"},
		},
		Scope: DoctorScope{
			Profile: "default", Project: "acme", ProjectSource: ".rootcause.toml",
			BaseURL: "https://app.replypen.com", BaseURLSource: "built-in production",
		},
		Capabilities: DoctorCapabilities{
			HarvestCorpusFormats: []string{"v1", "v2", "v3"}, ServerHarvestCorpus: "v4", Unsupported: true,
		},
		Update:   DoctorUpdate{Current: "1.2.3", Latest: "v1.2.4", Available: true},
		Findings: []DoctorFinding{{Path: "/Users/me/go/bin/rc", Message: "duplicate rc on PATH", Hint: "remove it"}},
	}
	var out bytes.Buffer
	if err := Doctor(&out, report); err != nil {
		t.Fatal(err)
	}
	assertGolden(t, "doctor.golden", out.String())
}

func TestUpdateCheckGolden(t *testing.T) {
	var out bytes.Buffer
	UpdateCheck(&out, UpdateStatus{
		Source: "github", RunningPath: "/Users/me/go/bin/rc", InstallKind: "Go install",
		Current: "v1.1.3", Latest: "v1.2.4", UpdateAvailable: true,
		InstallProblem: true, Installations: 2, OfferMigrate: true,
	})
	assertGolden(t, "update_check.golden", out.String())
}

func TestTenantSettingsGolden(t *testing.T) {
	ts := &client.TenantSettings{
		TenantID: "acme", Version: "7", AppliedAt: "2026-01-02T03:04:05Z",
		Settings: json.RawMessage(`{
			"tone": "formal",
			"signature": "Team ACME",
			"reply_window_hours": 24,
			"escalate_to": null,
			"legacy_flag": true
		}`),
	}
	schema := &SettingsSchema{
		Groups: []SettingsGroup{{Key: "persona", Label: "Persona"}, {Key: "channel", Label: "Kanaal"}},
		Fields: map[string]SettingsField{
			"tone":               {Group: "persona", Order: 1, Label: "Toon"},
			"signature":          {Group: "persona", Order: 2},
			"reply_window_hours": {Group: "channel", Order: 1, Label: "Antwoordvenster"},
			"escalate_to":        {Group: "channel", Order: 2, Label: "Escalatie"},
		},
	}
	var out bytes.Buffer
	TenantSettings(&out, ts, schema)
	// legacy_flag has no schema entry: it must still show up under "Other", never be hidden.
	assertGolden(t, "tenant_settings.golden", out.String())

	out.Reset()
	TenantSettings(&out, ts, nil)
	assertGolden(t, "tenant_settings_no_schema.golden", out.String())
}

func TestHierarchySettingsGolden(t *testing.T) {
	hs := &client.HierarchySettings{
		Scope: "tenant", Project: "acme", Tenant: "north",
		Settings: json.RawMessage(`{"persona":{"tone":"formal"}}`),
		Resolved: json.RawMessage(`{
			"persona":{"tone":{"value":"formal","source":"tenant"},"signature":{"value":"Team ACME","source":"project"}},
			"channel":{"reply_window_hours":{"value":24}}
		}`),
	}
	var out bytes.Buffer
	HierarchySettings(&out, hs)
	assertGolden(t, "hierarchy_settings.golden", out.String())
}

func TestKBSearchGolden(t *testing.T) {
	view := KBSearchView{
		ArticlesMatched: 2, Hits: 3, ArtifactDir: ".rootcause/kb/refunds", Truncated: true,
		Articles: []KBArticle{
			{Title: "Refund policy", URL: "https://help.example/refunds", Path: "billing/refund-policy.md",
				Hits: []KBHit{{Line: 12, Snippet: "refunds are issued within 14 days"}, {Line: 40, Snippet: "partial refunds"}}},
			{Title: "Chargebacks", Path: "billing/chargebacks.md", Hits: []KBHit{{Line: 3, Snippet: "a chargeback is not a refund"}}},
		},
	}
	var out bytes.Buffer
	KBSearch(&out, view)
	assertGolden(t, "kb_search.golden", out.String())
}
