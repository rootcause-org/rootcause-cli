package client

import (
	"encoding/json"
	"testing"
)

func TestOutboundEmailJSONShape(t *testing.T) {
	outbound := &OutboundEmail{
		To:      []string{"to@example.test"},
		Cc:      []string{"cc@example.test"},
		Bcc:     []string{"bcc@example.test"},
		Subject: "Follow-up",
	}
	for _, tc := range []struct {
		name    string
		value   any
		present bool
	}{
		{"run detail present", RunDetail{OutboundEmail: outbound}, true},
		{"run detail omitted", RunDetail{}, false},
		{"trace header present", RunHeader{OutboundEmail: outbound}, true},
		{"trace header omitted", RunHeader{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			raw, ok := got["outbound_email"]
			if ok != tc.present {
				t.Fatalf("outbound_email present = %t, want %t: %s", ok, tc.present, encoded)
			}
			if !tc.present {
				return
			}
			var fields map[string]any
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"to", "cc", "bcc", "subject"} {
				if _, ok := fields[key]; !ok {
					t.Errorf("outbound_email missing %q: %s", key, raw)
				}
			}
		})
	}
}
