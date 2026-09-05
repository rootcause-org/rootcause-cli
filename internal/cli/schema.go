package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newSchemaCmd builds `rc schema [resource]` over GET /api/v1/meta/schema — the self-describing config
// registry (fields, types, enums, write scopes, defaults, help). One registry on the server drives
// this, the web form, the API validation, and the agent's tool synthesis.
func newSchemaCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "schema [resource]",
		Short: "Show the config registry (fields, types, scopes, defaults)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var resource string
			if len(args) == 1 {
				resource = args[0]
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			resp, raw, err := c.GetSchema(e.ctx(), resource, e.scopeProject())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			render.Schema(e.out, resp)
			return nil
		},
	}
}

// newExplainCmd builds `rc explain <key>` — the full schema of one setting (type, enum, write scopes,
// default, help), found across all resources. The human twin of /meta/schema for a single knob.
func newExplainCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "describe <key>",
		Short: "Explain one config key (type, enum, scopes, default, help)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			key := args[0]
			c, err := e.newClient()
			if err != nil {
				return err
			}
			resp, _, err := c.GetSchema(e.ctx(), "", e.scopeProject())
			if err != nil {
				return err
			}
			resource, field, ok := findField(resp, key)
			if !ok {
				return fmt.Errorf("unknown config key %q (try `rc schema`)", key)
			}
			if e.jsonOut() {
				raw, err := json.Marshal(field)
				if err != nil {
					return err
				}
				return render.JSON(e.out, raw)
			}
			render.ExplainField(e.out, resource, field)
			return nil
		},
	}
}

// newAccessCmd builds `rc access` over GET /api/v1/meta/capabilities — what THIS token may do
// (effective scopes, writable keys, reachable resources, console reach). Named `access` to avoid
// colliding with `rc capabilities`, which lists the console's DB/script/action primitives.
func newAccessCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "access",
		Short: "Show what this token may do (scopes, writable keys, resources)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			resp, raw, err := c.GetAccess(e.ctx(), e.scopeProject())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			render.Access(e.out, resp)
			return nil
		},
	}
}

// findField locates a field by key across every resource in the schema, returning its resource name.
func findField(resp *client.SchemaResponse, key string) (string, client.FieldSchema, bool) {
	for name, bag := range resp.Resources {
		for _, f := range bag.Fields {
			if f.Key == key {
				return name, f, true
			}
		}
	}
	return "", client.FieldSchema{}, false
}
