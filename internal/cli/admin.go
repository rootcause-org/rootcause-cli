package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// errMissingCatalogKey is the clear "key= required" error for catalog upsert (the catalog is keyed on it).
var errMissingCatalogKey = errors.New("missing key=<k>")

// newAdminCmd builds the box-level admin surface — users / projects / catalog — over
// /api/v1/admin/{users,projects,catalog}. These bypass the generic per-project collection routes and
// require a GLOBAL-ADMIN token; they're box-wide, so they take NO --project scope (the server rejects a
// non-admin token). Each noun is a thin list/add/(set|upsert) over the bespoke admin client methods.
func newAdminCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "admin", Short: "Box-level administration (users/projects/catalog; global-admin token)"}
	cmd.AddCommand(newAdminUserCmd(e), newAdminProjectCmd(e), newAdminCatalogCmd(e))
	return cmd
}

// adminListCmd is the shared `ls` over GET /api/v1/admin/<resource>.
func adminListCmd(e *env, resource string) *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List " + resource,
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			l, raw, err := c.AdminList(e.ctx(), resource)
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			render.ItemList(e.out, l)
			return nil
		},
	}
}

// adminAddCmd is the shared `add k=v…` over POST /api/v1/admin/<resource>. The server owns field
// validation. `warn` is a one-line stderr note printed AFTER a successful create (e.g. a
// project add's webhook_secret is shown once) — empty for resources with no such caveat.
func adminAddCmd(e *env, resource, short, warn string) *cobra.Command {
	return &cobra.Command{
		Use:   "add k=v [k=v…]",
		Short: short,
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			body, err := parseItemArgs(args)
			if err != nil {
				return err
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			item, raw, err := c.AdminCreate(e.ctx(), resource, body)
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			if warn != "" {
				_, _ = e.err.Write([]byte(warn + "\n"))
			}
			render.Item(e.out, item)
			return nil
		},
	}
}

// adminSetCmd is the shared `set <id> k=v…` over PATCH /api/v1/admin/<resource>/<id>.
func adminSetCmd(e *env, resource, idHelp, short string) *cobra.Command {
	return &cobra.Command{
		Use:   "set <" + idHelp + "> k=v [k=v…]",
		Short: short,
		Args:  cobra.MinimumNArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			body, err := parseItemArgs(args[1:])
			if err != nil {
				return err
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			item, raw, err := c.AdminUpdate(e.ctx(), resource, args[0], body)
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			render.Item(e.out, item)
			return nil
		},
	}
}

func newAdminUserCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "user", Short: "Manage box-level users"}
	cmd.AddCommand(
		adminListCmd(e, "users"),
		adminAddCmd(e, "users", "Create a user (email=… [admin=true] [password=…])", ""),
		adminSetCmd(e, "users", "id", "Update a user ([admin=true|false] [password=…])"),
	)
	return cmd
}

func newAdminProjectCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "project", Short: "Manage box-level projects"}
	cmd.AddCommand(
		adminListCmd(e, "projects"),
		adminAddCmd(e, "projects",
			"Create a project (name=… [default_tier=…] [egress_mode=wildcard|enforce])",
			"note: the webhook_secret below is shown once — store it now"),
	)
	return cmd
}

func newAdminCatalogCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "catalog", Short: "Manage the integration catalog"}
	cmd.AddCommand(
		adminListCmd(e, "catalog"),
		adminCatalogUpsertCmd(e),
	)
	return cmd
}

// adminCatalogUpsertCmd: `rc admin catalog upsert key=<k> …` over POST /api/v1/admin/catalog (the
// catalog is keyed on `key`; POST is an upsert).
func adminCatalogUpsertCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "upsert key=<k> [k=v…]",
		Short: "Create or update a catalog entry (keyed on key=)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			body, err := parseItemArgs(args)
			if err != nil {
				return err
			}
			if body["key"] == nil || body["key"] == "" {
				return errMissingCatalogKey
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			item, raw, err := c.AdminCreate(e.ctx(), "catalog", body)
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			render.Item(e.out, item)
			return nil
		},
	}
}
