package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
)

// This file holds the bespoke (non-collection, non-bag) config-surface calls: the OpenRouter key,
// the branding logo binary, GitHub install status, the brain edit/consolidate queue, run
// feedback/retry, the database controls sub-resource, and the box-level admin endpoints. Each is one
// request mapping straight onto one endpoint — same "render, don't reshape" invariant as the rest of
// the client. JSON-shaped calls return the raw body for the -o json passthrough plus a typed view.

// RawScoped issues an arbitrary method/path with a JSON body, threading ?project=&tenant= onto the
// path, and returns the verbatim body bytes. It's the project-scoped sibling of Raw for the bespoke
// commands that share the collection scope semantics.
func (c *Client) RawScoped(ctx context.Context, method, path string, body any, project, tenant string) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, method, path+collectionScope(project, tenant), body, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// --- OpenRouter key (PUT/DELETE/reveal at /api/v1/settings/openrouter-key) ---

// SetOpenRouterKey stores the box-wide OpenRouter API key (PUT {key}). The key never rides in a URL
// or a log line — it's a JSON body field only.
func (c *Client) SetOpenRouterKey(ctx context.Context, key, project string) (json.RawMessage, error) {
	return c.RawScoped(ctx, http.MethodPut, "/api/v1/settings/openrouter-key", map[string]any{"key": key}, project, "")
}

// ClearOpenRouterKey removes the stored OpenRouter key (DELETE).
func (c *Client) ClearOpenRouterKey(ctx context.Context, project string) (json.RawMessage, error) {
	return c.RawScoped(ctx, http.MethodDelete, "/api/v1/settings/openrouter-key", nil, project, "")
}

// RevealOpenRouterKey returns {secret} — the stored key, shown once (POST .../reveal).
func (c *Client) RevealOpenRouterKey(ctx context.Context, project string) (Item, json.RawMessage, error) {
	return c.itemWrite(ctx, http.MethodPost, "/api/v1/settings/openrouter-key/reveal"+collectionScope(project, ""), nil)
}

// --- Branding logo (PUT multipart / DELETE at /api/v1/branding/logo) ---

// SetBrandingLogo uploads the logo binary as multipart `file` with its MIME type (PUT). Returns the
// verbatim response body for the -o json path.
func (c *Client) SetBrandingLogo(ctx context.Context, filename, contentType string, data []byte, project string) (json.RawMessage, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{fmt.Sprintf(`form-data; name="file"; filename=%q`, filename)}
	h["Content-Type"] = []string{contentType}
	part, err := mw.CreatePart(h)
	if err != nil {
		return nil, fmt.Errorf("build multipart: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return nil, fmt.Errorf("write multipart: %w", err)
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("close multipart: %w", err)
	}
	return c.doRaw(ctx, http.MethodPut, "/api/v1/branding/logo"+collectionScope(project, ""), mw.FormDataContentType(), buf.Bytes())
}

// ClearBrandingLogo removes the stored logo (DELETE).
func (c *Client) ClearBrandingLogo(ctx context.Context, project string) (json.RawMessage, error) {
	return c.RawScoped(ctx, http.MethodDelete, "/api/v1/branding/logo", nil, project, "")
}

// --- GitHub install status (GET /api/v1/github/status) ---

// GitHubStatus fetches {installed, account, install_url?}.
func (c *Client) GitHubStatus(ctx context.Context, project string) (json.RawMessage, error) {
	return c.RawScoped(ctx, http.MethodGet, "/api/v1/github/status", nil, project, "")
}

// --- Brain status / sync / promote / edit / consolidate ---

// BrainStatus fetches the on-box brain cache status relative to origin/main.
func (c *Client) BrainStatus(ctx context.Context, project, tenant string) (*BrainStatusResponse, json.RawMessage, error) {
	if err := requireTenantProject(project, tenant, "brain"); err != nil {
		return nil, nil, err
	}
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, scopedTreePath(project, tenant, "/brain/status", "/api/v1/brain/status"), nil, &raw); err != nil {
		return nil, nil, err
	}
	var out BrainStatusResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, err
	}
	return &out, raw, nil
}

