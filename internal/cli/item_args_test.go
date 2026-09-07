package cli

import (
	"reflect"
	"testing"
)

func TestParseItemArgsCoercesBoolLiterals(t *testing.T) {
	t.Parallel()
	got, err := parseItemArgs([]string{"email=a@b.c", "admin=true", "pr_enabled=false", "note=True", "v=1"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"email": "a@b.c", "admin": true, "pr_enabled": false, "note": "True", "v": "1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseItemArgs = %#v; want %#v", got, want)
	}
	if _, err := parseItemArgs([]string{"novalue"}); err == nil {
		t.Fatal("expected error for arg without '='")
	}
}

func TestParseRawItemArgsKeepsBoolText(t *testing.T) {
	t.Parallel()
	got, err := parseRawItemArgs([]string{"key=FLAG", "value=true"})
	if err != nil {
		t.Fatal(err)
	}
	if got["value"] != "true" {
		t.Fatalf("value = %#v; want the string \"true\"", got["value"])
	}
}
