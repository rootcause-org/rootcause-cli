package cli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/debugdump"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newRunCmd builds `rc run <id>` and `rc run <id> --events`. Without --events it's GET
// /api/v1/runs/{id} (one run, high level); with --events it's the full per-event trace. In JSON mode
// the trace is emitted as NDJSON (one event object per line) — streamable and jq-friendly, not a
// wrapping array — while the high-level view stays a single JSON object.
func newRunCmd(e *env) *cobra.Command {
	var events bool
	var full bool
	var debug bool
	var outDir string
	cmd := &cobra.Command{
		Use:   "run <id>",
		Short: "Show one run (--events for the trace, --full for the bundle, --debug to decompose it offline)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			id := args[0]
			if more := boolsSet(events, full, debug); more > 1 {
				return fmt.Errorf("--events, --full and --debug are mutually exclusive")
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			jsonMode := render.IsJSON(e.mode(), e.out)

			// --debug: decompose the /full bundle into a jq-able JSONL + a thin markdown index on disk, then
			// print the two paths. The calling agent drills in with bash/jq — we don't summarize into stdout.
			if debug {
				return runDebug(e, c, id, outDir)
			}

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
	cmd.Flags().BoolVar(&debug, "debug", false, "decompose the run into a jq-able JSONL + thin markdown index on disk")
	cmd.Flags().StringVar(&outDir, "out-dir", defaultDebugDir, "directory for --debug output files")
	return cmd
}

// defaultDebugDir is where `rc run <id> --debug` writes its two files unless --out-dir overrides it.
const defaultDebugDir = "rc-debug"

// runDebug pulls the run's /full bundle (cross-project for an all-projects admin token) and writes the
// raw jq-able JSONL event log + a thin markdown index, printing both paths. It does NOT render the run
// into stdout — the whole point is to hand the agent primitives (the two files) it drills into itself.
func runDebug(e *env, c *client.Client, id, outDir string) error {
	full, err := c.Full(e.ctx(), id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create out dir %s: %w", outDir, err)
	}
	jsonlPath := filepath.Join(outDir, debugdump.JSONLName(full))
	indexPath := filepath.Join(outDir, debugdump.IndexName(full))

	jf, err := os.Create(jsonlPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", jsonlPath, err)
	}
	if err := debugdump.EmitJSONL(jf, full); err != nil {
		_ = jf.Close()
		return fmt.Errorf("write %s: %w", jsonlPath, err)
	}
	if err := jf.Close(); err != nil {
		return fmt.Errorf("close %s: %w", jsonlPath, err)
	}
	if err := os.WriteFile(indexPath, []byte(debugdump.RenderIndex(full)), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", indexPath, err)
	}

	// Two paths + a one-line summary on stdout so the calling agent can relay them without re-fetching.
	fmt.Fprintln(e.out, indexPath)
	fmt.Fprintln(e.out, jsonlPath)
	fmt.Fprintf(e.err, "run %s · status=%s · %d events · read the index, then jq the jsonl\n",
		full.Run.RunID, full.Run.Status, len(full.Events))
	return nil
}

// boolsSet counts how many of the given flags are true (for mutual-exclusion checks).
func boolsSet(flags ...bool) int {
	n := 0
	for _, f := range flags {
		if f {
			n++
		}
	}
	return n
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
