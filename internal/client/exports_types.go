// Wire contract for harvest/export handles and their list items.
// Field names and omitempty MUST match the server verbatim; the ground rules live in types.go.
package client

// HarvestAccepted is the 202 body of POST /api/v1/projects/{project}/mailboxes/{id}/harvest — the queued export handle.
type HarvestAccepted struct {
	ExportID string `json:"export_id"`
	Status   string `json:"status"`
}

// ExportItem is one row of GET /api/v1/exports (and the whole of GET /api/v1/exports/{id}) — a
// local-synthesis corpus export (a harvest or a survey). Field names mirror the server verbatim; most
// counts/timestamps are omitempty (absent until the export runs/completes/is consumed). Truncated is
// always present (a harvest either hit its thread cap or didn't).
type ExportItem struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`             // harvest|survey|templates
	Format        string `json:"format,omitempty"` // retained artifact version; absent for surveys/unfinished legacy rows
	Status        string `json:"status"`           // pending|running|done|error|failed
	MailboxID     string `json:"mailbox_id"`
	Tenant        string `json:"tenant,omitempty"`
	Cleaned       *bool  `json:"cleaned,omitempty"`
	ThreadCount   *int   `json:"thread_count,omitempty"`
	TemplateCount *int   `json:"template_count,omitempty"`
	Truncated     bool   `json:"truncated"`
	CreatedAt     string `json:"created_at,omitempty"`
	CompletedAt   string `json:"completed_at,omitempty"`
	ConsumedAt    string `json:"consumed_at,omitempty"`
	Error         string `json:"error,omitempty"`
}

// Export aliases ExportItem for the single-item GET, matching the WatchedMailbox naming split.
type Export = ExportItem

// ExportList is GET /api/v1/exports — the exports (newest-first) under their envelope key.
type ExportList struct {
	Exports []ExportItem `json:"exports"`
}
