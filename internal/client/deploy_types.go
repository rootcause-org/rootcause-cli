// Wire contract for deploy state (host, brain channels/promotions, mirrors).
// Field names and omitempty MUST match the server verbatim; the ground rules live in types.go.
package client

// DeployStateResponse is GET /api/v1/deploy-state — the live SHA per moving plane plus the timelines
// the server actually stores. There is no host deploy history server-side (the box only knows what it
// runs now), so `rc fleet deploy-state` derives "what is not deployed yet" from a local rootcause
// checkout instead.
type DeployStateResponse struct {
	Project       string              `json:"project"`
	GeneratedAt   string              `json:"generated_at"`
	HistoryLimit  int                 `json:"history_limit"`
	Host          DeployHost          `json:"host"`
	Brain         DeployBrain         `json:"brain"`
	Mirrors       []DeployMirror      `json:"mirrors"`
	MirrorHistory []DeployMirrorEvent `json:"mirror_history"`
}

// DeployHost is the running host build: Release is the short git SHA the container was built from
// (empty when the box never exported RELEASE).
type DeployHost struct {
	Release     string  `json:"release"`
	StartedAt   string  `json:"started_at,omitempty"`
	UptimeHours float64 `json:"uptime_hours"`
}

// DeployBrain is the project brain plane: the box's clone of main plus the channel pointers a run
// resolves, with the recorded promotion timeline.
type DeployBrain struct {
	Dir        string                 `json:"dir,omitempty"`
	State      string                 `json:"state"`
	MainSHA    string                 `json:"main_sha,omitempty"`
	OriginSHA  string                 `json:"origin_sha,omitempty"`
	SyncedAt   string                 `json:"synced_at,omitempty"`
	Channels   []DeployBrainChannel   `json:"channels"`
	Promotions []DeployBrainPromotion `json:"promotions"`
}

// DeployBrainChannel is one managed channel pointer (stable|edge) on the box.
type DeployBrainChannel struct {
	Channel       string `json:"channel"`
	ResolvedSHA   string `json:"resolved_sha,omitempty"`
	OriginSHA     string `json:"origin_sha,omitempty"`
	MainSHA       string `json:"main_sha,omitempty"`
	MatchesOrigin bool   `json:"matches_origin"`
	MatchesMain   bool   `json:"matches_main"`
	State         string `json:"state"`
}

// DeployBrainPromotion is one recorded channel promotion. Outcome "started" means the attempt never
// finished — its requested SHA is not proof of what went live.
type DeployBrainPromotion struct {
	Channel      string `json:"channel"`
	OldSHA       string `json:"old_sha,omitempty"`
	RequestedSHA string `json:"requested_sha,omitempty"`
	NewSHA       string `json:"new_sha,omitempty"`
	Outcome      string `json:"outcome"`
	Detail       string `json:"detail,omitempty"`
	Actor        string `json:"actor,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
	FinishedAt   string `json:"finished_at,omitempty"`
}

// DeployMirror is one mirror's currently checked-out source commit.
type DeployMirror struct {
	Repo        string `json:"repo"`
	Tenant      string `json:"tenant,omitempty"`
	State       string `json:"state"`
	SHA         string `json:"sha,omitempty"`
	Subject     string `json:"subject,omitempty"`
	CommittedAt string `json:"committed_at,omitempty"`
	LastOkAt    string `json:"last_ok_at,omitempty"`
}

// DeployMirrorEvent is one observed SHA change of a mirror — the refresh timeline.
type DeployMirrorEvent struct {
	Repo        string `json:"repo"`
	Tenant      string `json:"tenant,omitempty"`
	SHA         string `json:"sha"`
	Subject     string `json:"subject,omitempty"`
	CommittedAt string `json:"committed_at,omitempty"`
	RefreshedAt string `json:"refreshed_at"`
}
