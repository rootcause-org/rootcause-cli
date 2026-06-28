package cli

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
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
			c, err := e.newClient()
			if err != nil {
				return err
			}
			// Schema-aware coercion (the "generated CLI" ideal): fetch the registry once so a list-typed
			// field comma-splits into a JSON array and a number-typed field rides as a JSON number. The
			// lookup is best-effort — on any miss parseSetArgs falls back to its known list/number keys,
			// so `config set` still works against an older/odd server (the server is the final validator).
			coerce := newValueCoercer(e, c)
			patch, err := parseSetArgs(args, coerce)
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

// kind is how a key's value is coerced into the PATCH body.
type valueKind int

const (
	kindString valueKind = iota
	kindNumber           // JSON number (e.g. max_run_usd)
	kindList             // comma-split → JSON array (e.g. pr.triggers, egress.allowlist)
)

// coercer resolves a settings key to its value kind. The schema-aware coercer is built from
// /meta/schema; on a miss it falls back to the known list/number keys so the CLI degrades gracefully.
type coercer func(key string) valueKind

// knownListKeys / knownNumberKeys are the static fallback when the schema lookup misses (older server,
// network blip, or a key the registry doesn't carry). The schema is authoritative when it answers.
var (
	knownListKeys   = map[string]bool{"egress.allowlist": true, "pr.triggers": true}
	knownNumberKeys = map[string]bool{"max_run_usd": true}
)

// fallbackCoercer is the no-schema path: classify by the known key sets only.
func fallbackCoercer(key string) valueKind {
	switch {
	case knownListKeys[key]:
		return kindList
	case knownNumberKeys[key]:
		return kindNumber
	default:
		return kindString
	}
}

// newValueCoercer fetches /meta/schema ONCE and returns a coercer that classifies a key by its declared
// field TYPE: a list/array type → kindList, a numeric type (int/float/number) → kindNumber, else the
// static fallback. A schema fetch failure degrades to fallbackCoercer (the known list/number key sets),
// so `config set` never hard-depends on the discovery endpoint. The fetch is lazy — it runs only when
// the first key is classified, so a set that touches no ambiguous key still works if the server lacks
// /meta/schema (the fallback handles the known list/number keys).
func newValueCoercer(e *env, c *client.Client) coercer {
	var (
		types  map[string]string
		loaded bool
	)
	return func(key string) valueKind {
		if !loaded {
			loaded = true
			if resp, err := c.GetSchema(e.ctx(), "", e.scopeProject()); err == nil {
				types = make(map[string]string)
				for _, bag := range resp.Resources {
					for _, f := range bag.Fields {
						types[f.Key] = f.Type
					}
				}
			}
		}
		typ, ok := types[key]
		if !ok {
			return fallbackCoercer(key)
		}
		switch normalizeType(typ) {
		case kindList:
			return kindList
		case kindNumber:
			return kindNumber
		default:
			// Schema knew the key as a scalar string/enum; still honor a known number/list override in
			// case the registry's type vocabulary drifts from what the CLI recognizes.
			return fallbackCoercer(key)
		}
	}
}

// normalizeType maps a server field-type string onto a value kind. List types are recognized loosely
// (the registry may say "list", "array", "string[]", or "*_list") so the CLI tolerates type-name drift.
func normalizeType(typ string) valueKind {
	t := strings.ToLower(strings.TrimSpace(typ))
	switch {
	case t == "list" || t == "array" || strings.HasSuffix(t, "[]") || strings.HasSuffix(t, "_list"):
		return kindList
	case t == "int" || t == "integer" || t == "number" || t == "float" || t == "float64":
		return kindNumber
	default:
		return kindString
	}
}

// parseSetArgs turns key=value args into the sparse PATCH body, using coerce to pick each value's JSON
// kind. Keys pass through verbatim (the server owns the whitelist). A LIST key comma-splits into a JSON
// array (empty value → empty array, the "clear" gesture); a NUMBER key parses to a JSON number, falling
// back to the raw string so the server returns the precise INVALID_SETTINGS message rather than a
// client-side guess; everything else is a string.
func parseSetArgs(args []string, coerce coercer) (map[string]any, error) {
	patch := make(map[string]any, len(args))
	for _, arg := range args {
		key, val, ok := strings.Cut(arg, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid argument %q: expected key=value", arg)
		}
		switch coerce(key) {
		case kindList:
			patch[key] = splitList(val)
		case kindNumber:
			if f, err := strconv.ParseFloat(val, 64); err == nil {
				patch[key] = f
			} else {
				patch[key] = val
			}
		default:
			patch[key] = val
		}
	}
	return patch, nil
}

// splitList comma-splits a list value into a JSON array, trimming surrounding whitespace per element.
// An empty value → an empty (non-nil) array so `pr.triggers=` clears the list rather than sending null.
func splitList(val string) []string {
	if strings.TrimSpace(val) == "" {
		return []string{}
	}
	parts := strings.Split(val, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}
