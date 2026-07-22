package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newCorpusCmd builds the `rc project corpus` group over the local-synthesis export API: `ls`/`get` read the
// harvest/survey corpus exports, and `download` fetches the Markdown corpus — to stdout, to a file, or
// split into a per-thread tree the local dream-cycle can grep. All endpoints need a connections:manage
// token; an all-projects token scopes with --project.
func newCorpusCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "corpus", Short: "Read local-synthesis corpus exports (harvest/survey)"}
	cmd.AddCommand(
		exportLsCmd(e),
		exportGetCmd(e),
		exportDownloadCmd(e),
		exportMineSettingsCmd(e),
	)
	return cmd
}

// exportMineSettingsCmd: POST /api/v1/exports/{id}/mine-settings → enqueue a shallow-mining pass over a
// completed harvest's corpus, proposing persona/triage settings (reviewed on the operator page). Prints
// the queued export handle (or -o json passthrough).
func exportMineSettingsCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "mine-settings <export-id>",
		Short: "Mine a completed harvest for persona/triage setting proposals",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			acc, raw, err := c.MineSettings(e.ctx(), args[0], e.scopeProject(), e.scopeTenant())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return e.renderJSON("export-mine-settings-"+args[0], raw)
			}
			_, _ = fmt.Fprintf(e.out, "queued %s (%s)\n", acc.ExportID, acc.Status)
			return nil
		},
	}
}

// exportLsCmd: GET /api/v1/exports → the exports table (or -o json passthrough).
func exportLsCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List exports (id, kind, format, status, threads, truncated, created/completed)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			l, raw, err := c.Exports(e.ctx(), e.scopeProject(), e.scopeTenant())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return e.renderJSON("export-ls", raw)
			}
			render.Exports(e.out, l)
			return nil
		},
	}
}

// exportGetCmd: GET /api/v1/exports/{id} → one export row (or -o json passthrough).
func exportGetCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "get <export-id>",
		Short: "Show one export",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			x, raw, err := c.Export(e.ctx(), args[0], e.scopeProject(), e.scopeTenant())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return e.renderJSON("export-get-"+args[0], raw)
			}
			render.Export(e.out, x)
			return nil
		},
	}
}

// exportDownloadCmd: GET /api/v1/exports/{id}/download → the Markdown corpus. By default it writes to
// stdout (or --out <file>). --split <dir> runs the client-side splitter, materializing an
// INDEX.md + per-thread files under <dir> (default .rootcause/exports/<id>/ when --split is given the
// empty string). --out and --split may be combined: the raw corpus is written before parsing so a
// format-drift failure cannot discard the downloaded bytes. The download marks the export consumed
// server-side, but remains re-downloadable for about 48 hours before eviction.
func exportDownloadCmd(e *env) *cobra.Command {
	var out string
	var split string
	cmd := &cobra.Command{
		Use:   "download <export-id>",
		Short: "Download the export's Markdown corpus (stdout, raw file, and/or split tree)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			splitSet := cmd.Flags().Changed("split")
			c, err := e.newClient()
			if err != nil {
				return err
			}
			body, err := c.DownloadExport(e.ctx(), args[0], e.scopeProject(), e.scopeTenant())
			if err != nil {
				return err
			}

			if out != "" {
				if err := os.WriteFile(out, body, 0o644); err != nil {
					return fmt.Errorf("write %s: %w", out, err)
				}
				_, _ = fmt.Fprintf(e.err, "wrote %d bytes → %s\n", len(body), out)
			}

			if splitSet {
				dir := strings.TrimSpace(split)
				if dir == "" {
					dir = filepath.Join(".rootcause", "exports", args[0])
				}
				res, err := splitExport(args[0], body, dir)
				if err != nil {
					return splitExportRescueError(args[0], out, err)
				}
				_, _ = fmt.Fprintf(e.out, "%s\n", res.dir)
				_, _ = fmt.Fprintf(e.err, "wrote %d threads → %s (INDEX.md + threads/)\n", res.threadCount, res.dir)
				return nil
			}

			if out != "" {
				return nil
			}
			return e.renderBytes("export-download-"+args[0], "body.md", body, "text")
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "write the raw corpus to this file (may be combined with --split)")
	cmd.Flags().StringVar(&split, "split", "", "split into a per-thread tree under this dir (empty → .rootcause/exports/<id>/)")
	return cmd
}

func splitExportRescueError(exportID, out string, splitErr error) error {
	if out != "" {
		return fmt.Errorf("split corpus: %w; raw download preserved at %s (the export can also be re-downloaded for ~48h after first download, before server eviction)", splitErr, out)
	}
	return fmt.Errorf("split corpus: %w; raw bytes were not saved — re-download within ~48h using `rc project corpus download %s --out <file>` (the server may evict the export after that window)", splitErr, exportID)
}

