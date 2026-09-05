// The wire contract — the exact JSON shapes the rootcause API returns — is spread across one
// `<endpoint>_types.go` file per endpoint family, sitting next to the methods that call it
// (run_types.go, console_types.go, brain_types.go, …). Wherever they live, field names and omitempty
// MUST match the server verbatim: the CLI only RENDERS these, it never invents or reshapes data.
// Anything the server omits stays a zero value (a pointer where "absent" must be distinguishable from
// "zero", e.g. last_success / kb_enrich_model).
//
// This file keeps only what is shared across those families.
package client

// hostOnlyMetadataKeys are the freeform run-metadata keys that carry host-only telemetry: LLM spend /
// token counts (unit economics) and the SERVING MODEL IDENTITY (route attribution — the rung that
// answered and the rung it fell back from are as cost-reverse-engineerable as the provider slug). The
// server strips all of them on projection and the typed DTOs dropped their fields, but `metadata` is a
// passthrough map: an older server still emitting them would otherwise reach a rendered surface or a
// debug dump. Every metadata passthrough filters on this. Mirrors the server's own hostOnlyMetadataKeys.
var hostOnlyMetadataKeys = map[string]bool{
	"cost_usd": true, "total_cost_usd": true, "run_cost_usd": true, "max_run_usd_spent": true,
	"tokens": true, "total_tokens": true, "run_total_tokens": true, "peak_context_tokens": true,
	"input_tokens": true, "output_tokens": true,
	"model": true, "model_fallback_from": true,
}

// HostOnlyMetadataKey reports whether a run-metadata key must never be rendered (spend / token counts /
// serving model identity).
func HostOnlyMetadataKey(k string) bool { return hostOnlyMetadataKeys[k] }
