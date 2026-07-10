package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newSpamCmd wires `rc project senders` — the per-project/tenant spam allow/block lists ("never spam" / "always
// spam"), a plane separate from the drafting sender lists. The spam endpoints address the project (and
// tenant) in the PATH (/api/v1/projects/{project}[/tenants/{slug}]/spam/…), so every subcommand
// resolves a concrete project slug (spamProject) rather than riding the ?project= collection scope.
func newSpamCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "senders",
		Short: "Manage the spam allow/block lists (never-spam / always-spam)",
	}
	cmd.AddCommand(
		spamListCmd(e),
		spamCreateCmd(e, "allow", "allows", "Never treat mail from <pattern> as spam"),
		spamCreateCmd(e, "block", "blocks", "Always treat mail from <pattern> as spam"),
		spamRmCmd(e),
	)
	return cmd
}

// spamListCmd is `rc project senders ls`: both lists in one table (VERDICT PATTERN TYPE SOURCE [CREATED]); -o json
// emits the raw allow+block bodies as a {"allows":…,"blocks":…} envelope so a consumer sees exactly
// what each endpoint returned.
func spamListCmd(e *env) *cobra.Command {
	var mailbox string
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the spam allow and block rules",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			project, err := spamProject(e, c)
			if err != nil {
				return err
			}
			allows, allowRaw, err := c.SpamList(e.ctx(), project, e.scopeTenant(), "allows")
			if err != nil {
				return err
			}
			blocks, blockRaw, err := c.SpamList(e.ctx(), project, e.scopeTenant(), "blocks")
			if err != nil {
				return err
			}
			if e.jsonOut() {
				env := fmt.Sprintf(`{"allows":%s,"blocks":%s}`, rawOrNull(allowRaw), rawOrNull(blockRaw))
				return render.JSON(e.out, []byte(env))
			}
			rules := append(append([]client.SpamRule{}, allows...), blocks...)
			render.SpamRules(e.out, filterSpamByMailbox(rules, mailbox))
			return nil
		},
	}
	cmd.Flags().StringVar(&mailbox, "mailbox", "", "show only rules scoped to this mailbox uuid (client-side filter)")
	return cmd
}

// filterSpamByMailbox keeps only rules whose mailbox scope matches (client-side filter for `rc project senders ls
// --mailbox`). An empty filter is a no-op — every rule passes. The server's GET returns all scopes below
// the resolved one; this narrows the table to one mailbox without a second round-trip.
func filterSpamByMailbox(rules []client.SpamRule, mailbox string) []client.SpamRule {
	if mailbox == "" {
		return rules
	}
	out := rules[:0:0]
	for _, r := range rules {
		if r.Mailbox == mailbox {
			out = append(out, r)
		}
	}
	return out
}

// spamCreateCmd builds `rc spam allow|block <pattern> [--reason …]` over POST on the matching list. The
// server infers match_type from the pattern shape, so the CLI sends only {pattern, reason}.
func spamCreateCmd(e *env, verb, list, short string) *cobra.Command {
	var reason, mailbox string
	cmd := &cobra.Command{
		Use:   verb + " <pattern>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			project, err := spamProject(e, c)
			if err != nil {
				return err
			}
			rule, raw, err := c.SpamCreate(e.ctx(), project, e.scopeTenant(), list, args[0], reason, mailbox)
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			render.SpamRules(e.out, []client.SpamRule{*rule})
			return nil
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "why this rule exists (shown in the list)")
	cmd.Flags().StringVar(&mailbox, "mailbox", "", "scope the rule to this mailbox uuid (else project/tenant scope)")
	return cmd
}

// spamRmCmd is `rc spam rm <id>`. A row's verdict isn't known from its id alone, so rm tries the block
// list first, then the allow list on a 404 — one simple `rm <id>` UX with no verdict flag to remember.
// --verdict allow|block targets one list directly when the caller already knows it.
func spamRmCmd(e *env) *cobra.Command {
	var verdict string
	cmd := &cobra.Command{
		Use:   "rm <id>",
		Short: "Delete a spam rule by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			project, err := spamProject(e, c)
			if err != nil {
				return err
			}
			lists, err := spamRmLists(verdict)
			if err != nil {
				return err
			}
			raw, err := spamTryDelete(e, c, project, args[0], lists)
			if err != nil {
				return err
			}
			if e.jsonOut() {
				if len(raw) > 0 {
					return render.JSON(e.out, raw)
				}
				return render.JSON(e.out, []byte(`{"deleted":"`+args[0]+`"}`))
			}
			_, _ = fmt.Fprintf(e.out, "deleted spam rule %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&verdict, "verdict", "", "target one list directly: allow|block (default: try both)")
	return cmd
}

// spamRmLists maps the optional --verdict to the list(s) rm should try, in order. Empty ⇒ blocks then
// allows (block is the higher-stakes list, so try it first).
func spamRmLists(verdict string) ([]string, error) {
	switch verdict {
	case "":
		return []string{"blocks", "allows"}, nil
	case "block":
		return []string{"blocks"}, nil
	case "allow":
		return []string{"allows"}, nil
	default:
		return nil, fmt.Errorf("--verdict must be allow or block, got %q", verdict)
	}
}

// spamTryDelete deletes the id from the first list that has it, treating a 404 from an earlier list as
// "not this list, try the next". Any non-404 error (or a 404 from the last list) surfaces verbatim.
func spamTryDelete(e *env, c *client.Client, project, id string, lists []string) (raw []byte, err error) {
	for i, list := range lists {
		raw, err = c.SpamDelete(e.ctx(), project, e.scopeTenant(), list, id)
		if err == nil {
			return raw, nil
		}
		if i < len(lists)-1 && isNotFound(err) {
			continue
		}
		return nil, err
	}
	return nil, err
}

// isNotFound reports whether err is an API 404 — the signal that a spam rule id belongs to the other
// list, so rm should fall through and try it.
func isNotFound(err error) bool {
	var apiErr *client.APIError
	return asAPIError(err, &apiErr) && apiErr.Status == 404
}

// spamProject resolves the concrete project slug for a spam PATH: the explicit/brain --project scope
// when set, else the login-bound project from whoami. Errors clearly for an all-projects token with no
// --project (the path needs a project segment; there's no server-side default).
func spamProject(e *env, c *client.Client) (string, error) {
	if project := e.scopeProject(); project != "" {
		return project, nil
	}
	who, err := c.Whoami(e.ctx())
	if err == nil && who != nil && who.Project != nil {
		switch {
		case who.Project.Slug != "":
			return who.Project.Slug, nil
		case who.Project.Name != "":
			return who.Project.Name, nil
		case who.Project.ID != "":
			return who.Project.ID, nil
		}
	}
	return "", fmt.Errorf("--project <project> is required for spam rules unless the active login is project-scoped")
}

// rawOrNull returns the raw body or the JSON literal null when the server sent nothing, so the
// combined -o json envelope is always valid JSON.
func rawOrNull(raw []byte) string {
	if len(raw) == 0 {
		return "null"
	}
	return string(raw)
}
