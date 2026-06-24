package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// FeedParams are the query filters for the windowed feed endpoints (/runs/events, /runs/egress). Zero
// values are omitted so the server applies its defaults. Before is the keyset cursor (a run id) — the
// CLI loops on it internally; a caller never sets it. Project is the explicit scope an all-projects admin
// token names per request (the `--all` fan-out); a pinned token ignores it server-side.
type FeedParams struct {
	Days    int
	Kind    string
	Limit   int
	Before  string
	Project string
}

// query renders the params into a URL query string (leading "?" when non-empty), so the JSON-passthrough
// path in the commands builds the identical URL the typed fetch hits.
func (p FeedParams) query() string {
	q := url.Values{}
	if p.Days > 0 {
		q.Set("days", strconv.Itoa(p.Days))
	}
	if p.Kind != "" {
		q.Set("kind", p.Kind)
	}
	if p.Limit > 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	if p.Before != "" {
		q.Set("before", p.Before)
	}
	if p.Project != "" {
		q.Set("project", p.Project)
	}
	if enc := q.Encode(); enc != "" {
		return "?" + enc
	}
	return ""
}

// EventsPath / EgressPath / HealthPath build the request URL for the JSON-passthrough path — the same
// URL the typed fetchers hit, so `-o json` and the table view can never diverge on what was requested.
func EventsPath(p FeedParams) string { return "/api/v1/runs/events" + p.query() }
func EgressPath(p FeedParams) string { return "/api/v1/runs/egress" + p.query() }

// HealthPath builds GET /api/v1/health?hours=&project= — project is the explicit scope an all-projects
// admin token names (the `--all` fan-out); "" omits it (a pinned token's own scope).
func HealthPath(hours int, project string) string {
	q := url.Values{}
	if hours > 0 {
		q.Set("hours", strconv.Itoa(hours))
	}
	if project != "" {
		q.Set("project", project)
	}
	if enc := q.Encode(); enc != "" {
		return "/api/v1/health?" + enc
	}
	return "/api/v1/health"
}

// maxFeedPages caps how many pages a feed loop fetches before giving up — a backstop against an
// unbounded crawl on a huge fleet. At the server's 2000-row page cap this is ~1M rows, far past any
// sane window; the cap is reported (never silent) so the caller can warn.
const maxFeedPages = 500

// EventsPage fetches ONE page of GET /api/v1/runs/events (the caller drives the cursor). Used by both
// the paging loop and—via Raw in the command—the JSON passthrough.
func (c *Client) EventsPage(ctx context.Context, p FeedParams) (*RunEventsResponse, error) {
	var out RunEventsResponse
	if err := c.do(ctx, http.MethodGet, EventsPath(p), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// EgressPage fetches ONE page of GET /api/v1/runs/egress.
func (c *Client) EgressPage(ctx context.Context, p FeedParams) (*EgressResponse, error) {
	var out EgressResponse
	if err := c.do(ctx, http.MethodGet, EgressPath(p), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AllEvents pages /runs/events until the window is exhausted (no next_before) or the page cap trips. It
// returns the accumulated rows and capped=true when it stopped at the cap (the caller warns to stderr —
// no silent truncation). The cursor threading is internal: a caller asks for the whole window.
func (c *Client) AllEvents(ctx context.Context, p FeedParams) (rows []RunEvent, capped bool, err error) {
	p.Before = ""
	for page := 0; page < maxFeedPages; page++ {
		resp, e := c.EventsPage(ctx, p)
		if e != nil {
			return nil, false, e
		}
		rows = append(rows, resp.Events...)
		if resp.NextBefore == "" {
			return rows, false, nil
		}
		p.Before = resp.NextBefore
	}
	return rows, true, nil
}

// AllEgress pages /runs/egress until the window is exhausted or the page cap trips (same contract as
// AllEvents).
func (c *Client) AllEgress(ctx context.Context, p FeedParams) (rows []EgressRow, capped bool, err error) {
	p.Before = ""
	for page := 0; page < maxFeedPages; page++ {
		resp, e := c.EgressPage(ctx, p)
		if e != nil {
			return nil, false, e
		}
		rows = append(rows, resp.Egress...)
		if resp.NextBefore == "" {
			return rows, false, nil
		}
		p.Before = resp.NextBefore
	}
	return rows, true, nil
}

// AllRuns pages GET /api/v1/runs (the run index, reused by `rc fleet`) until next_before runs out or the
// page cap trips. It accumulates the safe per-run rows AND keeps the FIRST page's summary (the
// server-computed health rollup over the most recent window) for the digest header.
func (c *Client) AllRuns(ctx context.Context, p RunsParams) (runs []RunSummary, capped bool, err error) {
	p.Before = ""
	for page := 0; page < maxFeedPages; page++ {
		resp, e := c.Runs(ctx, p)
		if e != nil {
			return nil, false, e
		}
		runs = append(runs, resp.Runs...)
		if resp.NextBefore == "" {
			return runs, false, nil
		}
		p.Before = resp.NextBefore
	}
	return runs, true, nil
}

// ThreadTracePath builds the request URL for GET /api/v1/threads/{id}/trace — the same URL the typed
// fetch hits, so `-o json` passthrough and the table view can never diverge on what was requested.
// project is the explicit scope an all-projects admin token names via --project; "" omits it (a pinned
// token's own scope, where the server disregards the param).
func ThreadTracePath(id, project string) string {
	path := "/api/v1/threads/" + url.PathEscape(id) + "/trace"
	if project != "" {
		path += "?project=" + url.QueryEscape(project)
	}
	return path
}

// ThreadTrace fetches GET /api/v1/threads/{id}/trace — every run for one thread (or session) id. Used by
// the table view of `rc thread`; the JSON path goes through Raw (ThreadTracePath) to keep the passthrough
// byte-faithful (render, don't reshape). project is the optional --project scope (see ThreadTracePath).
func (c *Client) ThreadTrace(ctx context.Context, id, project string) (*ThreadTrace, error) {
	var out ThreadTrace
	if err := c.do(ctx, http.MethodGet, ThreadTracePath(id, project), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Health fetches GET /api/v1/health?hours=&project= — the raw health inputs the CLI rolls up. project is
// the explicit scope an all-projects admin token names (the `--all` fan-out); "" is a pinned token's own.
func (c *Client) Health(ctx context.Context, hours int, project string) (*HealthResponse, error) {
	var out HealthResponse
	if err := c.do(ctx, http.MethodGet, HealthPath(hours, project), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
