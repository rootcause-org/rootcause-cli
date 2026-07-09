package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/debugdump"
	"github.com/rootcause-org/rootcause-cli/internal/outputspill"
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
	var brainDiff bool
	var stream bool
	cmd := &cobra.Command{
		Use:   "run <id>",
		Short: "Show one run (--events for the trace, --full for the bundle, --brain-diff for the brain commit, --debug to decompose it offline)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			id := args[0]
			if more := boolsSet(events, full, debug, brainDiff); more > 1 {
				return fmt.Errorf("--events, --full, --brain-diff and --debug are mutually exclusive")
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			jsonMode := render.IsJSON(e.mode(), e.out)

			// --debug: decompose the /trace bundle into a jq-able JSONL + a thin markdown index on disk, then
			// print the two paths. The calling agent drills in with bash/jq — we don't summarize into stdout.
			if debug {
				outDir := e.outDir
				if outDir == "" {
					outDir = defaultDebugDir
				}
				return runDebug(e, c, id, outDir)
			}

			if full {
				// JSON mode is the renderer's input contract: emit the bundle as JSONL from the raw bytes so
				// no server field is dropped on the cross-repo seam. Table mode decodes into the typed bundle.
				if jsonMode {
					raw, err := c.Raw(e.ctx(), "GET", client.RunTracePath(id), nil)
					if err != nil {
						return err
					}
					return emitFullJSONL(e, id, raw, stream)
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
					return emitNDJSON(e, id, resp.Events, stream)
				}
				render.Events(e.out, resp)
				return nil
			}

			if brainDiff {
				// JSON mode is a byte-faithful passthrough (render, don't reshape); table mode decodes the
				// typed BrainDiff and renders the commit + files + diff.
				if jsonMode {
					raw, err := c.Raw(e.ctx(), "GET", "/api/v1/runs/"+url.PathEscape(id)+"/brain-diff", nil)
					if err != nil {
						return err
					}
					return e.renderJSON("run-brain-diff-"+id, raw)
				}
				resp, err := c.BrainDiff(e.ctx(), id)
				if err != nil {
					return err
				}
				render.BrainDiff(e.out, resp)
				return nil
			}

			if jsonMode {
				raw, err := c.Raw(e.ctx(), "GET", "/api/v1/runs/"+url.PathEscape(id), nil)
				if err != nil {
					return err
				}
				return e.renderJSON("run-"+id, raw)
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
	cmd.Flags().BoolVar(&brainDiff, "brain-diff", false, "show the journal commit this run wrote to the brain")
	cmd.Flags().BoolVar(&stream, "stream", false, "stream JSONL to stdout even when it is large")
	cmd.AddCommand(runFeedbackCmd(e), runRetryCmd(e))
	return cmd
}

// runFeedbackCmd: `rc run feedback <id> [--score N] [--comment TEXT]` over POST
// /api/v1/runs/{id}/feedback. The score+comment feed the consolidation plane (run-trace feedback). At
// least one of --score/--comment must be given. Score rides as a JSON number.
func runFeedbackCmd(e *env) *cobra.Command {
	var score int
	var comment string
	var scoreSet bool
	cmd := &cobra.Command{
		Use:   "feedback <run-id> [--score N] [--comment TEXT]",
		Short: "Record score/comment feedback on a run's trace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cc *cobra.Command, args []string) error {
			scoreSet = cc.Flags().Changed("score")
			if !scoreSet && comment == "" {
				return fmt.Errorf("nothing to record: pass --score and/or --comment")
			}
			body := map[string]any{}
			if scoreSet {
				body["score"] = score
			}
			if comment != "" {
				body["comment"] = comment
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			raw, err := c.RunFeedback(e.ctx(), args[0], body, e.scopeProject())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				if len(raw) > 0 {
					return render.JSON(e.out, raw)
				}
				return render.JSON(e.out, []byte(`{"recorded":true}`))
			}
			_, _ = fmt.Fprintf(e.out, "feedback recorded for run %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().IntVar(&score, "score", 0, "feedback score (e.g. -1, 0, 1)")
	cmd.Flags().StringVar(&comment, "comment", "", "free-text feedback comment")
	return cmd
}

// runRetryCmd: `rc run retry <id> [--tier standard|pro|max]` over POST /api/v1/runs/{id}/retry. Prints
// the NEW run id (the server re-enqueues the run, optionally at a different tier).
func runRetryCmd(e *env) *cobra.Command {
	var tier string
	cmd := &cobra.Command{
		Use:   "retry <run-id> [--tier standard|pro|max]",
		Short: "Re-run a run (optionally at a different tier); prints the new run id",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			body := map[string]any{}
			if tier != "" {
				body["tier"] = tier
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			raw, err := c.RunRetry(e.ctx(), args[0], body, e.scopeProject())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			it := asItem(raw)
			if id := cellOf(it, "run_id"); id != "" {
				_, _ = fmt.Fprintln(e.out, id)
				return nil
			}
			render.Item(e.out, it)
			return nil
		},
	}
	cmd.Flags().StringVar(&tier, "tier", "", "model tier for the retry: standard|pro|max")
	return cmd
}

// cellOf extracts a string field from an Item (the new run id, etc.); "" if absent or non-string.
func cellOf(it client.Item, key string) string {
	raw, ok := it[key]
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

// defaultDebugDir is where `rc run <id> --debug` writes its two files unless --out-dir overrides it.
// All rc local artifacts live under the wholesale-gitignored `.rootcause/` dir (one ignore rule covers
// every subfolder); brains seed `/.rootcause/` so these dumps (real run data, PII) never get committed.
const defaultDebugDir = ".rootcause/debug"

// runDebug pulls the run's /trace bundle (cross-project for an all-projects admin token) and writes the
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

	jf, err := os.OpenFile(jsonlPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
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
	if err := os.WriteFile(indexPath, []byte(debugdump.RenderIndex(full)), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", indexPath, err)
	}

	if e.jsonOut() && !e.rawOutput {
		cfg := e.spillConfig()
		indexArt, err := outputspill.ArtifactForFile(cfg, indexPath, "text", false)
		if err != nil {
			return err
		}
		jsonlArt, err := outputspill.ArtifactForFile(cfg, jsonlPath, "jsonl", false)
		if err != nil {
			return err
		}
		return outputspill.WriteManifest(e.out, outputspill.Manifest{
			Spilled: true,
			Artifacts: map[string]outputspill.Artifact{
				"index": indexArt,
				"trace": jsonlArt,
			},
			Hints: []string{
				"sed -n '1,160p' " + outputspill.ShellQuote(indexArt.Path),
				"jq -r 'select(.type==\"event\")' " + outputspill.ShellQuote(jsonlArt.Path),
				"jq -r 'select(.disp==\"1\")' " + outputspill.ShellQuote(jsonlArt.Path),
			},
			RawModeHint: "rerun with --raw-output to print legacy debug paths to stdout",
		})
	}

	// Two paths + a one-line summary on stdout so the calling agent can relay them without re-fetching.
	_, _ = fmt.Fprintln(e.out, indexPath)
	_, _ = fmt.Fprintln(e.out, jsonlPath)
	_, _ = fmt.Fprintf(e.err, "run %s · status=%s · %d events · read the index, then jq the jsonl\n",
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
func emitNDJSON(e *env, id string, events []client.Event, stream bool) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			return err
		}
	}
	return emitMaybeSpilledJSONL(e, "run-"+shortID(id), "events.jsonl", buf.Bytes(), stream)
}

// emitFullJSONL turns the /trace bundle into the brain-renderer's input contract: a `{"type":"run",…}`
// header line followed by one `{"type":"event",…}` line per event. It works from the RAW bundle bytes
// (decomposing {run:{…},events:[…]}) so every server field rides through verbatim. The run header also
// gets the derived grounding_source_drift_count for quick jq filters. This is the stable cross-repo
// seam; keep its shape pinned by the golden test.
func emitFullJSONL(e *env, id string, raw json.RawMessage, stream bool) error {
	var bundle struct {
		Run    json.RawMessage   `json:"run"`
		Events []json.RawMessage `json:"events"`
	}
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return fmt.Errorf("decode full bundle: %w", err)
	}
	var buf bytes.Buffer
	if len(bundle.Run) > 0 {
		if err := emitTyped(&buf, bundle.Run, "run"); err != nil {
			return err
		}
	}
	for _, ev := range bundle.Events {
		if err := emitTyped(&buf, ev, "event"); err != nil {
			return err
		}
	}
	return emitMaybeSpilledJSONL(e, "run-"+shortID(id), "trace.jsonl", buf.Bytes(), stream)
}

func emitMaybeSpilledJSONL(e *env, label, name string, b []byte, stream bool) error {
	cfg := e.spillConfig()
	if cfg.Raw || stream || !cfg.ShouldSpillInline(b) {
		_, err := e.out.Write(b)
		return err
	}
	art, err := outputspill.WriteArtifact(cfg, cfg.DirFor(label), name, b, "jsonl", false)
	if err != nil {
		return err
	}
	return outputspill.WriteManifest(e.out, outputspill.ManifestForArtifact(art))
}

// emitTyped writes one JSONL line: the raw JSON object with a `"type"` key injected, value bytes
// preserved exactly (a map[string]RawMessage keeps each field's bytes; Go sorts the keys, giving a
// deterministic line). A non-object value is passed through unchanged so we never swallow the body.
func emitTyped(w interface{ Write([]byte) (int, error) }, obj json.RawMessage, typ string) error {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(obj, &fields); err != nil {
		// Not a JSON object — emit verbatim rather than dropping it.
		_, werr := w.Write(append(append([]byte{}, obj...), '\n'))
		return werr
	}
	fields["type"] = json.RawMessage(`"` + typ + `"`)
	if typ == "run" {
		if count, ok := groundingSourceDriftCount(fields["grounding_sources"]); ok {
			b, err := json.Marshal(count)
			if err != nil {
				return err
			}
			fields["grounding_source_drift_count"] = b
		}
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(fields)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	if id == "" {
		return "unknown"
	}
	return id
}

func groundingSourceDriftCount(raw json.RawMessage) (int, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var gs client.GroundingSources
	if err := json.Unmarshal(raw, &gs); err != nil {
		return 0, false
	}
	return client.GroundingSourceDriftCount(&gs), true
}