// BrainSync fetches origin/main, fast-forwards when safe, and refreshes warm bash sessions.
func (c *Client) BrainSync(ctx context.Context, project, tenant string) (*BrainSyncResponse, json.RawMessage, error) {
	if err := requireTenantProject(project, tenant, "brain"); err != nil {
		return nil, nil, err
	}
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodPost, scopedTreePath(project, tenant, "/brain/sync", "/api/v1/brain/sync"), map[string]any{}, &raw); err != nil {
		return nil, nil, err
	}
	var out BrainSyncResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, err
	}
	return &out, raw, nil
}

// BrainPromote moves one managed project channel to an exact tested commit. Promotion has no tenant
// route: tenant overlays always use main, and a tenant-scoped principal must not move a shared channel.
func (c *Client) BrainPromote(ctx context.Context, project string, req BrainPromoteRequest) (*BrainPromoteResponse, json.RawMessage, error) {
	if project == "" {
		return nil, nil, &APIError{Status: http.StatusBadRequest, Code: "PROJECT_REQUIRED", Message: "--project <project> is required to promote a brain channel"}
	}
	var raw json.RawMessage
	path := "/api/v1/projects/" + url.PathEscape(project) + "/brain/promote"
	if err := c.do(ctx, http.MethodPost, path, req, &raw); err != nil {
		return nil, nil, err
	}
	var out BrainPromoteResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, err
	}
	return &out, raw, nil
}

// BrainEdit queues an out-of-band brain edit from an instruction; returns {queued, job_id}.
func (c *Client) BrainEdit(ctx context.Context, instruction, project, tenant string) (json.RawMessage, error) {
	if err := requireTenantProject(project, tenant, "brain"); err != nil {
		return nil, err
	}
	return c.Raw(ctx, http.MethodPost, scopedTreePath(project, tenant, "/brain/edit", "/api/v1/brain/edit"), map[string]any{"instruction": instruction})
}

// BrainConsolidate queues the consolidation cron on demand; returns {queued, job_id}.
func (c *Client) BrainConsolidate(ctx context.Context, project, tenant string) (json.RawMessage, error) {
	if err := requireTenantProject(project, tenant, "brain"); err != nil {
		return nil, err
	}
	return c.Raw(ctx, http.MethodPost, scopedTreePath(project, tenant, "/brain/consolidate", "/api/v1/brain/consolidate"), map[string]any{})
}

func scopedTreePath(project, tenant, suffix, fallback string) string {
	if project == "" {
		return fallback
	}
	path := "/api/v1/projects/" + url.PathEscape(project)
	if tenant != "" {
		path += "/tenants/" + url.PathEscape(tenant)
	}
	return path + suffix
}

func requireTenantProject(project, tenant, resource string) error {
	if tenant != "" && project == "" {
		return fmt.Errorf("--project <project> is required with --tenant for %s", resource)
	}
	return nil
}

// --- Run feedback / retry (POST /api/v1/runs/{id}/{feedback,retry}) ---

// RunFeedback posts a score/comment on a run's trace.
func (c *Client) RunFeedback(ctx context.Context, id string, body map[string]any, project, tenant string) (json.RawMessage, error) {
	return c.RawScoped(ctx, http.MethodPost, "/api/v1/runs/"+url.PathEscape(id)+"/feedback", body, project, tenant)
}

// RunRetry re-runs a run (optionally at a different tier); returns the new run id.
func (c *Client) RunRetry(ctx context.Context, id string, body map[string]any, project, tenant string) (json.RawMessage, error) {
	return c.RawScoped(ctx, http.MethodPost, "/api/v1/runs/"+url.PathEscape(id)+"/retry", body, project, tenant)
}

// ProcessInboxThread resumes a triage-skipped or security-blocked thread through the canonical project tree.
func (c *Client) ProcessInboxThread(ctx context.Context, id, project, tenant string) (json.RawMessage, error) {
	if project == "" {
		return nil, fmt.Errorf("--project <project> is required to process an inbox thread")
	}
	path := scopedTreePath(project, tenant, "/inbox/threads/"+url.PathEscape(id)+"/process", "")
	return c.Raw(ctx, http.MethodPost, path, map[string]any{})
}

