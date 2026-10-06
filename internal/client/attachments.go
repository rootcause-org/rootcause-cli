package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// projectTreePath is the canonical /api/v1/projects/{p}[/tenants/{t}]<suffix> route; these endpoints
// have no flat current-project alias, so the caller must resolve the project first.
func projectTreePath(project, tenant, suffix string) (string, error) {
	if project == "" {
		return "", fmt.Errorf("--project <project> is required for an all-projects login")
	}
	p := "/api/v1/projects/" + url.PathEscape(project)
	if tenant != "" {
		p += "/tenants/" + url.PathEscape(tenant)
	}
	return p + suffix, nil
}

// SessionAttachments fetches a chat session's files.
func (c *Client) SessionAttachments(ctx context.Context, project, tenant, sessionID string) (*SessionAttachments, json.RawMessage, error) {
	path, err := projectTreePath(project, tenant, "/sessions/"+url.PathEscape(sessionID)+"/attachments")
	if err != nil {
		return nil, nil, err
	}
	return fetchBoth[SessionAttachments](ctx, c, http.MethodGet, path, nil)
}

// ResourceSessions fetches the chat sessions linked to one resource.
func (c *Client) ResourceSessions(ctx context.Context, project, tenant, kind, resourceID string) (*ResourceSessions, json.RawMessage, error) {
	path, err := projectTreePath(project, tenant, "/resources/"+url.PathEscape(kind)+"/"+url.PathEscape(resourceID)+"/sessions")
	if err != nil {
		return nil, nil, err
	}
	return fetchBoth[ResourceSessions](ctx, c, http.MethodGet, path, nil)
}

// DownloadAttachment streams one file (variant "" = stored, AttachmentVariantOriginal = as uploaded)
// into w and returns the server's X-Content-SHA256 ("" when absent). A 410 surfaces as an APIError
// with AttachmentExpiredCode.
func (c *Client) DownloadAttachment(ctx context.Context, project, tenant, sessionID, attachmentID, variant string, w io.Writer) (string, error) {
	path, err := projectTreePath(project, tenant, "/sessions/"+url.PathEscape(sessionID)+"/attachments/"+url.PathEscape(attachmentID))
	if err != nil {
		return "", err
	}
	if variant != "" {
		path += "?variant=" + url.QueryEscape(variant)
	}
	resp, err := c.openStream(ctx, sendSpec{method: http.MethodGet, path: path, accept: "application/octet-stream"})
	if err != nil {
		return "", err
	}
	sha := resp.Header.Get(AttachmentSHA256Header)
	return sha, copyResponse(resp, w)
}
