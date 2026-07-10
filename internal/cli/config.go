package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newOpenRouterKeyCmd builds `rc project model-key openrouter set|clear|reveal` over the bespoke
// /api/v1/settings/openrouter-key endpoint (PUT/DELETE/POST .../reveal). The key is box-wide. `set`
// reads the key from STDIN by default (secret hygiene); an explicit arg is accepted but lands in shell
// history. `reveal` is the only command that prints the value.
func newOpenRouterKeyCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "openrouter", Short: "Manage the OpenRouter API key (set/clear/reveal)"}
	cmd.AddCommand(openRouterKeySetCmd(e), openRouterKeyClearCmd(e), openRouterKeyRevealCmd(e))
	return cmd
}

func openRouterKeySetCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "set [<key>]",
		Short: "Store the OpenRouter key (from STDIN by default; never echoed)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var key string
			if len(args) == 1 && args[0] != "-" {
				key = args[0]
			} else {
				k, err := readSecretStdin(e, "OpenRouter key")
				if err != nil {
					return err
				}
				key = k
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			raw, err := c.SetOpenRouterKey(e.ctx(), key, e.scopeProject())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			_, _ = fmt.Fprintln(e.out, "OpenRouter key stored")
			return nil
		},
	}
}

func openRouterKeyClearCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Remove the stored OpenRouter key",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			raw, err := c.ClearOpenRouterKey(e.ctx(), e.scopeProject())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				if len(raw) > 0 {
					return render.JSON(e.out, raw)
				}
				return render.JSON(e.out, []byte(`{"cleared":true}`))
			}
			_, _ = fmt.Fprintln(e.out, "OpenRouter key cleared")
			return nil
		},
	}
}

func openRouterKeyRevealCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "reveal",
		Short: "Print the stored OpenRouter key (sensitive, shown once)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			item, raw, err := c.RevealOpenRouterKey(e.ctx(), e.scopeProject())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			_, _ = fmt.Fprintln(e.err, "warning: this is a live secret — handle with care; it is shown once")
			render.Secret(e.out, item)
			return nil
		},
	}
}

// kind is how a key's value is coerced into the PATCH body.
type valueKind int

const (
	kindString valueKind = iota
	kindNumber           // JSON number (e.g. max_run_usd)
	kindList             // comma-split → JSON array (e.g. pr.triggers, egress.allowlist)
	kindBool             // JSON boolean (e.g. actions_enabled, hide_attribution)
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
		case kindBool:
			return kindBool
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
	case t == "bool" || t == "boolean":
		return kindBool
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
		case kindBool:
			if b, err := strconv.ParseBool(val); err == nil {
				patch[key] = b
			} else {
				patch[key] = val // let the server return the precise "must be a boolean" message
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
