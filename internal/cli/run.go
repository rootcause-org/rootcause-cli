package cli

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newRunCmd builds `rc run <id>` and `rc run <id> --events`. Without --events it's GET
// /api/v1/runs/{id} (one run, high level); with --events it's the full per-event trace. In JSON mode
// the trace is emitted as NDJSON (one event object per line) — streamable and jq-friendly, not a
// wrapping array — while the high-level view stays a single JSON object.
func newRunCmd(e *env) *cobra.Command {
	var events bool
	var full bool
	cmd := &cobra.Command{
		Use:   "run <id>",
		Short: "Show one run (add --events for the trace, --full for the whole bundle)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			id := args[0]
			if events && full {
				return fmt.Errorf("--events and --full are mutually exclusive")
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			jsonMode := render.IsJSON(e.mode(), e.out)

			if full {
				// JSON mode is the renderer's input contract: emit the bundle as JSONL from the raw bytes so
				// no server field is dropped on the cross-repo seam. Table mode decodes into the typed bundle.
				if jsonMode {
					raw, err := c.Raw(e.ctx(), "GET", "/api/v1/runs/"+url.PathEscape(id)+"/full", nil)
					if err != nil {
						return err
					}
					return emitFullJSONL(e, raw)
				}
				resp, err := c.Full(e.ctx(), id)
				if err != nil {
					return err
				}
				render.Full(e.out, resp)
				return nil
			}

			if events {
				resp, err := c.Events(e.ctx(), id)
				if err != nil {
					return err
				}
				if jsonMode {
					return emitNDJSON(e, resp.Events)
				}
				render.Events(e.out, resp)
				return nil
			}

			if jsonMode {
				raw, err := c.Raw(e.ctx(), "GET", "/api/v1/runs/"+url.PathEscape(id), nil)
				if err != nil {
					return err
				}
				return render.JSON(e.out, raw)
			}
			detail, err := c.Run(e.ctx(), id)
			if err != nil {
				return err
			}
			render.Run(e.out, detail)
			return nil
		},
	}
	cmd.Flags().BoolVar(&events, "events", false, "show the full per-event trace")
	cmd.Flags().BoolVar(&full, "full", false, "show the whole bundle (header + trace; JSONL in -o json)")
	return cmd
}

// emitNDJSON writes one compact JSON object per event line. We re-marshal the typed events (rather
// than passing the server's wrapping {run_id,events:[...]} through) precisely to get the NDJSON shape
// the spec asks for: `| jq` over a stream, no enclosing array.
func emitNDJSON(e *env, events []client.Event) error {
	enc := json.NewEncoder(e.out)
	enc.SetEscapeHTML(false)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			return err
		}
	}
	return nil
}

// emitFullJSONL turns the /full bundle into the brain-renderer's input contract: a `{"type":"run",…}`
// header line followed by one `{"type":"event",…}` line per event. It works from the RAW bundle bytes
// (decomposing {run:{…},events:[…]}) so every server field rides through verbatim — the only transform
// is injecting the `type` discriminator. This is the stable cross-repo seam; keep its shape pinned by
// the golden test.
func emitFullJSONL(e *env, raw json.RawMessage) error {
	var bundle struct {
		Run    json.RawMessage   `json:"run"`
		Events []json.RawMessage `json:"events"`
	}
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return fmt.Errorf("decode full bundle: %w", err)
	}
	if len(bundle.Run) > 0 {
		if err := emitTyped(e, bundle.Run, "run"); err != nil {
			return err
		}
	}
	for _, ev := range bundle.Events {
		if err := emitTyped(e, ev, "event"); err != nil {
			return err
		}
	}
	return nil
}

// emitTyped writes one JSONL line: the raw JSON object with a `"type"` key injected, value bytes
// preserved exactly (a map[string]RawMessage keeps each field's bytes; Go sorts the keys, giving a
// deterministic line). A non-object value is passed through unchanged so we never swallow the body.
func emitTyped(e *env, obj json.RawMessage, typ string) error {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(obj, &fields); err != nil {
		// Not a JSON object — emit verbatim rather than dropping it.
		_, werr := e.out.Write(append(append([]byte{}, obj...), '\n'))
		return werr
	}
	fields["type"] = json.RawMessage(`"` + typ + `"`)
	enc := json.NewEncoder(e.out)
	enc.SetEscapeHTML(false)
	return enc.Encode(fields)
}
