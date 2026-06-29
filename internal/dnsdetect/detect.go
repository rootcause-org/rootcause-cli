// Package dnsdetect classifies which email backend a domain (or email address)
// uses — and whether rootcause can onboard it.
//
// rootcause has exactly two DNS-detectable channel adapters: "google" (Gmail /
// Google Workspace) and "microsoft" (Microsoft 365 / Graph). Everything else —
// self-hosted IMAP/SMTP, cPanel/Plesk hosting, regional ISPs — is NOT supported.
// (Intercom is app-config, not DNS-detectable, so it never shows here.) The call
// is made from public DNS so we know before a sales call whether a prospect is
// onboardable.
//
// Method (cheap → authoritative):
//  1. MX     — where inbound mail lands. Direct hit for Google/Microsoft, but many
//     orgs front their real server with a spam-filter GATEWAY (Mimecast,
//     Proofpoint, …) that hides the backend, so MX alone is not enough.
//  2. SPF    — who is authorized to SEND. Reveals the real infra behind a gateway,
//     so it's the tiebreaker.
//  3. autodiscover — a CNAME to autodiscover.outlook.com is a strong M365 signal.
//  4. rDNS   — for "other" verdicts, reverse-DNS of the SPF IPs and mail.<domain>
//     gives a human hint at the actual box.
//
// Verification-only records (ms=…, google-site-verification=…) are reported but
// never decide the verdict — they're often leftover from a trial or used for
// non-mail services, so they lie about the live backend.
package dnsdetect

import (
	"context"
	"net"
	"sort"
	"strings"
)

// --- signal tables -----------------------------------------------------------

// An MX host containing one of these => that backend, full stop.
var googleMX = []string{"google.com", "googlemail.com"}
var microsoftMX = []string{"mail.protection.outlook.com", "olc.protection.outlook.com"}

// SPF include/host fragments => who actually sends.
var googleSPF = []string{"_spf.google.com", "spf.google.com", "_netblocks.google.com"}
var microsoftSPF = []string{"spf.protection.outlook.com", "_spf.microsoft.com", "spf.messaging.microsoft.com"}

// MX hosts that are spam-filter / security GATEWAYS — they front the real
// mailserver, so an MX match here means "look deeper" (SPF), not "this is it".
var gatewayMX = []string{
	"spamfilter.be", "mimecast.com", "pphosted.com", "ppe-hosted.com", // proofpoint
	"barracudanetworks.com", "barracuda.com", "messagelabs.com", "symanteccloud.com",
	"antispamcloud.com", "spamexperts.com", "mailprotect", "securemx", "mailcontrol.com",
	"trendmicro.com", "fireeyecloud.com", "forcepoint", "mailanyone.net", "mxthunder",
	"emailsrvr.com", "qq.com", "perimeter", "spamh", "vadesecure", "hornetsecurity",
	"altospam", "mailinblack", "nospamproxy", "retarus.com",
}

// rDNS / hostname fragments hinting at a generic hosting / IMAP backend (color only).
var hostingHint = []string{
	"combell", "cpanel", "plesk", "openprovider", "hostbasket", "one.com", "ovh",
	"transip", "hetzner", "ionos", "1and1", "strato", "gandi", "leaseweb", "telenet",
	"proximus", "scarlet", "edpnet", "kpn", "register.it", "versio", "argeweb",
}

// Resolver is the injected DNS seam — the network dependency, so detection is
// testable fully offline. NewNetResolver wires it to the real net.Resolver.
type Resolver interface {
	// LookupMX returns MX host strings: lowercased, no trailing dot, no preference.
	LookupMX(ctx context.Context, domain string) ([]string, error)
	LookupTXT(ctx context.Context, domain string) ([]string, error)
	LookupCNAME(ctx context.Context, host string) ([]string, error)
	LookupHost(ctx context.Context, host string) ([]string, error) // A/AAAA
	LookupAddr(ctx context.Context, ip string) ([]string, error)   // PTR
}

// Result is the structured verdict for one target. SupportedProvider is a pointer
// so it serializes as null when the backend isn't onboardable.
type Result struct {
	Input             string   `json:"input"`
	Domain            string   `json:"domain"`
	Provider          string   `json:"provider"`
	SupportedProvider *string  `json:"supported_provider"`
	Supported         bool     `json:"supported"`
	Confidence        string   `json:"confidence"`
	MX                []string `json:"mx"`
	SPF               string   `json:"spf"`
	Autodiscover      []string `json:"autodiscover"`
	Signals           []string `json:"signals"`
	Notes             []string `json:"notes"`
}

