// This file is the ONE transport loop every request in this package goes through: build → auth →
// send → on 401 refresh the credential once and resend → back off on 429/5xx for requests that are
// safe to repeat → decode any non-2xx into a typed APIError. Everything else in the package is a
// thin endpoint mapping on top of it.
//
// Two axes are parameterised instead of forked:
//   - buffered vs streamed: openStream hands back the OPEN successful response (Download, the console
//     NDJSON stream, the chat SSE stream); fetch is openStream + read-all; do is fetch + JSON decode.
//   - which credential: sendSpec.tokens defaults to the client's OAuth TokenSource, but the chat embed
//     endpoints pass their widget/embed JWT as a StaticToken. A static source's Refresh returns the same
//     token, so the 401-refresh step self-disables for credentials that cannot be refreshed, while
//     backoff, timeout, header policy and error decoding still apply.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxRetries is the number of extra attempts after the first for a retryable status on a safe request.
const maxRetries = 3

// sendSpec is one request for the transport loop. Zero values mean the JSON defaults: Accept
// application/json, and Content-Type application/json when a body is present.
type sendSpec struct {
	method      string
	path        string
	body        []byte
	accept      string            // Accept header; "" → application/json
	contentType string            // body Content-Type; "" → application/json (multipart passes its boundary)
	headers     map[string]string // extra headers (the chat embed origin)
	tokens      TokenSource       // credential source; nil → the client's OAuth token source
}

// openStream runs the send loop and returns the OPEN successful response — the caller owns closing the
// body. A failed refresh preserves the server's original 401 envelope rather than masking it with the
// refresh error.
func (c *Client) openStream(ctx context.Context, spec sendSpec) (*http.Response, error) {
	tokens := spec.tokens
	if tokens == nil {
		tokens = c.tokens
	}
	token, err := tokens.Token(ctx)
	if err != nil {
		return nil, err
	}
	refreshed := false
	for attempt := 0; ; {
		resp, err := c.sendOnce(ctx, spec, token)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusUnauthorized && !refreshed {
			data, readErr := drainBody(resp)
			newToken, refreshErr := tokens.Refresh(ctx)
			if refreshErr == nil && newToken != "" && newToken != token {
				token, refreshed = newToken, true
				continue
			}
			if readErr != nil {
				return nil, readErr
			}
			return nil, decodeAPIError(resp.StatusCode, spec.method, spec.path, c.baseURL, data)
		}
		if retryableStatus(resp.StatusCode) && retryableRequest(spec.method, spec.path, spec.body) && attempt < maxRetries {
			delay := retryDelay(resp, attempt)
			_ = resp.Body.Close()
			attempt++
			select {
			case <-ctx.Done():
				return nil, &TransportError{Err: ctx.Err()}
			case <-time.After(delay):
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			data, readErr := drainBody(resp)
			if readErr != nil {
				return nil, readErr
			}
			return nil, decodeAPIError(resp.StatusCode, spec.method, spec.path, c.baseURL, data)
		}
		return resp, nil
	}
}

// fetch is openStream + read-all: the buffered path.
func (c *Client) fetch(ctx context.Context, spec sendSpec) ([]byte, error) {
	resp, err := c.openStream(ctx, spec)
	if err != nil {
		return nil, err
	}
	return drainBody(resp)
}

// sendOnce builds and performs exactly one HTTP round trip with the given bearer token.
func (c *Client) sendOnce(ctx context.Context, spec sendSpec, token string) (*http.Response, error) {
	var r io.Reader
	if len(spec.body) > 0 {
		r = bytes.NewReader(spec.body)
	}
	req, err := http.NewRequestWithContext(ctx, spec.method, c.baseURL+spec.path, r)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", orDefault(spec.accept, "application/json"))
	if len(spec.body) > 0 {
		req.Header.Set("Content-Type", orDefault(spec.contentType, "application/json"))
	}
	for k, v := range spec.headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// Connection-level failure (DNS, refused, TLS, timeout): include the base URL so a request that
		// silently went to the localhost default instead of the intended host is obvious.
		return nil, &TransportError{Err: fmt.Errorf("request %s %s (base %s): %w", spec.method, spec.path, c.baseURL, err)}
	}
	return resp, nil
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// drainBody reads and closes a response body, returning a TransportError on a read failure.
func drainBody(resp *http.Response) ([]byte, error) {
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, &TransportError{Err: fmt.Errorf("read response: %w", err)}
	}
	return data, nil
}

func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

func retryableRequest(method, path string, body []byte) bool {
	if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
		return true
	}
	if method != http.MethodPost || !strings.Contains(pathOnly(path), "/console/db/") || !strings.HasSuffix(pathOnly(path), "/query") {
		return false
	}
	var req DBQueryRequest
	return json.Unmarshal(body, &req) == nil && !req.Write
}

func retryDelay(resp *http.Response, attempt int) time.Duration {
	if raw := resp.Header.Get("Retry-After"); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds >= 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	return time.Duration(1<<attempt) * 250 * time.Millisecond
}

// do issues one JSON request through the transport loop and decodes the 2xx body into out. out may be
// a *json.RawMessage to capture the body unparsed for passthrough.
func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var reqBody []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = b
	}
	data, err := c.fetch(ctx, sendSpec{method: method, path: path, body: reqBody})
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	// A 204/empty body (e.g. a DELETE, or a no-content verb) is valid only where the caller asked for
	// raw bytes (*json.RawMessage) — leave out nil, nothing to decode. A typed target still requires a
	// body, so a malformed empty 2xx on a content endpoint stays a decode error rather than a silent
	// zero value.
	if _, raw := out.(*json.RawMessage); raw && len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
