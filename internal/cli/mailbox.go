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

// newMailboxCmd builds the `rc project mailbox` group over the connection-backed watched-mailbox API (the
// channel plane's live inbox watch): `ls` lists watched mailboxes, `mode` controls watch/processing/
// delivery as one state, and `connect` composes the dashboard Connections URL for browser OAuth.
// All endpoints need an admin (ManageConnections) token; an all-projects token scopes with --project,
// while a pinned-tenant token sees only its tenant.
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
		mailboxSeedIMAPCmd(e),
		mailboxPasswordLinkCmd(e),
		mailboxTestCmd(e),
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
// slug resolves from --project, else the brain-bound project, else `rc auth status`; if it can't be resolved
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
			if provider == "imap" {
				return fmt.Errorf("IMAP is not a browser-OAuth provider — connect it directly with: rc project mailbox connect-imap --email <addr> --imap-host <host>")
			}
			if !validConnectProviders[provider] {
				return fmt.Errorf("invalid --provider %q (one of: google, microsoft, intercom; for a generic mailbox use `rc project mailbox connect-imap`)", provider)
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

// mailboxSMTPPasswordEnv supplies a DISTINCT outgoing password for servers that don't accept the IMAP
// one (a common shape: an IMAP app password plus a separate SMTP relay password). Same rule as the IMAP
// password — env or prompt, never argv.
const mailboxSMTPPasswordEnv = "RC_MAILBOX_SMTP_PASSWORD"

// validTLSModes is the set `--imap-tls` / `--smtp-tls` accept, mirroring the server's channel.TLSMode.
var validTLSModes = map[string]bool{"none": true, "starttls": true, "implicit": true}

// imapServerFlags is the RESEARCHABLE half of an IMAP mailbox — everything an operator can work out
// without the customer. It is shared verbatim by `connect-imap` (which adds the password) and
// `seed-imap` (which hands the password step to the customer's no-login link), so the two can never
// drift on flag names, validation, or defaults.
type imapServerFlags struct {
	email        string
	username     string
	imapHost     string
	imapTLS      string
	smtpHost     string
	smtpTLS      string
	smtpUsername string
	imapPort     int
	smtpPort     int
}

func (f *imapServerFlags) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.email, "email", "", "mailbox email address (required)")
	cmd.Flags().StringVar(&f.username, "username", "", "IMAP/SMTP login username (default: --email)")
	cmd.Flags().StringVar(&f.imapHost, "imap-host", "", "IMAP server host (required)")
	cmd.Flags().IntVar(&f.imapPort, "imap-port", 0, "IMAP server port (default: server 993)")
	cmd.Flags().StringVar(&f.imapTLS, "imap-tls", "", "IMAP TLS mode: none|starttls|implicit (default: server implicit)")
	cmd.Flags().StringVar(&f.smtpHost, "smtp-host", "", "SMTP server host (default: --imap-host)")
	cmd.Flags().IntVar(&f.smtpPort, "smtp-port", 0, "SMTP server port (default: server 587)")
	cmd.Flags().StringVar(&f.smtpTLS, "smtp-tls", "", "SMTP TLS mode: none|starttls|implicit (default: server starttls)")
	cmd.Flags().StringVar(&f.smtpUsername, "smtp-username", "", "SMTP username override (default: --username)")
}

// normalize validates the flags and applies the client-side defaults the server also applies
// (username→email, smtp-host→imap-host). Ports/TLS stay 0/"" so the SERVER default stays authoritative.
func (f *imapServerFlags) normalize() error {
	f.email = strings.TrimSpace(f.email)
	if f.email == "" {
		return fmt.Errorf("--email is required")
	}
	f.imapHost = strings.TrimSpace(f.imapHost)
	if f.imapHost == "" {
		return fmt.Errorf("--imap-host is required")
	}
	if f.imapTLS != "" && !validTLSModes[f.imapTLS] {
		return fmt.Errorf("invalid --imap-tls %q (one of: none, starttls, implicit)", f.imapTLS)
	}
	if f.smtpTLS != "" && !validTLSModes[f.smtpTLS] {
		return fmt.Errorf("invalid --smtp-tls %q (one of: none, starttls, implicit)", f.smtpTLS)
	}
	if f.username == "" {
		f.username = f.email
	}
	if f.smtpHost == "" {
		f.smtpHost = f.imapHost
	}
	return nil
}

