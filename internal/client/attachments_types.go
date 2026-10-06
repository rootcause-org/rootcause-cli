package client

// Wire contract of `rc run attachments`: a chat session's files and the resource → session link.
// Field names match the server verbatim; a shape change is a one-line fix here.

// SessionAttachments is GET /api/v1/projects/{p}[/tenants/{t}]/sessions/{session}/attachments: every
// file in the session (user uploads and assistant-generated), oldest first.
type SessionAttachments struct {
	SessionID        string           `json:"session_id"`
	Surface          string           `json:"surface,omitempty"`
	TenantSlug       string           `json:"tenant_slug,omitempty"`
	SessionExpiresAt *string          `json:"session_expires_at,omitempty"`
	Attachments      []ChatAttachment `json:"attachments"`
}

// ChatAttachment is one stored file. For images the base fields describe the processed 1536px JPEG;
// Original, when present, is the file as uploaded. ProcessedOnly marks a legacy image whose original
// was never kept. Available is the server's live blob probe (nil = not reported, try the download).
type ChatAttachment struct {
	AttachmentID  string              `json:"attachment_id"`
	MessageID     string              `json:"message_id,omitempty"`
	Origin        string              `json:"origin"`
	Filename      string              `json:"filename"`
	MimeType      string              `json:"mime_type,omitempty"`
	SizeBytes     int64               `json:"size_bytes"`
	SHA256        string              `json:"sha256,omitempty"`
	MediaMode     string              `json:"media_mode,omitempty"`
	CreatedAt     string              `json:"created_at,omitempty"`
	Available     *bool               `json:"available,omitempty"`
	ProcessedOnly bool                `json:"processed_only,omitempty"`
	Original      *AttachmentOriginal `json:"original,omitempty"`
}

// AttachmentOriginal is the as-uploaded variant of a processed image (?variant=original).
type AttachmentOriginal struct {
	Filename  string `json:"filename"`
	MimeType  string `json:"mime_type,omitempty"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256,omitempty"`
	Available *bool  `json:"available,omitempty"`
}

// ResourceSessions is GET /api/v1/projects/{p}[/tenants/{t}]/resources/{kind}/{id}/sessions: the chat
// sessions whose executed actions touched one resource (e.g. support_ticket:<uuid>).
type ResourceSessions struct {
	Sessions []ResourceSession `json:"sessions"`
}

type ResourceSession struct {
	SessionID        string `json:"session_id"`
	Surface          string `json:"surface,omitempty"`
	Title            string `json:"title,omitempty"`
	LinkedAt         string `json:"linked_at,omitempty"`
	SessionCreatedAt string `json:"session_created_at,omitempty"`
	LastActiveAt     string `json:"last_active_at,omitempty"`
}

const (
	AttachmentOriginUser = "user"
	// AttachmentVariantOriginal selects the as-uploaded bytes of a processed image.
	AttachmentVariantOriginal = "original"
	// AttachmentSHA256Header carries the stored digest on a download, checked against the bytes received.
	AttachmentSHA256Header = "X-Content-SHA256"
	// AttachmentExpiredCode is the 410 error code for a file whose blob has aged out of storage.
	AttachmentExpiredCode = "ATTACHMENT_EXPIRED"
)
