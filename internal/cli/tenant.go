package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newTenantCmd builds tenant hierarchy settings plus the profile/projection record. `settings` edits
// the canonical project-tree hierarchy bag; `profile` uses the distinct tenant-profile API.
func newTenantCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tenant",
		Short: "Manage tenants (sub-scopes below a project) and their settings",
	}
	// ls/add/set are the tenant COLLECTION over /api/v1/tenants (id = slug). No `rm`: the server has no
	// delete verb — a tenant is archived via `set <slug> status=archived`.
	cmd.AddCommand(
		newTenantSettingsCmd(e),
		newTenantProfileCmd(e),
		listSubCmd(e, "tenants"),
		addSubCmd(e, "tenants"),
		getSubCmd(e, "tenants", "slug"),
		setSubCmd(e, "tenants", "slug"),
	)
	return cmd
}

func newTenantSettingsCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "settings",
		Short: "Read or edit nested tenant settings (persona/channel)",
	}
	idArg := func(_ *cobra.Command, args []string) (string, error) { return args[0], nil }
	cmd.AddCommand(
		hierarchySettingsGetCmd(e, "tenant", idArg),
		hierarchySettingsSetCmd(e, "tenant", idArg),
		newTenantSettingsSchemaCmd(e),
	)
	return cmd
}

func newTenantProfileCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Read or edit tenant projection/profile values",
	}
	cmd.AddCommand(
		newTenantProfileGetCmd(e),
		newTenantProfileSetCmd(e),
		newTenantProfileSchemaCmd(e),
	)
	return cmd
}

func newTenantProfileGetCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <slug>",
		Short: "Show a tenant's projection/profile values",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			tenant := args[0]
			c, err := e.newClient()
			if err != nil {
				return err
			}
			project := e.scopeProject()
			ts, raw, err := c.GetTenantSettings(e.ctx(), tenant, project)
			if err != nil {
				return err
			}
			if render.IsJSON(e.mode(), e.out) {
				// Passthrough: emit the whole record (tenant_id/version/applied_at + raw settings) so the
				// jq path sees exactly what the server holds.
				return e.renderJSON("tenant-profile-"+tenant, raw)
			}
			// Group/label using the schema when it's reachable; fall back to a plain sorted key=value
			// list if the schema fetch fails (never block a read on the schema endpoint).
			schema, _ := c.GetTenantSettingsSchema(e.ctx(), project)
			render.TenantSettings(e.out, ts, schemaView(parseSchema(schema)))
			return nil
		},
	}
	return cmd
}

func newTenantProfileSetCmd(e *env) *cobra.Command {
	var unset []string
	cmd := &cobra.Command{
		Use:   "set <slug> key=val [key=val …]",
		Short: "Edit tenant projection/profile values",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			tenant := args[0]
			patchArgs := args[1:]
			if len(patchArgs) == 0 && len(unset) == 0 {
				return fmt.Errorf("nothing to set: pass key=value pairs and/or --unset <key>")
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			// Fetch the schema first for client-side coercion + a fast clear error. If it's unreachable,
			// fall back to sending raw strings and let the server be the authority.
			project := e.scopeProject()
			rawSchema, schemaErr := c.GetTenantSettingsSchema(e.ctx(), project)
			schema := parseSchema(rawSchema)
			if schemaErr != nil {
				schema = nil
			}
			settings, err := buildTenantPatch(patchArgs, unset, schema)
			if err != nil {
				return err
			}
			ts, raw, err := c.PatchTenantSettings(e.ctx(), tenant, project, client.TenantSettingsPatchRequest{
				Settings: settings,
				Source:   "cli",
			})
			if err != nil {
				return err
			}
			if render.IsJSON(e.mode(), e.out) {
				return e.renderJSON("tenant-profile-"+tenant, raw)
			}
			render.TenantSettings(e.out, ts, schemaView(schema))
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&unset, "unset", nil, "unconfigure a key (sends explicit null); repeatable")
	return cmd
}

func newTenantSettingsSchemaCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Dump the hierarchy settings schema (debug/reference)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			raw, err := c.Raw(e.ctx(), "GET", "/api/v1/meta/schema", nil)
			if err != nil {
				return err
			}
			// The schema is always JSON; hierarchy_settings is the relevant section.
			return render.JSON(e.out, raw)
		},
	}
	return cmd
}

func newTenantProfileSchemaCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Dump the tenant profile JSON schema",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			raw, err := c.GetTenantSettingsSchema(e.ctx(), e.scopeProject())
			if err != nil {
				return err
			}
			return render.JSON(e.out, raw)
		},
	}
	return cmd
}

// --- schema model (the slice of the enriched JSON Schema the CLI needs) ---

// schemaProp is the per-property metadata the CLI uses for coercion + grouped rendering. types holds
// the JSON Schema type(s) minus null (a ["string","null"] becomes {"string"}); itemEnum is the allowed
// values for an array's items (for array-with-item-enum coercion); enum is the allowed scalar values.
type schemaProp struct {
	types    map[string]bool
	enum     []string // scalar enum values (nil ⇒ free), null excluded
	itemEnum []string // for type "array": allowed item values (nil ⇒ free)
	group    string
	order    int
	labelNL  string
	required bool
}

// schemaGroup is one x-groups entry — a section header for the grouped get output.
type schemaGroup struct {
	key     string
	labelNL string
	order   int
}

// tenantSchema is the parsed, render/coerce-ready view of the enriched schema. nil (a failed/absent
// fetch) means "no schema": set sends raw strings, get falls back to a flat key=value list.
type tenantSchema struct {
	props  map[string]schemaProp
	groups []schemaGroup
}

