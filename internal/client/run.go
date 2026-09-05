package client

import (
	"context"
	"encoding/json"
	"net/http"
)

// The `…WithRaw` siblings below return BOTH the typed value (for the table renderers) and the server's
// verbatim body (for `-o json`), so one `rc run <view> <id>` invocation makes exactly one HTTP request
// and the JSON output can never drop a field the CLI's structs don't know about yet.

// RunWithRaw fetches GET /api/v1/runs/{id} — typed detail + verbatim body.
func (c *Client) RunWithRaw(ctx context.Context, id, project, tenant string) (*RunDetail, json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, RunPath(id, project, tenant), nil, &raw); err != nil {
		return nil, nil, err
	}
	var out RunDetail
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, err
	}
	return &out, raw, nil
}

// FullWithRaw fetches GET /api/v1/runs/{id}/trace — typed bundle + verbatim body. `rc run trace`
// renders the typed bundle on a TTY and decomposes the RAW bytes into JSONL for `-o json`; `rc run
// guards` projects `.run.guards` out of the same raw body.
func (c *Client) FullWithRaw(ctx context.Context, id, project, tenant string) (*FullResponse, json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, RunTracePath(id, project, tenant), nil, &raw); err != nil {
		return nil, nil, err
	}
	var out FullResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, err
	}
	return &out, raw, nil
}

// BrainDiffWithRaw fetches GET /api/v1/runs/{id}/brain-diff — typed commit + verbatim body.
func (c *Client) BrainDiffWithRaw(ctx context.Context, id, project, tenant string) (*BrainDiff, json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, RunBrainDiffPath(id, project, tenant), nil, &raw); err != nil {
		return nil, nil, err
	}
	var out BrainDiff
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, err
	}
	return &out, raw, nil
}

// RunEgressWithRaw fetches GET /api/v1/runs/{id}/egress — typed response + verbatim body. When the
// convenience endpoint truncated its embedded HTTP slice we finish through the paged project feed
// (same contract as RunEgress) and splice the completed rows back into the raw body, so the JSON
// output stays both complete and faithful to every other server field.
func (c *Client) RunEgressWithRaw(ctx context.Context, id, project, tenant string) (*RunEgressResponse, json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, RunEgressPath(id, project, tenant), nil, &raw); err != nil {
		return nil, nil, err
	}
	var out RunEgressResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, err
	}
	if !out.HTTPTruncated {
		return &out, raw, nil
	}
	rows, meta, err := c.AllHTTPAudit(ctx, HTTPAuditParams{RunID: id, Project: project, Tenant: tenant})
	if err != nil {
		return nil, nil, err
	}
	out.HTTP = rows
	out.HTTPNextCursor = ""
	out.HTTPTruncated = meta.Capped
	spliced, err := spliceRunEgress(raw, out)
	if err != nil {
		return nil, nil, err
	}
	return &out, spliced, nil
}

// spliceRunEgress rewrites only the three HTTP-paging keys of the raw body; every other server field
// keeps its original bytes.
func spliceRunEgress(raw json.RawMessage, out RunEgressResponse) (json.RawMessage, error) {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	httpRows, err := json.Marshal(out.HTTP)
	if err != nil {
		return nil, err
	}
	truncated, err := json.Marshal(out.HTTPTruncated)
	if err != nil {
		return nil, err
	}
	fields["http"] = httpRows
	fields["http_truncated"] = truncated
	delete(fields, "http_next_cursor")
	return json.Marshal(fields)
}
