package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// DreamEvidenceParams are the filters of GET /api/v1/dream/evidence — the raw learning evidence
// (feedback, sent deltas, shadow verdicts, triage). Zero values are omitted so the server applies its
// defaults. ShadowSet distinguishes "don't filter" from an explicit --shadow=false.
type DreamEvidenceParams struct {
	Project       string
	Tenant        string
	Limit         int
	Days          int
	Plane         string
	Shadow        bool
	ShadowSet     bool
	Verdicts      []string
	IncludeBodies bool
}

// DreamEvidence fetches the evidence rows verbatim: there is no typed view — the planes are
// heterogeneous server-owned rows the CLI only passes through to `-o json`.
func (c *Client) DreamEvidence(ctx context.Context, p DreamEvidenceParams) (json.RawMessage, error) {
	q := url.Values{}
	if p.Project != "" {
		q.Set("project", p.Project)
	}
	if p.Tenant != "" {
		q.Set("tenant", p.Tenant)
	}
	if p.Limit > 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	if p.Days > 0 {
		q.Set("days", strconv.Itoa(p.Days))
	}
	if p.Plane != "" {
		q.Set("plane", p.Plane)
	}
	if p.ShadowSet {
		q.Set("shadow", strconv.FormatBool(p.Shadow))
	}
	if len(p.Verdicts) > 0 {
		q.Set("verdict", strings.Join(p.Verdicts, ","))
	}
	if p.IncludeBodies {
		q.Set("include_bodies", "true")
	}
	path := "/api/v1/dream/evidence"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	return c.raw(ctx, http.MethodGet, path, nil)
}
