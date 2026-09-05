// Wire contract for tenant profile settings.
// Field names and omitempty MUST match the server verbatim; the ground rules live in types.go.
package client

import "encoding/json"

// TenantSettings is GET /api/v1/tenants/{slug}/profile (and the echoed body of a PATCH), the
// tenant profile/projection record. It mirrors the server's tenantSettingsGetResponse /
// tenantSettingsPatchResponse field-for-field. Settings is the RAW stored object (kept as
// json.RawMessage so the CLI renders/echoes the exact keys+values the server holds — never reshaped;
// `{}` for a tenant that has never been written). The PATCH response drops nothing the GET carries, so
// one struct serves both.
type TenantSettings struct {
	TenantID  string          `json:"tenant_id"`
	Settings  json.RawMessage `json:"settings"`
	Version   string          `json:"version"`
	AppliedAt string          `json:"applied_at"`
}

// TenantSettingsPatchRequest is the PATCH /api/v1/tenants/{slug}/profile body:
// { "settings": { …partial… }, "source"?: "…" }. Settings is a raw key→value map so an explicit JSON
// null (the "unconfigure" gesture) rides through verbatim, distinct from an omitted key. Source is the
// provenance label ("cli"); omitempty so a blank source isn't sent (the server defaults it to "cli").
type TenantSettingsPatchRequest struct {
	Settings map[string]any `json:"settings"`
	Source   string         `json:"source,omitempty"`
}
