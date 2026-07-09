package cli

import (
	"encoding/json"
	"fmt"

	"github.com/rootcause-org/rootcause-cli/internal/outputspill"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

func (e *env) spillConfig() outputspill.Config {
	return outputspill.NewConfig(e.outDir, e.noPreview, e.rawOutput)
}

func (e *env) renderJSON(label string, raw json.RawMessage) error {
	cfg := e.spillConfig()
	if m, err := outputspill.MaybeSpillJSON(cfg, label, raw); err != nil {
		return err
	} else if m != nil {
		return outputspill.WriteManifest(e.out, *m)
	}
	return render.JSON(e.out, raw)
}

func (e *env) renderBytes(label, name string, b []byte, format string) error {
	cfg := e.spillConfig()
	if !cfg.ShouldSpillBytes(b) {
		_, err := e.out.Write(b)
		return err
	}
	art, err := outputspill.WriteArtifact(cfg, cfg.DirFor(label), name, b, format, false)
	if err != nil {
		return err
	}
	if e.jsonOut() {
		return outputspill.WriteManifest(e.out, outputspill.ManifestForArtifact(art))
	}
	return writeSpillPreview(e, art)
}

func writeSpillPreview(e *env, art outputspill.Artifact) error {
	if _, err := fmt.Fprintf(e.out, "[output too large: %d bytes, %d lines - full output saved to %s]\n", art.Bytes, art.Lines, art.Path); err != nil {
		return err
	}
	if art.Preview != nil {
		if art.Preview.Head != "" {
			if _, err := fmt.Fprint(e.out, art.Preview.Head); err != nil {
				return err
			}
			if art.Preview.Head[len(art.Preview.Head)-1] != '\n' {
				if _, err := fmt.Fprintln(e.out); err != nil {
					return err
				}
			}
		}
		if art.Preview.Tail != "" {
			if _, err := fmt.Fprintln(e.out, "...[middle omitted]..."); err != nil {
				return err
			}
			if _, err := fmt.Fprint(e.out, art.Preview.Tail); err != nil {
				return err
			}
			if art.Preview.Tail[len(art.Preview.Tail)-1] != '\n' {
				if _, err := fmt.Fprintln(e.out); err != nil {
					return err
				}
			}
		}
	}
	if len(art.Hints) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(e.out, "\nHints:"); err != nil {
		return err
	}
	for _, h := range art.Hints {
		if _, err := fmt.Fprintf(e.out, "  %s\n", h); err != nil {
			return err
		}
	}
	return nil
}
