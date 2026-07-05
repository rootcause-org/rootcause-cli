package client

import (
	"context"
	"encoding/json"
	"net/http"
)

type ConnectionProbeRequest struct {
	Capability string `json:"capability"`
	Label      string `json:"label,omitempty"`
	Write      bool   `json:"write,omitempty"`
	NotionPage string `json:"notion_page,omitempty"`
	Cleanup    bool   `json:"cleanup,omitempty"`
}

type ConnectionProbeResult struct {
	OK         bool             `json:"ok"`
	Capability ProbeCapability  `json:"capability"`
	Grant      ProbeGrant       `json:"grant"`
	Action     ProbeActionPlane `json:"action_plane"`
	Provider   *ProbeProvider   `json:"provider,omitempty"`
	Steps      []ProbeStep      `json:"steps"`
	Warnings   []string         `json:"warnings,omitempty"`
	raw        json.RawMessage
}

type ProbeCapability struct {
	Key      string   `json:"key"`
	Name     string   `json:"name,omitempty"`
	Platform string   `json:"platform"`
	Tier     string   `json:"tier"`
	Scopes   []string `json:"scopes"`
}

type ProbeGrant struct {
	Found bool   `json:"found"`
	Label string `json:"label"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type ProbeActionPlane struct {
	Enabled                 bool   `json:"enabled"`
	Mode                    string `json:"mode"`
	Status                  string `json:"status"`
	RunnerURLConfigured     bool   `json:"runner_url_configured"`
	ReverseSecretConfigured bool   `json:"reverse_secret_configured"`
}

type ProbeProvider struct {
	Name     string `json:"name"`
	Write    bool   `json:"write"`
	ReadBack bool   `json:"read_back"`
	Cleanup  bool   `json:"cleanup"`
	ObjectID string `json:"object_id,omitempty"`
	Error    string `json:"error,omitempty"`
}

type ProbeStep struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

func (r *ConnectionProbeResult) Raw() json.RawMessage {
	if r == nil {
		return nil
	}
	return r.raw
}

func (c *Client) ConnectionProbe(ctx context.Context, req ConnectionProbeRequest, project, tenant string) (*ConnectionProbeResult, json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodPost, "/api/v1/connections/probe"+collectionScope(project, tenant), req, &raw); err != nil {
		return nil, nil, err
	}
	var out ConnectionProbeResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, err
	}
	out.raw = raw
	return &out, raw, nil
}
