package idutil

import "testing"

func TestClassifyGmail(t *testing.T) {
	const hex = "19e875318442de06"
	const dec = uint64(1866870901077892614)

	cases := []struct {
		name    string
		token   string
		kind    string
		hex     string
		decimal uint64
	}{
		{"hex", hex, "hex", hex, dec},
		{"decimal", "1866870901077892614", "decimal", hex, dec},
		{"legacy thread-f", "thread-f:1866870901077892614", "legacy-f", hex, dec},
		{"legacy msg-f", "msg-f:1866870901077892614", "legacy-f", hex, dec},
		{"web url opaque", "https://mail.google.com/mail/u/2/#all/FMfcgzQgMCgKhphcthGsbxmQxBCTXKMQ", "web-opaque", "", 0},
		{"draft local r", "r4424735772614196583", "local", "", 0},
		{"all-mail local a", "msg-a:r-123456789", "local", "", 0},
		{"unknown", "not-an-id!!", "unknown", "", 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := ClassifyGmail(tc.token)
			if r.Kind != tc.kind {
				t.Errorf("kind = %q, want %q", r.Kind, tc.kind)
			}
			if r.Hex != tc.hex {
				t.Errorf("hex = %q, want %q", r.Hex, tc.hex)
			}
			if r.Decimal != tc.decimal {
				t.Errorf("decimal = %d, want %d", r.Decimal, tc.decimal)
			}
			if tc.hex != "" {
				wantTF := "thread-f:1866870901077892614"
				if r.ThreadF != wantTF {
					t.Errorf("thread_f = %q, want %q", r.ThreadF, wantTF)
				}
				wantURL := "https://mail.google.com/mail/u/0/#all/" + tc.hex
				if r.WebURL != wantURL {
					t.Errorf("web_url = %q, want %q", r.WebURL, wantURL)
				}
			}
		})
	}
}

// hex → decimal → hex must round-trip exactly.
func TestGmailRoundTrip(t *testing.T) {
	const hex = "19e875318442de06"
	fromHex := ClassifyGmail(hex)
	fromDec := ClassifyGmail("1866870901077892614")
	if fromHex.Hex != hex || fromDec.Hex != hex {
		t.Fatalf("round-trip mismatch: hex=%q dec=%q", fromHex.Hex, fromDec.Hex)
	}
	if fromHex.Decimal != fromDec.Decimal {
		t.Fatalf("decimal mismatch: %d vs %d", fromHex.Decimal, fromDec.Decimal)
	}
}

func TestGmailWebURLUser(t *testing.T) {
	r := ClassifyGmailForUser("19e875318442de06", "2")
	want := "https://mail.google.com/mail/u/2/#all/19e875318442de06"
	if r.WebURL != want {
		t.Errorf("web_url = %q, want %q", r.WebURL, want)
	}
}

// web-opaque must report a decoded byte count, never a hex value.
func TestGmailWebOpaqueNote(t *testing.T) {
	r := ClassifyGmail("FMfcgzQgMCgKhphcthGsbxmQxBCTXKMQ")
	if r.Kind != "web-opaque" {
		t.Fatalf("kind = %q, want web-opaque", r.Kind)
	}
	if r.Hex != "" {
		t.Errorf("hex should be empty for web-opaque, got %q", r.Hex)
	}
	if r.Note == "" {
		t.Errorf("expected a byte-count note")
	}
}
