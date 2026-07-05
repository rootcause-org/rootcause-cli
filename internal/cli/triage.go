package cli

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/render"
)

func newTriageCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "triage",
		Short: "Read or change mail triage policy and hard rules",
	}
	cmd.AddCommand(triagePolicyCmd(e), triageRulesCmd(e))
	return cmd
}

func triagePolicyCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "policy", Short: "Read or edit free-form triage guidance"}
	cmd.AddCommand(
		triagePolicyGetCmd(e),
		triagePolicySetCmd(e),
	)
	return cmd
}

func triagePolicyGetCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Show triage policy guidance",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			raw, err := triageRaw(e, http.MethodGet, "/api/v1/triage/policy", nil)
			if err != nil {
				return err
			}
			return render.JSON(e.out, raw)
		},
	}
}

func triagePolicySetCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "set <guidance>",
		Short: "Replace triage policy guidance",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			raw, err := triageRaw(e, http.MethodPatch, "/api/v1/triage/policy", map[string]any{"guidance": args[0]})
			if err != nil {
				return err
			}
			return render.JSON(e.out, raw)
		},
	}
}

func triageRulesCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "rules", Short: "Read or edit deterministic triage rules"}
	cmd.AddCommand(
		triageRulesListCmd(e),
		triageRuleAddCmd(e),
		triageRuleSetCmd(e),
		triageRuleRmCmd(e),
	)
	return cmd
}

func triageRulesListCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List triage hard rules",
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			raw, err := triageRaw(e, http.MethodGet, "/api/v1/triage/rules", nil)
			if err != nil {
				return err
			}
			return render.JSON(e.out, raw)
		},
	}
}

func triageRuleAddCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "add effect=<skip|force_process> match_kind=<...> pattern=<...> [header_name=...] [reason=...] [priority=N] [enabled=true|false]",
		Short: "Create a triage hard rule",
		Args:  cobra.MinimumNArgs(3),
		RunE: func(_ *cobra.Command, args []string) error {
			body, err := parseTriageRuleArgs(args)
			if err != nil {
				return err
			}
			raw, err := triageRaw(e, http.MethodPost, "/api/v1/triage/rules", body)
			if err != nil {
				return err
			}
			return render.JSON(e.out, raw)
		},
	}
}

func triageRuleSetCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "set <id> field=value [field=value...]",
		Short: "Patch a triage hard rule",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			body, err := parseTriageRuleArgs(args[1:])
			if err != nil {
				return err
			}
			raw, err := triageRaw(e, http.MethodPatch, "/api/v1/triage/rules/"+url.PathEscape(args[0]), body)
			if err != nil {
				return err
			}
			return render.JSON(e.out, raw)
		},
	}
}

func triageRuleRmCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <id>",
		Short: "Delete a triage hard rule",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			raw, err := triageRaw(e, http.MethodDelete, "/api/v1/triage/rules/"+url.PathEscape(args[0]), nil)
			if err != nil {
				return err
			}
			if e.jsonOut() {
				if len(raw) > 0 {
					return render.JSON(e.out, raw)
				}
				return render.JSON(e.out, []byte(`{"deleted":"`+args[0]+`"}`))
			}
			_, _ = fmt.Fprintf(e.out, "deleted triage rule %s\n", args[0])
			return nil
		},
	}
}

func triageRaw(e *env, method, path string, body map[string]any) ([]byte, error) {
	c, err := e.newClient()
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	if project := e.scopeProject(); project != "" {
		q.Set("project", project)
	}
	if tenant := e.scopeTenant(); tenant != "" {
		q.Set("tenant", tenant)
	}
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	return c.Raw(e.ctx(), method, path, body)
}

func parseTriageRuleArgs(args []string) (map[string]any, error) {
	body := make(map[string]any, len(args))
	for _, arg := range args {
		key, val, ok := strings.Cut(arg, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid argument %q: expected key=value", arg)
		}
		switch key {
		case "effect", "match_kind", "pattern", "header_name", "reason":
			body[key] = val
		case "enabled":
			b, err := strconv.ParseBool(val)
			if err != nil {
				return nil, fmt.Errorf("enabled: %q is not a boolean (use true/false)", val)
			}
			body[key] = b
		case "priority":
			n, err := strconv.Atoi(val)
			if err != nil {
				return nil, fmt.Errorf("priority: %q is not an integer", val)
			}
			body[key] = n
		default:
			return nil, fmt.Errorf("unknown triage rule field %q", key)
		}
	}
	return body, nil
}
