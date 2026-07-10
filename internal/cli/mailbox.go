package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newMailboxCmd builds the `rc mailbox` group over the connection-backed WATCHED-mailbox API (the
// channel plane's live inbox watch): `ls` lists watched mailboxes, `mode` controls watch/processing/
// delivery as one state, and
// `connect` composes the dashboard Connections URL for the browser OAuth a human must complete. The
// legacy email-keyed ROUTING table (tenant_mailboxes) lives under the nested `route` group so tenant
// onboarding keeps working. All endpoints need an admin (ManageConnections) token; an all-projects token
// scopes with --project, a pinned-tenant token sees only its tenant.
func newMailboxCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "mailbox", Short: "Manage watched mailboxes (the channel plane's inbox watch)"}
	cmd.AddCommand(
		mailboxLsCmd(e),
		mailboxModeCmd(e),
		mailboxHarvestCmd(e),
		mailboxIMAPEnvCmd(e),
		newMailboxSettingsCmd(e),
		mailboxConnectCmd(e),
		mailboxConnectIMAPCmd(e),
		newMailboxRouteCmd(e),
	)
	return cmd
}

// mailboxIMAPEnvCmd fetches one IMAP mailbox's protocol material and writes it to a local 0600 env file
// for scripts/local_imap_harvest.py. Secret values never go to stdout/stderr; stdout is only the path so
// callers can script it.
func mailboxIMAPEnvCmd(e *env) *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "imap-env <mailbox-id> --out .rootcause/imap/<mailbox-id>.env",
		Short: "Write an IMAP mailbox env file for local deep harvest (0600; values never printed)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			mailboxID := strings.TrimSpace(args[0])
			if out == "" {
				out = filepath.Join(".rootcause", "imap", mailboxID+".env")
			}
			if err := ensureRootcauseGitignore(out); err != nil {
				return err
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			if err := e.resolvePinnedProject(c); err != nil {
				return err
			}
			resp, _, err := c.IMAPMailboxEnv(e.ctx(), mailboxID, e.scopeProject(), e.scopeTenant())
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return fmt.Errorf("create %s: %w", filepath.Dir(out), err)
			}
			if err := writeEnvFile(out, resp.Env); err != nil {
				return err
			}
			if e.jsonOut() {
				return writeJSON(e, map[string]any{"path": out, "mailbox_id": resp.MailboxID, "email_address": resp.EmailAddress})
			}
			_, _ = fmt.Fprintln(e.out, out)
			_, _ = fmt.Fprintf(e.err, "wrote IMAP env for %s → %s (0600)\n", resp.EmailAddress, out)
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "write env file to this path (default .rootcause/imap/<mailbox-id>.env)")
	return cmd
}

// mailboxHarvestCmd starts a local-synthesis harvest of a mailbox (POST /mailboxes/{id}/harvest → a
// queued export). By default it prints the accepted {export_id, status}; --wait polls the export to a
// terminal status (done|error|failed) and prints the finished row. --clean (default true) requests the
// cleaned corpus; --max-threads caps the harvest (0 = server default). A 409 (HARVEST_IN_PROGRESS)
// surfaces verbatim through the error path. -o json passes the server body through.
func mailboxHarvestCmd(e *env) *cobra.Command {
	var clean bool
	var maxThreads int
	var wait bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "harvest <mailbox-id>",
		Short: "Start a local-synthesis harvest of a mailbox (optionally wait for the export)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			if err := e.resolvePinnedProject(c); err != nil {
				return err
			}
			// --clean is a pointer to the server: omit it (nil) unless the user set it, so the server default
			// (true) is authoritative and this CLI never hard-codes it.
			var cleanPtr *bool
			if cmd.Flags().Changed("clean") {
				cleanPtr = &clean
			}
			acc, raw, err := c.StartHarvest(e.ctx(), args[0], cleanPtr, maxThreads, e.scopeProject(), e.scopeTenant())
			if err != nil {
				return err
			}

			if !wait {
				if e.jsonOut() {
					return render.JSON(e.out, raw)
				}
				_, _ = fmt.Fprintf(e.out, "export_id: %s\nstatus: %s\n", acc.ExportID, acc.Status)
				_, _ = fmt.Fprintf(e.err, "queued — poll with: rc project corpus get %s\n", acc.ExportID)
				return nil
			}

			x, xraw, err := waitForExport(e, c, acc.ExportID, timeout)
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, xraw)
			}
			render.Export(e.out, x)
			return nil
		},
	}
	cmd.Flags().BoolVar(&clean, "clean", true, "request the cleaned corpus (server default true)")
	cmd.Flags().IntVar(&maxThreads, "max-threads", 0, "cap the harvest to N threads (0 = server default)")
	cmd.Flags().BoolVar(&wait, "wait", false, "poll the export until it reaches a terminal status")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "max time to wait under --wait")
	return cmd
}

