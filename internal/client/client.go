// Package client is the one thin HTTP wrapper over the rootcause JSON API: it sets the bearer
// key + base URL, speaks JSON, and on any non-2xx decodes the error envelope into a typed APIError
// (code+message+details carried through verbatim). It holds NO business logic — every method is one
// request mapping straight onto one endpoint, returning the wire struct for the render layer to show.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Client is an OAuth-bearer handle to the API. The access token resolves the caller's project +
// principal server-side (a pinned token scopes to one project; an all-projects admin token can name a
// per-request project on supported endpoints). The token comes from a TokenSource that refreshes it
// transparently — the client retries a 401 once after a forced refresh.
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

// BaseURL is the resolved API base URL (no trailing slash). Exposed so a command that composes a
// dashboard URL for a human (e.g. `rc mailbox connect`) points at the same server the client talks to.
func (c *Client) BaseURL() string { return c.baseURL }

// RunsParams are the query filters for GET /api/v1/runs. Zero values are omitted (the server applies
// its defaults), so `rc status` (no filters) and `rc runs --limit 10` share one path. Project is the
// explicit scope an all-projects admin token names per request (the `--all` fan-out); a pinned token
// ignores it server-side.
type RunsParams struct {
	Limit    int
	Kind     string
	Category string
	Before   string
	Project  string
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
	if p.Project != "" {
		q.Set("project", p.Project)
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

// Projects fetches GET /api/v1/projects — the fleet handles an all-projects admin token may see. Used by
// `rc projects` and the seed of every `--all` fan-out.
func (c *Client) Projects(ctx context.Context) (*ProjectsResponse, error) {
	var out ProjectsResponse
	if err := c.do(ctx, http.MethodGet, "/api/v1/projects", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RenameProject patches PATCH /api/v1/projects/{project}/rename with {"name":"new-slug"}, returning
// both the typed result for table output and raw bytes for JSON passthrough.
func (c *Client) RenameProject(ctx context.Context, project, name string) (*ProjectRenameResponse, json.RawMessage, error) {
	var raw json.RawMessage
	path := "/api/v1/projects/" + url.PathEscape(project) + "/rename"
	if err := c.do(ctx, http.MethodPatch, path, ProjectRenameRequest{Name: name}, &raw); err != nil {
		return nil, nil, err
	}
	var out ProjectRenameResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, fmt.Errorf("decode project rename response: %w", err)
	}
	return &out, raw, nil
}

// Whoami fetches GET /api/v1/whoami — the OAuth token's bound project/tenant scope.
func (c *Client) Whoami(ctx context.Context) (*WhoamiResponse, error) {
	var out WhoamiResponse
	if err := c.do(ctx, http.MethodGet, "/api/v1/whoami", nil, &out); err != nil {
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

// Full fetches GET /api/v1/runs/{id}/trace — the whole bundle (run header + per-event trace with the
// ai_usage join). Used by the table view of `rc run <id> --full`; the JSON path goes through Raw to
// keep the renderer's JSONL seam byte-faithful.
func (c *Client) Full(ctx context.Context, id string) (*FullResponse, error) {
	var out FullResponse
	if err := c.do(ctx, http.MethodGet, RunTracePath(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func RunTracePath(id string) string {
	return "/api/v1/runs/" + url.PathEscape(id) + "/trace"
}

// BrainDiff fetches GET /api/v1/runs/{id}/brain-diff — the ONE journal commit the run wrote to its
// brain. Used by the table view of `rc run <id> --brain-diff`; the JSON path goes through Raw to keep
// the passthrough byte-faithful (render, don't reshape).
func (c *Client) BrainDiff(ctx context.Context, id string) (*BrainDiff, error) {
	var out BrainDiff
	if err := c.do(ctx, http.MethodGet, "/api/v1/runs/"+url.PathEscape(id)+"/brain-diff", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Submit posts POST /api/v1/runs to trigger a run. It returns BOTH the typed 202 body (for the
// poll/render logic) AND the verbatim bytes, so a caller that must echo the response to a jq pipeline
// (`rc ask --no-wait -o json`) never drops a server field — same "render, don't reshape" invariant as
// the GET passthroughs. A pinned token supplies the project; an all-projects admin token may name one
// with req.Project. brain_ref (when set) names a non-main ref for a test run. Older deployed Prompt API
// builds rejected scenario/sender/subject as unknown fields, so a schema-malformed BAD_BODY retries the
// legacy prompt+tenant body only when no run-control field would be silently dropped.
func (c *Client) Submit(ctx context.Context, req SubmitRequest) (*SubmitResponse, json.RawMessage, error) {
	path := "/api/v1/runs"
	if req.Project != "" {
		path += "?project=" + url.QueryEscape(req.Project)
	}
	var raw json.RawMessage
	err := c.do(ctx, http.MethodPost, path, req, &raw)
	if err != nil && shouldRetryLegacySubmit(err, req) {
		raw = nil
		err = c.do(ctx, http.MethodPost, path, legacySubmitRequest{Prompt: req.Prompt, Tenant: req.Tenant}, &raw)
	}
	if err != nil {
		return nil, nil, err
	}
	var out SubmitResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, fmt.Errorf("decode submit response: %w", err)
	}
	return &out, raw, nil
}

type legacySubmitRequest struct {
	Prompt string `json:"prompt"`
	Tenant string `json:"tenant,omitempty"`
}

func shouldRetryLegacySubmit(err error, req SubmitRequest) bool {
	// A principal-bearing request must NEVER fall back to the bare {prompt,tenant} legacy body: the
	// legacy shape drops the principal silently, and a dropped principal is a silent under-scope (the run
	// would answer with tenant-only scope instead of the asserted identity's). This guard is security,
	// not parity — refuse the fallback and surface the original error instead.
	if req.Principal != nil {
		return false
	}
	if req.SessionID != "" || req.BrainRef != "" || req.ReasoningEffort != "" || len(req.Attachments) > 0 {
		return false
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.Status == http.StatusBadRequest && apiErr.Code == "BAD_BODY" && apiErr.Message == "malformed request body"
}

// Env fetches GET /api/v1/env — the project's PRODUCTION grounding secrets (decrypted), project ∪
// tenant when tenant is set. The response carries live secret VALUES, so callers must render NAMES
// only (or write the values straight to ./.env); never print a value to stdout/logs.
func (c *Client) Env(ctx context.Context, tenant, project string) (*EnvResponse, error) {
	path := "/api/v1/env"
	q := url.Values{}
	if tenant != "" {
		q.Set("tenant", tenant)
	}
	if project != "" {
		q.Set("project", project)
	}
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	var out EnvResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetBag fetches GET on a config bag at base (e.g. "/api/v1/kb"). The response is the generic
// {key:{value,effective,default,source}} map shared by every bag (settings/kb/branding/action).
func (c *Client) GetBag(ctx context.Context, base, project string) (*Settings, error) {
	var out Settings
	if err := c.do(ctx, http.MethodGet, bagURL(base, project), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PatchBag sends a sparse PATCH on a config bag at base (only the changed keys) and returns the new full
// bag. The body is an opaque key→value map: the server owns the whitelist and validation, so the CLI
// passes keys through verbatim and lets the server reject unknown/forbidden/invalid ones.
func (c *Client) PatchBag(ctx context.Context, base string, patch map[string]any, project string) (*Settings, error) {
	var out Settings
	if err := c.do(ctx, http.MethodPatch, bagURL(base, project), patch, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// bagURL appends ?project= when scoping an all-projects token onto a target project.
func bagURL(base, project string) string {
	if project != "" {
		return base + "?project=" + url.QueryEscape(project)
	}
	return base
}

// GetSettings fetches GET /api/v1/settings (the everyday bag).
func (c *Client) GetSettings(ctx context.Context, project string) (*Settings, error) {
	return c.GetBag(ctx, "/api/v1/settings", project)
}

// PatchSettings sends a sparse PATCH /api/v1/settings and returns the new full settings.
func (c *Client) PatchSettings(ctx context.Context, patch map[string]any, project string) (*Settings, error) {
	return c.PatchBag(ctx, "/api/v1/settings", patch, project)
}

// GetSchema fetches GET /api/v1/meta/schema[?resource=] — the declarative config registry. resource
// empty returns every resource; a name filters to one (404 if unknown).
func (c *Client) GetSchema(ctx context.Context, resource, project string) (*SchemaResponse, error) {
	q := url.Values{}
	if resource != "" {
		q.Set("resource", resource)
	}
	if project != "" {
		q.Set("project", project)
	}
	path := "/api/v1/meta/schema"
	if e := q.Encode(); e != "" {
		path += "?" + e
	}
	var out SchemaResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetAccess fetches GET /api/v1/meta/capabilities — what this token may do, optionally scoped to a
// project (an all-projects token must pass project to learn its per-project reach).
func (c *Client) GetAccess(ctx context.Context, project string) (*Access, error) {
	path := "/api/v1/meta/capabilities"
	if project != "" {
		path += "?project=" + url.QueryEscape(project)
	}
	var out Access
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func hierarchySettingsPath(scope, project, id string, resolved bool) string {
	path := "/api/v1/projects/" + url.PathEscape(project)
	switch scope {
	case "tenant":
		path += "/tenants/" + url.PathEscape(id)
	case "mailbox":
		path += "/mailboxes/" + url.PathEscape(id)
	}
	path += "/settings"
	if resolved {
		path += "?resolved=true"
	}
	return path
}

func (c *Client) GetHierarchySettings(ctx context.Context, scope, project, id string, resolved bool) (*HierarchySettings, error) {
	var out HierarchySettings
	if err := c.do(ctx, http.MethodGet, hierarchySettingsPath(scope, project, id, resolved), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) PatchHierarchySettings(ctx context.Context, scope, project, id string, patch map[string]any, resolved bool) (*HierarchySettings, error) {
	var out HierarchySettings
	if err := c.do(ctx, http.MethodPatch, hierarchySettingsPath(scope, project, id, resolved), patch, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) RawHierarchySettings(ctx context.Context, method, scope, project, id string, body map[string]any, resolved bool) (json.RawMessage, error) {
	return c.Raw(ctx, method, hierarchySettingsPath(scope, project, id, resolved), body)
}

// tenantProfileScope is the legacy profile endpoint's project selector. Pinned tokens ignore it
// server-side; all-projects admin tokens need it to select one brain/project.
func tenantProfileScope(project string) string {
	if project == "" {
		return ""
	}
	return "?project=" + url.QueryEscape(project)
}

// GetTenantSettings fetches GET /api/v1/tenants/{slug}/settings — one tenant's legacy
// projection/profile record (settings + version + applied_at). slug is path-escaped; project is the
// optional all-projects-token selector.
func (c *Client) GetTenantSettings(ctx context.Context, slug, project string) (*TenantSettings, error) {
	var out TenantSettings
	path := "/api/v1/tenants/" + url.PathEscape(slug) + "/settings" + tenantProfileScope(project)
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PatchTenantSettings sends a sparse PATCH /api/v1/tenants/{slug}/settings (only the keys in
// req.Settings; an explicit nil value → JSON null = unconfigure) and returns the merged legacy profile
// record. The server owns the schema/merge/validation; a bad merged value comes back as a 400
// validation_failed the command layer surfaces verbatim.
func (c *Client) PatchTenantSettings(ctx context.Context, slug, project string, req TenantSettingsPatchRequest) (*TenantSettings, error) {
	var out TenantSettings
	path := "/api/v1/tenants/" + url.PathEscape(slug) + "/settings" + tenantProfileScope(project)
	if err := c.do(ctx, http.MethodPatch, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetTenantSettingsSchema fetches GET /api/v1/tenants/settings/schema — the enriched profile JSON
// Schema (x-* render metadata included). Returned as raw bytes: `rc tenant profile schema` dumps it
// verbatim, and `set` parses it for client-side type/enum coercion. Not project-specific, but
// bearer-gated.
func (c *Client) GetTenantSettingsSchema(ctx context.Context, project string) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v1/tenants/settings/schema"+tenantProfileScope(project), nil, &raw); err != nil {
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
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("read response: %w", err)
	}
	return resp, data, nil
}

// do issues one request: OAuth bearer auth, JSON body in/out, and on non-2xx decodes the error
// envelope into a typed APIError (code/message/details verbatim). out may be a *json.RawMessage to
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
		return decodeAPIError(resp.StatusCode, method, path, c.baseURL, data)
	}

	if out != nil {
		// A 204/empty body (e.g. a DELETE, or a no-content verb) is valid only where the caller asked for
		// raw bytes (*json.RawMessage) — leave out nil, nothing to decode. A typed target still requires a
		// body, so a malformed empty 2xx on a content endpoint stays a decode error rather than a silent
		// zero value.
		if _, raw := out.(*json.RawMessage); raw && len(bytes.TrimSpace(data)) == 0 {
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
