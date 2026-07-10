package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/outputspill"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

func newCapabilitiesCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "capabilities",
		Short: "List direct production primitives available to this login",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			path := "/api/v1/console/capabilities" + consoleScope(e.scopeProject(), e.scopeTenant())
			if e.jsonOut() {
				raw, err := c.Raw(e.ctx(), http.MethodGet, path, nil)
				if err != nil {
					return err
				}
				return e.renderJSON("console-capabilities", raw)
			}
			resp, err := c.Capabilities(e.ctx(), e.scopeProject(), e.scopeTenant())
			if err != nil {
				return err
			}
			render.Capabilities(e.out, resp)
			return nil
		},
	}
}

func newConsoleDatabaseCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "database", Short: "Run guarded production database reads"}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List available databases",
			Args:  cobra.NoArgs,
			RunE: func(_ *cobra.Command, _ []string) error {
				c, err := e.newClient()
				if err != nil {
					return err
				}
				path := "/api/v1/console/db" + consoleScope(e.scopeProject(), e.scopeTenant())
				if e.jsonOut() {
					raw, err := c.Raw(e.ctx(), http.MethodGet, path, nil)
					if err != nil {
						return err
					}
					return e.renderJSON("console-db-list", raw)
				}
				resp, err := c.DBList(e.ctx(), e.scopeProject(), e.scopeTenant())
				if err != nil {
					return err
				}
				render.DBList(e.out, resp)
				return nil
			},
		},
		newDBSchemaCmd(e),
		newDBQueryCmd(e),
	)
	return cmd
}

func newDBSchemaCmd(e *env) *cobra.Command {
	var table string
	cmd := &cobra.Command{
		Use:   "schema <db>",
		Short: "Fetch database schema, optionally one table",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			path := "/api/v1/console/db/" + url.PathEscape(args[0]) + "/schema" + consoleScopeWithTable(e.scopeProject(), e.scopeTenant(), table)
			if e.jsonOut() {
				raw, err := c.Raw(e.ctx(), http.MethodGet, path, nil)
				if err != nil {
					return err
				}
				return e.renderJSON("console-db-schema", raw)
			}
			resp, err := c.DBSchema(e.ctx(), args[0], table, e.scopeProject(), e.scopeTenant())
			if err != nil {
				return err
			}
			render.DBSchema(e.out, resp)
			return nil
		},
	}
	cmd.Flags().StringVar(&table, "table", "", "limit schema to one table name")
	return cmd
}

func newDBQueryCmd(e *env) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "query <db> <sql>",
		Short: "Run a read-only SQL query through rootcause scoping",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			req := map[string]any{"sql": args[1]}
			if limit > 0 {
				req["limit"] = limit
			}
			path := "/api/v1/console/db/" + url.PathEscape(args[0]) + "/query" + consoleScope(e.scopeProject(), e.scopeTenant())
			if e.jsonOut() {
				raw, err := c.Raw(e.ctx(), http.MethodPost, path, req)
				if err != nil {
					return err
				}
				return e.renderJSON("console-db-query", raw)
			}
			resp, err := c.DBQuery(e.ctx(), args[0], client.DBQueryRequest{SQL: args[1], Limit: limit}, e.scopeProject(), e.scopeTenant())
			if err != nil {
				return err
			}
			render.DBQuery(e.out, resp)
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "max rows to return inline (server cap 500)")
	return cmd
}

func newBashCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "bash", Short: "List or run workspace console commands"}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List cataloged brain scripts",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			path := "/api/v1/console/bash" + consoleScope(e.scopeProject(), e.scopeTenant())
			if e.jsonOut() {
				raw, err := c.Raw(e.ctx(), http.MethodGet, path, nil)
				if err != nil {
					return err
				}
				return e.renderJSON("console-bash-list", raw)
			}
			resp, err := c.BashList(e.ctx(), e.scopeProject(), e.scopeTenant())
			if err != nil {
				return err
			}
			render.BashList(e.out, resp)
			return nil
		},
	})
	var timeout int
	runCmd := &cobra.Command{
		Use:   "run [--timeout N] <command>",
		Short: "Run one command in the guarded workspace console",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			req := client.BashRunRequest{Command: args[0], TimeoutS: timeout}
			path := "/api/v1/console/bash/run" + consoleScope(e.scopeProject(), e.scopeTenant())
			body := map[string]any{"command": req.Command}
			if req.TimeoutS > 0 {
				body["timeout_s"] = req.TimeoutS
			}
			raw, err := c.Raw(e.ctx(), http.MethodPost, path, body)
			if err != nil {
				return err
			}
			if e.jsonOut() {
				var meta client.BashRunResponse
				label := "bash-run"
				if json.Unmarshal(raw, &meta) == nil {
					label = bashRunLabel(&meta)
				}
				return e.renderJSON(label, raw)
			}
			var resp client.BashRunResponse
			if err := json.Unmarshal(raw, &resp); err != nil {
				return fmt.Errorf("decode bash run response: %w", err)
			}
			manifest, err := outputspill.MaybeSpillJSON(e.spillConfig(), bashRunLabel(&resp), raw)
			if err != nil {
				return err
			}
			render.BashRun(e.out, &resp, bashArtifacts(manifest))
			return nil
		},
	}
	runCmd.Flags().IntVar(&timeout, "timeout", 0, "per-command timeout in seconds")
	cmd.AddCommand(runCmd)
	return cmd
}

func newActionCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "action", Short: "Inspect and execute guarded rootcause actions"}
	cmd.AddCommand(actionListCmd(e), actionShowCmd(e), actionExecCmd(e, true), actionExecCmd(e, false))
	return cmd
}

func actionListCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available actions",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			path := "/api/v1/console/action" + consoleScope(e.scopeProject(), e.scopeTenant())
			if e.jsonOut() {
				raw, err := c.Raw(e.ctx(), http.MethodGet, path, nil)
				if err != nil {
					return err
				}
				return e.renderJSON("console-action-list", raw)
			}
			resp, err := c.ActionList(e.ctx(), e.scopeProject(), e.scopeTenant())
			if err != nil {
				return err
			}
			render.ActionList(e.out, resp)
			return nil
		},
	}
}

func actionShowCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show one action manifest",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			path := "/api/v1/console/action/" + url.PathEscape(args[0]) + consoleScope(e.scopeProject(), e.scopeTenant())
			if e.jsonOut() {
				raw, err := c.Raw(e.ctx(), http.MethodGet, path, nil)
				if err != nil {
					return err
				}
				return e.renderJSON("console-action-show-"+args[0], raw)
			}
			resp, err := c.ActionShow(e.ctx(), args[0], e.scopeProject(), e.scopeTenant())
			if err != nil {
				return err
			}
			render.ActionShow(e.out, resp)
			return nil
		},
	}
}

func actionExecCmd(e *env, dry bool) *cobra.Command {
	verb := "run"
	short := "Execute an action"
	if dry {
		verb = "preflight"
		short = "Run action preflight/dry-run"
	}
	var params string
	cmd := &cobra.Command{
		Use:   verb + " <id> --params '{...}'",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			var p map[string]any
			if params != "" {
				if err := json.Unmarshal([]byte(params), &p); err != nil {
					return fmt.Errorf("parse --params JSON: %w", err)
				}
			}
			body := map[string]any{"params": p}
			path := "/api/v1/console/action/" + url.PathEscape(args[0]) + "/" + verb + consoleScope(e.scopeProject(), e.scopeTenant())
			if e.jsonOut() {
				raw, err := c.Raw(e.ctx(), http.MethodPost, path, body)
				if err != nil {
					return err
				}
				return e.renderJSON("console-action-"+verb+"-"+args[0], raw)
			}
			req := client.ActionExecRequest{Params: p}
			var resp *client.ActionExecResponse
			if dry {
				resp, err = c.ActionPreflight(e.ctx(), args[0], req, e.scopeProject(), e.scopeTenant())
			} else {
				resp, err = c.ActionRun(e.ctx(), args[0], req, e.scopeProject(), e.scopeTenant())
			}
			if err != nil {
				return err
			}
			render.ActionExec(e.out, resp)
			return nil
		},
	}
	cmd.Flags().StringVar(&params, "params", "", "JSON object of action params")
	return cmd
}

func consoleScope(project, tenant string) string {
	q := url.Values{}
	if project != "" {
		q.Set("project", project)
	}
	if tenant != "" {
		q.Set("tenant", tenant)
	}
	if enc := q.Encode(); enc != "" {
		return "?" + enc
	}
	return ""
}

func consoleScopeWithTable(project, tenant, table string) string {
	q := url.Values{}
	if project != "" {
		q.Set("project", project)
	}
	if tenant != "" {
		q.Set("tenant", tenant)
	}
	if table != "" {
		q.Set("table", table)
	}
	if enc := q.Encode(); enc != "" {
		return "?" + enc
	}
	return ""
}

func bashRunLabel(r *client.BashRunResponse) string {
	id := r.RunID
	if len(id) > 8 {
		id = id[:8]
	}
	if id == "" {
		id = "unknown"
	}
	return fmt.Sprintf("bash-run-%s-seq-%d", id, r.Seq)
}

func bashArtifacts(m *outputspill.Manifest) map[string]outputspill.Artifact {
	if m == nil {
		return nil
	}
	arts := map[string]outputspill.Artifact{}
	for _, name := range []string{"stdout", "stderr"} {
		if art, ok := m.Artifacts[name]; ok {
			arts[name] = art
		}
	}
	return arts
}
