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
	"os"
	"strings"
	"time"
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
		http:    &http.Client{Timeout: httpTimeout()},
	}
}

// fetchBoth issues ONE request and returns BOTH the typed view and the verbatim body bytes — the
// seam every endpoint method uses, so a command fetches once and picks the shape by output mode
// (`-o json` emits the server's bytes untouched; the table view renders the struct). Decoding runs
// with UseNumber so a passthrough `any` field keeps the server's exact number literal.
func fetchBoth[T any](ctx context.Context, c *Client, method, path string, body any) (*T, json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, method, path, body, &raw); err != nil {
		return nil, nil, err
	}
	var out T
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		return nil, nil, fmt.Errorf("decode response: %w", err)
	}
	return &out, raw, nil
}

const defaultHTTPTimeout = 10 * time.Minute

func httpTimeout() time.Duration {
	raw := os.Getenv("RC_HTTP_TIMEOUT")
	if raw == "" {
		return defaultHTTPTimeout
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return defaultHTTPTimeout
	}
	return d
}

// BaseURL is the resolved API base URL (no trailing slash). Exposed so a command that composes a
// dashboard URL for a human (e.g. `rc project mailbox connect`) points at the same server the client talks to.
func (c *Client) BaseURL() string { return c.baseURL }

// RunsParams are the query filters for GET /api/v1/runs. Zero values are omitted (the server applies
// its defaults), so `rc status` (no filters) and `rc run list --limit 10` share one path. Project is the
// explicit scope an all-projects admin token names per request (the `--all` fan-out); a pinned token
// ignores it server-side.
type RunsParams struct {
	Limit    int
	Days     int
	Kind     string
	Category string
	Outcome  string
	Learning string
	Reviewed bool
	Before   string
	Session  string
	Project  string
	Tenant   string
}

// Runs fetches GET /api/v1/runs — the shared endpoint behind both `rc status` and `rc run list`.
func (c *Client) Runs(ctx context.Context, p RunsParams) (*RunsResponse, json.RawMessage, error) {
	q := url.Values{}
	if p.Limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", p.Limit))
	}
	if p.Days > 0 {
		q.Set("days", fmt.Sprintf("%d", p.Days))
	}
	if p.Kind != "" {
		q.Set("kind", p.Kind)
	}
	if p.Category != "" {
		q.Set("category", p.Category)
	}
	if p.Outcome != "" {
		q.Set("outcome", p.Outcome)
	}
	if p.Learning != "" {
		q.Set("learning", p.Learning)
	}
	if p.Reviewed {
		q.Set("reviewed", "true")
	}
	if p.Before != "" {
		q.Set("before", p.Before)
	}
	if p.Session != "" {
		q.Set("session", p.Session)
	}
	if p.Project != "" {
		q.Set("project", p.Project)
	}
	if p.Tenant != "" {
		q.Set("tenant", p.Tenant)
	}
	path := "/api/v1/runs"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	return fetchBoth[RunsResponse](ctx, c, http.MethodGet, path, nil)
}

// Projects fetches GET /api/v1/projects — the fleet handles an all-projects admin token may see. Used by
// `rc project list` and the seed of every `--all` fan-out.
func (c *Client) Projects(ctx context.Context) (*ProjectsResponse, json.RawMessage, error) {
	return fetchBoth[ProjectsResponse](ctx, c, http.MethodGet, "/api/v1/projects", nil)
}

