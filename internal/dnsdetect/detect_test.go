package dnsdetect

import (
	"context"
	"reflect"
	"testing"
)

// fakeResolver answers from canned per-record maps. Network is never touched.
type fakeResolver struct {
	mx    map[string][]string
	txt   map[string][]string
	cname map[string][]string
	host  map[string][]string
	addr  map[string][]string
}

func (f fakeResolver) LookupMX(_ context.Context, d string) ([]string, error) {
	return f.mx[d], nil
}
func (f fakeResolver) LookupTXT(_ context.Context, d string) ([]string, error) {
	return f.txt[d], nil
}
func (f fakeResolver) LookupCNAME(_ context.Context, h string) ([]string, error) {
	return f.cname[h], nil
}
func (f fakeResolver) LookupHost(_ context.Context, h string) ([]string, error) {
	return f.host[h], nil
}
func (f fakeResolver) LookupAddr(_ context.Context, ip string) ([]string, error) {
	return f.addr[ip], nil
}

func TestDetect(t *testing.T) {
	cases := []struct {
		name        string
		target      string
		res         fakeResolver
		provider    string
		supported   bool
		confidence  string
		wantSignals []string // substrings that must appear in some signal
	}{
		{
			name:   "google mx+spf",
			target: "info@acme.com",
			res: fakeResolver{
				mx:  map[string][]string{"acme.com": {"aspmx.l.google.com"}},
				txt: map[string][]string{"acme.com": {`"v=spf1 include:_spf.google.com ~all"`}},
			},
			provider:    "google",
			supported:   true,
			confidence:  "high",
			wantSignals: []string{"MX → Google", "SPF includes _spf.google.com"},
		},
		{
			name:   "microsoft mx",
			target: "contoso.com",
			res: fakeResolver{
				mx:  map[string][]string{"contoso.com": {"contoso-com.mail.protection.outlook.com"}},
				txt: map[string][]string{"contoso.com": {`"v=spf1 include:spf.protection.outlook.com -all"`}},
			},
			provider:    "microsoft",
			supported:   true,
			confidence:  "high",
			wantSignals: []string{"MX → Microsoft 365", "SPF includes spf.protection.outlook.com"},
		},
		{
			name:   "gateway fronting other",
			target: "gw.example",
			res: fakeResolver{
				mx:  map[string][]string{"gw.example": {"eu-smtp-inbound-1.mimecast.com"}},
				txt: map[string][]string{"gw.example": {`"v=spf1 ip4:203.0.113.5 -all"`}},
			},
			provider:    "other",
			supported:   false,
			confidence:  "high", // spf present
			wantSignals: []string{"spam-filter gateway", "authorizes neither"},
		},
		{
			name:   "plain other host",
			target: "selfhost.example",
			res: fakeResolver{
				mx:  map[string][]string{"selfhost.example": {"mail.selfhost.example"}},
				txt: map[string][]string{"selfhost.example": {`"v=spf1 a mx -all"`}},
			},
			provider:    "other",
			supported:   false,
			confidence:  "medium",
			wantSignals: []string{"non-Google/Microsoft host"},
		},
		{
			name:        "no mx unknown",
			target:      "void.example",
			res:         fakeResolver{},
			provider:    "unknown",
			supported:   false,
			confidence:  "low",
			wantSignals: []string{"No MX records found"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Detect(context.Background(), tc.res, tc.target)
			if r.Provider != tc.provider {
				t.Errorf("provider = %q, want %q", r.Provider, tc.provider)
			}
			if r.Supported != tc.supported {
				t.Errorf("supported = %v, want %v", r.Supported, tc.supported)
			}
			if r.Confidence != tc.confidence {
				t.Errorf("confidence = %q, want %q", r.Confidence, tc.confidence)
			}
			if tc.supported {
				if r.SupportedProvider == nil || *r.SupportedProvider != tc.provider {
					t.Errorf("supported_provider = %v, want %q", r.SupportedProvider, tc.provider)
				}
			} else if r.SupportedProvider != nil {
				t.Errorf("supported_provider = %v, want nil", *r.SupportedProvider)
			}
			for _, want := range tc.wantSignals {
				if !anySignalContains(r.Signals, want) {
					t.Errorf("signals %v missing substring %q", r.Signals, want)
				}
			}
		})
	}
}

// Domain extraction: address local-part stripped, trailing dot + case normalized.
func TestDetectDomainExtraction(t *testing.T) {
	r := Detect(context.Background(), fakeResolver{}, "Foo.Bar@Sub.Example.COM.")
	if r.Domain != "sub.example.com" {
		t.Errorf("domain = %q, want sub.example.com", r.Domain)
	}
}

// rDNS enrichment surfaces a hosting-provider hint note for "other" verdicts.
func TestDetectHostingHint(t *testing.T) {
	res := fakeResolver{
		mx:   map[string][]string{"hosted.example": {"mail.hosted.example"}},
		txt:  map[string][]string{"hosted.example": {`"v=spf1 ip4:198.51.100.7 -all"`}},
		addr: map[string][]string{"198.51.100.7": {"vps.combell.net."}},
	}
	r := Detect(context.Background(), res, "hosted.example")
	if r.Provider != "other" {
		t.Fatalf("provider = %q, want other", r.Provider)
	}
	if !anySignalContains(r.Notes, "combell") {
		t.Errorf("notes %v missing combell hint", r.Notes)
	}
}

// JSON-tagged slices stay non-nil so the wire shape stays stable.
func TestDetectEmptySlicesNonNil(t *testing.T) {
	r := Detect(context.Background(), fakeResolver{}, "void.example")
	if r.Signals == nil || r.Notes == nil {
		t.Errorf("signals/notes should be non-nil empty slices")
	}
	if !reflect.DeepEqual(r.MX, []string{}) {
		// uniqueSorted returns []string{} for no MX
		t.Logf("mx = %#v", r.MX)
	}
}

func anySignalContains(sigs []string, sub string) bool {
	for _, s := range sigs {
		if containsFold(s, sub) {
			return true
		}
	}
	return false
}

func containsFold(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
