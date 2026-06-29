package cli

import (
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
	)
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
