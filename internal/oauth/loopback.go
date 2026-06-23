// PKCE loopback login (the laptop default): bind a localhost port, open the browser at /oauth/authorize
// with an S256 challenge, catch the redirect back to http://127.0.0.1:<port>/callback, and exchange the
// code (bound to the verifier) for the token pair. No client secret — the PKCE verifier IS the proof.
package oauth

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// callbackResult is what the loopback handler hands back: the authorization code, or an OAuth error the
// server redirected with.
type callbackResult struct {
	code string
	err  error
}

// LoginPKCE runs the loopback authorization-code+PKCE flow. opener is called with the authorize URL to
// launch the user's browser (the CLI passes a real browser-opener; tests pass a stub that drives the
// callback). out receives the human-facing "opening your browser / paste this URL" lines. ctx bounds
// the wait for the redirect.
func (c *Client) LoginPKCE(ctx context.Context, opener func(string) error, out io.Writer) (Tokens, error) {
	// Bind a free loopback port FIRST — the redirect_uri must carry the exact port we listen on.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Tokens{}, fmt.Errorf("bind loopback port: %w", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d%s", port, callbackPath)

	pk, err := newPKCE()
	if err != nil {
		return Tokens{}, err
	}
	state, err := randString(16)
	if err != nil {
		return Tokens{}, err
	}

	authURL := c.BaseURL + authorizePath + "?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {CLIClientID},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {pk.challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}.Encode()

	resultCh := make(chan callbackResult, 1)
	srv := &http.Server{Handler: callbackHandler(state, resultCh)}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	fmt.Fprintln(out, "Opening your browser to sign in. If it doesn't open, visit:")
	fmt.Fprintf(out, "    %s\n", authURL)
	if opener != nil {
		// A failed open is non-fatal: the URL is already printed for a manual paste.
		_ = opener(authURL)
	}

	select {
	case <-ctx.Done():
		return Tokens{}, fmt.Errorf("timed out waiting for the browser sign-in (%w)", ctx.Err())
	case res := <-resultCh:
		if res.err != nil {
			return Tokens{}, res.err
		}
		return c.exchangeCode(ctx, res.code, pk.verifier, redirectURI)
	}
}

// callbackHandler serves the single /callback hit: it validates state, extracts the code (or surfaces a
// redirected ?error=), writes a human "you can close this tab" page, and signals the waiting flow. Any
// other path (a favicon probe, a stray request) gets a 404 and is ignored.
func callbackHandler(wantState string, resultCh chan<- callbackResult) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			res := callbackResult{err: &Error{Code: e, Description: q.Get("error_description")}}
			writeClosePage(w, false)
			send(resultCh, res)
			return
		}
		if q.Get("state") != wantState {
			writeClosePage(w, false)
			send(resultCh, callbackResult{err: fmt.Errorf("oauth callback state mismatch — possible CSRF, aborting")})
			return
		}
		code := q.Get("code")
		if code == "" {
			writeClosePage(w, false)
			send(resultCh, callbackResult{err: fmt.Errorf("oauth callback carried no code")})
			return
		}
		writeClosePage(w, true)
		send(resultCh, callbackResult{code: code})
	})
	return mux
}

// send delivers the first result and never blocks (the channel is buffered to 1; a second callback —
// e.g. a browser retry — is dropped).
func send(ch chan<- callbackResult, res callbackResult) {
	select {
	case ch <- res:
	default:
	}
}

// writeClosePage renders the minimal "return to your terminal" page the browser lands on.
func writeClosePage(w http.ResponseWriter, ok bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	title, msg := "Signed in", "You're signed in to rootcause. You can close this tab and return to your terminal."
	if !ok {
		title, msg = "Sign-in failed", "Something went wrong signing in. Return to your terminal for details."
	}
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><title>rootcause — %s</title>
<style>body{font-family:system-ui,sans-serif;background:#0f1115;color:#e6e6e6;display:flex;min-height:100vh;margin:0;align-items:center;justify-content:center}
.card{background:#171a21;padding:2rem;border-radius:12px;max-width:420px;text-align:center}h1{font-size:1.1rem;margin:0 0 .75rem}p{margin:0;color:#9aa0aa;line-height:1.5}</style>
</head><body><div class="card"><h1>%s</h1><p>%s</p></div></body></html>`, title, title, msg)
}