// splitResult reports where the split wrote and how many threads it materialized.
type splitResult struct {
	dir         string
	threadCount int
}

// splitExport parses the corpus (verifying harvest_format), then writes <dir>/INDEX.md and one
// <dir>/threads/<file>.md per thread (each with an export_id/thread front-matter). It ensures the
// default .rootcause tree is gitignored so a harvested corpus (customer email) is never committed.
func splitExport(exportID string, corpus []byte, dir string) (*splitResult, error) {
	parsed, err := parseCorpus(string(corpus))
	if err != nil {
		return nil, err
	}

	if err := ensureRootcauseGitignore(dir); err != nil {
		return nil, err
	}
	threadsDir := filepath.Join(dir, "threads")
	if err := os.MkdirAll(threadsDir, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", threadsDir, err)
	}

	fallbackMonth := monthOf(parsed.harvestedAt)
	var index strings.Builder
	writeIndexHeader(&index, exportID, parsed)

	for _, t := range parsed.threads {
		name := threadFileName(t, fallbackMonth)
		if err := os.WriteFile(filepath.Join(threadsDir, name), []byte(threadFileContent(exportID, t)), 0o644); err != nil {
			return nil, fmt.Errorf("write thread %s: %w", name, err)
		}
		writeIndexRow(&index, t, name)
	}

	if err := os.WriteFile(filepath.Join(dir, "INDEX.md"), []byte(index.String()), 0o644); err != nil {
		return nil, fmt.Errorf("write INDEX.md: %w", err)
	}
	return &splitResult{dir: dir, threadCount: len(parsed.threads)}, nil
}

// threadFileContent wraps a thread section in a small YAML front-matter (export_id + a stable
// "<export_id>#<idx>" thread handle) followed by the original section body.
func threadFileContent(exportID string, t splitThread) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "export_id: %s\n", exportID)
	fmt.Fprintf(&b, "thread: %q\n", fmt.Sprintf("%s#%d", exportID, t.idx))
	b.WriteString("---\n\n")
	b.WriteString(t.body)
	return b.String()
}

// writeIndexHeader writes the INDEX.md title + export front-matter summary + the table header.
func writeIndexHeader(b *strings.Builder, exportID string, c *splitCorpus) {
	fmt.Fprintf(b, "# Export %s\n\n", exportID)
	if c.mailbox != "" {
		fmt.Fprintf(b, "- mailbox: %s\n", c.mailbox)
	}
	if c.harvestedAt != "" {
		fmt.Fprintf(b, "- harvested_at: %s\n", c.harvestedAt)
	}
	if c.cleaned != "" {
		fmt.Fprintf(b, "- cleaned: %s\n", c.cleaned)
	}
	fmt.Fprintf(b, "- threads: %d\n\n", len(c.threads))
	b.WriteString("| span | domains | subject | msgs | file |\n")
	b.WriteString("|---|---|---|---|---|\n")
}

// writeIndexRow writes one INDEX.md table row for a thread: its span start, participant domains,
// subject, message count, and relative file path.
func writeIndexRow(b *strings.Builder, t splitThread, name string) {
	span := t.spanStart
	if span == "" {
		span = "-"
	}
	domains := strings.Join(participantDomains(t.participants), ", ")
	if domains == "" {
		domains = "-"
	}
	fmt.Fprintf(b, "| %s | %s | %s | %d | threads/%s |\n",
		span, domains, escapeTableCell(t.subject), t.msgCount, name)
}

// escapeTableCell neutralizes the `|` that would break a Markdown table cell.
func escapeTableCell(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}

// ensureRootcauseGitignore makes sure a corpus written under a .rootcause tree can never be committed:
// when dir lives under a .rootcause directory, it writes a `*` .gitignore at that .rootcause root
// (idempotent). For an explicit out-of-tree --split dir the caller owns gitignoring, so this is a no-op.
func ensureRootcauseGitignore(dir string) error {
	root := rootcauseRoot(dir)
	if root == "" {
		return nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", root, err)
	}
	gi := filepath.Join(root, ".gitignore")
	if _, err := os.Stat(gi); err == nil {
		return nil // already present — leave any operator edits alone
	}
	if err := os.WriteFile(gi, []byte("*\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", gi, err)
	}
	return nil
}

// rootcauseRoot returns the `.rootcause` directory dir sits under (so its .gitignore can be seeded), or
// "" when dir isn't under a .rootcause tree.
func rootcauseRoot(dir string) string {
	parts := strings.Split(filepath.ToSlash(dir), "/")
	for i, p := range parts {
		if p == ".rootcause" {
			return filepath.Join(parts[:i+1]...)
		}
	}
	return ""
}
