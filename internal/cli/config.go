package cli

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newConfigCmd builds `rc config get` and `rc config set k=v …`, mapping onto GET/PATCH
// /api/v1/settings. The server owns the key whitelist and all validation; the CLI just shapes the
// sparse PATCH body and renders the result, surfacing INVALID_SETTINGS field errors verbatim.
func newConfigCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Read or change project settings",
	}
	cmd.AddCommand(newConfigGetCmd(e), newConfigSetCmd(e))
	return cmd
}

func newConfigGetCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Show current settings (value / effective / default)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			if render.IsJSON(e.mode(), e.out) {
				raw, err := c.Raw(e.ctx(), "GET", settingsPath(e.scopeProject()), nil)
				if err != nil {
					return err
				}
				return render.JSON(e.out, raw)
			}
			s, err := c.GetSettings(e.ctx(), e.scopeProject())
			if err != nil {
				return err
			}
			render.Settings(e.out, s)
			return nil
		},
	}
}

func newConfigSetCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "set k=v [k=v…]",
		Short: "Change settings (sparse, validate-then-apply server-side)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			patch, err := parseSetArgs(args)
			if err != nil {
				return err
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			// PATCH returns the full updated settings; render that (so the user sees the new effective
			// values), JSON passthrough included.
			if render.IsJSON(e.mode(), e.out) {
				raw, err := c.Raw(e.ctx(), "PATCH", settingsPath(e.scopeProject()), patch)
				if err != nil {
					return err
				}
				return render.JSON(e.out, raw)
			}
			s, err := c.PatchSettings(e.ctx(), patch, e.scopeProject())
			if err != nil {
				return err
			}
			render.Settings(e.out, s)
			return nil
		},
	}
}

func settingsPath(project string) string {
	if project == "" {
		return "/api/v1/settings"
	}
	return "/api/v1/settings?project=" + url.QueryEscape(project)
}

// parseSetArgs turns key=value args into the sparse PATCH body. Keys pass through verbatim (the
// server owns the whitelist). Values are sent as strings EXCEPT max_run_usd, which the contract
// requires as a JSON number — we try to parse it as a float and send a number; if it doesn't parse,
// we fall back to the string and let the server reject it with INVALID_SETTINGS (better a server-side
// validation message than a client-side guess).
func parseSetArgs(args []string) (map[string]any, error) {
	patch := make(map[string]any, len(args))
	for _, arg := range args {
		key, val, ok := strings.Cut(arg, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid argument %q: expected key=value", arg)
		}
		if key == "max_run_usd" {
			if f, err := strconv.ParseFloat(val, 64); err == nil {
				patch[key] = f
				continue
			}
		}
		patch[key] = val
	}
	return patch, nil
}