// RenameProject patches PATCH /api/v1/projects/{project}/rename with {"name":"new-slug"}, returning
// both the typed result for table output and raw bytes for JSON passthrough.
func (c *Client) RenameProject(ctx context.Context, project, name string) (*ProjectRenameResponse, json.RawMessage, error) {
	path := "/api/v1/projects/" + url.PathEscape(project) + "/rename"
	return fetchBoth[ProjectRenameResponse](ctx, c, http.MethodPatch, path, ProjectRenameRequest{Name: name})
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
func (c *Client) Run(ctx context.Context, id, project, tenant string) (*RunDetail, error) {
	var out RunDetail
	if err := c.do(ctx, http.MethodGet, collectionScopePath("/api/v1/runs/"+url.PathEscape(id), project, tenant), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func RunPath(id, project, tenant string) string {
	return collectionScopePath("/api/v1/runs/"+url.PathEscape(id), project, tenant)
}

// Events fetches GET /api/v1/runs/{id}/events — the full per-event trace.
func (c *Client) Events(ctx context.Context, id, project, tenant string) (*EventsResponse, error) {
	var out EventsResponse
	if err := c.do(ctx, http.MethodGet, collectionScopePath("/api/v1/runs/"+url.PathEscape(id)+"/events", project, tenant), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Full fetches GET /api/v1/runs/{id}/trace — the whole bundle (run header + per-event trace with the
// ai_usage join). Used by the table view of `rc run trace <id>`; the JSON path goes through Raw to
// keep the renderer's JSONL seam byte-faithful.
func (c *Client) Full(ctx context.Context, id, project, tenant string) (*FullResponse, error) {
	var out FullResponse
	if err := c.do(ctx, http.MethodGet, RunTracePath(id, project, tenant), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func RunTracePath(id, project, tenant string) string {
	return collectionScopePath("/api/v1/runs/"+url.PathEscape(id)+"/trace", project, tenant)
}

// BrainDiff fetches GET /api/v1/runs/{id}/brain-diff — the ONE journal commit the run wrote to its
// brain. Used by the table view of `rc run brain-diff <id>`; the JSON path goes through Raw to keep
// the passthrough byte-faithful (render, don't reshape).
func (c *Client) BrainDiff(ctx context.Context, id, project, tenant string) (*BrainDiff, error) {
	var out BrainDiff
	path := RunBrainDiffPath(id, project, tenant)
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func RunBrainDiffPath(id, project, tenant string) string {
	return collectionScopePath("/api/v1/runs/"+url.PathEscape(id)+"/brain-diff", project, tenant)
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

// DryScope posts POST /api/v1/runs with dry_scope:true — resolve and return the principal scope this run
// WOULD get, WITHOUT running the agent (no LLM spend, no run row). Returns BOTH the typed ScopePreview
// (for table rendering) and the verbatim bytes (for `-o json` passthrough), the same "render, don't
// reshape" invariant as Submit. No legacy fallback: dry_scope is a new field a stale server never had.
func (c *Client) DryScope(ctx context.Context, req SubmitRequest) (*ScopePreview, json.RawMessage, error) {
	req.DryScope = true
	path := "/api/v1/runs"
	if req.Project != "" {
		path += "?project=" + url.QueryEscape(req.Project)
	}
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodPost, path, req, &raw); err != nil {
		return nil, nil, err
	}
	var out ScopePreview
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, fmt.Errorf("decode dry-scope response: %w", err)
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
func (c *Client) GetBag(ctx context.Context, base, project string) (*Settings, json.RawMessage, error) {
	return fetchBoth[Settings](ctx, c, http.MethodGet, bagURL(base, project), nil)
}

// PatchBag sends a sparse PATCH on a config bag at base (only the changed keys) and returns the new full
// bag. The body is an opaque key→value map: the server owns the whitelist and validation, so the CLI
// passes keys through verbatim and lets the server reject unknown/forbidden/invalid ones.
func (c *Client) PatchBag(ctx context.Context, base string, patch map[string]any, project string) (*Settings, json.RawMessage, error) {
	return fetchBoth[Settings](ctx, c, http.MethodPatch, bagURL(base, project), patch)
}

// bagURL appends ?project= when scoping an all-projects token onto a target project.
func bagURL(base, project string) string {
	if project != "" {
		return base + "?project=" + url.QueryEscape(project)
	}
	return base
}

// GetSchema fetches GET /api/v1/meta/schema[?resource=] — the declarative config registry. resource
// empty returns every resource; a name filters to one (404 if unknown).
func (c *Client) GetSchema(ctx context.Context, resource, project string) (*SchemaResponse, json.RawMessage, error) {
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
	return fetchBoth[SchemaResponse](ctx, c, http.MethodGet, path, nil)
}

// GetAccess fetches GET /api/v1/meta/capabilities — what this token may do, optionally scoped to a
// project (an all-projects token must pass project to learn its per-project reach).
func (c *Client) GetAccess(ctx context.Context, project string) (*Access, json.RawMessage, error) {
	path := "/api/v1/meta/capabilities"
	if project != "" {
		path += "?project=" + url.QueryEscape(project)
	}
	return fetchBoth[Access](ctx, c, http.MethodGet, path, nil)
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

// tenantProfileScope is the profile endpoint's project selector. Pinned tokens validate it server-side;
// all-projects admin tokens need it to select one brain/project.
func tenantProfileScope(project string) string {
	if project == "" {
		return ""
	}
	return "?project=" + url.QueryEscape(project)
}

// GetTenantSettings fetches GET /api/v1/tenants/{slug}/profile — one tenant's
// projection/profile record (settings + version + applied_at). slug is path-escaped; project is the
// optional all-projects-token selector. Raw bytes preserve future server fields for JSON output.
func (c *Client) GetTenantSettings(ctx context.Context, slug, project string) (*TenantSettings, json.RawMessage, error) {
	path := "/api/v1/tenants/" + url.PathEscape(slug) + "/profile" + tenantProfileScope(project)
	return fetchBoth[TenantSettings](ctx, c, http.MethodGet, path, nil)
}

// PatchTenantSettings sends a sparse PATCH /api/v1/tenants/{slug}/profile (only the keys in
// req.Settings; an explicit nil value → JSON null = unconfigure) and returns the merged profile
// record. The server owns the schema/merge/validation; a bad merged value comes back as a 400
// validation_failed the command layer surfaces verbatim.
func (c *Client) PatchTenantSettings(ctx context.Context, slug, project string, req TenantSettingsPatchRequest) (*TenantSettings, json.RawMessage, error) {
	path := "/api/v1/tenants/" + url.PathEscape(slug) + "/profile" + tenantProfileScope(project)
	return fetchBoth[TenantSettings](ctx, c, http.MethodPatch, path, req)
}

// GetTenantSettingsSchema fetches GET /api/v1/tenant-profiles/schema — the enriched profile JSON
// Schema (x-* render metadata included). Returned as raw bytes: `rc tenant profile schema` dumps it
// verbatim, and `set` parses it for client-side type/enum coercion. Not project-specific, but
// bearer-gated.
func (c *Client) GetTenantSettingsSchema(ctx context.Context, project string) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v1/tenant-profiles/schema"+tenantProfileScope(project), nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// Routes fetches GET /api/v1/meta/routes — the canonical route manifest (`rc dev routes`).
func (c *Client) Routes(ctx context.Context) (*RouteManifest, json.RawMessage, error) {
	return fetchBoth[RouteManifest](ctx, c, http.MethodGet, "/api/v1/meta/routes", nil)
}

// OpenAPI fetches GET /api/v1/meta/openapi.json — dumped verbatim, never reshaped.
func (c *Client) OpenAPI(ctx context.Context) (json.RawMessage, error) {
	return c.Raw(ctx, http.MethodGet, "/api/v1/meta/openapi.json", nil)
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

// Download streams one successful response directly to w. It keeps the normal OAuth refresh and safe
// GET retry policy without buffering potentially large artifacts in memory.
func (c *Client) Download(ctx context.Context, path string, w io.Writer) error {
	resp, err := c.openStream(ctx, sendSpec{method: http.MethodGet, path: path, accept: "application/octet-stream"})
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(w, resp.Body)
	closeErr := resp.Body.Close()
	if copyErr != nil {
		return &TransportError{Err: fmt.Errorf("stream response: %w", copyErr)}
	}
	if closeErr != nil {
		return &TransportError{Err: fmt.Errorf("close response: %w", closeErr)}
	}
	if resp.ContentLength >= 0 && written != resp.ContentLength {
		return &TransportError{Err: fmt.Errorf("incomplete response: received %d of %d bytes", written, resp.ContentLength)}
	}
	return nil
}
