// Package oauth is the CLI's client for rootcause's first-party OAuth 2.1 server. It speaks the four
// flows the CLI needs against the static, well-known CLI client (no dynamic registration): the PKCE
// loopback login (default, laptop), the device-authorization login (RFC 8628, SSH/headless), the
// refresh-token rotation that keeps a short-lived access token fresh, and revocation (logout).
//
// It is pure protocol — no token storage, no file I/O, no policy. The CLI layer wires it to the token
// store and decides WHEN to refresh; this package only knows HOW to talk to /oauth/*.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CLIClientID is the well-known client_id of the first-party rootcause CLI — a static public (PKCE)
// client seeded server-side (migration 00055), so the CLI need not dynamically register on every
// machine. MUST match the server constant oauth.CLIClientID.
const CLIClientID = "rcocl_cli"

// callbackPath is the loopback redirect path. The server's seeded redirect_uris are
// http://127.0.0.1/callback + http://localhost/callback, matched PORT-INSENSITIVELY (RFC 8252 §7.3),
// so the CLI binds any free port but the scheme+host+path must match exactly.
const callbackPath = "/callback"

// Endpoint paths off the issuer (= the configured base URL).
const (
	authorizePath  = "/oauth/authorize"
	tokenPath      = "/oauth/token"
	revokePath     = "/oauth/revoke"
	deviceAuthPath = "/oauth/device_authorization"
)

// deviceGrantType is the RFC 8628 token-endpoint grant_type the CLI polls with.
const deviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"

// Tokens is the token endpoint's success payload, as the CLI consumes it. RefreshToken is empty when
// the server rotated nothing (a non-rotating machine grant); the caller then keeps its existing one.
type Tokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int // access-token lifetime, seconds
}

// Error is a parsed RFC 6749 token-endpoint error ({"error","error_description"}). Code is the machine
// code (e.g. invalid_grant, authorization_pending); the CLI keys re-login on ErrInvalidGrant.
type Error struct {
	Code        string
	Description string
	Status      int
}

func (e *Error) Error() string {
	if e.Description != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Description)
	}
	return e.Code
}

// IsInvalidGrant reports whether err is an OAuth invalid_grant — a dead/expired/revoked refresh token,
// the signal for the CLI to prompt a fresh `rc login`.
func IsInvalidGrant(err error) bool {
	var oe *Error
	return asOAuthError(err, &oe) && oe.Code == "invalid_grant"
}

// Client talks to one issuer's /oauth endpoints. BaseURL is the issuer (the configured rootcause base
// URL); HTTP defaults to a sane client when nil.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	// now is the clock for computing absolute expiry in tests; nil → time.Now.
	now func() time.Time
}

// NewClient builds an OAuth client for the given issuer/base URL.
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// Refresh exchanges a refresh token for a fresh access token (and, for a rotating grant, a new refresh
// token). On a rotated grant the server returns a new refresh_token; on a non-rotating machine grant it
// omits it and the caller keeps the one it has. An invalid_grant (expired/revoked) comes back as an
// *Error the caller can detect with IsInvalidGrant.
func (c *Client) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	return c.postToken(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {CLIClientID},
	})
}

// Revoke revokes a token (access or refresh) per RFC 7009. Idempotent server-side — an unknown token is
// still a success — so logout can call it best-effort.
func (c *Client) Revoke(ctx context.Context, token string) error {
	form := url.Values{"token": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+revokePath, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("revoke request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return parseTokenError(resp)
	}
	return nil
}

// exchangeCode is the PKCE loopback flow's final step: trade the authorization code (bound to the
// verifier + redirect_uri) for the token pair.
func (c *Client) exchangeCode(ctx context.Context, code, codeVerifier, redirectURI string) (Tokens, error) {
	return c.postToken(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {codeVerifier},
		"redirect_uri":  {redirectURI},
		"client_id":     {CLIClientID},
	})
}

// postToken POSTs a form to /oauth/token and decodes the success or error body. A non-2xx with a parsed
// {"error",...} body becomes an *Error (so device polling can branch on authorization_pending etc.).
func (c *Client) postToken(ctx context.Context, form url.Values) (Tokens, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+tokenPath, strings.NewReader(form.Encode()))
	if err != nil {
		return Tokens{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return Tokens{}, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Tokens{}, parseTokenError(resp)
	}
	var body struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		TokenType    string `json:"token_type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Tokens{}, fmt.Errorf("decode token response: %w", err)
	}
	if body.AccessToken == "" {
		return Tokens{}, fmt.Errorf("token response carried no access_token")
	}
	return Tokens{
		AccessToken:  body.AccessToken,
		RefreshToken: body.RefreshToken,
		ExpiresIn:    body.ExpiresIn,
	}, nil
}

// parseTokenError reads an RFC 6749 error body into an *Error, degrading to a status-only Error when the
// body isn't the expected JSON shape.
func parseTokenError(resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var body struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if json.Unmarshal(data, &body) == nil && body.Error != "" {
		return &Error{Code: body.Error, Description: body.Description, Status: resp.StatusCode}
	}
	return &Error{Code: "http_error", Description: fmt.Sprintf("HTTP %d", resp.StatusCode), Status: resp.StatusCode}
}

// --- PKCE ------------------------------------------------------------------------------------------

// pkce is a generated PKCE pair: the verifier the CLI keeps secret + the S256 challenge it sends.
type pkce struct {
	verifier  string
	challenge string
}

// newPKCE mints a high-entropy verifier (43 chars base64url of 32 random bytes) and its S256 challenge.
func newPKCE() (pkce, error) {
	v, err := randString(32)
	if err != nil {
		return pkce{}, err
	}
	sum := sha256.Sum256([]byte(v))
	return pkce{verifier: v, challenge: base64.RawURLEncoding.EncodeToString(sum[:])}, nil
}

// randString returns n random bytes as a base64url (no padding) string — used for the PKCE verifier and
// the CSRF state.
func randString(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read randomness: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// decodeJSON decodes a small JSON body into v with a 1 MB cap.
func decodeJSON(r io.Reader, v any) error {
	return json.NewDecoder(io.LimitReader(r, 1<<20)).Decode(v)
}

// asOAuthError is errors.As specialized to *Error (kept local so callers needn't import errors just to
// type-assert).
func asOAuthError(err error, target **Error) bool {
	for err != nil {
		if e, ok := err.(*Error); ok {
			*target = e
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