func (f *imapServerFlags) request(tenant string) client.IMAPConnectRequest {
	return client.IMAPConnectRequest{
		Tenant:       tenant,
		EmailAddress: f.email,
		Username:     f.username,
		IMAPHost:     f.imapHost,
		IMAPPort:     f.imapPort,
		IMAPTLS:      f.imapTLS,
		SMTPHost:     f.smtpHost,
		SMTPPort:     f.smtpPort,
		SMTPTLS:      f.smtpTLS,
		SMTPUsername: f.smtpUsername,
	}
}

// mailboxConnectIMAPCmd connects a generic IMAP/SMTP mailbox (POST /mailboxes/imap/connect). Unlike
// `mailbox connect` (browser OAuth), this is a direct state-changing API call: the server live-probes
// IMAP login + SELECT INBOX + SMTP AUTH before persisting, so a bad config fails loud (IMAP_PROBE_FAILED
// / BAD_IMAP_CONFIG) and saves nothing; a duplicate is a 409. The password NEVER rides in argv — it comes
// from $RC_MAILBOX_PASSWORD or an interactive stdin prompt. Defaults mirror the server (username→email,
// smtp-host→imap-host; ports/TLS left 0/"" so the server applies 993/implicit + 587/starttls). On success
// it prints the mailbox id + status and a one-line hint for turning it on with `mailbox mode ... live`.
func mailboxConnectIMAPCmd(e *env) *cobra.Command {
	var f imapServerFlags
	var promptSMTPPassword bool
	cmd := &cobra.Command{
		Use:   "connect-imap --email <addr> --imap-host <host> [flags]",
		Short: "Connect a generic IMAP/SMTP mailbox (live-probed before it's saved)",
		Long: "Link a generic IMAP/SMTP mailbox to a project (or tenant with --tenant). The server logs in over " +
			"IMAP, selects INBOX, and authenticates SMTP before persisting anything — a failure saves nothing.\n\n" +
			"The password is read from $" + mailboxPasswordEnv + " or, if unset, prompted on stdin — never passed " +
			"as an argument. Defaults mirror the server: --username defaults to --email, --smtp-host to --imap-host, " +
			"and ports/TLS default to 993/implicit (IMAP) and 587/starttls (SMTP).\n\n" +
			"Don't have the password? Use `rc project mailbox seed-imap` instead and send the customer the " +
			"no-login link it prints.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := f.normalize(); err != nil {
				return err
			}

			password := os.Getenv(mailboxPasswordEnv)
			if password == "" {
				p, err := readSecretStdin(e, "mailbox password")
				if err != nil {
					return err
				}
				password = p
			}

			smtpPassword := os.Getenv(mailboxSMTPPasswordEnv)
			if smtpPassword == "" && promptSMTPPassword {
				p, err := readSecretStdin(e, "SMTP password")
				if err != nil {
					return err
				}
				smtpPassword = p
			}

			c, err := e.newClient()
			if err != nil {
				return err
			}
			if err := e.resolvePinnedProject(c); err != nil {
				return err
			}
			req := f.request(e.scopeTenant())
			req.Password = password
			req.SMTPPassword = smtpPassword
			m, raw, err := c.ConnectIMAPMailbox(e.ctx(), req, e.scopeProject())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			render.WatchedMailbox(e.out, m)
			// A green connect can still carry a warning (typically: drafts can't be placed here), so the
			// checklist is printed whenever the server sent one rather than only on failure.
			if m != nil && m.Probe != nil {
				_, _ = fmt.Fprintln(e.out)
				render.IMAPProbeSteps(e.out, m.Probe.Steps)
			}
			if m != nil {
				_, _ = fmt.Fprintf(e.err, "connected — start processing with: rc project mailbox mode %s live\n", m.ID)
			}
			return nil
		},
	}
	f.bind(cmd)
	cmd.Flags().BoolVar(&promptSMTPPassword, "smtp-password-prompt", false, "prompt for a separate SMTP password (else $"+mailboxSMTPPasswordEnv+", else the IMAP password)")
	return cmd
}