// --- Database controls (GET/PATCH /api/v1/databases/{dsn}/controls) ---

// DatabaseControls fetches a database's controls sub-resource.
func (c *Client) DatabaseControls(ctx context.Context, dsn, project, tenant string) (json.RawMessage, error) {
	return c.RawScoped(ctx, http.MethodGet, "/api/v1/databases/"+url.PathEscape(dsn)+"/controls", nil, project, tenant)
}

// SetDatabaseControls patches a database's controls sub-resource (sparse).
func (c *Client) SetDatabaseControls(ctx context.Context, dsn string, body map[string]any, project, tenant string) (json.RawMessage, error) {
	return c.RawScoped(ctx, http.MethodPatch, "/api/v1/databases/"+url.PathEscape(dsn)+"/controls", body, project, tenant)
}

// ScopePreview mints the scoped views a real run of (tenant, principal) would see and returns per-table
// counts + sample rows + compiled predicates. The tenant + principal ride the BODY (the scoped identity),
// not the query — only project resolution uses ?project=. Returns raw + typed for -o json / table.
func (c *Client) ScopePreview(ctx context.Context, dsn string, body map[string]any, project string) (*ScopePreviewReport, json.RawMessage, error) {
	raw, err := c.RawScoped(ctx, http.MethodPost, "/api/v1/databases/"+url.PathEscape(dsn)+"/scope-preview", body, project, "")
	if err != nil {
		return nil, nil, err
	}
	var out ScopePreviewReport
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, raw, err
	}
	return &out, raw, nil
}

// --- Admin (box-level: /api/v1/admin/{users,projects,catalog}) ---
// These bypass the generic collection routes and require a global-admin token. No project scope —
// they're box-wide.

// AdminList GETs an admin collection (users/projects/catalog).
func (c *Client) AdminList(ctx context.Context, resource string) (*ListResponse, json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v1/admin/"+resource, nil, &raw); err != nil {
		return nil, nil, err
	}
	var out ListResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, err
	}
	return &out, raw, nil
}

// AdminCreate POSTs to an admin collection, returning the echoed item + raw body.
func (c *Client) AdminCreate(ctx context.Context, resource string, body map[string]any) (Item, json.RawMessage, error) {
	return c.itemWrite(ctx, http.MethodPost, "/api/v1/admin/"+resource, body)
}

// AdminUpdate PATCHes /api/v1/admin/{resource}/{id} (sparse), returning the updated item + raw body.
func (c *Client) AdminUpdate(ctx context.Context, resource, id string, body map[string]any) (Item, json.RawMessage, error) {
	return c.itemWrite(ctx, http.MethodPatch, "/api/v1/admin/"+resource+"/"+url.PathEscape(id), body)
}

// doRaw issues one request with an explicit Content-Type and a pre-built body (the multipart path),
// returning the verbatim 2xx body bytes. It reuses attempt's bearer auth + 401-retry by calling the
// shared low-level helper.
func (c *Client) doRaw(ctx context.Context, method, path, contentType string, reqBody []byte) (json.RawMessage, error) {
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return nil, err
	}
	resp, data, err := c.attemptCT(ctx, method, path, contentType, reqBody, token)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		if newToken, rerr := c.tokens.Refresh(ctx); rerr == nil && newToken != "" && newToken != token {
			resp, data, err = c.attemptCT(ctx, method, path, contentType, reqBody, newToken)
			if err != nil {
				return nil, err
			}
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeAPIError(resp.StatusCode, method, path, c.baseURL, data)
	}
	return json.RawMessage(data), nil
}

// attemptCT is attempt with a caller-supplied Content-Type (so the multipart boundary rides along),
// instead of the hard-coded application/json.
func (c *Client) attemptCT(ctx context.Context, method, path, contentType string, reqBody []byte, token string) (*http.Response, []byte, error) {
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
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("request %s %s (base %s): %w", method, path, c.baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("read response: %w", err)
	}
	return resp, data, nil
}
