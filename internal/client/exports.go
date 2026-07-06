package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// StartHarvest posts POST /api/v1/mailboxes/{id}/harvest → the 202 accept body {export_id, status}. It
// returns the typed accept AND the raw bytes so -o json echoes the verbatim server body. A 409
// (HARVEST_IN_PROGRESS) surfaces as an APIError through the command layer.
func (c *Client) StartHarvest(ctx context.Context, mailboxID string, clean *bool, maxThreads int, project string) (*HarvestAccepted, json.RawMessage, error) {
	path := watchedScope("/api/v1/mailboxes/"+url.PathEscape(mailboxID)+"/harvest", project)
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

// Exports fetches GET /api/v1/exports → the export list (newest-first). Returns the typed list and the
// raw body for -o json passthrough.
func (c *Client) Exports(ctx context.Context, project string) (*ExportList, json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, watchedScope("/api/v1/exports", project), nil, &raw); err != nil {
		return nil, nil, err
	}
	var out ExportList
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, fmt.Errorf("decode exports: %w", err)
	}
	return &out, raw, nil
}

// Export fetches GET /api/v1/exports/{id} → one export item + the raw body for -o json.
func (c *Client) Export(ctx context.Context, id, project string) (*ExportItem, json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, watchedScope("/api/v1/exports/"+url.PathEscape(id), project), nil, &raw); err != nil {
		return nil, nil, err
	}
	var out ExportItem
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, fmt.Errorf("decode export: %w", err)
	}
	return &out, raw, nil
}

// DownloadExport fetches GET /api/v1/exports/{id}/download → the raw Markdown corpus bytes. This
// request marks the export consumed server-side. Unlike the JSON methods it sets Accept: text/markdown
// and returns the body bytes as-is; a non-2xx still decodes the JSON error envelope (e.g. 404
// BODY_UNAVAILABLE when the body isn't ready/was evicted).
func (c *Client) DownloadExport(ctx context.Context, id, project string) ([]byte, error) {
	path := watchedScope("/api/v1/exports/"+url.PathEscape(id)+"/download", project)
	return c.attemptRawWithRefresh(ctx, http.MethodGet, path, "text/markdown")
}

// attemptRawWithRefresh sends one request with the Accept header set to accept, returning the raw 2xx
// body bytes. On a 401 it forces a token refresh and retries once (the JSON `do` pattern, minus the
// body decode). A non-2xx decodes the JSON error envelope via decodeAPIError so a harvest error still
// surfaces as a typed APIError.
func (c *Client) attemptRawWithRefresh(ctx context.Context, method, path, accept string) ([]byte, error) {
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return nil, err
	}
	resp, data, err := c.attemptAccept(ctx, method, path, accept, token)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		if newToken, rerr := c.tokens.Refresh(ctx); rerr == nil && newToken != "" && newToken != token {
			resp, data, err = c.attemptAccept(ctx, method, path, accept, newToken)
			if err != nil {
				return nil, err
			}
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeAPIError(resp.StatusCode, method, path, c.baseURL, data)
	}
	return data, nil
}

// attemptAccept is attempt with a caller-chosen Accept header (attempt hardcodes application/json). No
// request body: the download endpoints are GETs.
func (c *Client) attemptAccept(ctx context.Context, method, path, accept, token string) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", accept)
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
