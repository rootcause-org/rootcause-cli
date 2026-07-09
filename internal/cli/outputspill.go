package cli

import (
	"encoding/json"

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