// parseSchema turns the raw enriched JSON Schema into the slice the CLI needs. A nil/garbage input
// yields nil (callers treat that as "no schema available" and degrade gracefully).
func parseSchema(raw json.RawMessage) *tenantSchema {
	if len(raw) == 0 {
		return nil
	}
	var doc struct {
		Required []string `json:"required"`
		XGroups  []struct {
			Key     string `json:"key"`
			LabelNL string `json:"label_nl"`
			Order   int    `json:"order"`
		} `json:"x-groups"`
		Properties map[string]struct {
			Type  json.RawMessage `json:"type"`
			Enum  []any           `json:"enum"`
			Items struct {
				Enum []any `json:"enum"`
			} `json:"items"`
			XGroup string `json:"x-group"`
			XOrder int    `json:"x-order"`
			XLabel string `json:"x-label-nl"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	required := map[string]bool{}
	for _, r := range doc.Required {
		required[r] = true
	}
	ts := &tenantSchema{props: map[string]schemaProp{}}
	for _, g := range doc.XGroups {
		ts.groups = append(ts.groups, schemaGroup{key: g.Key, labelNL: g.LabelNL, order: g.Order})
	}
	sort.SliceStable(ts.groups, func(i, j int) bool { return ts.groups[i].order < ts.groups[j].order })
	for name, p := range doc.Properties {
		ts.props[name] = schemaProp{
			types:    parseTypeSet(p.Type),
			enum:     stringEnum(p.Enum),
			itemEnum: stringEnum(p.Items.Enum),
			group:    p.XGroup,
			order:    p.XOrder,
			labelNL:  p.XLabel,
			required: required[name],
		}
	}
	return ts
}

// parseTypeSet reads a JSON Schema `type` (a string or an array of strings) into a set, dropping
// "null" (a value's nullability is handled separately — the unconfigure path sends an explicit null).
func parseTypeSet(raw json.RawMessage) map[string]bool {
	out := map[string]bool{}
	if len(raw) == 0 {
		return out
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		if one != "null" && one != "" {
			out[one] = true
		}
		return out
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		for _, t := range many {
			if t != "null" && t != "" {
				out[t] = true
			}
		}
	}
	return out
}

// stringEnum projects a JSON enum ([]any, may contain null) to its non-null string members. Returns
// nil when there is no enum (a free-text field) so callers can distinguish "no constraint".
func stringEnum(vals []any) []string {
	if len(vals) == 0 {
		return nil
	}
	var out []string
	for _, v := range vals {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// --- set: build the sparse PATCH body ---

// buildTenantPatch turns key=val args + --unset keys into the sparse settings map for the PATCH. Each
// value is coerced against the schema (bool/int/number, scalar-enum membership, array-with-item-enum)
// for a fast, clear CLIENT-side error before the request; an unknown key (not in the schema) passes
// through as a string for the server to reject. An empty value (`key=`) or a key in `unset` sends an
// explicit JSON null (the unconfigure gesture). A key given both a value and --unset is a conflict.
func buildTenantPatch(args, unset []string, schema *tenantSchema) (map[string]any, error) {
	out := make(map[string]any, len(args)+len(unset))
	seen := map[string]bool{}
	for _, arg := range args {
		key, val, ok := strings.Cut(arg, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid argument %q: expected key=value", arg)
		}
		if seen[key] {
			return nil, fmt.Errorf("key %q given more than once", key)
		}
		seen[key] = true
		if val == "" {
			// `key=` unconfigures (explicit null), same as --unset.
			out[key] = nil
			continue
		}
		coerced, err := coerceValue(key, val, schema)
		if err != nil {
			return nil, err
		}
		out[key] = coerced
	}
	for _, key := range unset {
		if key == "" {
			return nil, fmt.Errorf("invalid --unset: empty key")
		}
		if seen[key] {
			return nil, fmt.Errorf("key %q both set and --unset", key)
		}
		seen[key] = true
		out[key] = nil
	}
	return out, nil
}

// coerceValue maps a CLI string to the JSON type the schema expects, validating client-side for a fast
// error. With no schema (or an unknown key), the value passes through as a string and the server is the
// authority. Booleans accept true/false; integers parse base-10; numbers parse as float; a scalar enum
// must be a member; an array field splits on commas and validates each item against the item enum.
func coerceValue(key, val string, schema *tenantSchema) (any, error) {
	if schema == nil {
		return val, nil
	}
	prop, ok := schema.props[key]
	if !ok {
		// Unknown key: let the server reject it (additionalProperties:false → validation_failed) rather
		// than guess. Sending the string keeps the error server-authoritative.
		return val, nil
	}
	switch {
	case prop.types["boolean"]:
		b, err := strconv.ParseBool(val)
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not a boolean (use true/false)", key, val)
		}
		return b, nil
	case prop.types["integer"]:
		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not an integer", key, val)
		}
		return n, nil
	case prop.types["number"]:
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not a number", key, val)
		}
		return f, nil
	case prop.types["array"]:
		// Comma-separated multi-select; trim members, validate against the item enum when present.
		// Non-nil initial slice so an emptied value (e.g. `key= ` / `key=,`) marshals to [] (an empty
		// list), not null — the explicit unconfigure gesture is `key=` (empty string), handled above.
		items := []string{}
		for _, raw := range strings.Split(val, ",") {
			item := strings.TrimSpace(raw)
			if item == "" {
				continue
			}
			if len(prop.itemEnum) > 0 && !contains(prop.itemEnum, item) {
				return nil, fmt.Errorf("%s: %q is not one of %s", key, item, strings.Join(prop.itemEnum, ", "))
			}
			items = append(items, item)
		}
		return items, nil
	default:
		// string (or unconstrained): enforce a scalar enum if the schema declares one.
		if len(prop.enum) > 0 && !contains(prop.enum, val) {
			return nil, fmt.Errorf("%s: %q is not one of %s", key, val, strings.Join(prop.enum, ", "))
		}
		return val, nil
	}
}

func contains(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

// --- get: render ---

// schemaView projects the parsed schema onto what rendering needs (groups in order, per-field group /
// order / label). The parsed schema stays in the CLI because its type and enum information drives
// `set`'s coercion, which is logic, not presentation.
func schemaView(schema *tenantSchema) *render.SettingsSchema {
	if schema == nil {
		return nil
	}
	view := &render.SettingsSchema{Fields: make(map[string]render.SettingsField, len(schema.props))}
	for _, g := range schema.groups {
		view.Groups = append(view.Groups, render.SettingsGroup{Key: g.key, Label: g.labelNL})
	}
	for name, p := range schema.props {
		view.Fields[name] = render.SettingsField{Group: p.group, Order: p.order, Label: p.labelNL}
	}
	return view
}
