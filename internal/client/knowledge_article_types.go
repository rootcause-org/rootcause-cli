// Wire contract for the help-centre article write path (`replypen: helpcenter/v1` blocks applied
// against Help Scout / Intercom / KnowledgeOwl). Field names and omitempty MUST match the server
// verbatim; the ground rules live in types.go.
package client

import "encoding/json"

type KnowledgeArticleApplyRequest struct {
	Markdown string `json:"markdown"`
	DryRun   bool   `json:"dry_run"`
	Publish  bool   `json:"publish"`
}

// KnowledgeArticleRef identifies the article the block resolved to. DraftSaved marks the Help Scout
// draft lane: the live article was left untouched.
type KnowledgeArticleRef struct {
	ID         string `json:"id"`
	Number     string `json:"number,omitempty"`
	URL        string `json:"url,omitempty"`
	Title      string `json:"title,omitempty"`
	Status     string `json:"status,omitempty"`
	DraftSaved bool   `json:"draft_saved,omitempty"`
}

// KnowledgeArticleProviderRequest is one provider call: what WOULD be sent on a dry run, what WAS
// sent on a real write. Auth headers are never part of it.
type KnowledgeArticleProviderRequest struct {
	Method string          `json:"method"`
	URL    string          `json:"url"`
	Body   json.RawMessage `json:"body,omitempty"`
}

type KnowledgeArticleResync struct {
	Queued bool   `json:"queued"`
	JobID  string `json:"job_id,omitempty"`
}

// KnowledgeArticleApplyResponse is the apply verdict. Resync/AuditID appear only after a real write
// that actually changed something; Changed=false means nothing was sent and Requests is empty.
type KnowledgeArticleApplyResponse struct {
	Op       string                            `json:"op"`
	Provider string                            `json:"provider"`
	DryRun   bool                              `json:"dry_run"`
	Changed  bool                              `json:"changed"`
	Article  KnowledgeArticleRef               `json:"article"`
	Changes  []string                          `json:"changes,omitempty"`
	Requests []KnowledgeArticleProviderRequest `json:"requests,omitempty"`
	Resync   *KnowledgeArticleResync           `json:"resync,omitempty"`
	AuditID  string                            `json:"audit_id,omitempty"`
}

// KnowledgeArticleGetResponse carries the round-trippable block: Markdown is a `replypen:
// helpcenter/v1` document with `op: update`, ready to edit and feed back to apply.
type KnowledgeArticleGetResponse struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
	URL      string `json:"url,omitempty"`
	Status   string `json:"status,omitempty"`
	HasDraft bool   `json:"has_draft"`
	Markdown string `json:"markdown"`
}
