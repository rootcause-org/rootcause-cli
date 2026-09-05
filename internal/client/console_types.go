// Wire contract for the dev console plane (capabilities, the db browse/query surface, warm bash sessions).
// Field names and omitempty MUST match the server verbatim; the ground rules live in types.go.
package client

type ConsoleDBInfo struct {
	Name        string `json:"name"`
	Env         string `json:"env"`
	Description string `json:"description,omitempty"`
	Scoped      bool   `json:"scoped"`
	PIIMasked   bool   `json:"pii_masked"`
	// Writable is true when the project has sealed a <X>_WRITE_DSN for this database in .env.action —
	// the presence of write-plane credentials that `query --write` (scope console:db:write) commits to.
	Writable bool `json:"writable,omitempty"`
}

type ConsoleScriptInfo struct {
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	Purpose     string   `json:"purpose,omitempty"`
	Args        string   `json:"args,omitempty"`
	RequiredEnv []string `json:"required_env,omitempty"`
}

type ConsoleActionSummary struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
	Risk        string `json:"risk,omitempty"`
	Preflight   bool   `json:"preflight"`

	// Enriched catalog fields (additive; absent from the legacy /capabilities projection).
	HasPreflight bool                    `json:"has_preflight"`
	HasPolicy    bool                    `json:"has_policy"`
	Autonomy     ActionAutonomyGauge     `json:"autonomy"`
	Connections  []ActionConnectionState `json:"connections,omitempty"`
	Params       []ActionParamSpec       `json:"params,omitempty"`
	Stats        ActionStats             `json:"stats"`
	Digest       string                  `json:"digest,omitempty"`
}

type CapabilitiesResponse struct {
	Project    string                 `json:"project"`
	Tenant     string                 `json:"tenant,omitempty"`
	Brain      BrainStatus            `json:"brain"`
	Databases  []ConsoleDBInfo        `json:"databases"`
	Scripts    []ConsoleScriptInfo    `json:"scripts"`
	Actions    []ConsoleActionSummary `json:"actions"`
	EgressMode string                 `json:"egress_mode"`
	Planes     map[string]string      `json:"planes"`
}

type DBListResponse struct {
	Project   string          `json:"project"`
	Tenant    string          `json:"tenant,omitempty"`
	Databases []ConsoleDBInfo `json:"databases"`
}

type DBSchemaResponse struct {
	Project string          `json:"project"`
	Tenant  string          `json:"tenant,omitempty"`
	DB      string          `json:"db"`
	Tables  []DBSchemaTable `json:"tables"`
}

type DBSchemaTable struct {
	Schema  string           `json:"schema"`
	Name    string           `json:"name"`
	Columns []DBSchemaColumn `json:"columns"`
}

type DBSchemaColumn struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
}

type DBQueryRequest struct {
	SQL    string            `json:"sql"`
	Params map[string]string `json:"params,omitempty"`
	Limit  int               `json:"limit,omitempty"`
	All    bool              `json:"all,omitempty"`
	// Write routes the statement to the project's sealed write-plane DSN (scope console:db:write) and
	// commits unless DryRun is set; omitempty so a plain read never carries it.
	Write bool `json:"write,omitempty"`
	// DryRun preserves the write plane and authorization but rolls the transaction back.
	DryRun bool `json:"dry_run,omitempty"`
}

type DBQueryResponse struct {
	Project    string          `json:"project"`
	Tenant     string          `json:"tenant,omitempty"`
	DB         string          `json:"db"`
	RunID      string          `json:"run_id"`
	Columns    []string        `json:"columns"`
	ColumnInfo []DBQueryColumn `json:"column_info,omitempty"`
	Rows       [][]any         `json:"rows"`
	// RowsAffected is the write's CommandTag row count; a pointer because it is present only on a write
	// response (absent on a read), distinct from a real 0-row write. Write echoes that the statement ran
	// on the write plane.
	RowsAffected *int64 `json:"rows_affected,omitempty"`
	Write        bool   `json:"write,omitempty"`
	DryRun       bool   `json:"dry_run,omitempty"`
	RowCount     int    `json:"row_count"`
	Truncated    bool   `json:"truncated"`
	Limit        int    `json:"limit,omitempty"`
	LimitClamped bool   `json:"limit_clamped,omitempty"`
	DurationMs   int64  `json:"duration_ms"`
}

type DBQueryStreamHeader struct {
	Project      string          `json:"project"`
	Tenant       string          `json:"tenant,omitempty"`
	DB           string          `json:"db"`
	RunID        string          `json:"run_id"`
	Columns      []string        `json:"columns"`
	ColumnInfo   []DBQueryColumn `json:"column_info,omitempty"`
	BatchSize    int             `json:"batch_size"`
	LimitClamped bool            `json:"limit_clamped,omitempty"`
}

type DBQueryStreamMeta struct {
	RowCount   int   `json:"row_count"`
	DurationMs int64 `json:"duration_ms"`
	Truncated  bool  `json:"truncated"`
}

type DBQueryColumn struct {
	Name    string `json:"name"`
	TypeOID uint32 `json:"type_oid"`
	Type    string `json:"type,omitempty"`
	Format  string `json:"format,omitempty"`
}

type BashListResponse struct {
	Project string              `json:"project"`
	Tenant  string              `json:"tenant,omitempty"`
	Brain   BrainStatus         `json:"brain"`
	Scripts []ConsoleScriptInfo `json:"scripts"`
}

type BashRunRequest struct {
	Command  string `json:"command"`
	TimeoutS int    `json:"timeout_s,omitempty"`
}

type BashRunResponse struct {
	Project         string `json:"project"`
	Tenant          string `json:"tenant,omitempty"`
	BrainResolved   string `json:"brain_resolved,omitempty"`
	RunID           string `json:"run_id"`
	Seq             int32  `json:"seq"`
	ExitCode        int    `json:"exit_code"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	StdoutTruncated bool   `json:"stdout_truncated"`
	StderrTruncated bool   `json:"stderr_truncated"`
	TimedOut        bool   `json:"timed_out"`
	DurationMs      int64  `json:"duration_ms"`
	EgressBlocked   bool   `json:"egress_blocked"`
}
