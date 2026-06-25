package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/config"
	"github.com/rootcause-org/rootcause-cli/internal/oauth"
	"github.com/rootcause-org/rootcause-cli/internal/token"
)

// loginTimeout bounds the wait for a human to complete the browser/device sign-in before the CLI gives
// up (a generous window — the device flow's own expiry usually fires first).
const loginTimeout = 10 * time.Minute

// newLoginCmd builds `rc login` — the OAuth sign-in. By default it runs the PKCE loopback flow (opens a
// browser, catches the redirect on a localhost port); --device runs the RFC 8628 device flow for an
// SSH/headless box (print a code, approve it in a browser anywhere). The resulting access + refresh
// tokens are stored under the resolved profile in the 0600 token store; every later `rc` refreshes the
// access token transparently. The project the token is scoped to is chosen on the consent screen in the
// browser (a project, or — for a global admin — all projects), not on the CLI.
func newLoginCmd(e *env) *cobra.Command {
	var device bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in with OAuth (PKCE loopback by default, --device for headless)",
		Long: "Sign in to rootcause. By default this opens your browser and catches the redirect on a\n" +
			"localhost port (PKCE). On a headless/SSH box, use --device to get a short code you approve in\n" +
			"a browser on any device.\n\n" +
			"Tokens are stored per profile in " + tokenStorePath() + " (0600). Pick the project (or\n" +
			"all-projects, if you're an admin) on the browser consent screen.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			res, err := config.Load(e.profile)
			if err != nil {
				return err
			}
			base := res.BaseURL
			if e.baseURLOvr != "" {
				base = e.baseURLOvr
			}

			oc := oauth.NewClient(base)
			ctx, cancel := context.WithTimeout(e.ctx(), loginTimeout)
			defer cancel()

			var toks oauth.Tokens
			if device {
				toks, err = oc.LoginDevice(ctx, e.err)
			} else {
				toks, err = oc.LoginPKCE(ctx, e.browserOpener(), e.err)
			}
			if err != nil {
				return fmt.Errorf("login failed: %w", err)
			}

			t := token.Token{
				AccessToken:  toks.AccessToken,
				RefreshToken: toks.RefreshToken,
				ExpiresAt:    time.Now().Add(time.Duration(toks.ExpiresIn) * time.Second),
				BaseURL:      base,
			}
			if err := token.Save(res.Profile, t); err != nil {
				return err
			}

			target := res.Profile
			if res.Brain != nil {
				target = res.Brain.Project + " (brain " + res.Brain.Dir + ")"
			}
			_, _ = fmt.Fprintf(e.out, "logged in — token stored for profile %q\n", res.Profile)
			_, _ = fmt.Fprintf(e.err, "target: %s · %s\n", target, base)
			return nil
		},
	}
	cmd.Flags().BoolVar(&device, "device", false, "use the device-authorization flow (headless/SSH — no local browser)")
	return cmd
}

// newLogoutCmd builds `rc logout` — revoke the profile's tokens server-side (best-effort) and clear them
// from the local store. After this the profile is signed out; `rc login` signs back in.
func newLogoutCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Revoke and clear this profile's stored tokens",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			res, err := config.Load(e.profile)
			if err != nil {
				return err
			}
			t, ok, err := token.Load(res.Profile)
			if err != nil {
				return err
			}
			if !ok {
				_, _ = fmt.Fprintf(e.out, "already logged out (profile %q)\n", res.Profile)
				return nil
			}
			// Best-effort revocation: a network failure here must not block clearing the local store (the
			// user asked to log out). Revoke both tokens; the refresh is the one that matters.
			base := t.BaseURL
			if base == "" {
				base = res.BaseURL
			}
			if e.baseURLOvr != "" {
				base = e.baseURLOvr
			}
			oc := oauth.NewClient(base)
			ctx, cancel := context.WithTimeout(e.ctx(), 15*time.Second)
			defer cancel()
			if t.RefreshToken != "" {
				if rerr := oc.Revoke(ctx, t.RefreshToken); rerr != nil {
					_, _ = fmt.Fprintf(e.err, "warning: could not revoke refresh token server-side: %v\n", rerr)
				}
			}
			if t.AccessToken != "" {
				_ = oc.Revoke(ctx, t.AccessToken)
			}
			if err := token.Delete(res.Profile); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(e.out, "logged out (profile %q)\n", res.Profile)
			return nil
		},
	}
	return cmd
}

