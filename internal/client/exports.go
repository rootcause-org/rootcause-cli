package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// This file maps the local-synthesis harvest/export endpoints (start a mailbox harvest, list/read the
// resulting exports, download the Markdown corpus) onto thin client calls. The list/read paths return
// both the typed value (table view) and the raw body (-o json passthrough — render, don't reshape).
// The download is the one non-JSON body: it's raw Markdown, so it goes through attemptRaw with an
// Accept: text/markdown request and still decodes the JSON error envelope on a non-2xx.

// HarvestRequest is the POST /mailboxes/{id}/harvest body. Clean is a pointer so nil omits the field
// (server default true); MaxThreads omits at 0 (server default).
type HarvestRequest struct {
	Clean      *bool `json:"clean,omitempty"`
	MaxThreads int   `json:"max_threads,omitempty"`
}

// StartHarvest posts POST /api/v1/projects/{project}/mailboxes/{id}/harvest → the 202 accept body
// {export_id, status}. It returns the typed accept AND the raw bytes so -o json echoes the verbatim
// server body. A 409 (HARVEST_IN_PROGRESS) surfaces as an APIError through the command layer.
func (c *Client) StartHarvest(ctx context.Context, mailboxID string, clean *bool, maxThreads int, project, tenant string) (*HarvestAccepted, json.RawMessage, error) {
	if err := requireTenantProject(project, tenant, "exports"); err != nil {
		return nil, nil, err
	}
	path := watchedProjectPath(project, "/mailboxes/"+url.PathEscape(mailboxID)+"/harvest")
	if path == "" {
		return nil, nil, &APIError{Status: http.StatusBadRequest, Code: "PROJECT_REQUIRED", Message: "starting a harvest requires a project scope"}
	}
	path = collectionScopePath(path, "", tenant)
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodPost, path, HarvestRequest{Clean: clean, MaxThreads: maxThreads}, &raw); err != nil {
		return nil, nil, err
	}
	var out HarvestAccepted
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, fmt.Errorf("decode harvest accept: %w", err)
	}
	return &out, raw, nil
}

// StartTemplates starts a Gmail canned-response export. The accepted handle is
// polled and downloaded through the same generic export endpoints as harvest.
func (c *Client) StartTemplates(ctx context.Context, mailboxID, project, tenant string) (*HarvestAccepted, json.RawMessage, error) {
	if err := requireTenantProject(project, tenant, "exports"); err != nil {
		return nil, nil, err
	}
	path := watchedProjectPath(project, "/mailboxes/"+url.PathEscape(mailboxID)+"/templates")
	if path == "" {
		return nil, nil, &APIError{Status: http.StatusBadRequest, Code: "PROJECT_REQUIRED", Message: "exporting templates requires a project scope"}
	}
	path = collectionScopePath(path, "", tenant)
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodPost, path, nil, &raw); err != nil {
		return nil, nil, err
	}
	var out HarvestAccepted
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, fmt.Errorf("decode templates accept: %w", err)
	}
	return &out, raw, nil
}

// MineSettings posts POST /api/v1/exports/{id}/mine-settings → the 202 accept body {export_id, status}:
// it enqueues a shallow-mining pass over the completed harvest's corpus (→ proposed persona/triage
// settings). Reuses HarvestAccepted (same {export_id,status} shape). A non-harvest / not-done / evicted
// body surfaces as a typed APIError through the command layer.
func (c *Client) MineSettings(ctx context.Context, id, project, tenant string) (*HarvestAccepted, json.RawMessage, error) {
	if err := requireTenantProject(project, tenant, "exports"); err != nil {
		return nil, nil, err
	}
	path := exportItemPath(id, "/mine-settings", project, tenant)
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodPost, path, nil, &raw); err != nil {
		return nil, nil, err
	}
	var out HarvestAccepted
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, fmt.Errorf("decode mine-settings accept: %w", err)
	}
	return &out, raw, nil
}

// Exports fetches GET /api/v1/exports → the export list (newest-first). Returns the typed list and the
// raw body for -o json passthrough.
func (c *Client) Exports(ctx context.Context, project, tenant string) (*ExportList, json.RawMessage, error) {
	if err := requireTenantProject(project, tenant, "exports"); err != nil {
		return nil, nil, err
	}
	var raw json.RawMessage
	path := scopedTreePath(project, tenant, "/exports", "/api/v1/exports"+collectionScope("", tenant))
	if err := c.do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, nil, err
	}
	var out ExportList
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, fmt.Errorf("decode exports: %w", err)
	}
	return &out, raw, nil
}

// Export fetches GET /api/v1/exports/{id} → one export item + the raw body for -o json.
func (c *Client) Export(ctx context.Context, id, project, tenant string) (*ExportItem, json.RawMessage, error) {
	if err := requireTenantProject(project, tenant, "exports"); err != nil {
		return nil, nil, err
	}
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, exportItemPath(id, "", project, tenant), nil, &raw); err != nil {
		return nil, nil, err
	}
	var out ExportItem
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, fmt.Errorf("decode export: %w", err)
	}
	return &out, raw, nil
}

// DownloadExport fetches GET /api/v1/exports/{id}/download → raw artifact bytes. This
// request marks the export consumed server-side. Unlike the JSON methods it sets Accept: text/markdown
// and returns the body bytes as-is; a non-2xx still decodes the JSON error envelope (e.g. 404
// BODY_UNAVAILABLE when the body isn't ready/was evicted).
func (c *Client) DownloadExport(ctx context.Context, id, project, tenant string) ([]byte, error) {
	if err := requireTenantProject(project, tenant, "exports"); err != nil {
		return nil, err
	}
	path := exportItemPath(id, "/download", project, tenant)
	return c.attemptRawWithRefresh(ctx, http.MethodGet, path, "text/markdown")
}

func exportItemPath(id, suffix, project, tenant string) string {
	path := watchedProjectPath(project, "/exports/"+url.PathEscape(id)+suffix)
	if path == "" {
		path = "/api/v1/exports/" + url.PathEscape(id) + suffix
	}
	return collectionScopePath(path, "", tenant)
}

// attemptRawWithRefresh sends one bodyless request with a caller-chosen Accept header (the download
// endpoints are GETs that don't want application/json), returning the raw 2xx body bytes. Auth,
// 401-refresh, retry policy and the typed APIError on a non-2xx come from the shared transport loop.
func (c *Client) attemptRawWithRefresh(ctx context.Context, method, path, accept string) ([]byte, error) {
	return c.fetch(ctx, sendSpec{method: method, path: path, accept: accept})
}
