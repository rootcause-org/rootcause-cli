package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// knowledgeArticleProviders is the closed provider set of the write path. The value becomes a path
// segment on `article get`, so it is validated locally rather than shipped to the server verbatim.
var knowledgeArticleProviders = []string{"helpscout", "intercom", "knowledgeowl"}

// newKnowledgeArticleCmd is the help-centre WRITE path: a `replypen: helpcenter/v1` markdown block in,
// a provider article out. Reading articles for grounding stays on `rc project knowledge content`.
func newKnowledgeArticleCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "article",
		Short: "Write and read help-centre articles from a markdown block",
		Long: "Write and read help-centre articles from a markdown block.\n\n" +
			"The unit is one file: `replypen: helpcenter/v1` YAML front matter (op: create|update,\n" +
			"provider, id or collection_id, title, optional keywords/anchor) + the article body in markdown.\n" +
			"A write lands in the provider's DRAFT lane unless you pass --publish, and `apply --dry-run`\n" +
			"first prints the exact provider calls. `get` hands you an editable block for a live article.\n" +
			"The project needs a help-centre write grant (once, by an owner):\n" +
			"  rc project connection add integration_key=<helpscout_docs|intercom|knowledgeowl> \\\n" +
			"    label=help-center tier=write token=…",
	}
	cmd.AddCommand(knowledgeArticleApplyCmd(e), knowledgeArticleGetCmd(e))
	return cmd
}

func knowledgeArticleApplyCmd(e *env) *cobra.Command {
	var from string
	var dryRun, publish bool
	cmd := &cobra.Command{
		Use:   "apply --from <file.md>",
		Short: "Apply a help-centre article block to its provider",
		Long: "Apply a help-centre article block (`replypen: helpcenter/v1`) to its provider.\n\n" +
			"Run it with --dry-run first: the output is the exact provider calls that would be sent, in full.\n" +
			"A published article is refused unless you pass --publish; without it the change lands in the\n" +
			"provider's draft lane and the live article stays untouched. --from - reads the block from stdin.\n" +
			"A real write needs the `knowledge:write` token scope; --dry-run and `article get` work with read.\n\n" +
			"Example block:\n" +
			"  ---\n" +
			"  replypen: helpcenter/v1\n" +
			"  op: update\n" +
			"  provider: helpscout\n" +
			"  id: \"1234\"\n" +
			"  title: Refunds within 14 days\n" +
			"  ---\n" +
			"  Body markdown; it replaces the whole article.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			markdown, err := readArticleBlock(e, from)
			if err != nil {
				return err
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			if err := e.resolvePinnedProject(c); err != nil {
				return err
			}
			resp, raw, err := c.KnowledgeArticleApply(e.ctx(), e.scopeProject(), e.scopeTenant(),
				client.KnowledgeArticleApplyRequest{Markdown: markdown, DryRun: dryRun, Publish: publish})
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			render.KnowledgeArticleApply(e.out, resp)
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "markdown file holding the article block (- reads stdin)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show the provider calls without sending them")
	cmd.Flags().BoolVar(&publish, "publish", false, "allow changing a published (live) article")
	return cmd
}

func knowledgeArticleGetCmd(e *env) *cobra.Command {
	var provider, id, out string
	cmd := &cobra.Command{
		Use:   "get --provider <provider> --id <id>",
		Short: "Fetch one help-centre article as an applyable markdown block",
		Long: "Fetch one help-centre article as an applyable markdown block.\n\n" +
			"The output is a ready `replypen: helpcenter/v1` block with `op: update` and the article's\n" +
			"current title/ids in the front matter: edit the body (or the title) and feed the same file\n" +
			"back to `rc project knowledge article apply --from FILE.md --dry-run`.\n" +
			"--id is the provider-native article id (the id in the article's help-centre URL or in the\n" +
			"synced KB file's front matter), never our internal document id.\n" +
			"Use --out FILE to write the block straight to disk instead of stdout. Reading works with the\n" +
			"ordinary read scope; only the apply write needs `knowledge:write`.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := validateKnowledgeArticleProvider(provider); err != nil {
				return err
			}
			if strings.TrimSpace(id) == "" {
				return fmt.Errorf("--id <article-id> is required")
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			if err := e.resolvePinnedProject(c); err != nil {
				return err
			}
			resp, raw, err := c.KnowledgeArticleGet(e.ctx(), e.scopeProject(), e.scopeTenant(), provider, id)
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			if out != "" {
				if err := os.WriteFile(out, []byte(resp.Markdown), 0o644); err != nil {
					return fmt.Errorf("write %s: %w", out, err)
				}
				_, _ = fmt.Fprintf(e.out, "wrote %s\n", out)
				return nil
			}
			_, _ = io.WriteString(e.out, resp.Markdown)
			return nil
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "", "help-centre provider ("+strings.Join(knowledgeArticleProviders, ", ")+")")
	cmd.Flags().StringVar(&id, "id", "", "provider-native article id")
	cmd.Flags().StringVar(&out, "out", "", "write the block to this file instead of stdout")
	return cmd
}

// readArticleBlock reads the block bytes verbatim — whitespace and fence placement are part of the
// contract the server parses, so nothing is trimmed or re-encoded here.
func readArticleBlock(e *env, from string) (string, error) {
	switch from {
	case "":
		return "", fmt.Errorf("--from <file.md> is required (use - to read the block from stdin)")
	case "-":
		markdown, err := readAllStdin(e)
		if err != nil {
			return "", err
		}
		return checkArticleBlockSize(markdown, "stdin")
	default:
		b, err := os.ReadFile(from)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", from, err)
		}
		return checkArticleBlockSize(string(b), from)
	}
}

func checkArticleBlockSize(markdown, source string) (string, error) {
	if strings.TrimSpace(markdown) == "" {
		return "", fmt.Errorf("empty article block from %s", source)
	}
	if len(markdown) > client.MaxKnowledgeArticleMarkdownBytes {
		return "", fmt.Errorf("article block from %s is %d bytes, over the %d-byte limit", source, len(markdown), client.MaxKnowledgeArticleMarkdownBytes)
	}
	return markdown, nil
}

func validateKnowledgeArticleProvider(provider string) error {
	for _, p := range knowledgeArticleProviders {
		if provider == p {
			return nil
		}
	}
	return fmt.Errorf("invalid --provider %q (must be one of %s)", provider, strings.Join(knowledgeArticleProviders, ", "))
}
