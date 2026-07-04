package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newDatabaseCmd: `rc database ls/get/set` over the databases collection (id = dsn) PLUS a `controls`
// sub-group over the nested /databases/{dsn}/controls sub-resource. The noun is `database` (not `db`)
// because `rc db` is already the guarded production-read console command — keeping them distinct avoids
// shadowing that surface.
func newDatabaseCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "database", Short: "Manage registered databases (list/read/update + access controls)"}
	cmd.AddCommand(
		listSubCmd(e, "databases"),
		getSubCmd(e, "databases", "dsn"),
		setSubCmd(e, "databases", "dsn"),
		newDatabaseControlsCmd(e),
		newDatabasePreviewCmd(e),
	)
	return cmd
}

// newDatabasePreviewCmd: `rc database preview <dsn> --tenant … --principal-kind … --principal-id …
// [--table …]` over POST /api/v1/databases/{dsn}/scope-preview — the ONE real per-principal preview. It
// mints the scoped views a real run of (tenant, principal) would see and returns per-table counts + sample
// rows + the compiled predicate. Preview-only (never writes); the principal pair validates together.
func newDatabasePreviewCmd(e *env) *cobra.Command {
	var tenant, principalKind, principalID, table string
	cmd := &cobra.Command{
		Use:   "preview <dsn>",
		Short: "Preview the scoped rows a (tenant, principal) would see",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			kind := strings.TrimSpace(principalKind)
			id := strings.TrimSpace(principalID)
			if (kind == "") != (id == "") {
				return fmt.Errorf("--principal-kind and --principal-id must be provided together (both or neither)")
			}
			body := map[string]any{}
			if t := strings.TrimSpace(tenant); t != "" {
				body["tenant"] = t
			}
			if kind != "" {
				body["principal"] = map[string]any{"kind": kind, "external_id": id}
			}
			if tb := strings.TrimSpace(table); tb != "" {
				body["tables"] = []string{tb}
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			report, raw, err := c.ScopePreview(e.ctx(), args[0], body, e.scopeProject())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			render.ScopePreview(e.out, report)
			return nil
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant slug to bind the preview to (omit for a flat/cross-tenant preview)")
	cmd.Flags().StringVar(&principalKind, "principal-kind", "", "principal kind to scope by (e.g. kampadmin_person); requires --principal-id")
	cmd.Flags().StringVar(&principalID, "principal-id", "", "principal external id (the asserted identity); requires --principal-kind")
	cmd.Flags().StringVar(&table, "table", "", "limit the preview to a single view")
	return cmd
}

// newDatabaseControlsCmd: `rc database controls get|set <dsn>` over GET/PATCH
// /api/v1/databases/{dsn}/controls — a hand-written nested-path call (not a generic collection route).
func newDatabaseControlsCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "controls", Short: "Read or change a database's access controls"}
	cmd.AddCommand(databaseControlsGetCmd(e), databaseControlsSetCmd(e))
	return cmd
}

func databaseControlsGetCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "get <dsn>",
		Short: "Show a database's controls",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			raw, err := c.DatabaseControls(e.ctx(), args[0], e.scopeProject(), e.scopeTenant())
			if err != nil {
				return err
			}
			return render.JSON(e.out, raw)
		},
	}
}

// databaseControlsSetCmd accepts EITHER a single JSON object arg or k=v pairs (mirrors the action
// --params / collection set ergonomics): a leading "{" is parsed as the whole PATCH body, otherwise the
// args are k=v fields. The server owns the controls whitelist + validation.
func databaseControlsSetCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "set <dsn> <json | k=v…>",
		Short: "Change a database's controls (JSON object or k=v pairs; sparse)",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			body, err := parseJSONOrItemArgs(args[1:])
			if err != nil {
				return err
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			raw, err := c.SetDatabaseControls(e.ctx(), args[0], body, e.scopeProject(), e.scopeTenant())
			if err != nil {
				return err
			}
			return render.JSON(e.out, raw)
		},
	}
}
