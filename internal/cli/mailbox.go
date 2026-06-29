package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newMailboxCmd builds the `rc mailbox` group over the connection-backed WATCHED-mailbox API (the
// channel plane's live inbox watch): `ls` lists watched mailboxes, `pause`/`resume` toggle one, and
// `connect` composes the dashboard Connections URL for the browser OAuth a human must complete. The
// legacy email-keyed ROUTING table (tenant_mailboxes) lives under the nested `route` group so tenant
// onboarding keeps working. All endpoints need an admin (ManageConnections) token; an all-projects token
// scopes with --project, a pinned-tenant token sees only its tenant.
func newMailboxCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "mailbox", Short: "Manage watched mailboxes (the channel plane's inbox watch)"}
	cmd.AddCommand(
		mailboxLsCmd(e),
		mailboxPauseCmd(e),
		mailboxResumeCmd(e),
		mailboxConnectCmd(e),
		newMailboxRouteCmd(e),
	)
	return cmd
}

// mailboxLsCmd: GET /api/v1/mailboxes/watched → the watched-mailbox table (or -o json passthrough).
func mailboxLsCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List watched mailboxes (id, provider, email, status, tenant, expiry, error)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			l, raw, err := c.WatchedMailboxes(e.ctx(), e.scopeProject())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			render.WatchedMailboxes(e.out, l)
			return nil
		},
	}
}

// mailboxPauseCmd: POST /api/v1/mailboxes/{id}/pause → the updated item (status:"paused").
func mailboxPauseCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "pause <id>",
		Short: "Pause watching a mailbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			m, raw, err := c.PauseWatchedMailbox(e.ctx(), args[0], e.scopeProject())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			render.WatchedMailbox(e.out, m)
			return nil
		},
	}
}

// mailboxResumeCmd: POST /api/v1/mailboxes/{id}/resume → the updated item. A Subscribe failure on resume
// is still a 200 with status:"needs_attention" + error_message — the render surfaces that message, so
// this is not treated as a command error.
func mailboxResumeCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "resume <id>",
		Short: "Resume watching a mailbox (surfaces needs_attention on a subscribe failure)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			m, raw, err := c.ResumeWatchedMailbox(e.ctx(), args[0], e.scopeProject())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			render.WatchedMailbox(e.out, m)
			if m != nil && m.Status == "needs_attention" && m.ErrorMessage != "" {
				_, _ = fmt.Fprintf(e.err, "note: mailbox needs attention — %s\n", m.ErrorMessage)
			}
			return nil
		},
	}
}

// validConnectProviders is the set `mailbox connect` accepts. google + microsoft are the DNS-detectable
// channel adapters; intercom is app-config. OAuth is browser-based, so this command never calls the API
// — it composes and prints the dashboard Connections URL for a human to open.
var validConnectProviders = map[string]bool{"google": true, "microsoft": true, "intercom": true}

// mailboxConnectCmd composes + prints the dashboard Connections URL for the human to open and click
// "Connect <provider>". It makes NO state-changing API call (OAuth runs in the browser). The project
// slug resolves from --project, else the brain-bound project, else `rc whoami`; if it can't be resolved
// it prints the dashboard root with an instruction. The URL goes to STDOUT (so it can be captured); a
// one-line hint goes to STDERR.
func mailboxConnectCmd(e *env) *cobra.Command {
	var provider string
	cmd := &cobra.Command{
		Use:   "connect --provider google|microsoft|intercom [--project …]",
		Short: "Print the dashboard Connections URL to start a provider's browser OAuth",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			provider = strings.ToLower(strings.TrimSpace(provider))
			if provider == "" {
				return fmt.Errorf("--provider is required (one of: google, microsoft, intercom)")
			}
			if !validConnectProviders[provider] {
				return fmt.Errorf("invalid --provider %q (one of: google, microsoft, intercom)", provider)
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			base := c.BaseURL()
			slug, tenant := e.connectScope(c)
			if slug == "" {
				_, _ = fmt.Fprintf(e.err, "note: could not resolve a project — open the dashboard → Connections and click \"Connect %s\"\n", provider)
				_, _ = fmt.Fprintln(e.out, base+"/")
				return nil
			}
			url := base + "/projects/" + slug + "/connections"
			if tenant != "" {
				url = base + "/projects/" + slug + "/tenants/" + tenant + "/connections"
			}
			_, _ = fmt.Fprintf(e.err, "open this URL and click \"Connect %s\":\n", provider)
			_, _ = fmt.Fprintln(e.out, url)
			return nil
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "", "channel provider to connect: google|microsoft|intercom")
	return cmd
}

// connectScope resolves the project slug + optional tenant for the Connections URL: --project (or the
// brain's auto-project) first, else `rc whoami`'s login-bound project. An explicit --tenant (or login
// tenant) selects the tenant-scoped Connections page. A best-effort resolution: a whoami failure leaves
// the slug empty so the caller falls back to the dashboard root.
func (e *env) connectScope(c *client.Client) (slug, tenant string) {
	if p := e.scopeProject(); p != "" {
		slug = p
	}
	tenant = e.scopeTenant()
	if slug != "" {
		return slug, tenant
	}
	who, err := c.Whoami(e.ctx())
	if err != nil || who == nil || who.Project == nil {
		return "", tenant
	}
	if who.Project.Slug != "" {
		slug = who.Project.Slug
	} else if who.Project.Name != "" {
		slug = who.Project.Name
	}
	if tenant == "" && who.Tenant != nil {
		if who.Tenant.Slug != "" {
			tenant = who.Tenant.Slug
		} else {
			tenant = who.Tenant.Name
		}
	}
	return slug, tenant
}

// newMailboxRouteCmd preserves the LEGACY routing table (tenant_mailboxes — the email-keyed routing the
// channel plane uses to map an inbound address to a project/tenant) under `rc mailbox route ls|add`. It
// targets the generic /api/v1/mailboxes collection (id = mailbox_id uuid); create is an upsert keyed on
// the email, so `add` doubles as edit. This is NOT the watched-mailbox set (`rc mailbox ls`).
func newMailboxRouteCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "route",
		Short: "Legacy inbound routing table (email→project/tenant); NOT the watched mailboxes",
		Long: "Manage the legacy email-keyed routing table (tenant_mailboxes): which inbound address routes " +
			"to which project/tenant. This is the generic /api/v1/mailboxes collection, distinct from the " +
			"connection-backed watched mailboxes shown by `rc mailbox ls`. Kept for tenant onboarding.",
	}
	cmd.AddCommand(
		listSubCmd(e, "mailboxes"),
		addSubCmd(e, "mailboxes"),
	)
	return cmd
}
