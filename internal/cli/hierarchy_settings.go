package cli

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// hierarchySchema is the nested settings surface as the SERVER describes it: group prefix → bare field
// name → that field's schema. The CLI holds no key list of its own — discovery is the contract, so a
// knob the server gained (e.g. channel.chat_mode) is settable the day it ships, with no CLI release.
type hierarchySchema map[string]map[string]client.FieldSchema

// newHierarchySchema folds /meta/schema into the group→field index. Two sources, same shape:
// hierarchy_settings[group].field_schemas (authoritative — the JSONB-only channel.* keys live only
// there), plus any resource field whose dotted key prefix IS its group (persona.*, which also rides the
// flat settings bag). An older server that sends only the bare `fields` list still yields settable keys;
// those carry no type, and coerceHierarchyValue then infers one from the literal.
func newHierarchySchema(resp *client.SchemaResponse) hierarchySchema {
	out := hierarchySchema{}
	put := func(group, field string, f client.FieldSchema) {
		if group == "" || field == "" {
			return
		}
		if out[group] == nil {
			out[group] = map[string]client.FieldSchema{}
		}
		if existing, ok := out[group][field]; ok && existing.Type != "" && f.Type == "" {
			return
		}
		out[group][field] = f
	}
	for group, g := range resp.HierarchySettings {
		for _, name := range g.Fields {
			put(group, name, client.FieldSchema{Key: group + "." + name})
		}
		for _, f := range g.FieldSchemas {
			if _, field, ok := strings.Cut(f.Key, "."); ok {
				put(group, field, f)
			}
		}
	}
	for _, bag := range resp.Resources {
		for _, f := range bag.Fields {
			group, field, ok := strings.Cut(f.Key, ".")
			if !ok || group != f.Group {
				// Not a nested settings key (e.g. models.agent is group "model"): the hierarchy routes
				// never carry it, so listing it here would only invite a 400.
				continue
			}
			put(group, field, f)
		}
	}
	return out
}

// groups lists the settable group prefixes, sorted. Empty schema ⇒ the two groups every server has, so
// the "unknown group" message stays useful even against a server that describes nothing.
func (h hierarchySchema) groups() []string {
	if len(h) == 0 {
		return []string{"channel", "persona"}
	}
	out := make([]string, 0, len(h))
	for g := range h {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

func newProjectHierarchySettingsCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "behavior",
		Short: "Read or edit nested project settings (persona/channel)",
	}
	cmd.AddCommand(
		hierarchySettingsGetCmd(e, "project", func(*cobra.Command, []string) (string, error) { return "", nil }),
		hierarchySettingsSetCmd(e, "project", func(*cobra.Command, []string) (string, error) { return "", nil }),
	)
	return cmd
}

func newMailboxSettingsCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "settings",
		Short: "Read or edit nested mailbox settings (persona/channel)",
	}
	idArg := func(_ *cobra.Command, args []string) (string, error) { return args[0], nil }
	cmd.AddCommand(
		hierarchySettingsGetCmd(e, "mailbox", idArg),
		hierarchySettingsSetCmd(e, "mailbox", idArg),
	)
	return cmd
}

func hierarchySettingsGetCmd(e *env, scope string, idArg func(*cobra.Command, []string) (string, error)) *cobra.Command {
	use := "get"
	args := cobra.NoArgs
	if scope == "mailbox" || scope == "tenant" {
		use = "get <id>"
		if scope == "tenant" {
			use = "get <slug>"
		}
		args = cobra.ExactArgs(1)
	}
	return &cobra.Command{
		Use:   use,
		Short: "Show settings with resolved provenance",
		Args:  args,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			project, err := hierarchyProject(e, c)
			if err != nil {
				return err
			}
			id, err := idArg(cmd, args)
			if err != nil {
				return err
			}
			if e.jsonOut() {
				raw, err := c.RawHierarchySettings(e.ctx(), http.MethodGet, scope, project, id, nil, true)
				if err != nil {
					return err
				}
				return render.JSON(e.out, raw)
			}
			resp, err := c.GetHierarchySettings(e.ctx(), scope, project, id, true)
			if err != nil {
				return err
			}
			render.HierarchySettings(e.out, resp)
			return nil
		},
	}
}

func hierarchySettingsSetCmd(e *env, scope string, idArg func(*cobra.Command, []string) (string, error)) *cobra.Command {
	var unset []string
	use := "set group.key=value [group.key=value...]"
	args := cobra.ArbitraryArgs
	if scope == "mailbox" || scope == "tenant" {
		use = "set <id> group.key=value [group.key=value...]"
		if scope == "tenant" {
			use = "set <slug> group.key=value [group.key=value...]"
		}
		args = cobra.MinimumNArgs(1)
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: "Patch settings (nested; key= or --unset clears local override)",
		Args:  args,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			project, err := hierarchyProject(e, c)
			if err != nil {
				return err
			}
			id := ""
			patchArgs := args
			if scope != "project" {
				id, err = idArg(cmd, args)
				if err != nil {
					return err
				}
			}
			if scope != "project" {
				patchArgs = args[1:]
			}
			if len(patchArgs) == 0 && len(unset) == 0 {
				return fmt.Errorf("nothing to set: pass group.key=value pairs and/or --unset group.key")
			}
			// Validate against the server's self-describing registry, never a baked-in key list: one
			// fetch, before the PATCH. A set needs the network anyway, so a discovery failure is a hard
			// error — silently skipping validation would turn a typo into a confusing server 400.
			schemaResp, _, err := c.GetSchema(e.ctx(), "", project)
			if err != nil {
				return err
			}
			patch, err := buildHierarchyPatch(patchArgs, unset, newHierarchySchema(schemaResp))
			if err != nil {
				return err
			}
			if e.jsonOut() {
				raw, err := c.RawHierarchySettings(e.ctx(), http.MethodPatch, scope, project, id, patch, true)
				if err != nil {
					return err
				}
				return render.JSON(e.out, raw)
			}
			resp, err := c.PatchHierarchySettings(e.ctx(), scope, project, id, patch, true)
			if err != nil {
				return err
			}
			render.HierarchySettings(e.out, resp)
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&unset, "unset", nil, "clear a scope-local override (group.key); repeatable")
	return cmd
}

