// Command rc is the rootcause CLI: a thin, scriptable client over the rootcause JSON API. It
// holds no business logic — every subcommand is one HTTP call rendered for humans (TTY) or piped as
// JSON. main is intentionally trivial: all wiring lives in internal/cli.
package main

import (
	"os"

	"github.com/rootcause-org/rootcause-cli/internal/cli"
)

// version is the CLI version string surfaced by `rc --version`. Overridable at build time via
// -ldflags "-X main.version=…".
var version = ""

func main() {
	os.Exit(cli.Execute(cli.ResolveVersion(version)))
}
