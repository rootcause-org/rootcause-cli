package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

type dbQueryStreamFrame struct {
	Type         string          `json:"type"`
	Project      string          `json:"project"`
	Tenant       string          `json:"tenant,omitempty"`
	DB           string          `json:"db"`
	RunID        string          `json:"run_id"`
	Columns      []string        `json:"columns"`
	ColumnInfo   []DBQueryColumn `json:"column_info,omitempty"`
	BatchSize    int             `json:"batch_size"`
	LimitClamped bool            `json:"limit_clamped,omitempty"`
	Row          []any           `json:"row"`
	RowCount     int             `json:"row_count"`
	DurationMs   int64           `json:"duration_ms"`
	Truncated    bool            `json:"truncated"`
	Error        struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Status  int    `json:"status"`
	} `json:"error"`
}

// DBQueryStream consumes the all=true NDJSON frame protocol. A complete response has exactly one header,
// zero or more rows, and one final meta frame whose row count must match what crossed the wire.
func (c *Client) DBQueryStream(
	ctx context.Context,
	db string,
	req DBQueryRequest,
	project, tenant string,
	onHeader func(*DBQueryStreamHeader) error,
	onRow func([]any) error,
) (*DBQueryStreamMeta, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}
	path := "/api/v1/console/db/" + url.PathEscape(db) + "/query" + consoleScope(project, tenant)
	resp, err := c.openStream(ctx, http.MethodPost, path, body, "application/x-ndjson")
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	headerSeen := false
	rowCount := 0
	for {
		var frame dbQueryStreamFrame
		if err := dec.Decode(&frame); err != nil {
			_ = resp.Body.Close()
			if err == io.EOF {
				return nil, &TransportError{Err: fmt.Errorf("incomplete query stream: missing final meta frame after %d rows", rowCount)}
			}
			return nil, &TransportError{Err: fmt.Errorf("decode query stream after %d rows: %w", rowCount, err)}
		}
		switch frame.Type {
		case "header":
			if headerSeen || rowCount != 0 {
				_ = resp.Body.Close()
				return nil, &TransportError{Err: fmt.Errorf("invalid query stream: duplicate or misplaced header")}
			}
			headerSeen = true
			header := &DBQueryStreamHeader{
				Project: frame.Project, Tenant: frame.Tenant, DB: frame.DB, RunID: frame.RunID,
				Columns: frame.Columns, ColumnInfo: frame.ColumnInfo, BatchSize: frame.BatchSize,
				LimitClamped: frame.LimitClamped,
			}
			if err := onHeader(header); err != nil {
				_ = resp.Body.Close()
				return nil, err
			}
		case "row":
			if !headerSeen {
				_ = resp.Body.Close()
				return nil, &TransportError{Err: fmt.Errorf("invalid query stream: row before header")}
			}
			if err := onRow(frame.Row); err != nil {
				_ = resp.Body.Close()
				return nil, err
			}
			rowCount++
		case "meta":
			if !headerSeen {
				_ = resp.Body.Close()
				return nil, &TransportError{Err: fmt.Errorf("invalid query stream: meta before header")}
			}
			if frame.Truncated {
				_ = resp.Body.Close()
				return nil, &TransportError{Err: fmt.Errorf("invalid query stream: all=true ended truncated")}
			}
			if frame.RowCount != rowCount {
				_ = resp.Body.Close()
				return nil, &TransportError{Err: fmt.Errorf("incomplete query stream: received %d rows but final frame declared %d", rowCount, frame.RowCount)}
			}
			var extra any
			if err := dec.Decode(&extra); err != io.EOF {
				_ = resp.Body.Close()
				if err == nil {
					return nil, &TransportError{Err: fmt.Errorf("invalid query stream: frame after final meta")}
				}
				return nil, &TransportError{Err: fmt.Errorf("decode query stream trailer: %w", err)}
			}
			if err := resp.Body.Close(); err != nil {
				return nil, &TransportError{Err: fmt.Errorf("close query stream: %w", err)}
			}
			return &DBQueryStreamMeta{RowCount: frame.RowCount, DurationMs: frame.DurationMs, Truncated: false}, nil
		case "error":
			_ = resp.Body.Close()
			return nil, &APIError{
				Status: frame.Error.Status, Code: frame.Error.Code, Message: frame.Error.Message,
				Method: http.MethodPost, Path: path, BaseURL: c.baseURL,
			}
		default:
			_ = resp.Body.Close()
			return nil, &TransportError{Err: fmt.Errorf("invalid query stream frame type %q", frame.Type)}
		}
	}
}

func (c *Client) BashList(ctx context.Context, project, tenant string) (*BashListResponse, error) {
	var out BashListResponse
	err := c.do(ctx, http.MethodGet, "/api/v1/console/bash"+consoleScope(project, tenant), nil, &out)
	return &out, err
}

func (c *Client) BashRun(ctx context.Context, req BashRunRequest, project, tenant string) (*BashRunResponse, error) {
	var out BashRunResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/console/bash/run"+consoleScope(project, tenant), req, &out)
	return &out, err
}

func (c *Client) FileGet(ctx context.Context, remote, project, tenant string, w io.Writer) error {
	q := url.Values{}
	q.Set("path", remote)
	if project != "" {
		q.Set("project", project)
	}
	if tenant != "" {
		q.Set("tenant", tenant)
	}
	return c.Download(ctx, "/api/v1/console/file?"+q.Encode(), w)
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

func (c *Client) ActionProbe(ctx context.Context, project string) (*ActionProbeResponse, json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodPost, bagURL("/api/v1/action/probe", project), map[string]any{}, &raw); err != nil {
		return nil, nil, err
	}
	var out ActionProbeResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, err
	}
	return &out, raw, nil
}
