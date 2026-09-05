package client

import (
	"encoding/json"
	"sort"
)

// Wire shapes for the embedded-chat control plane (/api/v1[/projects/{p}]/chat/*, /principals*).
// Field names match the server verbatim; the widget/SSE runtime shapes live in chat.go.

// ChatSecretResponse is the body of POST /chat/secret/{rotate,reveal} — the signing secret, printed
// once, with who rotated it and when.
type ChatSecretResponse struct {
	Secret    string `json:"secret"`
	RotatedBy string `json:"rotated_by"`
	RotatedAt string `json:"rotated_at"`
}

// ChatTokenRequest mints a short-lived embed token for one origin, optionally bound to a principal.
type ChatTokenRequest struct {
	Origin        string `json:"origin"`
	PrincipalKind string `json:"principal_kind"`
	ExternalID    string `json:"external_id"`
	Tenant        string `json:"tenant,omitempty"`
}

type ChatTokenResponse struct {
	Token string `json:"token"`
}

// PrincipalManifest is GET/PATCH /principals — the declared principal kinds and the optional email
// lookup. Kinds/EmailLookup stay raw: the manifest body is server-owned and freeform, the CLI only
// reports WHICH kinds exist and WHETHER an email lookup is configured.
type PrincipalManifest struct {
	Kinds       map[string]json.RawMessage `json:"kinds"`
	EmailLookup json.RawMessage            `json:"email_lookup"`
}

// KindNames lists the declared principal kinds, sorted for a deterministic report.
func (m *PrincipalManifest) KindNames() []string {
	out := make([]string, 0, len(m.Kinds))
	for k := range m.Kinds {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// HasEmailLookup reports whether the manifest configures an email lookup — an explicit JSON null
// counts as absent, exactly like a missing key.
func (m *PrincipalManifest) HasEmailLookup() bool {
	return len(m.EmailLookup) > 0 && string(m.EmailLookup) != "null"
}

// PrincipalResolveRequest resolves an email (or passes through an already-canonical external id) to a
// principal external id. Exactly one of Email / ExternalID is set by the caller.
type PrincipalResolveRequest struct {
	Kind       string `json:"kind"`
	Email      string `json:"email,omitempty"`
	ExternalID string `json:"external_id,omitempty"`
	Tenant     string `json:"tenant,omitempty"`
}

type PrincipalResolveResponse struct {
	ExternalID string `json:"external_id"`
}
