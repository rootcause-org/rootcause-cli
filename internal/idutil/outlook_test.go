package idutil

import (
	"strings"
	"testing"
)

func TestClassifyOutlook(t *testing.T) {
	// Graph REST message id: starts AAMk, >=100 chars.
	graphMsg := "AAMk" + strings.Repeat("Ab1", 40) // 124 chars
	// conversationId: starts AAQk, >=16 chars, valid base64.
	convID := "AAQk" + strings.Repeat("Cd2", 8) // 28 chars
	// OWA/EWS id: 0x01 0x04 prefix + filler + 16-byte trailing GUID, base64.
	owaID := "AQRBQkNERUZHSAABAgMEBQYHCAkKCwwNDg8="
	wantGUID := "03020100-0504-0706-0809-0a0b0c0d0e0f"

	cases := []struct {
		name   string
		token  string
		kind   string
		column string
		value  string
		guid   string
	}{
		{"graph message", graphMsg, "graph-message", "messages.external_message_id", graphMsg, ""},
		{"conversation", convID, "conversation", "threads.external_thread_id", convID, ""},
		{"owa web id", owaID, "owa-web", "", "", wantGUID},
		{"unknown short", "xyz", "unknown", "", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := ClassifyOutlook(tc.token)
			if r.Kind != tc.kind {
				t.Errorf("kind = %q, want %q", r.Kind, tc.kind)
			}
			if r.MatchColumn != tc.column {
				t.Errorf("match_column = %q, want %q", r.MatchColumn, tc.column)
			}
			if r.MatchValue != tc.value {
				t.Errorf("match_value = %q, want %q", r.MatchValue, tc.value)
			}
			if r.EmbeddedGUID != tc.guid {
				t.Errorf("embedded_guid = %q, want %q", r.EmbeddedGUID, tc.guid)
			}
		})
	}
}

// An Outlook web URL wrapping a graph id: extract + URL-decode the /id/ segment,
// then classify the inner id.
func TestClassifyOutlookURL(t *testing.T) {
	graphMsg := "AAMk" + strings.Repeat("Ab1", 40)
	urlTok := "https://outlook.office.com/mail/inbox/id/" + graphMsg
	r := ClassifyOutlook(urlTok)
	if r.URLID != graphMsg {
		t.Errorf("url_id = %q, want %q", r.URLID, graphMsg)
	}
	if r.Kind != "graph-message" {
		t.Errorf("kind = %q, want graph-message", r.Kind)
	}
	if r.MatchValue != graphMsg {
		t.Errorf("match_value = %q, want %q", r.MatchValue, graphMsg)
	}
}

// %2B/%2F-encoded OWA id in a URL must be URL-decoded before base64 decode.
func TestClassifyOutlookURLEncoded(t *testing.T) {
	// owaID with + and / percent-encoded.
	owaEnc := "https://outlook.live.com/mail/inbox/id/AQRBQkNERUZHSAABAgMEBQYHCAkKCwwNDg8%3D"
	r := ClassifyOutlook(owaEnc)
	if r.Kind != "owa-web" {
		t.Fatalf("kind = %q, want owa-web", r.Kind)
	}
	if r.EmbeddedGUID != "03020100-0504-0706-0809-0a0b0c0d0e0f" {
		t.Errorf("embedded_guid = %q", r.EmbeddedGUID)
	}
	if r.MatchColumn != "" {
		t.Errorf("owa-web must not be offline-resolvable, got column %q", r.MatchColumn)
	}
}
