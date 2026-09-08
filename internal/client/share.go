// Wire contract for the SHARE-TOKEN endpoints: the account-less read plane behind a run link
// (`/runs/<id>?t=…`) or a chat share link (`/s/<token>`). The token in the link IS the credential —
// these calls carry no bearer, so the transport must be handed an empty-token source.
//
// Shapes are deliberately the SAME as their authenticated twins (FullResponse, ThreadTrace), so every
// consumer — render, debugdump — works unchanged on a shared run.
package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// SharedRunTracePath is GET /api/v1/shared/runs/{id}/trace — the detail-tier FullResponse.
func SharedRunTracePath(id, token string) string {
	return "/api/v1/shared/runs/" + url.PathEscape(id) + "/trace?t=" + url.QueryEscape(token)
}

// SharedRunThreadPath is GET /api/v1/shared/runs/{id}/thread — the ThreadTrace around a shared run.
func SharedRunThreadPath(id, token string) string {
	return "/api/v1/shared/runs/" + url.PathEscape(id) + "/thread?t=" + url.QueryEscape(token)
}

// SharedSessionRunsPath is GET /api/v1/shared/sessions/{token}/runs — the ThreadTrace envelope for a
// shared chat session, plus its session id and title.
func SharedSessionRunsPath(token string) string {
	return "/api/v1/shared/sessions/" + url.PathEscape(token) + "/runs"
}

// SharedTranscriptPath is GET /s/{token}/transcript — the shared chat session's replayed messages. It
// is an APP route, not /api/v1: the share page itself reads it.
func SharedTranscriptPath(token string) string {
	return "/s/" + url.PathEscape(token) + "/transcript"
}

// SharedRunTrace fetches the shared run bundle. Same shape as Full — the debug decomposer can't tell.
func (c *Client) SharedRunTrace(ctx context.Context, id, token string) (*FullResponse, json.RawMessage, error) {
	return fetchBoth[FullResponse](ctx, c, http.MethodGet, SharedRunTracePath(id, token), nil)
}

// SharedRunThread fetches the thread trace around a shared run. Run rows additionally carry ShareURL.
func (c *Client) SharedRunThread(ctx context.Context, id, token string) (*ThreadTrace, json.RawMessage, error) {
	return fetchBoth[ThreadTrace](ctx, c, http.MethodGet, SharedRunThreadPath(id, token), nil)
}

// SharedSessionRuns fetches every run behind a shared chat session.
func (c *Client) SharedSessionRuns(ctx context.Context, token string) (*SharedSession, json.RawMessage, error) {
	return fetchBoth[SharedSession](ctx, c, http.MethodGet, SharedSessionRunsPath(token), nil)
}

// SharedTranscript fetches the shared chat session's messages.
func (c *Client) SharedTranscript(ctx context.Context, token string) (*SharedTranscript, json.RawMessage, error) {
	return fetchBoth[SharedTranscript](ctx, c, http.MethodGet, SharedTranscriptPath(token), nil)
}

// SharedSession is the ThreadTrace envelope of GET /api/v1/shared/sessions/{token}/runs plus the two
// session-only fields.
type SharedSession struct {
	ThreadTrace
	SessionID string `json:"session_id,omitempty"`
	Title     string `json:"title,omitempty"`
}

// SharedTranscript is GET /s/{token}/transcript. Messages mirror the host's chat.MessageWire.
type SharedTranscript struct {
	Title    string              `json:"title"`
	Messages []TranscriptMessage `json:"messages"`
}

// TranscriptMessage is one replayed chat message. CreatedAt is optional: MessageWire carries `seq` as
// the ordering fact and a timestamp only where the server chose to add one — render it when present
// rather than inventing an order.
type TranscriptMessage struct {
	ID        string           `json:"id"`
	Role      string           `json:"role"`
	Seq       int64            `json:"seq"`
	RunID     string           `json:"run_id,omitempty"`
	CreatedAt string           `json:"created_at,omitempty"`
	Parts     []TranscriptPart `json:"parts"`
}

// TranscriptPart is one part of a message. Only `text` and `file` have a readable projection; every
// other persisted type (the data-* cards) renders as a one-line placeholder, so a new server-side card
// degrades instead of disappearing.
type TranscriptPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Filename string `json:"filename,omitempty"`
	Title    string `json:"title,omitempty"`
}
