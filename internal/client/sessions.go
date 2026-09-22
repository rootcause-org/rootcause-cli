package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// SessionSummary is one chat CONVERSATION as GET /api/v1/sessions projects it: the rung above a run.
// Field names match the server verbatim. `shared` is a boolean by design — the share token itself is a
// live account-less credential and never rides a list.
type SessionSummary struct {
	SessionID      string           `json:"session_id"`
	TenantSlug     string           `json:"tenant_slug,omitempty"`
	Surface        string           `json:"surface"`
	Flavor         string           `json:"flavor,omitempty"`
	Origin         string           `json:"origin,omitempty"`
	Title          string           `json:"title,omitempty"`
	Status         string           `json:"status,omitempty"`
	Archived       bool             `json:"archived,omitempty"`
	CreatedAt      string           `json:"created_at"`
	LastActivityAt string           `json:"last_activity_at,omitempty"`
	Turns          int64            `json:"turns"`
	FirstQuestion  string           `json:"first_question,omitempty"`
	Principal      SessionPrincipal `json:"principal"`
	Outcomes       map[string]int   `json:"outcomes,omitempty"`
	WorstOutcome   string           `json:"worst_outcome,omitempty"`
	Feedback       *SessionFeedback `json:"feedback,omitempty"`
	Shared         bool             `json:"shared,omitempty"`
}

// SessionPrincipal names who held the conversation: the asserted end-user principal on the embed
// surface, or "member" + a user id on the dashboard. Never an email.
type SessionPrincipal struct {
	Kind string `json:"kind,omitempty"`
	ID   string `json:"id,omitempty"`
}

// SessionFeedback is the run_feedback rollup over the conversation's turns — aggregate only.
type SessionFeedback struct {
	Count      int64 `json:"count"`
	MinScore   int   `json:"min_score,omitempty"`
	MaxScore   int   `json:"max_score,omitempty"`
	HasComment bool  `json:"has_comment,omitempty"`
}

type SessionsResponse struct {
	Sessions   []SessionSummary `json:"sessions"`
	NextBefore string           `json:"next_before,omitempty"`
}

// SessionsParams are the query filters for GET /api/v1/sessions. Zero values are omitted so the server
// applies its own defaults (kind=chat, surface=all, limit 50).
type SessionsParams struct {
	Kind    string
	Days    int
	Surface string
	Limit   int
	Before  string
	Project string
	Tenant  string
}

// Sessions fetches GET /api/v1/sessions — the conversation index behind `rc run sessions`.
func (c *Client) Sessions(ctx context.Context, p SessionsParams) (*SessionsResponse, json.RawMessage, error) {
	q := url.Values{}
	for k, v := range map[string]string{
		"kind": p.Kind, "surface": p.Surface, "before": p.Before, "project": p.Project, "tenant": p.Tenant,
	} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if p.Days > 0 {
		q.Set("days", fmt.Sprintf("%d", p.Days))
	}
	if p.Limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", p.Limit))
	}
	path := "/api/v1/sessions"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	return fetchBoth[SessionsResponse](ctx, c, http.MethodGet, path, nil)
}
