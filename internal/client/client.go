// Package client is the one thin HTTP wrapper over the rootcause JSON API: it sets the bearer
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

// Client is an OAuth-bearer handle to the API. The access token resolves the caller's project +
// principal server-side (a pinned token scopes to one project; an all-projects admin token reads
// cross-project), so there is no project parameter anywhere. The token comes from a TokenSource that
// refreshes it transparently — the client retries a 401 once after a forced refresh.
type Client struct {
	baseURL string
	tokens  TokenSource
	http    *http.Client
}

// pathOnly strips the query string from a request path for error display (the query is noise when the
// point is which endpoint was missing).
func pathOnly(path string) string {
	if i := strings.IndexByte(path, '?'); i >= 0 {
		return path[:i]
	}
	return path
}

// New builds a Client. baseURL is trimmed of a trailing slash so path joins stay clean. tokens supplies
// (and refreshes) the bearer access token.
func New(baseURL string, tokens TokenSource) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		tokens:  tokens,
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

// Full fetches GET /api/v1/runs/{id}/full — the whole bundle (run header + per-event trace with the
// ai_usage join). Used by the table view of `rc run <id> --full`; the JSON path goes through Raw to
// keep the renderer's JSONL seam byte-faithful.
func (c *Client) Full(ctx context.Context, id string) (*FullResponse, error) {
	var out FullResponse
	if err := c.do(ctx, http.MethodGet, "/api/v1/runs/"+url.PathEscape(id)+"/full", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Submit posts POST /api/v1/runs to trigger a run. It returns BOTH the typed 202 body (for the
// poll/render logic) AND the verbatim bytes, so a caller that must echo the response to a jq pipeline
// (`rc ask --no-wait -o json`) never drops a server field — same "render, don't reshape" invariant as
// the GET passthroughs. The project is resolved from the bearer key; brain_ref (when set) names a
// non-main ref for a test run.
func (c *Client) Submit(ctx context.Context, req SubmitRequest) (*SubmitResponse, json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodPost, "/api/v1/runs", req, &raw); err != nil {
		return nil, nil, err
	}
	var out SubmitResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, fmt.Errorf("decode submit response: %w", err)
	}
	return &out, raw, nil
}

// Env fetches GET /api/v1/env — the project's PRODUCTION grounding secrets (decrypted), project ∪
// tenant when tenant is set. The response carries live secret VALUES, so callers must render NAMES
// only (or write the values straight to ./.env); never print a value to stdout/logs.
func (c *Client) Env(ctx context.Context, tenant string) (*EnvResponse, error) {
	path := "/api/v1/env"
	if tenant != "" {
		path += "?tenant=" + url.QueryEscape(tenant)
	}
	var out EnvResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
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

// GetTenantSettings fetches GET /api/v1/tenants/{slug}/settings — one practice's current onboarding
// record (settings + version + applied_at). slug is path-escaped; the project is the bearer key's.
func (c *Client) GetTenantSettings(ctx context.Context, slug string) (*TenantSettings, error) {
	var out TenantSettings
	if err := c.do(ctx, http.MethodGet, "/api/v1/tenants/"+url.PathEscape(slug)+"/settings", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PatchTenantSettings sends a sparse PATCH /api/v1/tenants/{slug}/settings (only the keys in
// req.Settings; an explicit nil value → JSON null = unconfigure) and returns the merged record. The
// server owns the schema/merge/validation; a bad merged value comes back as a 400 validation_failed
// the command layer surfaces verbatim.
func (c *Client) PatchTenantSettings(ctx context.Context, slug string, req TenantSettingsPatchRequest) (*TenantSettings, error) {
	var out TenantSettings
	if err := c.do(ctx, http.MethodPatch, "/api/v1/tenants/"+url.PathEscape(slug)+"/settings", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetTenantSettingsSchema fetches GET /api/v1/tenants/settings/schema — the enriched JSON Schema
// (x-* render metadata included). Returned as raw bytes: `rc tenant settings schema` dumps it verbatim,
// and `set` parses it for client-side type/enum coercion. Not project-specific, but bearer-gated.
func (c *Client) GetTenantSettingsSchema(ctx context.Context) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v1/tenants/settings/schema", nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
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

// attempt builds and sends one request with the given bearer token, draining and returning the response
// body bytes (the body is closed before return so the caller can retry without leaking a connection).
func (c *Client) attempt(ctx context.Context, method, path string, reqBody []byte, token string) (*http.Response, []byte, error) {
	var r io.Reader
	if reqBody != nil {
		r = bytes.NewReader(reqBody)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, r)
	if err != nil {
		return nil, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// Connection-level failure (DNS, refused, TLS, timeout): include the base URL so a request that
		// silently went to the localhost default instead of the intended host is obvious.
		return nil, nil, fmt.Errorf("request %s %s (base %s): %w", method, path, c.baseURL, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("read response: %w", err)
	}
	return resp, data, nil
}

// do issues one request: OAuth bearer auth, JSON body in/out, and on non-2xx decodes the error
// envelope into a typed APIError (code/message/fields verbatim). out may be a *json.RawMessage to
// capture the body unparsed for passthrough. On a 401 (an access token that expired/was revoked
// mid-flight) it forces a token refresh and retries ONCE — the body is buffered up front so the retry
// can resend it.
func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var reqBody []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = b
	}

	token, err := c.tokens.Token(ctx)
	if err != nil {
		return err
	}

	resp, data, err := c.attempt(ctx, method, path, reqBody, token)
	if err != nil {
		return err
	}
	// One transparent retry on a 401: the access token expired between pre-flight refresh and the
	// request (or was revoked). Force a refresh and resend; a still-401 surfaces verbatim below.
	if resp.StatusCode == http.StatusUnauthorized {
		if newToken, rerr := c.tokens.Refresh(ctx); rerr == nil && newToken != "" && newToken != token {
			resp, data, err = c.attempt(ctx, method, path, reqBody, newToken)
			if err != nil {
				return err
			}
		}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Try the standard envelope; if it doesn't decode, fall back to an APIError that still carries the
		// method/path/base URL so a non-JSON 404/405 (proxy, or an older server missing the endpoint) is
		// diagnosable rather than a bare "HTTP 405".
		apiErr := &APIError{Status: resp.StatusCode}
		var env errorEnvelope
		var vfe validationFailedEnvelope
		switch {
		case json.Unmarshal(data, &env) == nil && env.Error.Code != "":
			apiErr.Code = env.Error.Code
			apiErr.Message = env.Error.Message
			apiErr.Fields = env.Error.Fields
		case json.Unmarshal(data, &vfe) == nil && vfe.Error != "":
			// The tenant-settings shape: error is a string code, per-field errors are a map. Map it onto
			// the same APIError so the command layer's one verbatim path renders it (sorted for a stable
			// order, since map iteration isn't).
			apiErr.Code = vfe.Error
			apiErr.Message = "settings rejected"
			apiErr.Fields = sortedFieldErrors(vfe.FieldErrors)
		default:
			apiErr.Method = method
			apiErr.Path = pathOnly(path)
			apiErr.BaseURL = c.baseURL
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
