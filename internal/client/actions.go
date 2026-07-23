package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// ActionFeedParams are the server-side filters for the operator-only, cross-run action feed.
// Zero values are omitted so the server applies its defaults. Action and Status are repeatable,
// exact-match filters; values within each filter are ORed by the server.
type ActionFeedParams struct {
	Days     int
	Actions  []string
	Statuses []string
	Limit    int
	Cursor   string
	Project  string
	Tenant   string
}

func (p ActionFeedParams) query() string {
	q := url.Values{}
	if p.Days > 0 {
		q.Set("days", strconv.Itoa(p.Days))
	}
	for _, action := range p.Actions {
		// Preserve even an empty exact filter so a direct client caller can never accidentally broaden
		// action="" into the absent-filter "all actions" query. The server returns its validation error.
		q.Add("action", action)
	}
	for _, status := range p.Statuses {
		q.Add("status", status)
	}
	if p.Limit > 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	if p.Cursor != "" {
		q.Set("cursor", p.Cursor)
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

// ActionsPath builds GET /api/v1/actions with the same query used by ActionsPage.
func ActionsPath(p ActionFeedParams) string {
	return "/api/v1/actions" + p.query()
}

// ActionsPage fetches one keyset-paginated page of cross-run action history.
func (c *Client) ActionsPage(ctx context.Context, p ActionFeedParams) (*ActionFeedResponse, error) {
	var out ActionFeedResponse
	if err := c.do(ctx, http.MethodGet, ActionsPath(p), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AllActions fetches the complete requested window, following the server's opaque cursor unchanged.
// capped is true if the shared feed-page safety cap stopped the crawl before the final page.
func (c *Client) AllActions(ctx context.Context, p ActionFeedParams) (items []ActionFeedItem, capped bool, err error) {
	p.Cursor = ""
	for page := 0; page < maxFeedPages; page++ {
		resp, e := c.ActionsPage(ctx, p)
		if e != nil {
			return nil, false, e
		}
		items = append(items, resp.Items...)
		if resp.NextCursor == "" {
			return items, false, nil
		}
		p.Cursor = resp.NextCursor
	}
	return items, true, nil
}
