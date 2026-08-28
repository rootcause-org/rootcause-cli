package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// kind is how a key's value is coerced into the PATCH body.
type valueKind int

const (
	kindString valueKind = iota
	kindNumber           // JSON number (e.g. max_run_usd)
	kindList             // comma-split → JSON array (e.g. pr.triggers, egress.allowlist)
	kindBool             // JSON boolean (e.g. actions_enabled, hide_attribution)
	kindObject           // raw JSON object passed through (e.g. models.agent, a closed record of members)
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
		case kindObject:
			return kindObject
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
	case t == "object" || t == "record":
		return kindObject
	default:
		return kindString
	}
}

// parseSetArgs turns key=value args into the sparse PATCH body, using coerce to pick each value's JSON
// kind. Keys pass through verbatim (the server owns the whitelist). A LIST key comma-splits into a JSON
// array (empty value → empty array, the "clear" gesture); a NUMBER key parses to a JSON number, falling
// back to the raw string so the server returns the precise INVALID_SETTINGS message rather than a
// client-side guess; an OBJECT key rides through as raw JSON; everything else is a string.
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
		case kindObject:
			obj, err := parseObjectValue(key, val)
			if err != nil {
				return nil, err
			}
			patch[key] = obj
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

// parseObjectValue passes an object-typed value through as raw JSON: members are kept verbatim so the
// server (which owns the closed member whitelist and their types) is the only validator. An empty value
// is the clear gesture — an empty object, mirroring the empty-list clear. Anything that is not a JSON
// object is rejected here rather than sent, since the server error would blame the whole key.
func parseObjectValue(key, val string) (map[string]json.RawMessage, error) {
	if strings.TrimSpace(val) == "" {
		return map[string]json.RawMessage{}, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(val), &obj); err != nil || obj == nil {
		return nil, fmt.Errorf("invalid value for %s: expected a JSON object (e.g. %s='{\"tier\":\"pro\"}'), got %q", key, key, val)
	}
	return obj, nil
}
