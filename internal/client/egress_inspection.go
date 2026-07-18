package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

type HTTPAuditParams struct {
	Days     int
	Limit    int
	Cursor   string
	RunID    string
	Host     string
	Source   string
	Method   string
	Decision string
	Project  string
	Tenant   string
}

func (p HTTPAuditParams) query() string {
	q := url.Values{}
	if p.Days > 0 {
		q.Set("days", strconv.Itoa(p.Days))
	}
	if p.Limit > 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	if p.Cursor != "" {
		q.Set("cursor", p.Cursor)
	}
	if p.RunID != "" {
		q.Set("run_id", p.RunID)
	}
	if p.Host != "" {
		q.Set("host", p.Host)
	}
	if p.Source != "" {
		q.Set("source", p.Source)
	}
	if p.Method != "" {
		q.Set("method", p.Method)
	}
	if p.Decision != "" {
		q.Set("decision", p.Decision)
	}
	if p.Project != "" {
		q.Set("project", p.Project)
	}
	if p.Tenant != "" {
		q.Set("tenant", p.Tenant)
	}
	if encoded := q.Encode(); encoded != "" {
		return "?" + encoded
	}
	return ""
}

func APILogPath(p HTTPAuditParams) string { return "/api/v1/api-log" + p.query() }

func RunEgressPath(id, project, tenant string) string {
	return collectionScopePath("/api/v1/runs/"+url.PathEscape(id)+"/egress", project, tenant)
}

func RunActionsPath(id string, p HTTPAuditParams) string {
	base := "/api/v1/runs/" + url.PathEscape(id) + "/actions"
	return base + p.query()
}

func (c *Client) RunEgress(ctx context.Context, id, project, tenant string) (*RunEgressResponse, error) {
	var out RunEgressResponse
	if err := c.do(ctx, http.MethodGet, RunEgressPath(id, project, tenant), nil, &out); err != nil {
		return nil, err
	}
	// The convenience endpoint caps its embedded HTTP slice. Finish through the paged project feed
	// whenever the server says it truncated, preserving a complete per-run inspection contract.
	if out.HTTPTruncated {
		rows, capped, err := c.AllHTTPAudit(ctx, HTTPAuditParams{RunID: id, Project: project, Tenant: tenant})
		if err != nil {
			return nil, err
		}
		out.HTTP = rows
		out.HTTPNextCursor = ""
		out.HTTPTruncated = capped
	}
	return &out, nil
}

func (c *Client) HTTPAuditPage(ctx context.Context, p HTTPAuditParams) (*HTTPAuditResponse, error) {
	var out HTTPAuditResponse
	if err := c.do(ctx, http.MethodGet, APILogPath(p), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) AllHTTPAudit(ctx context.Context, p HTTPAuditParams) (rows []HTTPAuditRow, capped bool, err error) {
	p.Cursor = ""
	for page := 0; page < maxFeedPages; page++ {
		resp, e := c.HTTPAuditPage(ctx, p)
		if e != nil {
			return nil, false, e
		}
		rows = append(rows, resp.Items...)
		if resp.NextCursor == "" {
			return rows, false, nil
		}
		p.Cursor = resp.NextCursor
	}
	return rows, true, nil
}

func (c *Client) ActionHistoryPage(ctx context.Context, id string, p HTTPAuditParams) (*ActionHistoryResponse, error) {
	var out ActionHistoryResponse
	if err := c.do(ctx, http.MethodGet, RunActionsPath(id, p), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) AllActionHistory(ctx context.Context, id string, p HTTPAuditParams) (rows []ActionHistoryRow, capped bool, err error) {
	p.Cursor = ""
	for page := 0; page < maxFeedPages; page++ {
		resp, e := c.ActionHistoryPage(ctx, id, p)
		if e != nil {
			return nil, false, e
		}
		rows = append(rows, resp.Items...)
		if resp.NextCursor == "" {
			return rows, false, nil
		}
		p.Cursor = resp.NextCursor
	}
	return rows, true, nil
}
