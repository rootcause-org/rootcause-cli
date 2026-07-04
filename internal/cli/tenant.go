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

// newTenantCmd builds tenant hierarchy settings plus the legacy profile/projection record. `settings`
// edits the canonical project-tree hierarchy bag; `profile` talks to the still-shipped tenant profile
// API until the server renames that route off "settings".
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
		setSubCmd(e, "tenants", "slug"),
	)
	return cmd
}

func newTenantSettingsCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "settings",
		Short: "Read or edit nested tenant settings (persona/channel)",
	}
	idArg := func(_ *cobra.Command, _ []string) (string, error) {
		tenant := e.tenantSlug()
		if tenant == "" {
			return "", errMissingTenant
		}
		return tenant, nil
	}
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
		Use:   "get --tenant <slug>",
		Short: "Show a tenant's projection/profile values",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			tenant := e.tenantSlug()
			if tenant == "" {
				return errMissingTenant
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			project := e.scopeProject()
			ts, err := c.GetTenantSettings(e.ctx(), tenant, project)
			if err != nil {
				return err
			}
			if render.IsJSON(e.mode(), e.out) {
				// Passthrough: emit the whole record (tenant_id/version/applied_at + raw settings) so the
				// jq path sees exactly what the server holds.
				return writeJSON(e, ts)
			}
			// Group/label using the schema when it's reachable; fall back to a plain sorted key=value
			// list if the schema fetch fails (never block a read on the schema endpoint).
			schema, _ := c.GetTenantSettingsSchema(e.ctx(), project)
			renderTenantSettings(e, ts, parseSchema(schema))
			return nil
		},
	}
	return cmd
}

func newTenantProfileSetCmd(e *env) *cobra.Command {
	var unset []string
	cmd := &cobra.Command{
		Use:   "set --tenant <slug> key=val [key=val …]",
		Short: "Edit tenant projection/profile values",
		Args:  cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			tenant := e.tenantSlug()
			if tenant == "" {
				return errMissingTenant
			}
			if len(args) == 0 && len(unset) == 0 {
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
			settings, err := buildTenantPatch(args, unset, schema)
			if err != nil {
				return err
			}
			ts, err := c.PatchTenantSettings(e.ctx(), tenant, project, client.TenantSettingsPatchRequest{
				Settings: settings,
				Source:   "cli",
			})
			if err != nil {
				return err
			}
			if render.IsJSON(e.mode(), e.out) {
				return writeJSON(e, ts)
			}
			renderTenantSettings(e, ts, schema)
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

// errMissingTenant is the shared "--tenant required" error for get/set (schema is tenant-agnostic).
var errMissingTenant = fmt.Errorf("--tenant <slug> is required")

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

// renderTenantSettings prints a tenant's settings for a human. With a schema it groups fields by
// x-groups (ordered), labels them with x-label-nl, and orders within a group by x-order — only fields
// the record actually carries are shown. Without a schema it falls back to a flat, sorted key=value
// list. The header line carries the tenant + version so an operator can confirm what they just wrote.
func renderTenantSettings(e *env, ts *client.TenantSettings, schema *tenantSchema) {
	values := map[string]json.RawMessage{}
	if len(ts.Settings) > 0 {
		_ = json.Unmarshal(ts.Settings, &values)
	}
	_, _ = fmt.Fprintf(e.out, "tenant: %s", ts.TenantID)
	if ts.Version != "" {
		_, _ = fmt.Fprintf(e.out, "  version: %s", ts.Version)
	}
	if ts.AppliedAt != "" {
		_, _ = fmt.Fprintf(e.out, "  applied: %s", ts.AppliedAt)
	}
	_, _ = fmt.Fprintln(e.out)

	if len(values) == 0 {
		_, _ = fmt.Fprintln(e.out, "(no settings configured)")
		return
	}

	if schema == nil {
		// Plain key=value, sorted — acceptable fallback per the spec.
		for _, k := range sortedRawKeys(values) {
			_, _ = fmt.Fprintf(e.out, "  %s = %s\n", k, formatValue(values[k]))
		}
		return
	}

	rendered := map[string]bool{}
	for _, g := range schema.groups {
		// Collect this group's present fields, ordered by x-order then name.
		var keys []string
		for k := range values {
			if p, ok := schema.props[k]; ok && p.group == g.key {
				keys = append(keys, k)
			}
		}
		if len(keys) == 0 {
			continue
		}
		sort.SliceStable(keys, func(i, j int) bool {
			pi, pj := schema.props[keys[i]], schema.props[keys[j]]
			if pi.order != pj.order {
				return pi.order < pj.order
			}
			return keys[i] < keys[j]
		})
		_, _ = fmt.Fprintf(e.out, "\n%s:\n", g.labelNL)
		for _, k := range keys {
			rendered[k] = true
			_, _ = fmt.Fprintf(e.out, "  %s = %s\n", labelFor(schema, k), formatValue(values[k]))
		}
	}
	// Any field not placed by the schema (unknown key on the stored record) — show it so nothing is
	// silently hidden.
	var orphans []string
	for k := range values {
		if !rendered[k] {
			orphans = append(orphans, k)
		}
	}
	if len(orphans) > 0 {
		sort.Strings(orphans)
		_, _ = fmt.Fprintln(e.out, "\nOther:")
		for _, k := range orphans {
			_, _ = fmt.Fprintf(e.out, "  %s = %s\n", k, formatValue(values[k]))
		}
	}
}

// labelFor returns "key (Dutch label)" when the schema has an x-label-nl, else just the key — so the
// raw key (what `set` takes) is always visible alongside the human label.
func labelFor(schema *tenantSchema, key string) string {
	if p, ok := schema.props[key]; ok && p.labelNL != "" {
		return fmt.Sprintf("%s (%s)", key, p.labelNL)
	}
	return key
}

// formatValue renders one stored JSON value compactly: a JSON string as its bare text, null as the
// literal "null" (an explicitly-unconfigured field), anything else as its compact JSON.
func formatValue(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "null" || trimmed == "" {
		return "null"
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return trimmed
}

func sortedRawKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
