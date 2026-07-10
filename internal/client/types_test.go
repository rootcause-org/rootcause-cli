package client

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPagedRowTypesPreserveUnknownJSONFields(t *testing.T) {
	for name, target := range map[string]any{
		"run":    &RunSummary{},
		"event":  &RunEvent{},
		"egress": &EgressRow{},
	} {
		t.Run(name, func(t *testing.T) {
			input := []byte(`{"run_id":"r-1","future_field":{"kept":true}}`)
			if err := json.Unmarshal(input, target); err != nil {
				t.Fatal(err)
			}
			got, err := json.Marshal(target)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(got), `"future_field":{"kept":true}`) {
				t.Fatalf("unknown field dropped: %s", got)
			}
		})
	}
}