// Detect classifies one domain (or email address) into a structured verdict.
func Detect(ctx context.Context, r Resolver, target string) Result {
	// domain = part after '@', trimmed, no trailing dot, lowercased.
	domain := target
	if i := strings.Index(domain, "@"); i >= 0 {
		domain = domain[i+1:]
	}
	domain = strings.ToLower(strings.TrimRight(strings.TrimSpace(domain), "."))

	mxHosts := uniqueSorted(lookup(ctx, r.LookupMX, domain))
	txts := lookup(ctx, r.LookupTXT, domain)
	spf := spfRecord(txts)
	spfL := strings.ToLower(spf)
	autodiscover := lookup(ctx, r.LookupCNAME, "autodiscover."+domain)
	autodiscoverS := strings.ToLower(strings.Join(autodiscover, " "))

	var msVerify, gVerify []string
	for _, t := range txts {
		clean := strings.Trim(t, `"`)
		if strings.HasPrefix(strings.ToLower(clean), "ms=") {
			msVerify = append(msVerify, clean)
		}
		if strings.Contains(strings.ToLower(t), "google-site-verification") {
			gVerify = append(gVerify, clean)
		}
	}

	mxIsGoogle := hit(googleMX, mxHosts)
	mxIsMicrosoft := hit(microsoftMX, mxHosts)
	mxIsGateway := hit(gatewayMX, mxHosts)
	spfIsGoogle := anyContains(spfL, googleSPF)
	spfIsMicrosoft := anyContains(spfL, microsoftSPF)
	autodiscoverM365 := strings.Contains(autodiscoverS, "outlook.com")

	signals := []string{}
	var provider, confidence string

	// --- decision logic: MX + SPF agreement is the gold standard --------------
	switch {
	case mxIsGoogle || spfIsGoogle:
		provider = "google"
		if mxIsGoogle {
			signals = append(signals, "MX → Google ("+strings.Join(matching(mxHosts, googleMX), ", ")+")")
		}
		if spfIsGoogle {
			signals = append(signals, "SPF includes _spf.google.com")
		}
		switch {
		case mxIsGoogle:
			confidence = "high"
		default:
			confidence = "medium"
		}
	case mxIsMicrosoft || spfIsMicrosoft || autodiscoverM365:
		provider = "microsoft"
		if mxIsMicrosoft {
			signals = append(signals, "MX → Microsoft 365 ("+strings.Join(containing(mxHosts, "protection.outlook.com"), ", ")+")")
		}
		if spfIsMicrosoft {
			signals = append(signals, "SPF includes spf.protection.outlook.com")
		}
		if autodiscoverM365 {
			signals = append(signals, "autodiscover CNAME → autodiscover.outlook.com")
		}
		strong := boolToInt(mxIsMicrosoft) + boolToInt(spfIsMicrosoft) + boolToInt(autodiscoverM365)
		if mxIsMicrosoft || strong >= 2 {
			confidence = "high"
		} else {
			confidence = "medium"
		}
	case mxIsGateway:
		// Spam-filter gateway fronts an unknown backend, and SPF didn't point to
		// G/MS → the real server is self-hosted/other. Enrich with rDNS below.
		provider = "other"
		if spf != "" {
			confidence = "high"
		} else {
			confidence = "medium"
		}
		signals = append(signals, "MX is a spam-filter gateway ("+strings.Join(matching(mxHosts, gatewayMX), ", ")+") — not the backend")
		if spf != "" {
			signals = append(signals, "SPF authorizes neither Google nor Microsoft: "+spf)
		}
	case len(mxHosts) > 0:
		provider = "other"
		confidence = "medium"
		signals = append(signals, "MX points to a non-Google/Microsoft host: "+strings.Join(mxHosts, ", "))
		if spf != "" {
			signals = append(signals, "SPF: "+spf)
		}
	default:
		provider = "unknown"
		confidence = "low"
		signals = append(signals, "No MX records found — domain may not receive mail")
	}

	// --- rDNS enrichment for non-G/MS verdicts (color, not decision) ----------
	backendHint := ""
	if provider == "other" || provider == "unknown" {
		var probeIPs []string
		for _, tok := range strings.Fields(strings.ReplaceAll(spfL, "ip4:", " ip4:")) {
			if strings.HasPrefix(tok, "ip4:") {
				ip := strings.SplitN(tok[4:], "/", 2)[0]
				if strings.Count(ip, ".") == 3 {
					probeIPs = append(probeIPs, ip)
				}
			}
		}
		for _, ip := range lookup(ctx, r.LookupHost, "mail."+domain) {
			if strings.Count(ip, ".") == 3 {
				probeIPs = append(probeIPs, ip)
			}
		}
		seen := map[string]bool{}
		var ptrs []string
		for _, ip := range probeIPs {
			if seen[ip] {
				continue
			}
			seen[ip] = true
			p := ptr(ctx, r, ip)
			if p != "" {
				ptrs = append(ptrs, ip+" → "+p)
			} else {
				ptrs = append(ptrs, ip)
			}
		}
		if len(ptrs) > 0 {
			signals = append(signals, "Real mail host(s): "+strings.Join(ptrs, "; "))
		}
		blob := strings.ToLower(strings.Join(ptrs, " "))
		for _, h := range hostingHint {
			if strings.Contains(blob, h) {
				backendHint = h
				break
			}
		}
	}

	// Verification-only records: reported, never decisive.
	notes := []string{}
	if len(msVerify) > 0 {
		notes = append(notes, "Has Microsoft domain-verification TXT ("+strings.Join(msVerify, ", ")+") — verification only, not proof of a live M365 mailbox")
	}
	if len(gVerify) > 0 {
		notes = append(notes, "Has google-site-verification TXT — usually Search Console/Analytics, not Workspace mail")
	}
	if backendHint != "" {
		notes = append(notes, "Backend host suggests hosting/IMAP provider: "+backendHint)
	}

	supported := provider == "google" || provider == "microsoft"
	var sp *string
	if supported {
		p := provider
		sp = &p
	}

	return Result{
		Input:             target,
		Domain:            domain,
		Provider:          provider,
		SupportedProvider: sp,
		Supported:         supported,
		Confidence:        confidence,
		MX:                mxHosts,
		SPF:               spf,
		Autodiscover:      autodiscover,
		Signals:           signals,
		Notes:             notes,
	}
}