// mailboxSeedIMAPCmd stores the researchable half of an IMAP mailbox WITHOUT a password (POST
// .../mailboxes/imap/seed) and prints the no-login password link the customer (or their IT provider)
// opens to supply it. This is the answer to the one setup step an operator cannot do for a customer;
// the alternative — asking someone to email a password — is exactly what we should never encourage.
// The mailbox parks in awaiting_credential (excluded from the poll sweep, so nothing fails in the
// background) and goes live by itself once the entered password passes the server's live check.
func mailboxSeedIMAPCmd(e *env) *cobra.Command {
	var f imapServerFlags
	cmd := &cobra.Command{
		Use:   "seed-imap --email <addr> --imap-host <host> [flags]",
		Short: "Store an IMAP mailbox's server settings without a password and print its no-login password link",
		Long: "Save everything about a generic IMAP/SMTP mailbox except the password, then print a stable, " +
			"no-login link to send to whoever holds that password.\n\n" +
			"Nothing is probed yet — there is no credential to authenticate with. The mailbox sits in " +
			"awaiting_credential (never polled, so it cannot fail in the background) until someone opens the " +
			"link and enters the password; it goes live automatically when that password passes the live check.\n\n" +
			"The link does not expire and is reusable: the same URL is how the customer rotates the password " +
			"later. It can only SET the password for this one mailbox — never read one, and never reach another " +
			"mailbox. Use `rc project mailbox password-link <id>` to print it again.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := f.normalize(); err != nil {
				return err
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			if err := e.resolvePinnedProject(c); err != nil {
				return err
			}
			m, raw, err := c.SeedIMAPMailbox(e.ctx(), f.request(e.scopeTenant()), e.scopeProject())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			render.WatchedMailbox(e.out, m)
			if m != nil && m.PasswordLink != "" {
				// stdout so it can be piped/copied; the instruction goes to stderr.
				_, _ = fmt.Fprintf(e.out, "\n%s\n", m.PasswordLink)
				_, _ = fmt.Fprintln(e.err, "send that link to whoever has the mailbox password — we start watching the inbox as soon as they enter it")
			}
			return nil
		},
	}
	f.bind(cmd)
	return cmd
}

// mailboxPasswordLinkCmd reprints the no-login password link for an existing IMAP mailbox — the seeded
// one an operator misplaced, and the rotation link for a mailbox whose password has since changed.
func mailboxPasswordLinkCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "password-link <mailbox-id>",
		Short: "Print the no-login password link for an IMAP mailbox (also used for rotation)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			if err := e.resolvePinnedProject(c); err != nil {
				return err
			}
			m, raw, err := c.MailboxPasswordLink(e.ctx(), strings.TrimSpace(args[0]), e.scopeProject(), e.scopeTenant())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			if m == nil || m.PasswordLink == "" {
				return fmt.Errorf("no password link available — the server has no signing key configured for this surface")
			}
			_, _ = fmt.Fprintln(e.out, m.PasswordLink)
			return nil
		},
	}
}

// errMailboxUnreachable makes `mailbox test` script-usable: a failed checklist exits non-zero without
// printing a cobra usage block (the checklist above IS the message).
var errMailboxUnreachable = fmt.Errorf("mailbox connection check failed")

// mailboxTestCmd re-runs the live connection check against a CONNECTED mailbox's stored credentials
// (POST .../mailboxes/{id}/probe) and prints the stage-by-stage checklist. It changes nothing except
// clearing a stale failure notice when the mailbox comes back green — the loop for "connect, fix,
// re-test" without re-entering credentials. Exits non-zero when a required stage failed, so it is
// usable in a script.
func mailboxTestCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "test <mailbox-id>",
		Short: "Re-run the live IMAP/SMTP check on a connected mailbox and print the checklist",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			if err := e.resolvePinnedProject(c); err != nil {
				return err
			}
			m, raw, err := c.ProbeIMAPMailbox(e.ctx(), strings.TrimSpace(args[0]), e.scopeProject(), e.scopeTenant())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				if rerr := render.JSON(e.out, raw); rerr != nil {
					return rerr
				}
			} else {
				render.WatchedMailbox(e.out, m)
				if m != nil && m.Probe != nil {
					_, _ = fmt.Fprintln(e.out)
					render.IMAPProbe(e.out, m.Probe)
				}
			}
			if m != nil && m.Probe != nil && !m.Probe.OK {
				silenceUsage(cmd)
				return errMailboxUnreachable
			}
			return nil
		},
	}
	return cmd
}

// connectScope resolves the project slug + optional tenant for the Connections URL: --project (or the
// brain's auto-project) first, else `rc auth status`'s login-bound project. An explicit --tenant (or login
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
