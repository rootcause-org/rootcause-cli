package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// This file maps the connection-backed WATCHED-mailbox endpoints — the channel plane's live inbox watch
// (list / pause / resume) — onto thin client calls. These are distinct from the generic /mailboxes
// collection (the legacy email-keyed routing table behind `rc mailbox route`): watched mailboxes are
// connection-backed and carry a subscription/sync-cursor lifecycle. Each method returns both the typed
// value (for the table view) and the raw body (for -o json passthrough — render, don't reshape).

// watchedScope appends the ?project= scope an all-projects admin token names per request; "" omits it
// (a pinned token's own scope, where the server disregards the param).
func watchedScope(path, project string) string {
	if project != "" {
		return path + "?project=" + url.QueryEscape(project)
	}
	return path
}

// WatchedMailboxes fetches GET /api/v1/mailboxes/watched — every connection-backed mailbox the channel
// plane watches. Returns the parsed list (for the table) and the raw body (for -o json passthrough).
func (c *Client) WatchedMailboxes(ctx context.Context, project string) (*WatchedMailboxList, json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, watchedScope("/api/v1/mailboxes/watched", project), nil, &raw); err != nil {
		return nil, nil, err
	}
	var out WatchedMailboxList
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, err
	}
	return &out, raw, nil
}

// PauseWatchedMailbox posts POST /api/v1/mailboxes/{id}/pause (no body) → the single updated item.
func (c *Client) PauseWatchedMailbox(ctx context.Context, id, project string) (*WatchedMailbox, json.RawMessage, error) {
	return c.watchedVerb(ctx, id, "pause", project)
}

// ResumeWatchedMailbox posts POST /api/v1/mailboxes/{id}/resume (no body) → the updated item. A
// Subscribe failure still returns 200 with status="needs_attention" + error_message (NOT an error — the
// CLI surfaces the message), so this is the success path for that case too.
func (c *Client) ResumeWatchedMailbox(ctx context.Context, id, project string) (*WatchedMailbox, json.RawMessage, error) {
	return c.watchedVerb(ctx, id, "resume", project)
}

func (c *Client) watchedVerb(ctx context.Context, id, verb, project string) (*WatchedMailbox, json.RawMessage, error) {
	path := watchedScope("/api/v1/mailboxes/"+url.PathEscape(id)+"/"+verb, project)
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodPost, path, nil, &raw); err != nil {
		return nil, nil, err
	}
	var out WatchedMailbox
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, err
	}
	return &out, raw, nil
}
