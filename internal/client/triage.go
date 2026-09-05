package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// The mail-triage policy + hard-rule endpoints. The rule/policy bodies are server-owned and freeform
// (the server validates the field set), so these methods carry bytes both ways: the CLI's whole job is
// to put the path/verb here and pass the response through to `-o json`.

// triagePath resolves the flat /api/v1/triage/* alias onto the canonical project tree when a scope is
// named. Tenant selection is never a flat query — the deprecated alias ignores it.
func triagePath(suffix, project, tenant string) (string, error) {
	if tenant != "" && project == "" {
		return "", fmt.Errorf("--project <project> is required with --tenant for triage")
	}
	if project == "" {
		return "/api/v1/triage" + suffix, nil
	}
	path := "/api/v1/projects/" + url.PathEscape(project)
	if tenant != "" {
		path += "/tenants/" + url.PathEscape(tenant)
	}
	return path + "/triage" + suffix, nil
}

func (c *Client) triage(ctx context.Context, method, suffix, project, tenant string, body map[string]any) (json.RawMessage, error) {
	path, err := triagePath(suffix, project, tenant)
	if err != nil {
		return nil, err
	}
	return c.Raw(ctx, method, path, body)
}

// TriagePolicy fetches the free-form triage guidance.
func (c *Client) TriagePolicy(ctx context.Context, project, tenant string) (json.RawMessage, error) {
	return c.triage(ctx, http.MethodGet, "/policy", project, tenant, nil)
}

// SetTriagePolicy replaces the free-form triage guidance.
func (c *Client) SetTriagePolicy(ctx context.Context, project, tenant, guidance string) (json.RawMessage, error) {
	return c.triage(ctx, http.MethodPatch, "/policy", project, tenant, map[string]any{"guidance": guidance})
}

// TriageRules lists the deterministic hard rules.
func (c *Client) TriageRules(ctx context.Context, project, tenant string) (json.RawMessage, error) {
	return c.triage(ctx, http.MethodGet, "/rules", project, tenant, nil)
}

// CreateTriageRule adds one hard rule.
func (c *Client) CreateTriageRule(ctx context.Context, project, tenant string, body map[string]any) (json.RawMessage, error) {
	return c.triage(ctx, http.MethodPost, "/rules", project, tenant, body)
}

// PatchTriageRule changes the named fields of one hard rule.
func (c *Client) PatchTriageRule(ctx context.Context, project, tenant, id string, body map[string]any) (json.RawMessage, error) {
	return c.triage(ctx, http.MethodPatch, "/rules/"+url.PathEscape(id), project, tenant, body)
}

// DeleteTriageRule removes one hard rule (an empty 2xx body is normal).
func (c *Client) DeleteTriageRule(ctx context.Context, project, tenant, id string) (json.RawMessage, error) {
	return c.triage(ctx, http.MethodDelete, "/rules/"+url.PathEscape(id), project, tenant, nil)
}
