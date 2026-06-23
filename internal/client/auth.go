package client

import "context"

// TokenSource supplies the bearer access token for each request and refreshes it when the server
// rejects it. The INTENT is to keep all OAuth refresh policy OUT of the client: the client just asks for
// a token, and on a 401 asks for a fresh one and retries ONCE. The concrete source (CLI layer) owns the
// token store, the pre-expiry refresh, and the persist-after-refresh — the client stays a thin HTTP
// wrapper that knows nothing about OAuth.
type TokenSource interface {
	// Token returns a currently-valid access token, refreshing pre-emptively if it's near expiry. An
	// error here means the caller can't authenticate at all (no stored token / refresh failed) — the
	// command surfaces it as a "run `rc login`" prompt.
	Token(ctx context.Context) (string, error)
	// Refresh forces a refresh after the server rejected the access token mid-flight (a 401), returning
	// the new token. A nil-returning/erroring Refresh means re-login is required.
	Refresh(ctx context.Context) (string, error)
}

// staticTokenSource is a fixed bearer with no refresh — used by tests and any caller that already holds
// a token it doesn't manage. Refresh returns the same token (a 401 then surfaces verbatim).
type staticTokenSource string

func (s staticTokenSource) Token(context.Context) (string, error)   { return string(s), nil }
func (s staticTokenSource) Refresh(context.Context) (string, error) { return string(s), nil }

// StaticToken wraps a fixed bearer token as a TokenSource (no refresh).
func StaticToken(tok string) TokenSource { return staticTokenSource(tok) }
