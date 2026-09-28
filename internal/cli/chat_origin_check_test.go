package cli

import (
	"context"
	"net"
	"testing"
)

func TestUnresolvableOriginsFlagsOnlyNXDOMAIN(t *testing.T) {
	lookup := func(_ context.Context, host string) ([]string, error) {
		switch host {
		case "staging.app.example.com":
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		case "flaky.example.com":
			return nil, &net.DNSError{Err: "timeout", Name: host, IsTimeout: true}
		}
		return []string{"1.2.3.4"}, nil
	}
	got := unresolvableOrigins(context.Background(), []string{
		"https://app-staging.example.com", "https://staging.app.example.com",
		"https://flaky.example.com", "http://localhost:3333", "http://127.0.0.1:8080",
	}, lookup)
	if len(got) != 1 || got[0] != "https://staging.app.example.com" {
		t.Fatalf("unresolvableOrigins = %v", got)
	}
}

func TestNearMissRejectsNamesTheLikelyTypo(t *testing.T) {
	origins := []string{"https://staging.app.example.com", "https://app.other.io"}
	got := nearMissRejects([]doctorReject{
		{Code: "ORIGIN_NOT_ALLOWED", Origin: "https://app-staging.example.com"},
		{Code: "ORIGIN_NOT_ALLOWED", Origin: "https://unrelated.dev"},
		{Code: "ORIGIN_NOT_ALLOWED", Origin: "https://app.example.com", Stale: true},
		{Code: "TOKEN_REPLAYED", Origin: "https://app.example.com"},
	}, origins)
	if len(got) != 1 || got["https://app-staging.example.com"] != "https://staging.app.example.com" {
		t.Fatalf("nearMissRejects = %v", got)
	}
}