// waitForExport polls GET /exports/{id} until the export reaches a terminal status (done|error|failed)
// or the timeout elapses, printing a terse live status line to stderr on a TTY (never stdout, so a
// piped/JSON path stays clean). Mirrors ask.go's waitForRun — no fixed sleep in tests: the interval is
// a small fixed poll floored for the loop, and the context timeout bounds it. It returns the terminal
// export AND its raw body so the JSON caller passes the verbatim server bytes through without a second
// GET (avoiding a redundant round-trip + TOCTOU).
func waitForExport(e *env, c *client.Client, id string, timeout time.Duration) (*client.ExportItem, json.RawMessage, error) {
	const interval = time.Second
	ctx, cancel := context.WithTimeout(e.ctx(), timeout)
	defer cancel()

	showProgress := render.IsTerminal(e.err)
	for {
		x, raw, err := c.Export(ctx, id, e.scopeProject(), e.scopeTenant())
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return nil, nil, fmt.Errorf("timed out after %s waiting for export %s", timeout, id)
			}
			return nil, nil, err
		}
		if isTerminalExportStatus(x.Status) {
			if showProgress {
				_, _ = fmt.Fprintf(e.err, "\r\033[K")
			}
			return x, raw, nil
		}
		if showProgress {
			_, _ = fmt.Fprintf(e.err, "\r\033[K%s … %s", id, x.Status)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			if showProgress {
				_, _ = fmt.Fprintf(e.err, "\r\033[K")
			}
			return nil, nil, fmt.Errorf("timed out after %s waiting for export %s (last status: %s)", timeout, id, x.Status)
		case <-timer.C:
		}
	}
}

