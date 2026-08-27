package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSafeReadRetriesServerFailure(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls < 3 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "retry", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c := New(srv.URL, StaticToken("test"))
	if _, err := c.Raw(context.Background(), http.MethodGet, "/probe", nil); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestMutatingPostDoesNotRetry(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		http.Error(w, "fail", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := New(srv.URL, StaticToken("test"))
	_, err := c.BashRun(context.Background(), BashRunRequest{Command: "touch /tmp/x"}, "", "")
	if err == nil || calls != 1 {
		t.Fatalf("err/calls = %v/%d, want failure/1", err, calls)
	}
}

func TestHTTPTimeoutEnvironment(t *testing.T) {
	t.Setenv("RC_HTTP_TIMEOUT", "42s")
	c := New("https://example.test", StaticToken("test"))
	if c.http.Timeout != 42*time.Second {
		t.Fatalf("timeout = %s", c.http.Timeout)
	}
}
