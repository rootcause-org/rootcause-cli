package render

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

type wrappedWriter struct{ io.Writer }

func (w wrappedWriter) UnwrapWriter() io.Writer { return w.Writer }

func TestAutoModePreservedThroughWriterDecorator(t *testing.T) {
	f, err := os.OpenFile("/dev/null", os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if IsJSON(ModeAuto, f) != IsJSON(ModeAuto, wrappedWriter{Writer: f}) {
		t.Fatal("writer decorator changed auto output mode")
	}
}

// A server still emitting spend/token metadata must not leak it through the freeform passthrough.
func TestMetadataPassthroughDropsSpendKeys(t *testing.T) {
	md := map[string]any{
		"total_cost_usd": 1.23, "cost_usd": 0.4, "tokens": 900, "peak_context_tokens": 50000,
		"outcome": "answered", "run_url": "https://x", "channel": "email",
	}
	got := sortedMetadataKeys(md)
	if len(got) != 1 || got[0] != "channel" {
		t.Fatalf("metadata keys = %v, want only [channel]", got)
	}
}

// TestConsolePrincipalHeader pins the bound-principal header on the three console views. The header is
// the only place an agent learns that an empty result may mean "hidden from this identity" rather than
// "no such data", and it must stay silent on an unbound call (or a host that never echoed a principal).
func TestConsolePrincipalHeader(t *testing.T) {
	principal := &client.Principal{Kind: "kampadmin_admin", ExternalID: "11111111-2222-3333-4444-555555555555"}
	hidden := []string{"public.payouts", "public.users"}

	var query bytes.Buffer
	DBQuery(&query, &client.DBQueryResponse{
		RunID: "abcdef12", Columns: []string{"id"}, Rows: [][]any{{"1"}}, RowCount: 1,
		Principal: principal, HiddenTables: hidden,
	})
	var schema bytes.Buffer
	DBSchema(&schema, &client.DBSchemaResponse{
		DB: "prod", Tables: []client.DBSchemaTable{{Schema: "public", Name: "invoices"}},
		Principal: principal, HiddenTables: hidden,
	})
	var bash bytes.Buffer
	BashRun(&bash, &client.BashRunResponse{RunID: "abcdef12", Stdout: "ok\n", Principal: principal}, nil)

	for name, got := range map[string]string{"query": query.String(), "schema": schema.String(), "bash": bash.String()} {
		if !strings.Contains(got, "Principal: kampadmin_admin=11111111-2222-3333-4444-555555555555") {
			t.Errorf("%s view missing principal header:\n%s", name, got)
		}
	}
	for name, got := range map[string]string{"query": query.String(), "schema": schema.String()} {
		if !strings.Contains(got, "Hidden:    public.payouts, public.users") {
			t.Errorf("%s view missing hidden tables:\n%s", name, got)
		}
	}

	var unbound bytes.Buffer
	DBQuery(&unbound, &client.DBQueryResponse{RunID: "abcdef12", Columns: []string{"id"}, Rows: [][]any{{"1"}}, RowCount: 1})
	if strings.Contains(unbound.String(), "Principal") || strings.Contains(unbound.String(), "Hidden") {
		t.Errorf("unbound query view leaked a principal header:\n%s", unbound.String())
	}
}
