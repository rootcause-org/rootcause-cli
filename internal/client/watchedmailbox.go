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

// SetWatchedMailboxProcessing posts POST /api/v1/mailboxes/{id}/processing/{enable,disable} (no body) →
// the updated item. This is the silent-onboarding gate, orthogonal to pause/resume (the watch
// lifecycle): a watching mailbox can still be held silent.
func (c *Client) SetWatchedMailboxProcessing(ctx context.Context, id string, enabled bool, project string) (*WatchedMailbox, json.RawMessage, error) {
	verb := "processing/disable"
	if enabled {
		verb = "processing/enable"
	}
	return c.watchedVerb(ctx, id, verb, project)
}

// IMAPConnectRequest is the POST /api/v1/mailboxes/imap/connect body. Field names mirror the server's
// imapConnectRequest verbatim. Ports are omitempty so 0 lets the server apply its defaults (993/implicit
// IMAP, 587/starttls SMTP); optional SMTP overrides fall back server-side to the IMAP username/password.
type IMAPConnectRequest struct {
	Tenant       string `json:"tenant,omitempty"`
	EmailAddress string `json:"email_address"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	IMAPHost     string `json:"imap_host"`
	IMAPPort     int    `json:"imap_port,omitempty"`
	IMAPTLS      string `json:"imap_tls,omitempty"`
	SMTPHost     string `json:"smtp_host,omitempty"`
	SMTPPort     int    `json:"smtp_port,omitempty"`
	SMTPTLS      string `json:"smtp_tls,omitempty"`
	SMTPUsername string `json:"smtp_username,omitempty"`
	SMTPPassword string `json:"smtp_password,omitempty"`
}

// ConnectIMAPMailbox posts POST /api/v1/mailboxes/imap/connect — the server live-probes IMAP login +
// SELECT INBOX + SMTP AUTH before persisting, so a bad config returns an error envelope
// (IMAP_PROBE_FAILED / BAD_IMAP_CONFIG) and nothing is saved; a duplicate is a 409 MAILBOX_IN_USE. On
// success it returns the created watched-mailbox item. The tenant (if any) rides in the body; `project`
// rides as ?project= for an all-projects admin token.
func (c *Client) ConnectIMAPMailbox(ctx context.Context, req IMAPConnectRequest, project string) (*WatchedMailbox, json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodPost, watchedScope("/api/v1/mailboxes/imap/connect", project), req, &raw); err != nil {
		return nil, nil, err
	}
	var out WatchedMailbox
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, err
	}
	return &out, raw, nil
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
