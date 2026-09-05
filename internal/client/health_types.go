// Wire contract for the project health roll-up (mirrors, mailboxes, dead letters, brain boot).
// Field names and omitempty MUST match the server verbatim; the ground rules live in types.go.
package client

// HealthMirror is one raw mirror_health row from GET /api/v1/health — the input `rc fleet health` applies its
// staleness/state rule to. HoursSinceOK is nil when the mirror never succeeded (the CLI renders "never").
type HealthMirror struct {
	Repo                string   `json:"repo"`
	State               string   `json:"state"`
	ConsecutiveFailures int32    `json:"consecutive_failures"`
	LastOkAt            string   `json:"last_ok_at,omitempty"`
	LastError           string   `json:"last_error,omitempty"`
	HoursSinceOK        *float64 `json:"hours_since_ok"`
}

// HealthMailbox is one watched mailbox row from GET /api/v1/health whose watch needs attention.
type HealthMailbox struct {
	ID                        string `json:"id"`
	Provider                  string `json:"provider"`
	EmailAddress              string `json:"email_address"`
	Status                    string `json:"status"`
	Tenant                    string `json:"tenant,omitempty"`
	SubscriptionExpiresAt     string `json:"subscription_expires_at,omitempty"`
	SpamSubscriptionExpiresAt string `json:"spam_subscription_expires_at,omitempty"`
	ErrorMessage              string `json:"error_message,omitempty"`
	UpdatedAt                 string `json:"updated_at,omitempty"`
	// Ingest liveness. HoursSinceSync is nil when the mailbox never completed a sync — "no signal yet",
	// which is not the same as stale.
	LastSuccessfulSyncAt    string   `json:"last_successful_sync_at,omitempty"`
	HoursSinceSync          *float64 `json:"hours_since_sync"`
	ConsecutiveSyncFailures int      `json:"consecutive_sync_failures"`
}

// HealthDeadLetter is one terminally dead-lettered run from GET /api/v1/health.
type HealthDeadLetter struct {
	RunID      string `json:"run_id"`
	Kind       string `json:"kind"`
	Error      string `json:"error"`
	FinishedAt string `json:"finished_at,omitempty"`
}

// HealthBrainBoot is the latest brain boot check for ONE brain in GET /api/v1/health — the project
// brain (Tenant empty) or a tenant brain overlay. Reason is populated only when OK is false.
type HealthBrainBoot struct {
	Tenant    string `json:"tenant,omitempty"`
	SHA       string `json:"sha"`
	OK        bool   `json:"ok"`
	Reason    string `json:"reason,omitempty"`
	CheckedAt string `json:"checked_at,omitempty"`
}

// HealthResponse is GET /api/v1/health — the RAW health inputs; the CLI decides healthy/unhealthy.
type HealthResponse struct {
	WindowHours  int                `json:"window_hours"`
	Mirrors      []HealthMirror     `json:"mirrors"`
	BrainBoot    []HealthBrainBoot  `json:"brain_boot"`
	Mailboxes    []HealthMailbox    `json:"mailboxes"`
	DeadLettered []HealthDeadLetter `json:"dead_lettered"`
}
