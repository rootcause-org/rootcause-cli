// Wire contract for watched mailboxes and the IMAP connect probe.
// Field names and omitempty MUST match the server verbatim; the ground rules live in types.go.
package client

// WatchedMailbox is one row of GET /api/v1/projects/{project}/mailboxes — a connection-backed mailbox the channel
// plane actively watches. Field
// names mirror the server verbatim. Tenant/SubscriptionExpiresAt/ErrorMessage are omitempty: absent for
// a non-tenant mailbox / a provider without a renewable subscription / a healthy mailbox.
type WatchedMailbox struct {
	ID           string `json:"id"`
	Provider     string `json:"provider"`
	EmailAddress string `json:"email_address"`
	// awaiting_credential = seeded without a password; the customer still has to open its password link.
	Status                string `json:"status"` // active|paused|connected|needs_attention|awaiting_credential
	Mode                  string `json:"mode"`   // off|watch|shadow|live
	Tenant                string `json:"tenant,omitempty"`
	ProcessingEnabled     bool   `json:"processing_enabled"` // false = silent onboarding: polled, not processed
	HasSyncCursor         bool   `json:"has_sync_cursor"`
	SubscriptionExpiresAt string `json:"subscription_expires_at,omitempty"`
	ErrorMessage          string `json:"error_message,omitempty"`
	// Ingest liveness — the only health signal a POLL mailbox (IMAP) has, since it never carries a
	// subscription expiry. Empty LastSuccessfulSyncAt means it has not completed a sync yet.
	LastSuccessfulSyncAt    string `json:"last_successful_sync_at,omitempty"`
	ConsecutiveSyncFailures int    `json:"consecutive_sync_failures"`
	// Probe rides along on the connect + probe responses only, never on a list row.
	Probe *IMAPProbe `json:"probe,omitempty"`
	// PasswordLink rides along on the seed + password-link responses only. It is a no-login URL that can
	// only SET this mailbox's password, never read it — safe to forward to whoever holds the credential.
	PasswordLink string `json:"password_link,omitempty"`
}

// IMAPProbeStep is one stage of the live IMAP/SMTP connection check. Detail is a caller-safe hint that
// names the fix, not the transport error.
type IMAPProbeStep struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok | failed | warning | skipped
	Detail string `json:"detail,omitempty"`
}

// IMAPProbe is the connection checklist. OK is false only when a REQUIRED stage failed — a warning
// (typically: drafts cannot be placed) reports a limitation without blocking the mailbox.
type IMAPProbe struct {
	OK    bool            `json:"ok"`
	Steps []IMAPProbeStep `json:"steps"`
}

// WatchedMailboxList is GET /api/v1/projects/{project}/mailboxes — the watched-mailbox set under its envelope key.
type WatchedMailboxList struct {
	Mailboxes []WatchedMailbox `json:"mailboxes"`
}
