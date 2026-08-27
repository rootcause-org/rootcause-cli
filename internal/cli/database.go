package cli

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newDatabaseCmd: `rc project database ls/get/set` over the databases collection (id = dsn) plus a
// `controls` sub-group over the nested /databases/{dsn}/controls sub-resource. Guarded production reads
// live separately at `rc dev console database`.
func newDatabaseCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "database",
		Short: "Manage registered databases (list/read/update + access controls)",
		Long: `Manage registered databases (list/read/update + access controls).

There is no "database add": a database EXISTS because a sealed grounding env key named
<PROJECT>_<DBKEY>_DSN exists. To register one (read-only DB role; the rootcause box must be
network-allowlisted by your DB provider):

  printf %s "$DSN" | rc project env set key=<PROJECT>_<DBKEY>_DSN

<dbkey> lowercased is the db= short name brain scripts use: PROBACKUP_BACKUPS_DSN <-> lib.db(db="backups").
Verify the DSN env actually resolves with ` + "`rc dev console database list`" + `.

` + "`ls`" + ` here lists the DSNs this project has CONFIGURED (description, scope manifest, or PII columns) —
a brand-new DSN shows up once you annotate it: ` + "`rc project database set <PROJECT>_<DBKEY>_DSN description=…`" + `.

` + "`*_WRITE_DSN`" + ` keys are the action/write plane (--plane action), not grounding databases.`,
	}
	cmd.AddCommand(
		listSubCmd(e, "databases"),
		withDSNHint(getSubCmd(e, "databases", "dsn")),
		withDSNHint(databaseSetCmd(e)),
		newDatabaseControlsCmd(e),
		newDatabasePreviewCmd(e),
	)
	return cmd
}

// databaseConventionHint is the one-line discovery hint for the implicit registration convention: a
// database is not created through this surface, it exists because its grounding DSN env exists.
const databaseConventionHint = "databases come from grounding env keys `<PROJECT>_<DBKEY>_DSN` " +
	"(register: printf %s \"$DSN\" | rc project env set key=<PROJECT>_<DBKEY>_DSN); " +
	"`rc dev console database list` shows which DSN envs actually exist"

// databaseSetCmd is the generic collection `set` with a databases-specific Long listing the ONE editable
// field, so an agent doesn't have to guess the k=v vocabulary (server: databasesAdapter.update).
func databaseSetCmd(e *env) *cobra.Command {
	cmd := setSubCmd(e, "databases", "dsn")
	cmd.Long = `Update a database (sparse k=v).

Accepted keys:
  description=<text>   one-line description shown to the agent (empty value clears it)

Scope/PII controls live under ` + "`rc project database controls set`" + `, not here. The DSN itself is the
sealed grounding env key ` + "`<PROJECT>_<DBKEY>_DSN`" + ` — set it with ` + "`rc project env set`" + `.`
	return cmd
}

// withDSNHint appends the registration convention to a 404 from a databases get/set, so an unknown dsn
// teaches the caller how databases come into being instead of a bare NOT_FOUND.
func withDSNHint(cmd *cobra.Command) *cobra.Command {
	inner := cmd.RunE
	cmd.RunE = func(c *cobra.Command, args []string) error {
		err := inner(c, args)
		var apiErr *client.APIError
		if err != nil && asAPIError(err, &apiErr) && apiErr.Status == http.StatusNotFound && apiErr.Code != "" {
			clone := *apiErr
			clone.Message = apiErr.Message + "\n  " + databaseConventionHint
			return &clone
		}
		return err
	}
	return cmd
}

// newDatabasePreviewCmd: `rc project database preview <dsn> --tenant … --principal-kind … --principal-id …
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

// newDatabaseControlsCmd: `rc project database controls get|set <dsn>` over GET/PATCH
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