// newWhoamiCmd builds `rc whoami` — answer "which profile/project will rc hit from here, and am I
// signed in?" entirely from LOCAL state (the brain marker, the persistent flags, and the token store).
// It does not call the server: there is no identity endpoint, so memberships/identity aren't shown —
// the token's project binding lives server-side and is enforced on each request.
func newWhoamiCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Show the resolved profile/project/tenant + sign-in status (local; no server call)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			res, err := config.Load(e.profile)
			if err != nil {
				return err
			}
			base := res.BaseURL
			if e.baseURLOvr != "" {
				base = e.baseURLOvr
			}

			t, loggedIn, err := token.Load(res.Profile)
			if err != nil {
				return err
			}
			autoProject := ""
			if !loggedIn && e.profile == "" && res.Brain != nil {
				if fallback, ok, ferr := token.Load(config.DefaultProfile); ferr != nil {
					return ferr
				} else if ok {
					t = fallback
					loggedIn = true
					res.Profile = config.DefaultProfile
					autoProject = res.Brain.Project
				}
			}
			if loggedIn && t.BaseURL != "" && e.baseURLOvr == "" {
				base = t.BaseURL
			}

			status, expiry := "not logged in — run `rc login`", ""
			if loggedIn {
				status = "logged in"
				if !t.ExpiresAt.IsZero() {
					if t.Expired(time.Now(), 0) {
						expiry = "access token expired (auto-refreshes on next use)"
					} else {
						expiry = "access token valid until " + t.ExpiresAt.Format(time.RFC3339)
					}
				}
			}

			tenant := e.scopeTenantFromResolved(res)
			tenantSource := e.tenantSourceFromResolved(res)
			// --project is a server-side SCOPE (not a profile): when set it names the project each read
			// request targets. A brain fallback to the default profile does the same automatically.
			project := res.Project
			if e.project != "" {
				project = e.project
			} else if autoProject != "" {
				project = autoProject
			}
			if e.jsonOut() {
				return writeJSON(e, map[string]any{
					"profile":       res.Profile,
					"project":       emptyDash(project),
					"tenant":        tenant,
					"tenant_source": tenantSource,
					"base_url":      base,
					"brain_dir":     brainDir(res),
					"logged_in":     loggedIn,
					"expires_at":    tokenExpiry(t, loggedIn),
				})
			}

			_, _ = fmt.Fprintf(e.out, "profile:   %s\n", res.Profile)
			_, _ = fmt.Fprintf(e.out, "project:   %s\n", emptyDash(project))
			if e.project != "" {
				_, _ = fmt.Fprintf(e.out, "           (--project scope; needs an all-projects token)\n")
			} else if autoProject != "" {
				_, _ = fmt.Fprintf(e.out, "           (brain scope via default profile)\n")
			}
			if tenant != "" {
				if tenantSource != "" {
					_, _ = fmt.Fprintf(e.out, "tenant:    %s (%s)\n", tenant, tenantSource)
				} else {
					_, _ = fmt.Fprintf(e.out, "tenant:    %s\n", tenant)
				}
			}
			_, _ = fmt.Fprintf(e.out, "base URL:  %s\n", base)
			if res.Brain != nil {
				_, _ = fmt.Fprintf(e.out, "brain:     %s\n", res.Brain.Dir)
			}
			_, _ = fmt.Fprintf(e.out, "auth:      %s\n", status)
			if expiry != "" {
				_, _ = fmt.Fprintf(e.out, "           %s\n", expiry)
			}
			return nil
		},
	}
	return cmd
}

// browserOpener is the function LoginPKCE uses to launch the browser — the real opener in normal use,
// or a test-injected stub.
func (e *env) browserOpener() func(string) error {
	if e.openBrowser != nil {
		return e.openBrowser
	}
	return oauth.OpenBrowser
}

// scopeTenantFromResolved is scopeTenant computed against an already-loaded Resolved (whoami loads its
// own, rather than via newClient).
func (e *env) scopeTenantFromResolved(res config.Resolved) string {
	if e.tenant != "" {
		return e.tenant
	}
	return res.Tenant
}

func (e *env) tenantSourceFromResolved(res config.Resolved) string {
	if e.tenant != "" {
		return "--tenant"
	}
	return res.TenantSource
}

// tokenExpiry renders the stored expiry for the JSON view ("" when logged out / unknown).
func tokenExpiry(t token.Token, loggedIn bool) string {
	if !loggedIn || t.ExpiresAt.IsZero() {
		return ""
	}
	return t.ExpiresAt.Format(time.RFC3339)
}

// tokenStorePath is the token store path for help text (degrades to a generic label if unresolved).
func tokenStorePath() string {
	if p, err := token.Path(); err == nil {
		return p
	}
	return "~/.config/rootcause/tokens.json"
}

func brainDir(res config.Resolved) string {
	if res.Brain != nil {
		return res.Brain.Dir
	}
	return ""
}

func emptyDash(s string) string { return emptyOr(s, "—") }

func emptyOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
