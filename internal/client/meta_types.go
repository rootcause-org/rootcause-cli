package client

// The GET /api/v1/meta/routes manifest — the server's own list of every public route, rendered by
// `rc dev routes`. Field names match the server verbatim.

type RouteManifest struct {
	Routes []APIRoute `json:"routes"`
}

type APIRoute struct {
	Method     string   `json:"method"`
	Path       string   `json:"path"`
	Summary    string   `json:"summary"`
	Auth       string   `json:"auth"`
	Scopes     []string `json:"scopes,omitempty"`
	Request    string   `json:"request,omitempty"`
	Response   string   `json:"response,omitempty"`
	Deprecated bool     `json:"deprecated,omitempty"`
}
