// share.go is the account-less seam: a developer pastes a run link
// (https://app.replypen.com/runs/<id>?t=<tok>) or a chat share link (https://app.replypen.com/s/<tok>)
// and gets the same artifacts a logged-in user gets. The token in the link IS the credential, so this
// path deliberately bypasses the whole credential ladder — no profile, no token store, no config.Load
// requirement. Everything downstream (debugdump, render) is the same code the bearer path runs.
package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/config"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

const shareTokenFlag = "share-token"

// The backquoted word is cobra's placeholder name for the flag value — keep exactly one.
const shareTokenHelp = "share `token` from a run/share link's ?t= parameter; with it no login is needed"

// shareTarget is a parsed run link. Token empty ⇒ the ordinary bearer path. Origin is set only by a
// pasted URL: the link's own host is authoritative, since the token is only valid there.
type shareTarget struct {
	runID  string
	token  string
	origin string
}

func (t shareTarget) shared() bool { return t.token != "" }

// parseRunTarget accepts a bare run id or a full run URL. An explicit --share-token wins over one
// embedded in the URL (the flag is the later, more deliberate statement).
func parseRunTarget(arg, flagToken string) (shareTarget, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return shareTarget{}, fmt.Errorf("expected a run id or a run link like https://app.replypen.com/runs/<id>?t=<token>")
	}
	if !looksLikeURL(arg) {
		return shareTarget{runID: arg, token: strings.TrimSpace(flagToken)}, nil
	}
	u, err := url.Parse(arg)
	if err != nil {
		return shareTarget{}, fmt.Errorf("not a usable run link %q: %w", arg, err)
	}
	segs := pathSegments(u)
	if len(segs) == 2 && segs[0] == "s" {
		return shareTarget{}, fmt.Errorf("%s is a shared chat session link — use `rc run session %s`", arg, arg)
	}
	if len(segs) != 2 || segs[0] != "runs" || segs[1] == "" {
		return shareTarget{}, fmt.Errorf("not a run link: %s (expected https://<host>/runs/<id>?t=<token>)", arg)
	}
	token := strings.TrimSpace(flagToken)
	if token == "" {
		token = strings.TrimSpace(u.Query().Get("t"))
	}
	return shareTarget{runID: segs[1], token: token, origin: origin(u)}, nil
}

// parseSessionTarget accepts a chat share URL or a bare share token.
func parseSessionTarget(arg, flagToken string) (shareTarget, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return shareTarget{}, fmt.Errorf("expected a share link like https://app.replypen.com/s/<token>, or the token itself")
	}
	if !looksLikeURL(arg) {
		token := strings.TrimSpace(flagToken)
		if token == "" {
			token = arg
		}
		return shareTarget{token: token}, nil
	}
	u, err := url.Parse(arg)
	if err != nil {
		return shareTarget{}, fmt.Errorf("not a usable share link %q: %w", arg, err)
	}
	segs := pathSegments(u)
	if len(segs) == 2 && segs[0] == "runs" {
		return shareTarget{}, fmt.Errorf("%s is a run link — use `rc run debug %s`", arg, arg)
	}
	if len(segs) != 2 || segs[0] != "s" || segs[1] == "" {
		return shareTarget{}, fmt.Errorf("not a share link: %s (expected https://<host>/s/<token>)", arg)
	}
	token := strings.TrimSpace(flagToken)
	if token == "" {
		token = segs[1]
	}
	return shareTarget{token: token, origin: origin(u)}, nil
}

func looksLikeURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

func origin(u *url.URL) string { return u.Scheme + "://" + u.Host }

func pathSegments(u *url.URL) []string {
	return strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
}

// runShareHint is the self-heal line for a run a caller cannot read: an agent that has the share link
// somewhere in its context can retry without a login. Every denial gets it — the account-less
// endpoints answer a uniform 404 by design, so we never probe the server to find out which case it is.
const runShareHint = "this run is not visible to your login (or you are not logged in). " +
	"If you were sent a link like https://app.replypen.com/runs/<id>?t=<token>, rerun with the full link, " +
	"or add --share-token <token>. Chat links (https://app.replypen.com/s/<token>) → rc run session '<link>'"

// sessionShareHint: a chat share token is only usable as the whole link the sender pasted.
const sessionShareHint = "this chat session is not readable with that token (expired, revoked, or truncated). " +
	"Use the full link exactly as it was shared with you: rc run session 'https://app.replypen.com/s/<token>'"

// withRunAccessHint tags the denials a share link can fix: no login at all (exitAuth) and the
// 401/403/404/UNKNOWN_RUN a logged-in member gets for a run outside their scope.
func withRunAccessHint(err error) error { return withHint(err, hintIfDenied(err, runShareHint)) }

