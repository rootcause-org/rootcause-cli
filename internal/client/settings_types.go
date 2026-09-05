// Wire contract for the config surface (setting bags, schema registry, capabilities, hierarchy settings).
// Field names and omitempty MUST match the server verbatim; the ground rules live in types.go.
package client

import "encoding/json"

// SettingField is one field of the server's generic settings bag: the stored override (value),
// effective (value-or-default), default, the provenance of the effective value ("override"|"default"),
// and — only with GET ?include=schema — the field's registry schema. Scalars are kept as
// json.RawMessage so the CLI renders the exact type the server holds (number for max_run_usd, string
// otherwise) without a typed-per-key shape.
type SettingField struct {
	Value     json.RawMessage `json:"value"`
	Effective json.RawMessage `json:"effective"`
	Default   json.RawMessage `json:"default"`
	Source    string          `json:"source"`
	Schema    json.RawMessage `json:"schema,omitempty"`
	RotatedBy string          `json:"rotated_by,omitempty"`
	RotatedAt string          `json:"rotated_at,omitempty"`
}

// Settings is GET /api/v1/settings (PATCH returns the same shape): a generic key→field map, mirroring
// the server's registry-driven bag. A field absent from the map (e.g. kb_enrich_model when KB sync is
// off) is simply unset for this project. The CLI holds no per-key knowledge — it renders whatever keys
// the server sends, so a new server-side knob shows up with no CLI change.
type Settings map[string]SettingField

// SchemaResponse is GET /api/v1/meta/schema: the declarative config registry as JSON, keyed by
// resource name. The self-describing surface `rc schema`/`rc explain` render.
type SchemaResponse struct {
	Resources map[string]BagSchema `json:"resources"`
	// HierarchySettings is the nested persona/channel/... surface (`rc project settings behavior set`,
	// tenant/mailbox twins) keyed by group prefix. Separate from Resources because those groups live in
	// the hierarchy JSONB, not in a flat project-row bag.
	HierarchySettings map[string]HierarchyGroupSchema `json:"hierarchy_settings,omitempty"`
}

// HierarchyGroupSchema is one nested settings group (persona, channel, …): the levels it may be set at,
// its bare field names, and — on a current server — the full per-field schema the CLI validates against.
type HierarchyGroupSchema struct {
	SettableAt   []string      `json:"settable_at,omitempty"`
	Fields       []string      `json:"fields,omitempty"`
	FieldSchemas []FieldSchema `json:"field_schemas,omitempty"`
}

// BagSchema is one resource's schema: its name + every field descriptor.
type BagSchema struct {
	Name   string        `json:"name"`
	Fields []FieldSchema `json:"fields"`
}

// FieldSchema is one settable field's public description — everything a human or agent needs to write
// it without out-of-band docs.
type FieldSchema struct {
	Key       string          `json:"key"`
	Scope     string          `json:"scope"`
	Group     string          `json:"group"`
	Type      string          `json:"type"`
	Enum      []string        `json:"enum,omitempty"`
	Scopes    []string        `json:"scopes,omitempty"`
	Sensitive bool            `json:"sensitive,omitempty"`
	Help      string          `json:"help"`
	Default   json.RawMessage `json:"default,omitempty"`
	// Members describes an object-typed field's CLOSED set of scalar members (e.g. models.agent →
	// tier/model/effort/engine); such a key is written as one JSON object, never member-by-member.
	Members []FieldSchema `json:"members,omitempty"`
}

// Access is GET /api/v1/meta/capabilities: what THIS token may do (effective scopes, writable keys,
// reachable resources, console reach). The agent/operator pre-flight. Named Access to avoid confusion
// with the console CapabilitiesResponse (which lists DB/script/action primitives, not token authority).
type Access struct {
	Email        string         `json:"email,omitempty"`
	AllProjects  bool           `json:"all_projects"`
	Project      *ScopeItem     `json:"project,omitempty"`
	Tenant       *ScopeItem     `json:"tenant,omitempty"`
	Scopes       []string       `json:"scopes"`
	WritableKeys []string       `json:"writable_keys"`
	Resources    []string       `json:"resources"`
	Console      ConsoleCapsSum `json:"console"`
	Formats      AccessFormats  `json:"formats"`
}

// AccessFormats is the wire-format versions THAT box currently writes (token-independent). It is the
// server's own answer to "can this rc still parse what you produce?" — read it instead of re-pinning the
// server's current corpus version here. Empty against a server older than the field.
type AccessFormats struct {
	HarvestCorpus string `json:"harvest_corpus"`
}

// HierarchySettings is GET/PATCH /api/v1/projects/{project}/settings and its tenant/mailbox children.
// Settings is the scope-local nested override bag ({persona:{...},channel:{...}}); Resolved is present
// only when ?resolved=true and carries effective values plus provenance per field.
type HierarchySettings struct {
	Scope    string          `json:"scope"`
	Project  string          `json:"project,omitempty"`
	Tenant   string          `json:"tenant,omitempty"`
	Mailbox  string          `json:"mailbox,omitempty"`
	Settings json.RawMessage `json:"settings"`
	Resolved json.RawMessage `json:"resolved,omitempty"`
}

// ScopeItem is a project/tenant identity in a capabilities response.
type ScopeItem struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	Slug string `json:"slug,omitempty"`
}

// ConsoleCapsSum is the dev-console reach broken out as booleans.
type ConsoleCapsSum struct {
	DB     bool `json:"db"`
	Bash   bool `json:"bash"`
	Action bool `json:"action"`
}
