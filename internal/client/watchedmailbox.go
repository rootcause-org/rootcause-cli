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

func watchedProjectPath(project, suffix string) string {
	if project != "" {
		return "/api/v1/projects/" + url.PathEscape(project) + suffix
	}
	return ""
}

// WatchedMailboxes fetches GET /api/v1/mailboxes/watched — every connection-backed mailbox the channel
// plane watches. Returns the parsed list (for the table) and the raw body (for -o json passthrough).
func (c *Client) WatchedMailboxes(ctx context.Context, project, tenant string) (*WatchedMailboxList, json.RawMessage, error) {
	if project == "" {
		return nil, nil, &APIError{Status: http.StatusBadRequest, Code: "PROJECT_REQUIRED", Message: "mailboxes require a project scope"}
	}
	if err := requireTenantProject(project, tenant, "mailboxes"); err != nil {
		return nil, nil, err
	}
	var raw json.RawMessage
	path := scopedTreePath(project, tenant, "/mailboxes", "")
	if err := c.do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, nil, err
	}
	var out WatchedMailboxList
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, err
	}
	return &out, raw, nil
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

type IMAPEnvResponse struct {
	MailboxID    string            `json:"mailbox_id"`
	EmailAddress string            `json:"email_address"`
	Env          map[string]string `json:"env"`
}

// ConnectIMAPMailbox posts POST /api/v1/mailboxes/imap/connect — the server live-probes IMAP login +
// SELECT INBOX + SMTP AUTH before persisting, so a bad config returns an error envelope
// (IMAP_PROBE_FAILED / BAD_IMAP_CONFIG) and nothing is saved; a duplicate is a 409 MAILBOX_IN_USE. On
// success it returns the created watched-mailbox item. The tenant (if any) rides in the body; `project`
// rides as ?project= for an all-projects admin token.
func (c *Client) ConnectIMAPMailbox(ctx context.Context, req IMAPConnectRequest, project string) (*WatchedMailbox, json.RawMessage, error) {
	path := watchedProjectPath(project, "/mailboxes/imap/connect")
	if path == "" {
		return nil, nil, &APIError{Status: http.StatusBadRequest, Code: "PROJECT_REQUIRED", Message: "connecting a mailbox requires a project scope"}
	}
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodPost, path, req, &raw); err != nil {
		return nil, nil, err
	}
	var out WatchedMailbox
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, err
	}
	return &out, raw, nil
}

// IMAPMailboxEnv fetches the scoped local-harvest env projection from the canonical project tree.
func (c *Client) IMAPMailboxEnv(ctx context.Context, id, project, tenant string) (*IMAPEnvResponse, json.RawMessage, error) {
	if project == "" {
		return nil, nil, &APIError{Status: http.StatusBadRequest, Code: "PROJECT_REQUIRED", Message: "IMAP env requires a project scope"}
	}
	if err := requireTenantProject(project, tenant, "mailboxes"); err != nil {
		return nil, nil, err
	}
	path := watchedProjectPath(project, "/mailboxes/"+url.PathEscape(id)+"/imap-env")
	path = collectionScopePath(path, "", tenant)
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, nil, err
	}
	var out IMAPEnvResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, err
	}
	return &out, raw, nil
}

func (c *Client) SetWatchedMailboxMode(ctx context.Context, id, mode, project, tenant string) (*WatchedMailbox, json.RawMessage, error) {
	path := watchedProjectPath(project, "/mailboxes/"+url.PathEscape(id)+"/mode")
	if path == "" {
		return nil, nil, &APIError{Status: http.StatusBadRequest, Code: "PROJECT_REQUIRED", Message: "mailbox mode requires a project scope"}
	}
	path = collectionScopePath(path, "", tenant)
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodPost, path, map[string]any{"mode": mode}, &raw); err != nil {
		return nil, nil, err
	}
	var out WatchedMailbox
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, err
	}
	return &out, raw, nil
}
