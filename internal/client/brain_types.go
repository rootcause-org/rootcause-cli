// Wire contract for the brain plane (status/sync, promote/preflight, render, mirrors, developer access).
// Field names and omitempty MUST match the server verbatim; the ground rules live in types.go.
package client

type BrainStatus struct {
	Available bool                 `json:"available"`
	Dir       string               `json:"dir,omitempty"`
	Ref       string               `json:"ref"`
	LocalSHA  string               `json:"local_sha,omitempty"`
	RemoteSHA string               `json:"remote_sha,omitempty"`
	Ahead     int                  `json:"ahead"`
	Behind    int                  `json:"behind"`
	Dirty     bool                 `json:"dirty"`
	Stale     bool                 `json:"stale"`
	State     string               `json:"state"`
	SyncedAt  string               `json:"synced_at,omitempty"`
	Message   string               `json:"message,omitempty"`
	Channels  []BrainChannelStatus `json:"channels,omitempty"`
	// BootCheck is the server's last brain boot check (run-view symlinks + projection spec/compile) for
	// this brain. Nil when no candidate commit has been checked yet — a failing check keeps the box's
	// local main at the last-good commit instead of advancing it.
	BootCheck *BrainBootCheck `json:"boot_check,omitempty"`
}

// BrainBootCheck is one brain boot-check verdict. Reason is populated only when OK is false.
type BrainBootCheck struct {
	SHA       string `json:"sha"`
	OK        bool   `json:"ok"`
	Reason    string `json:"reason,omitempty"`
	CheckedAt string `json:"checked_at"`
}

type BrainChannelStatus struct {
	Channel       string `json:"channel"`
	ResolvedSHA   string `json:"resolved_sha,omitempty"`
	OriginSHA     string `json:"origin_sha,omitempty"`
	MainSHA       string `json:"main_sha,omitempty"`
	MatchesOrigin bool   `json:"matches_origin"`
	MatchesMain   bool   `json:"matches_main"`
	State         string `json:"state"`
	Provenance    string `json:"provenance,omitempty"`
}

type BrainStatusResponse struct {
	Project string      `json:"project"`
	Status  BrainStatus `json:"status"`
}

type BrainSyncResult struct {
	Before              BrainStatus `json:"before"`
	After               BrainStatus `json:"after"`
	Fetched             bool        `json:"fetched"`
	FastForwarded       bool        `json:"fast_forwarded"`
	ManualReconcile     bool        `json:"manual_reconcile"`
	RefreshedWorkspaces int         `json:"refreshed_workspaces,omitempty"`
	// Refused is set when the server rejected the sync because the candidate commit failed its boot
	// check; local main stays on the last-good commit and Message carries the reason.
	Refused bool   `json:"refused,omitempty"`
	Message string `json:"message,omitempty"`
}

type BrainSyncResponse struct {
	Project string          `json:"project"`
	Sync    BrainSyncResult `json:"sync"`
}

type BrainPromoteRequest struct {
	Channel string `json:"channel"`
	SHA     string `json:"sha"`
}

type BrainPromoteResponse struct {
	Project    string `json:"project"`
	Channel    string `json:"channel"`
	OldSHA     string `json:"old_sha"`
	NewSHA     string `json:"new_sha"`
	Changed    bool   `json:"changed"`
	Idempotent bool   `json:"idempotent"`
}

// BrainPreflightRequest dry-runs a promotion: which candidate commit, onto which managed channel. The
// server never resolves a ref for us — SHA is the exact 40-character commit, as for a real promote.
type BrainPreflightRequest struct {
	Channel string `json:"channel"`
	SHA     string `json:"sha"`
}

type BrainPreflightResponse struct {
	Project string      `json:"project"`
	Canary  BrainCanary `json:"canary"`
}

