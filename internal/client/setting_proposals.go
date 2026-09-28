package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// SettingProposal mirrors the server's settingProposalItem. Payload stays raw: its shape is per-kind and
// the CLI only summarizes it for the table.
type SettingProposal struct {
	ID           string          `json:"id"`
	Level        string          `json:"level"`
	Tenant       string          `json:"tenant,omitempty"`
	MailboxID    string          `json:"mailbox_id,omitempty"`
	Kind         string          `json:"kind"`
	Status       string          `json:"status"`
	Source       string          `json:"source"`
	SourceRef    string          `json:"source_ref,omitempty"`
	Rationale    string          `json:"rationale,omitempty"`
	Payload      json.RawMessage `json:"payload"`
	CurrentValue *string         `json:"current_value,omitempty"`
	CreatedAt    string          `json:"created_at"`
	DecidedAt    string          `json:"decided_at,omitempty"`
	DecidedBy    string          `json:"decided_by,omitempty"`
	Error        string          `json:"error,omitempty"`
}

// settingProposalsPath builds /api/v1/projects/{project}[/tenants/{slug}]/settings-proposals[/suffix].
func settingProposalsPath(project, tenant, suffix string) string {
	p := "/api/v1/projects/" + url.PathEscape(project)
	if tenant != "" {
		p += "/tenants/" + url.PathEscape(tenant)
	}
	return p + "/settings-proposals" + suffix
}

// SettingProposals lists the review queue (pending first, then recently decided).
func (c *Client) SettingProposals(ctx context.Context, project, tenant string) ([]SettingProposal, json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, settingProposalsPath(project, tenant, ""), nil, &raw); err != nil {
		return nil, nil, err
	}
	var env struct {
		Items []SettingProposal `json:"items"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, raw, err
	}
	return env.Items, raw, nil
}

// SettingProposalGet reads one proposal.
func (c *Client) SettingProposalGet(ctx context.Context, project, tenant, id string) (json.RawMessage, error) {
	return c.raw(ctx, http.MethodGet, settingProposalsPath(project, tenant, "/"+url.PathEscape(id)), nil)
}

// SettingProposalDecide posts approve|reject; the settled row comes back (status applied|failed|rejected).
func (c *Client) SettingProposalDecide(ctx context.Context, project, tenant, id, verb, reason string) (*SettingProposal, json.RawMessage, error) {
	var body map[string]any
	if reason != "" {
		body = map[string]any{"reason": reason}
	}
	raw, err := c.raw(ctx, http.MethodPost, settingProposalsPath(project, tenant, "/"+url.PathEscape(id)+"/"+verb), body)
	if err != nil {
		return nil, nil, err
	}
	var rec SettingProposal
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, raw, err
	}
	return &rec, raw, nil
}
