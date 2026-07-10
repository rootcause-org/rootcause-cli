package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

var hierarchyFields = map[string]map[string]valueKind{
	"channel": {
		"labeling_enabled":       kindBool,
		"inbox_cleaning_enabled": kindBool,
		"draft_font_css":         kindString,
		"note_from_address":      kindString,
	},
	"persona": {
		"signature": kindString,
		"tone":      kindString,
		"language":  kindString,
		"formality": kindString,
		"guidance":  kindString,
	},
}

func newProjectHierarchySettingsCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hierarchy",
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
			renderHierarchySettings(e, resp)
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
			patch, err := buildHierarchyPatch(patchArgs, unset)
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
			renderHierarchySettings(e, resp)
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

func buildHierarchyPatch(args, unset []string) (map[string]any, error) {
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
		if err := putHierarchyValue(root, key, val, val == ""); err != nil {
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
		if err := putHierarchyValue(root, key, "", true); err != nil {
			return nil, err
		}
	}
	return root, nil
}

func putHierarchyValue(root map[string]any, dotted, val string, clear bool) error {
	group, field, ok := strings.Cut(dotted, ".")
	if !ok || group == "" || field == "" {
		return fmt.Errorf("%s: expected group.key (persona.* or channel.*)", dotted)
	}
	fields, ok := hierarchyFields[group]
	if !ok {
		return fmt.Errorf("%s: unknown settings group %q (want persona or channel)", dotted, group)
	}
	kind, ok := fields[field]
	if !ok {
		return fmt.Errorf("%s: unknown %s setting", dotted, group)
	}
	bag, _ := root[group].(map[string]any)
	if bag == nil {
		bag = map[string]any{}
		root[group] = bag
	}
	if clear {
		bag[field] = nil
		return nil
	}
	if kind == kindBool {
		b, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("%s: %q is not a boolean (use true/false)", dotted, val)
		}
		bag[field] = b
		return nil
	}
	bag[field] = val
	return nil
}

func renderHierarchySettings(e *env, hs *client.HierarchySettings) {
	_, _ = fmt.Fprintf(e.out, "scope: %s", hs.Scope)
	if hs.Project != "" {
		_, _ = fmt.Fprintf(e.out, "  project: %s", hs.Project)
	}
	if hs.Tenant != "" {
		_, _ = fmt.Fprintf(e.out, "  tenant: %s", hs.Tenant)
	}
	if hs.Mailbox != "" {
		_, _ = fmt.Fprintf(e.out, "  mailbox: %s", hs.Mailbox)
	}
	_, _ = fmt.Fprintln(e.out)

	local := nestedRaw(hs.Settings)
	if len(local) == 0 {
		_, _ = fmt.Fprintln(e.out, "\nLocal overrides:\n  (none)")
	} else {
		_, _ = fmt.Fprintln(e.out, "\nLocal overrides:")
		renderNestedRaw(e, local)
	}
	resolved := nestedRaw(hs.Resolved)
	if len(resolved) > 0 {
		_, _ = fmt.Fprintln(e.out, "\nResolved:")
		renderResolved(e, resolved)
	}
}

func nestedRaw(raw json.RawMessage) map[string]map[string]json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var out map[string]map[string]json.RawMessage
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}

func renderNestedRaw(e *env, groups map[string]map[string]json.RawMessage) {
	for _, group := range sortedNestedGroups(groups) {
		_, _ = fmt.Fprintf(e.out, "  %s:\n", group)
		for _, field := range sortedRawKeys(groups[group]) {
			_, _ = fmt.Fprintf(e.out, "    %s = %s\n", field, formatValue(groups[group][field]))
		}
	}
}

func renderResolved(e *env, groups map[string]map[string]json.RawMessage) {
	for _, group := range sortedNestedGroups(groups) {
		_, _ = fmt.Fprintf(e.out, "  %s:\n", group)
		for _, field := range sortedRawKeys(groups[group]) {
			_, _ = fmt.Fprintf(e.out, "    %s = %s\n", field, formatResolvedField(groups[group][field]))
		}
	}
}

func formatResolvedField(raw json.RawMessage) string {
	var f struct {
		Value  json.RawMessage `json:"value"`
		Source string          `json:"source"`
	}
	if json.Unmarshal(raw, &f) != nil {
		return formatValue(raw)
	}
	if f.Source == "" {
		return formatValue(f.Value)
	}
	return fmt.Sprintf("%s (%s)", formatValue(f.Value), f.Source)
}

func sortedNestedGroups(m map[string]map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
