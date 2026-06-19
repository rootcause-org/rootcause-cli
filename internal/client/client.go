// Package client is the one thin HTTP wrapper over the rootcause-light JSON API: it sets the bearer
// key + base URL, speaks JSON, and on any non-2xx decodes the error envelope into a typed APIError
// (code+message+fields carried through verbatim). It holds NO business logic — every method is one
// request mapping straight onto one endpoint, returning the wire struct for the render layer to show.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Client is a bearer-authenticated handle to one project's API (the key resolves the project
// server-side, so there is no project parameter anywhere).
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// New builds a Client. baseURL is trimmed of a trailing slash so path joins stay clean.
func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{},
	}
}

// RunsParams are the query filters for GET /api/v1/runs. Zero values are omitted (the server applies
// its defaults), so `rc status` (no filters) and `rc runs --limit 10` share one path.
type RunsParams struct {
	Limit    int
	Kind     string
	Category string
	Before   string
}

// Runs fetches GET /api/v1/runs — the shared endpoint behind both `rc status` and `rc runs`.
func (c *Client) Runs(ctx context.Context, p RunsParams) (*RunsResponse, error) {
	q := url.Values{}
	if p.Limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", p.Limit))
	}
	if p.Kind != "" {
		q.Set("kind", p.Kind)
	}
	if p.Category != "" {
		q.Set("category", p.Category)
	}
	if p.Before != "" {
		q.Set("before", p.Before)
	}
	path := "/api/v1/runs"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	var out RunsResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Run fetches GET /api/v1/runs/{id} — one run, high level.
func (c *Client) Run(ctx context.Context, id string) (*RunDetail, error) {
	var out RunDetail
	if err := c.do(ctx, http.MethodGet, "/api/v1/runs/"+url.PathEscape(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Events fetches GET /api/v1/runs/{id}/events — the full per-event trace.
func (c *Client) Events(ctx context.Context, id string) (*EventsResponse, error) {
	var out EventsResponse
	if err := c.do(ctx, http.MethodGet, "/api/v1/runs/"+url.PathEscape(id)+"/events", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetSettings fetches GET /api/v1/settings.
func (c *Client) GetSettings(ctx context.Context) (*Settings, error) {
	var out Settings
	if err := c.do(ctx, http.MethodGet, "/api/v1/settings", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PatchSettings sends a sparse PATCH /api/v1/settings (only the changed keys) and returns the new
// full settings. The body is an opaque key→value map: the server owns the whitelist and validation,
// so the CLI passes keys through verbatim and lets the server reject unknown/forbidden ones.
func (c *Client) PatchSettings(ctx context.Context, patch map[string]any) (*Settings, error) {
	var out Settings
	if err := c.do(ctx, http.MethodPatch, "/api/v1/settings", patch, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RawRuns / RawRun / RawEvents / RawSettings return the response BODY bytes for JSON passthrough, so
// `-o json` emits exactly what the server sent (the CLI renders; it never reshapes for jq). The
// pretty-print happens in the render layer; here we just carry bytes.
func (c *Client) Raw(ctx context.Context, method, path string, body map[string]any) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, method, path, body, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// do issues one request: bearer auth, JSON body in/out, and on non-2xx decodes the error envelope
// into a typed APIError (code/message/fields verbatim). out may be a *json.RawMessage to capture the
// body unparsed for passthrough.
func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Try the standard envelope; if it doesn't decode, fall back to a status-only APIError so the
		// caller still fails cleanly without a panic.
		apiErr := &APIError{Status: resp.StatusCode}
		var env errorEnvelope
		if json.Unmarshal(data, &env) == nil && env.Error.Code != "" {
			apiErr.Code = env.Error.Code
			apiErr.Message = env.Error.Message
			apiErr.Fields = env.Error.Fields
		}
		return apiErr
	}

	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
