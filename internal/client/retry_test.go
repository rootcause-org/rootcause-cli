package client

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type failedRefreshSource struct{}

func (failedRefreshSource) Token(context.Context) (string, error) { return "expired", nil }
func (failedRefreshSource) Refresh(context.Context) (string, error) {
	return "", errors.New("refresh failed")
}

// rotatingSource hands out "stale" until Refresh is called, then "fresh" — the mid-flight expiry the
// transport loop must recover from with exactly one forced refresh.
type rotatingSource struct{ refreshes int }

func (r *rotatingSource) Token(context.Context) (string, error) {
	if r.refreshes == 0 {
		return "stale", nil
	}
	return "fresh", nil
}

func (r *rotatingSource) Refresh(context.Context) (string, error) {
	r.refreshes++
	return "fresh", nil
}

// bearerGate answers 401 to anything but "Bearer fresh", then serves body.
func bearerGate(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer fresh" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"code":"TOKEN_EXPIRED","message":"expired"}}`)
			return
		}
		_, _ = io.WriteString(w, body)
	}
}

func TestBufferedPathRefreshesOn401ThenSucceeds(t *testing.T) {
	srv := httptest.NewServer(bearerGate(`{"ok":true}`))
	defer srv.Close()
	src := &rotatingSource{}
	c := New(srv.URL, src)
	raw, err := c.raw(context.Background(), http.MethodGet, "/probe", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"ok":true}` || src.refreshes != 1 {
		t.Fatalf("body/refreshes = %s/%d, want {\"ok\":true}/1", raw, src.refreshes)
	}
}

func TestStreamPathRefreshesOn401ThenSucceeds(t *testing.T) {
	srv := httptest.NewServer(bearerGate("artifact-bytes"))
	defer srv.Close()
	src := &rotatingSource{}
	c := New(srv.URL, src)
	var buf bytes.Buffer
	if err := c.Download(context.Background(), "/artifact", &buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "artifact-bytes" || src.refreshes != 1 {
		t.Fatalf("body/refreshes = %q/%d, want artifact-bytes/1", buf.String(), src.refreshes)
	}
}

func TestStreamPathRetriesServerFailure(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls < 3 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "retry", http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, "artifact-bytes")
	}))
	defer srv.Close()
	c := New(srv.URL, StaticToken("test"))
	var buf bytes.Buffer
	if err := c.Download(context.Background(), "/artifact", &buf); err != nil {
		t.Fatal(err)
	}
	if calls != 3 || buf.String() != "artifact-bytes" {
		t.Fatalf("calls/body = %d/%q, want 3/artifact-bytes", calls, buf.String())
	}
}

// A read-only console query is the one POST the retry policy treats as safe, and it takes the stream
// path — so the NDJSON stream must back off like the buffered reads do.
func TestQueryStreamRetriesServerFailure(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls < 3 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "retry", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "{\"type\":\"header\",\"columns\":[\"a\"]}\n{\"type\":\"meta\",\"row_count\":0}\n")
	}))
	defer srv.Close()
	c := New(srv.URL, StaticToken("test"))
	meta, err := c.DBQueryStream(context.Background(), "main", DBQueryRequest{SQL: "select 1"}, "p", "t",
		func(*DBQueryStreamHeader) error { return nil },
		func([]any) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || meta.RowCount != 0 {
		t.Fatalf("calls/rows = %d/%d, want 3/0", calls, meta.RowCount)
	}
}

// The chat embed plane carries a widget JWT instead of the OAuth bearer, but it goes through the same
// loop: a 5xx must still come back as a decoded APIError, not a raw stream.
func TestChatSendDecodesServerErrorEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if got := req.Header.Get("X-RC-Embed-Origin"); got != "https://example.test" {
			t.Errorf("origin header = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"error":{"code":"UPSTREAM","message":"chat backend down"}}`)
	}))
	defer srv.Close()
	c := New(srv.URL, StaticToken("unused"))
	_, err := c.ChatSend(context.Background(), "p", "https://example.test", "embed-jwt", "sess", "msg", nil, io.Discard)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadGateway || apiErr.Code != "UPSTREAM" {
		t.Fatalf("error = %#v, want decoded APIError", err)
	}
}

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
	if _, err := c.raw(context.Background(), http.MethodGet, "/probe", nil); err != nil {
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
	_, _, err := c.BashRun(context.Background(), BashRunRequest{Command: "touch /tmp/x"}, "", "")
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

func TestDownloadPreservesUnauthorizedEnvelopeWhenRefreshFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"code":"TOKEN_EXPIRED","message":"sign in again"}}`)
	}))
	defer srv.Close()
	c := New(srv.URL, failedRefreshSource{})
	err := c.Download(context.Background(), "/artifact", io.Discard)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized || apiErr.Code != "TOKEN_EXPIRED" {
		t.Fatalf("error = %#v, want original 401 APIError", err)
	}
}
