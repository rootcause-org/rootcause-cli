package client

import (
	"context"
	"net/http"
	"net/url"
)

func consoleScope(project, tenant string) string {
	q := url.Values{}
	if project != "" {
		q.Set("project", project)
	}
	if tenant != "" {
		q.Set("tenant", tenant)
	}
	if enc := q.Encode(); enc != "" {
		return "?" + enc
	}
	return ""
}

func (c *Client) Capabilities(ctx context.Context, project, tenant string) (*CapabilitiesResponse, error) {
	var out CapabilitiesResponse
	err := c.do(ctx, http.MethodGet, "/api/v1/console/capabilities"+consoleScope(project, tenant), nil, &out)
	return &out, err
}

func (c *Client) DBList(ctx context.Context, project, tenant string) (*DBListResponse, error) {
	var out DBListResponse
	err := c.do(ctx, http.MethodGet, "/api/v1/console/db"+consoleScope(project, tenant), nil, &out)
	return &out, err
}

func (c *Client) DBSchema(ctx context.Context, db, table, project, tenant string) (*DBSchemaResponse, error) {
	q := url.Values{}
	if project != "" {
		q.Set("project", project)
	}
	if tenant != "" {
		q.Set("tenant", tenant)
	}
	if table != "" {
		q.Set("table", table)
	}
	path := "/api/v1/console/db/" + url.PathEscape(db) + "/schema"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	var out DBSchemaResponse
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return &out, err
}

func (c *Client) DBQuery(ctx context.Context, db string, req DBQueryRequest, project, tenant string) (*DBQueryResponse, error) {
	var out DBQueryResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/console/db/"+url.PathEscape(db)+"/query"+consoleScope(project, tenant), req, &out)
	return &out, err
}

func (c *Client) BashList(ctx context.Context, project, tenant string) (*BashListResponse, error) {
	var out BashListResponse
	err := c.do(ctx, http.MethodGet, "/api/v1/console/bash"+consoleScope(project, tenant), nil, &out)
	return &out, err
}

func (c *Client) ActionList(ctx context.Context, project, tenant string) (*ActionListResponse, error) {
	var out ActionListResponse
	err := c.do(ctx, http.MethodGet, "/api/v1/console/action"+consoleScope(project, tenant), nil, &out)
	return &out, err
}

func (c *Client) ActionShow(ctx context.Context, id, project, tenant string) (*ActionShowResponse, error) {
	var out ActionShowResponse
	err := c.do(ctx, http.MethodGet, "/api/v1/console/action/"+url.PathEscape(id)+consoleScope(project, tenant), nil, &out)
	return &out, err
}

func (c *Client) ActionPreflight(ctx context.Context, id string, req ActionExecRequest, project, tenant string) (*ActionExecResponse, error) {
	var out ActionExecResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/console/action/"+url.PathEscape(id)+"/preflight"+consoleScope(project, tenant), req, &out)
	return &out, err
}

func (c *Client) ActionRun(ctx context.Context, id string, req ActionExecRequest, project, tenant string) (*ActionExecResponse, error) {
	var out ActionExecResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/console/action/"+url.PathEscape(id)+"/run"+consoleScope(project, tenant), req, &out)
	return &out, err
}
