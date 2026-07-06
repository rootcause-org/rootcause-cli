package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// SpamRule is one row of a project's spam allow/block list — the "never spam" / "always spam" plane
// separate from the drafting sender lists. Field names mirror the server's spamRuleItem exactly (the
// CLI renders what the server sends; it never reshapes). Created is optional: it only appears in the
// table when the server includes it.
type SpamRule struct {
	ID        string `json:"id"`
	Verdict   string `json:"verdict"` // "allow" | "block"
	MatchType string `json:"match_type"`
	Pattern   string `json:"pattern"`
	Reason    string `json:"reason,omitempty"`
	Source    string `json:"source,omitempty"`
	Tenant    string `json:"tenant,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

// spamPath builds /api/v1/projects/{project}[/tenants/{slug}]/spam/{list}[/{id}]. The spam endpoints
// address the project (and tenant) IN THE PATH — unlike the ?project=/?tenant= collection endpoints —
// so this joins the segments and path-escapes every dynamic one.
func spamPath(project, tenant, list, id string) string {
	p := "/api/v1/projects/" + url.PathEscape(project)
	if tenant != "" {
		p += "/tenants/" + url.PathEscape(tenant)
	}
	p += "/spam/" + list
	if id != "" {
		p += "/" + url.PathEscape(id)
	}
	return p
}

// SpamList fetches GET on one spam list ("allows" or "blocks"), returning the parsed rows for the table
// and the raw body for -o json passthrough.
func (c *Client) SpamList(ctx context.Context, project, tenant, list string) ([]SpamRule, json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, spamPath(project, tenant, list, ""), nil, &raw); err != nil {
		return nil, nil, err
	}
	return decodeSpamRules(raw), raw, nil
}

// SpamCreate posts a {pattern, reason} body to one spam list; the server infers match_type from the
// pattern shape. Returns the echoed rule + raw body.
func (c *Client) SpamCreate(ctx context.Context, project, tenant, list, pattern, reason string) (*SpamRule, json.RawMessage, error) {
	body := map[string]any{"pattern": pattern}
	if reason != "" {
		body["reason"] = reason
	}
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodPost, spamPath(project, tenant, list, ""), body, &raw); err != nil {
		return nil, nil, err
	}
	var rule SpamRule
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &rule)
	}
	return &rule, raw, nil
}

// SpamDelete sends DELETE on one spam list row by id. A 204/empty body is fine; any returned body rides
// back raw for the -o json path.
func (c *Client) SpamDelete(ctx context.Context, project, tenant, list, id string) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodDelete, spamPath(project, tenant, list, id), nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// decodeSpamRules accepts either a bare JSON array of rules or a single-key envelope ({"allows":[…]} /
// {"blocks":[…]}), tolerating whichever list shape the server settles on.
func decodeSpamRules(raw json.RawMessage) []SpamRule {
	var direct []SpamRule
	if json.Unmarshal(raw, &direct) == nil {
		return direct
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return nil
	}
	for _, v := range obj {
		var items []SpamRule
		if json.Unmarshal(v, &items) == nil && len(items) > 0 {
			return items
		}
	}
	return nil
}
