package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// This file maps the server's generic collection resources (connections, repos, members, tokens) onto
// thin client calls. The CLI stays deliberately dumb about item shapes: an item is a flat JSON object
// keyed by field names + "id", and the renderer shows whatever keys the server returns (sorted). So a
// new server-side field on any collection appears with no CLI change — the same "render, don't reshape"
// invariant the settings bag follows. Every method returns the raw body bytes; the command layer either
// pretty-prints them (-o json) or hands them to the generic flat-item renderers.

// Item is one collection element: a flat object of field→value plus "id". Kept as a generic map so the
// CLI holds no per-resource struct — the server owns the field set.
type Item map[string]json.RawMessage

// ListResponse is a collection GET. The server wraps the array under its resource key (e.g.
// {"connections":[…]}) OR returns a bare array; UnmarshalJSON accepts both so the CLI doesn't hard-code
// the envelope. Items are kept generic (render whatever keys came back).
type ListResponse struct {
	Items []Item
}

// UnmarshalJSON accepts either a bare JSON array of items or a single-key object wrapping that array
// (the resource-keyed envelope), so one type serves every collection regardless of its wrapper.
func (l *ListResponse) UnmarshalJSON(data []byte) error {
	var arr []Item
	if err := json.Unmarshal(data, &arr); err == nil {
		l.Items = arr
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	for _, v := range obj {
		var items []Item
		if err := json.Unmarshal(v, &items); err == nil {
			l.Items = items
			return nil
		}
	}
	return nil // an object with no array value → empty list (the explicit no-rows case)
}

// collectionPath builds /api/v1/<resource>[/<id>][/<verb>] with the shared ?project=&tenant= scope. id
// and verb are optional; empty segments are skipped. id is path-escaped (a repo id is a name, a
// connection id a uuid).
func collectionPath(resource, id, verb, project, tenant string) string {
	p := "/api/v1/" + resource
	if id != "" {
		p += "/" + url.PathEscape(id)
	}
	if verb != "" {
		p += "/" + verb
	}
	return p + collectionScope(project, tenant)
}

func collectionScope(project, tenant string) string {
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

// List fetches GET /api/v1/<resource>, returning both the parsed items (for the table renderer) and the
// raw body (for -o json passthrough).
func (c *Client) List(ctx context.Context, resource, project, tenant string) (*ListResponse, json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, collectionPath(resource, "", "", project, tenant), nil, &raw); err != nil {
		return nil, nil, err
	}
	var out ListResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, err
	}
	return &out, raw, nil
}

// Create posts POST /api/v1/<resource> with the supplied flat body, returning the echoed item (a
// connection create deliberately omits the secret) and the raw body.
func (c *Client) Create(ctx context.Context, resource string, body map[string]any, project, tenant string) (Item, json.RawMessage, error) {
	return c.itemWrite(ctx, http.MethodPost, collectionPath(resource, "", "", project, tenant), body)
}

// Patch sends PATCH /api/v1/<resource>/<id> (sparse) and returns the updated item + raw body.
func (c *Client) Patch(ctx context.Context, resource, id string, body map[string]any, project, tenant string) (Item, json.RawMessage, error) {
	return c.itemWrite(ctx, http.MethodPatch, collectionPath(resource, id, "", project, tenant), body)
}

// Verb posts POST /api/v1/<resource>/<id>/<verb> (reveal/rotate/revoke) with no body, returning the
// verb's flat response (e.g. {"secret":…}) + raw body.
func (c *Client) Verb(ctx context.Context, resource, id, verb, project, tenant string) (Item, json.RawMessage, error) {
	return c.itemWrite(ctx, http.MethodPost, collectionPath(resource, id, verb, project, tenant), nil)
}

// Delete sends DELETE /api/v1/<resource>/<id>. A 204/empty body is fine; any returned body is passed
// back raw for the -o json path.
func (c *Client) Delete(ctx context.Context, resource, id, project, tenant string) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodDelete, collectionPath(resource, id, "", project, tenant), nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// itemWrite is the shared POST/PATCH path: send body (may be nil), decode the flat item, keep raw bytes.
// A 204/empty response decodes to a nil item — fine for verbs that echo nothing.
func (c *Client) itemWrite(ctx context.Context, method, path string, body map[string]any) (Item, json.RawMessage, error) {
	var raw json.RawMessage
	var b any
	if body != nil {
		b = body
	}
	if err := c.do(ctx, method, path, b, &raw); err != nil {
		return nil, nil, err
	}
	var item Item
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &item) // a non-object (e.g. empty) leaves item nil; the renderer handles it
	}
	return item, raw, nil
}