// withSessionAccessHint is the same for a chat share token.
func withSessionAccessHint(err error) error {
	return withHint(err, hintIfDenied(err, sessionShareHint))
}

func hintIfDenied(err error, hint string) string {
	var ce *commandError
	if errors.As(err, &ce) && ce.code == exitAuth {
		return hint
	}
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
			return hint
		}
	}
	return ""
}

// newShareClient builds a token-less client for the shared endpoints. config.Load is BEST-EFFORT on
// purpose: the reader may have no profile, no token store and no HOME at all — a link must still open.
func (e *env) newShareClient(t shareTarget) *client.Client {
	base := t.origin
	if base == "" {
		base = e.baseURLOvr
	}
	if base == "" {
		if res, err := config.Load(e.profile); err == nil && res.BaseURL != "" {
			base = res.BaseURL
		} else {
			base = config.DefaultBaseURL
		}
	}
	return client.New(base, client.StaticToken(""))
}

// addShareTokenFlag wires --share-token onto a command that accepts a pasted link.
func addShareTokenFlag(cmd *cobra.Command, target *string) {
	cmd.Flags().StringVar(target, shareTokenFlag, "", shareTokenHelp)
}

// newRunSessionCmd writes ONE markdown file for a shared chat session: its transcript in reading order
// plus the runs behind it, each row carrying the `rc run debug` command that drills that run.
func newRunSessionCmd(e *env) *cobra.Command {
	var shareToken string
	cmd := &cobra.Command{
		Use:   "session <share-url|token>",
		Short: "Dump a shared chat session (transcript + its runs) to one markdown file",
		Long: "Read a chat session someone shared with you — no login needed, the link is the credential. " +
			"Paste https://app.replypen.com/s/<token> (or just the token) and rc writes " +
			".rootcause/debug/session-<session_id>.md: the transcript in reading order, then the runs " +
			"behind it with a ready-to-run `rc run debug` command per run.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			target, err := parseSessionTarget(args[0], shareToken)
			if err != nil {
				return err
			}
			return withSessionAccessHint(runSession(e, e.newShareClient(target), target.token))
		},
	}
	addShareTokenFlag(cmd, &shareToken)
	return cmd
}

// runSession fetches the two shared-session feeds and writes the single markdown artifact, printing its
// path (the file is the deliverable — like `rc run debug`, we don't summarize into stdout).
func runSession(e *env, c *client.Client, token string) error {
	// Runs first: a dead/unknown token then fails with the API's UNKNOWN_SESSION envelope rather than
	// the /s/ page's HTML 404.
	session, runsRaw, err := c.SharedSessionRuns(e.ctx(), token)
	if err != nil {
		return err
	}
	transcript, transcriptRaw, err := c.SharedTranscript(e.ctx(), token)
	if err != nil {
		return err
	}
	if e.jsonOut() {
		return e.renderJSON("session-"+sessionSlug(session, token), composeSessionJSON(transcriptRaw, runsRaw))
	}

	outDir := e.outDir
	if outDir == "" {
		outDir = defaultDebugDir
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create out dir %s: %w", outDir, err)
	}
	var buf bytes.Buffer
	render.SessionMarkdown(&buf, session, transcript)
	path := filepath.Join(outDir, "session-"+sessionSlug(session, token)+".md")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	_, _ = fmt.Fprintln(e.out, path)
	_, _ = fmt.Fprintf(e.err, "session %s · %d message(s) · %d run(s)\n",
		sessionSlug(session, token), len(transcript.Messages), len(session.Runs))
	return nil
}

// sessionSlug names the artifact by the session id when the server gave one; the share token is the
// stable fallback, so two different sessions never collide on one filename.
func sessionSlug(s *client.SharedSession, token string) string {
	if s != nil && s.SessionID != "" {
		return s.SessionID
	}
	return token
}

// composeSessionJSON is the `-o json` shape: BOTH server bodies verbatim under named keys, so a
// consumer that wants the raw rows never has to re-request them.
func composeSessionJSON(transcript, runs json.RawMessage) json.RawMessage {
	if len(transcript) == 0 {
		transcript = json.RawMessage("null")
	}
	if len(runs) == 0 {
		runs = json.RawMessage("null")
	}
	var buf bytes.Buffer
	buf.WriteString(`{"transcript":`)
	buf.Write(transcript)
	buf.WriteString(`,"runs":`)
	buf.Write(runs)
	buf.WriteString(`}`)
	return buf.Bytes()
}
