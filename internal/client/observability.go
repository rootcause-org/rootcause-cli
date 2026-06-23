package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// FeedParams are the query filters for the windowed feed endpoints (/runs/events, /runs/egress). Zero
// values are omitted so the server applies its defaults. Before is the keyset cursor (a run id) — the
// CLI loops on it internally; a caller never sets it.
type FeedParams struct {
	Days   int
	Kind   string
	Limit  int
	Before string
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
	if enc := q.Encode(); enc != "" {
		return "?" + enc
	}
	return ""
}

// EventsPath / EgressPath / HealthPath build the request URL for the JSON-passthrough path — the same
// URL the typed fetchers hit, so `-o json` and the table view can never diverge on what was requested.
func EventsPath(p FeedParams) string { return "/api/v1/runs/events" + p.query() }
func EgressPath(p FeedParams) string { return "/api/v1/runs/egress" + p.query() }
func HealthPath(hours int) string {
	if hours > 0 {
		return "/api/v1/health?hours=" + strconv.Itoa(hours)
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

// Health fetches GET /api/v1/health?hours= — the raw health inputs the CLI rolls up.
func (c *Client) Health(ctx context.Context, hours int) (*HealthResponse, error) {
	var out HealthResponse
	if err := c.do(ctx, http.MethodGet, HealthPath(hours), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