// isTerminalExportStatus reports whether an export status is final. The in-progress states are
// pending/running; everything else non-empty (done|error|failed, or a new terminal state) ends the wait
// rather than hanging to the timeout.
func isTerminalExportStatus(s string) bool {
	switch s {
	case "", "pending", "running", "queued":
		return false
	default:
		return true
	}
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
			if err := e.resolvePinnedProject(c); err != nil {
				return err
			}
			l, raw, err := c.WatchedMailboxes(e.ctx(), e.scopeProject(), e.scopeTenant())
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

func mailboxModeCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "mode <id> <off|watch|shadow|live>",
		Short: "Set the mailbox watch, processing, and delivery mode",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			switch args[1] {
			case "off", "watch", "shadow", "live":
			default:
				return fmt.Errorf("invalid mode %q: want off, watch, shadow, or live", args[1])
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			if err := e.resolvePinnedProject(c); err != nil {
				return err
			}
			m, raw, err := c.SetWatchedMailboxMode(e.ctx(), args[0], args[1], e.scopeProject(), e.scopeTenant())
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

// mailboxPasswordEnv is the env var the IMAP connect command reads the mailbox password from so it
// never lands in argv / the process table / shell history. Absent → interactive stdin prompt.
const mailboxPasswordEnv = "RC_MAILBOX_PASSWORD"

// validTLSModes is the set `--imap-tls` / `--smtp-tls` accept, mirroring the server's channel.TLSMode.
var validTLSModes = map[string]bool{"none": true, "starttls": true, "implicit": true}

// mailboxConnectIMAPCmd connects a generic IMAP/SMTP mailbox (POST /mailboxes/imap/connect). Unlike
// `mailbox connect` (browser OAuth), this is a direct state-changing API call: the server live-probes
// IMAP login + SELECT INBOX + SMTP AUTH before persisting, so a bad config fails loud (IMAP_PROBE_FAILED
// / BAD_IMAP_CONFIG) and saves nothing; a duplicate is a 409. The password NEVER rides in argv — it comes
// from $RC_MAILBOX_PASSWORD or an interactive stdin prompt. Defaults mirror the server (username→email,
// smtp-host→imap-host; ports/TLS left 0/"" so the server applies 993/implicit + 587/starttls). On success
// it prints the mailbox id + status and a one-line hint for turning it on with `mailbox mode ... live`.
func mailboxConnectIMAPCmd(e *env) *cobra.Command {
	var email, username, imapHost, imapTLS, smtpHost, smtpTLS, smtpUsername string
	var imapPort, smtpPort int
	cmd := &cobra.Command{
		Use:   "connect-imap --email <addr> --imap-host <host> [flags]",
		Short: "Connect a generic IMAP/SMTP mailbox (live-probed before it's saved)",
		Long: "Link a generic IMAP/SMTP mailbox to a project (or tenant with --tenant). The server logs in over " +
			"IMAP, selects INBOX, and authenticates SMTP before persisting anything — a failure saves nothing.\n\n" +
			"The password is read from $" + mailboxPasswordEnv + " or, if unset, prompted on stdin — never passed " +
			"as an argument. Defaults mirror the server: --username defaults to --email, --smtp-host to --imap-host, " +
			"and ports/TLS default to 993/implicit (IMAP) and 587/starttls (SMTP).",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			email = strings.TrimSpace(email)
			if email == "" {
				return fmt.Errorf("--email is required")
			}
			imapHost = strings.TrimSpace(imapHost)
			if imapHost == "" {
				return fmt.Errorf("--imap-host is required")
			}
			if imapTLS != "" && !validTLSModes[imapTLS] {
				return fmt.Errorf("invalid --imap-tls %q (one of: none, starttls, implicit)", imapTLS)
			}
			if smtpTLS != "" && !validTLSModes[smtpTLS] {
				return fmt.Errorf("invalid --smtp-tls %q (one of: none, starttls, implicit)", smtpTLS)
			}

			password := os.Getenv(mailboxPasswordEnv)
			if password == "" {
				p, err := readSecretStdin(e, "mailbox password")
				if err != nil {
					return err
				}
				password = p
			}

			if username == "" {
				username = email
			}
			if smtpHost == "" {
				smtpHost = imapHost
			}

			c, err := e.newClient()
			if err != nil {
				return err
			}
			if err := e.resolvePinnedProject(c); err != nil {
				return err
			}
			req := client.IMAPConnectRequest{
				Tenant:       e.scopeTenant(),
				EmailAddress: email,
				Username:     username,
				Password:     password,
				IMAPHost:     imapHost,
				IMAPPort:     imapPort,
				IMAPTLS:      imapTLS,
				SMTPHost:     smtpHost,
				SMTPPort:     smtpPort,
				SMTPTLS:      smtpTLS,
				SMTPUsername: smtpUsername,
			}
			m, raw, err := c.ConnectIMAPMailbox(e.ctx(), req, e.scopeProject())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			render.WatchedMailbox(e.out, m)
			if m != nil {
				_, _ = fmt.Fprintf(e.err, "connected — start processing with: rc project mailbox mode %s live\n", m.ID)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "mailbox email address (required)")
	cmd.Flags().StringVar(&username, "username", "", "IMAP/SMTP login username (default: --email)")
	cmd.Flags().StringVar(&imapHost, "imap-host", "", "IMAP server host (required)")
	cmd.Flags().IntVar(&imapPort, "imap-port", 0, "IMAP server port (default: server 993)")
	cmd.Flags().StringVar(&imapTLS, "imap-tls", "", "IMAP TLS mode: none|starttls|implicit (default: server implicit)")
	cmd.Flags().StringVar(&smtpHost, "smtp-host", "", "SMTP server host (default: --imap-host)")
	cmd.Flags().IntVar(&smtpPort, "smtp-port", 0, "SMTP server port (default: server 587)")
	cmd.Flags().StringVar(&smtpTLS, "smtp-tls", "", "SMTP TLS mode: none|starttls|implicit (default: server starttls)")
	cmd.Flags().StringVar(&smtpUsername, "smtp-username", "", "SMTP username override (default: --username)")
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