// --- helpers -----------------------------------------------------------------

func lookup(ctx context.Context, fn func(context.Context, string) ([]string, error), arg string) []string {
	out, err := fn(ctx, arg)
	if err != nil {
		return nil
	}
	return out
}

func ptr(ctx context.Context, r Resolver, ip string) string {
	names, err := r.LookupAddr(ctx, ip)
	if err != nil || len(names) == 0 {
		return ""
	}
	return strings.TrimRight(names[0], ".")
}

// spfRecord finds the v=spf1 TXT record, stripping quotes and joining split chunks.
func spfRecord(txts []string) string {
	for _, t := range txts {
		clean := strings.ReplaceAll(strings.Trim(t, `"`), `" "`, "")
		if strings.HasPrefix(strings.ToLower(clean), "v=spf1") {
			return clean
		}
	}
	return ""
}

func uniqueSorted(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// hit reports whether any host in hay contains any of the needles.
func hit(needles, hay []string) bool {
	for _, item := range hay {
		for _, n := range needles {
			if strings.Contains(item, n) {
				return true
			}
		}
	}
	return false
}

func anyContains(haystack string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}

// matching returns hosts that contain any of the needle fragments.
func matching(hosts, needles []string) []string {
	var out []string
	for _, h := range hosts {
		for _, n := range needles {
			if strings.Contains(h, n) {
				out = append(out, h)
				break
			}
		}
	}
	return out
}

// containing returns hosts that contain the given substring.
func containing(hosts []string, sub string) []string {
	var out []string
	for _, h := range hosts {
		if strings.Contains(h, sub) {
			out = append(out, h)
		}
	}
	return out
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// NewNetResolver returns a Resolver backed by the OS's net.DefaultResolver.
func NewNetResolver() Resolver { return netResolver{r: net.DefaultResolver} }

type netResolver struct{ r *net.Resolver }

func (n netResolver) LookupMX(ctx context.Context, domain string) ([]string, error) {
	mxs, err := n.r.LookupMX(ctx, domain)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(mxs))
	for _, mx := range mxs {
		out = append(out, strings.ToLower(strings.TrimRight(mx.Host, ".")))
	}
	return out, nil
}

func (n netResolver) LookupTXT(ctx context.Context, domain string) ([]string, error) {
	return n.r.LookupTXT(ctx, domain)
}

func (n netResolver) LookupCNAME(ctx context.Context, host string) ([]string, error) {
	cname, err := n.r.LookupCNAME(ctx, host)
	if err != nil {
		return nil, err
	}
	if cname == "" {
		return nil, nil
	}
	return []string{strings.TrimRight(cname, ".")}, nil
}

func (n netResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	return n.r.LookupHost(ctx, host)
}

func (n netResolver) LookupAddr(ctx context.Context, ip string) ([]string, error) {
	return n.r.LookupAddr(ctx, ip)
}
