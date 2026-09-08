package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// MaxKnowledgeArticleMarkdownBytes mirrors the server's cap on an apply body, so an oversized file
// fails locally with a clear message instead of as a 400 after the upload.
const MaxKnowledgeArticleMarkdownBytes = 512 << 10

// KnowledgeArticleApply sends one `replypen: helpcenter/v1` block to the project's help centre. The
// tenant twin of the route is the tenant's own help centre, so both selectors ride the tree path.
func (c *Client) KnowledgeArticleApply(ctx context.Context, project, tenant string, req KnowledgeArticleApplyRequest) (*KnowledgeArticleApplyResponse, json.RawMessage, error) {
	if err := requireTenantProject(project, tenant, "knowledge articles"); err != nil {
		return nil, nil, err
	}
	if project == "" {
		return nil, nil, &APIError{Status: http.StatusBadRequest, Code: "PROJECT_REQUIRED", Message: "knowledge article commands require a project scope"}
	}
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodPost, treePath(project, tenant, "/knowledge/articles/apply"), req, &raw); err != nil {
		return nil, nil, err
	}
	var out KnowledgeArticleApplyResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, raw, err
	}
	return &out, raw, nil
}

// KnowledgeArticleGet fetches one live article rendered back as an applyable block.
func (c *Client) KnowledgeArticleGet(ctx context.Context, project, tenant, provider, id string) (*KnowledgeArticleGetResponse, json.RawMessage, error) {
	if err := requireTenantProject(project, tenant, "knowledge articles"); err != nil {
		return nil, nil, err
	}
	if project == "" {
		return nil, nil, &APIError{Status: http.StatusBadRequest, Code: "PROJECT_REQUIRED", Message: "knowledge article commands require a project scope"}
	}
	suffix := "/knowledge/articles/" + url.PathEscape(provider) + "/" + url.PathEscape(id)
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, treePath(project, tenant, suffix), nil, &raw); err != nil {
		return nil, nil, err
	}
	var out KnowledgeArticleGetResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, raw, err
	}
	return &out, raw, nil
}
