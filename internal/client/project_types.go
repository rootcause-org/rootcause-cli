// Wire contract for projects, the whoami scope, and the project env bag.
// Field names and omitempty MUST match the server verbatim; the ground rules live in types.go.
package client

// Project is one row of GET /api/v1/projects — a fleet handle (id + name). It's what `rc project list`
// renders and the seed the `--all` fan-out lists before hitting each project's read surface with
// ?project=<id>. Mirrors the server's projectItem field-for-field.
type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ProjectsResponse is GET /api/v1/projects — the projects the bearer may see (every one for an
// all-projects admin token; just its own for a project-pinned token).
type ProjectsResponse struct {
	Projects []Project `json:"projects"`
}

// ProjectRenameRequest is PATCH /api/v1/projects/{project}/rename.
type ProjectRenameRequest struct {
	Name string `json:"name"`
}

// ProjectRenameResponse is PATCH /api/v1/projects/{project}/rename — the server-side project slug,
// brain repo, GitHub repo, and deployed local-dir rename result.
type ProjectRenameResponse struct {
	ID                string `json:"id"`
	PreviousName      string `json:"previous_name"`
	Name              string `json:"name"`
	PreviousBrainRepo string `json:"previous_brain_repo"`
	BrainRepo         string `json:"brain_repo"`
	GitHubRenamed     bool   `json:"github_renamed"`
	LocalDirRenamed   bool   `json:"local_dir_renamed"`
	URL               string `json:"url"`
}

type WhoamiScope struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	Slug string `json:"slug,omitempty"`
}

type WhoamiResponse struct {
	Email       string       `json:"email,omitempty"`
	AllProjects bool         `json:"all_projects"`
	Project     *WhoamiScope `json:"project,omitempty"`
	Tenant      *WhoamiScope `json:"tenant,omitempty"`
}

// --- observability feeds (rc fleet / patterns / health) ---

// EnvResponse is GET /api/v1/env — the resolved grounding env. Keys holds live secret VALUES (the
// whole point: `rc project env pull` writes them to ./.env). The CLI NEVER prints a value: it renders key
// NAMES only and writes values solely to the 0600 file.
type EnvResponse struct {
	Project string            `json:"project"`
	Tenant  string            `json:"tenant,omitempty"`
	Keys    map[string]string `json:"keys"`
}