// BrainCanary is the server's verdict on a candidate: would it degrade or break any tenant pinned to the
// channel? Keys and reasons only — a tenant VALUE never crosses this wire.
type BrainCanary struct {
	Channel string `json:"channel"`
	SHA     string `json:"sha"`
	OK      bool   `json:"ok"`
	// Templated is false when the candidate carries no projection.yaml (nothing to break).
	Templated bool `json:"templated"`
	// Consumers is the active tenant pin count. Checked may be zero for an untemplated brain even when
	// consumers is positive, because there is no projection to compile.
	Consumers int `json:"consumers"`
	Checked   int `json:"checked"`
	// Skipped counts tenants excluded from this channel's set: on the other channel, frozen at an exact
	// SHA, or not active.
	Skipped int                 `json:"skipped"`
	Tenants []BrainCanaryTenant `json:"tenants"`
	// Error is a candidate-wide failure no single tenant owns (an unparseable projection.yaml).
	Error string `json:"error"`
	// Note explains a trivially-passing verdict (untemplated brain, or nobody pinned to the channel).
	Note string `json:"note"`
}

type BrainCanaryTenant struct {
	Tenant string `json:"tenant"`
	// Status is ok | degraded | failed; anything but ok would block a promotion.
	Status       string                   `json:"status"`
	Degraded     []BrainCanaryDegradation `json:"degraded"`
	FilesDropped int                      `json:"files_dropped"`
	Error        string                   `json:"error"`
}

type BrainCanaryDegradation struct {
	Key    string `json:"key"`
	File   string `json:"file"`
	Reason string `json:"reason"`
}

// BrainRenderRequest compiles ONE tenant's projection of a brain commit, in memory — the on-box cache is
// never touched. Sha and Channel are mutually exclusive; omitting both means the tenant's current channel.
// Paths are brain-relative paths or globs (ignored when All).
type BrainRenderRequest struct {
	Tenant  string   `json:"tenant"`
	SHA     string   `json:"sha,omitempty"`
	Channel string   `json:"channel,omitempty"`
	Paths   []string `json:"paths,omitempty"`
	All     bool     `json:"all,omitempty"`
}

// BrainRenderFile is one compiled file exactly as /brain would mount it for this tenant.
type BrainRenderFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type BrainRenderStats struct {
	FilesRendered      int `json:"files_rendered"`
	FilesCopied        int `json:"files_copied"`
	FilesDropped       int `json:"files_dropped"`
	PlaceholdersFilled int `json:"placeholders_filled"`
	BranchesCollapsed  int `json:"branches_collapsed"`
}

// BrainRenderResponse carries the compiled files plus the same key/file/reason degradation shape the
// promote-time canary reports, so a degraded projection reads identically in preflight and render.
type BrainRenderResponse struct {
	Project      string                   `json:"project"`
	Tenant       string                   `json:"tenant"`
	SHA          string                   `json:"sha"`
	Channel      string                   `json:"channel"`
	Files        []BrainRenderFile        `json:"files"`
	Stats        BrainRenderStats         `json:"stats"`
	Degradations []BrainCanaryDegradation `json:"degradations"`
}

type MirrorRefreshRequest struct {
	Repo        string `json:"repo"`
	ExpectedSHA string `json:"expected_sha"`
}

type MirrorRefreshResponse struct {
	Project             string `json:"project"`
	Repo                string `json:"repo"`
	Branch              string `json:"branch"`
	ExpectedSHA         string `json:"expected_sha"`
	ActualSHA           string `json:"actual_sha"`
	Verified            bool   `json:"verified"`
	JobID               int64  `json:"job_id"`
	RefreshedWorkspaces int    `json:"refreshed_workspaces"`
}

// BrainDeveloperInvitationRequest grants one GitHub user access to one tenant brain repository.
// The server owns GitHub App credentials; rc only forwards the handle in the JSON body.
type BrainDeveloperInvitationRequest struct {
	GitHubHandle string `json:"github_handle"`
}

// BrainDeveloperInvitation is the idempotent access receipt returned by the tenant-brain endpoint.
// InvitationURL is empty once the developer already has active repository access.
type BrainDeveloperInvitation struct {
	Project       string `json:"project"`
	Tenant        string `json:"tenant"`
	Repository    string `json:"repository"`
	GitHubHandle  string `json:"github_handle"`
	Permission    string `json:"permission"`
	State         string `json:"state"`
	InvitationURL string `json:"invitation_url,omitempty"`
}