func hierarchyProject(e *env, c *client.Client) (string, error) {
	if project := e.scopeProject(); project != "" {
		return project, nil
	}
	who, err := c.Whoami(e.ctx())
	if err == nil && who != nil && who.Project != nil {
		switch {
		case who.Project.Slug != "":
			return who.Project.Slug, nil
		case who.Project.Name != "":
			return who.Project.Name, nil
		case who.Project.ID != "":
			return who.Project.ID, nil
		}
	}
	return "", fmt.Errorf("--project <project> is required for hierarchy settings unless the active login is project-scoped")
}

func buildHierarchyPatch(args, unset []string, schema hierarchySchema) (map[string]any, error) {
	root := map[string]any{}
	seen := map[string]bool{}
	for _, arg := range args {
		key, val, ok := strings.Cut(arg, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid argument %q: expected group.key=value", arg)
		}
		if seen[key] {
			return nil, fmt.Errorf("key %q given more than once", key)
		}
		seen[key] = true
		if err := putHierarchyValue(root, schema, key, val, val == ""); err != nil {
			return nil, err
		}
	}
	for _, key := range unset {
		if key == "" {
			return nil, fmt.Errorf("invalid --unset: empty key")
		}
		if seen[key] {
			return nil, fmt.Errorf("key %q both set and --unset", key)
		}
		seen[key] = true
		if err := putHierarchyValue(root, schema, key, "", true); err != nil {
			return nil, err
		}
	}
	return root, nil
}

func putHierarchyValue(root map[string]any, schema hierarchySchema, dotted, val string, clear bool) error {
	group, field, ok := strings.Cut(dotted, ".")
	if !ok || group == "" || field == "" {
		return fmt.Errorf("%s: expected group.key (%s)", dotted, strings.Join(schema.groups(), ".* or ")+".*")
	}
	fields, ok := schema[group]
	if !ok {
		return fmt.Errorf("%s: unknown settings group %q (want %s)", dotted, group, strings.Join(schema.groups(), " or "))
	}
	f, ok := fields[field]
	if !ok {
		return fmt.Errorf("%s: unknown %s setting (try `rc schema`)", dotted, group)
	}
	bag, _ := root[group].(map[string]any)
	if bag == nil {
		bag = map[string]any{}
		root[group] = bag
	}
	// A clear (`key=` or --unset) drops the local override: no value, so no type or enum to check —
	// only the key itself has to exist.
	if clear {
		bag[field] = nil
		return nil
	}
	v, err := coerceHierarchyValue(f, dotted, val)
	if err != nil {
		return err
	}
	bag[field] = v
	return nil
}

// coerceHierarchyValue turns the CLI string into the JSON value the field's DECLARED type wants. An
// enum is checked here (the allowed set is right there in the schema, so a typo shouldn't cost a round
// trip); every other constraint stays the server's. A field with no declared type (older /meta/schema)
// falls back to inferring bool/int from the literal, so channel.labeling_enabled=true is still a JSON
// boolean.
func coerceHierarchyValue(f client.FieldSchema, dotted, val string) (any, error) {
	if len(f.Enum) > 0 {
		for _, allowed := range f.Enum {
			if val == allowed {
				return val, nil
			}
		}
		return nil, fmt.Errorf("%s: %q is not one of %s", dotted, val, strings.Join(f.Enum, ", "))
	}
	if f.Type == "" {
		return inferHierarchyValue(val), nil
	}
	switch normalizeType(f.Type) {
	case kindBool:
		b, err := strconv.ParseBool(val)
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not a boolean (use true/false)", dotted, val)
		}
		return b, nil
	case kindNumber:
		if n, err := strconv.ParseInt(val, 10, 64); err == nil {
			return n, nil
		}
		fl, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not a number", dotted, val)
		}
		return fl, nil
	case kindList:
		return splitList(val), nil
	default:
		return val, nil
	}
}

// inferHierarchyValue is the typeless path: a bool/int literal becomes JSON of that kind, anything else
// stays a string. Only reachable against a server whose /meta/schema names the key but not its type.
func inferHierarchyValue(val string) any {
	switch strings.ToLower(val) {
	case "true":
		return true
	case "false":
		return false
	}
	if n, err := strconv.ParseInt(val, 10, 64); err == nil {
		return n
	}
	return val
}
