package cli

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"
)

// unresolvableOrigins returns configured origins whose host has no DNS record.
// A near-miss typo (staging.app.x vs app-staging.x) usually does not resolve, and
// nothing else flags it: the page on the real host just fails ORIGIN_NOT_ALLOWED.
// Only an authoritative NXDOMAIN counts; timeouts and resolver errors stay silent.
func unresolvableOrigins(ctx context.Context, origins []string, lookup func(context.Context, string) ([]string, error)) []string {
	var out []string
	for _, o := range origins {
		host := originHost(o)
		if host == "" || host == "localhost" || net.ParseIP(host) != nil {
			continue
		}
		lctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		_, err := lookup(lctx, host)
		cancel()
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			out = append(out, o)
		}
	}
	return out
}

// nearMissRejects pairs each recent origin rejection with the configured origin
// it most resembles, so a doctor run names the likely typo instead of just
// "not allowed".
func nearMissRejects(rejects []doctorReject, origins []string) map[string]string {
	out := map[string]string{}
	for _, r := range rejects {
		if r.Stale || r.Origin == "" || containsCLI(origins, r.Origin) {
			continue
		}
		if r.Code != "ORIGIN_NOT_ALLOWED" && r.Code != "ORIGIN_MISMATCH" {
			continue
		}
		if near, ok := nearestOrigin(r.Origin, origins); ok {
			out[r.Origin] = near
		}
	}
	return out
}

// nearestOrigin returns the configured origin closest to target when it is a
// plausible typo: same registrable suffix (last two labels) or a host edit
// distance of at most 4.
func nearestOrigin(target string, origins []string) (string, bool) {
	th := originHost(target)
	if th == "" {
		return "", false
	}
	best, bestDist := "", 1<<30
	for _, o := range origins {
		oh := originHost(o)
		if oh == "" || oh == th {
			continue
		}
		d := levenshtein(th, oh)
		if d <= 4 || registrable(th) == registrable(oh) {
			if d < bestDist {
				best, bestDist = o, d
			}
		}
	}
	return best, best != ""
}

func originHost(origin string) string {
	u, err := url.Parse(origin)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func registrable(host string) string {
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return host
	}
	return strings.Join(labels[len(labels)-2:], ".")
}

func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
