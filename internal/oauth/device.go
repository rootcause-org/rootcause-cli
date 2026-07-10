// Device-authorization grant (RFC 8628) — the headless/SSH login fallback. The CLI POSTs for a
// device+user code, prints the verification URL + short code for the human to open in a browser on any
// device, then polls the token endpoint until the human approves (or it's denied/expires).
package oauth

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// deviceAuth is the /oauth/device_authorization response.
type deviceAuth struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// LoginDevice runs the device flow end to end: request a code, print the human instructions to out, and
// poll until the token pair is minted. ctx bounds the overall wait (in addition to the server's
// expires_in). out is where the "go here, type this" prompt is written (stderr in practice).
func (c *Client) LoginDevice(ctx context.Context, out io.Writer) (Tokens, error) {
	da, err := c.startDevice(ctx)
	if err != nil {
		return Tokens{}, err
	}

	_, _ = fmt.Fprintln(out, "To sign in, open this URL in a browser:")
	if da.VerificationURIComplete != "" {
		_, _ = fmt.Fprintf(out, "    %s\n", da.VerificationURIComplete)
		_, _ = fmt.Fprintf(out, "  (or %s and enter code %s)\n", da.VerificationURI, da.UserCode)
	} else {
		_, _ = fmt.Fprintf(out, "    %s\n", da.VerificationURI)
		_, _ = fmt.Fprintf(out, "  and enter code: %s\n", da.UserCode)
	}
	_, _ = fmt.Fprintln(out, "Waiting for approval…")

	interval := time.Duration(da.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := c.clock().Add(time.Duration(da.ExpiresIn) * time.Second)

	for {
		// Poll wait first (RFC 8628: a client must not poll faster than the interval).
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Tokens{}, ctx.Err()
		case <-timer.C:
		}
		if da.ExpiresIn > 0 && !c.clock().Before(deadline) {
			return Tokens{}, fmt.Errorf("device code expired before approval — run `rc auth login` again")
		}

		tok, err := c.postToken(ctx, url.Values{
			"grant_type":  {deviceGrantType},
			"device_code": {da.DeviceCode},
			"client_id":   {CLIClientID},
		})
		if err == nil {
			return tok, nil
		}
		var oe *Error
		if !asOAuthError(err, &oe) {
			return Tokens{}, err // a transport error, not an RFC polling state
		}
		switch oe.Code {
		case "authorization_pending":
			// keep polling at the current interval
		case "slow_down":
			interval += 5 * time.Second // RFC 8628 §3.5: back off by 5s
		case "access_denied":
			return Tokens{}, fmt.Errorf("device login was denied")
		case "expired_token":
			return Tokens{}, fmt.Errorf("device code expired before approval — run `rc auth login` again")
		default:
			return Tokens{}, err
		}
	}
}

// startDevice POSTs /oauth/device_authorization and decodes the issued codes + verification URIs.
func (c *Client) startDevice(ctx context.Context) (deviceAuth, error) {
	form := url.Values{"client_id": {CLIClientID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+deviceAuthPath, strings.NewReader(form.Encode()))
	if err != nil {
		return deviceAuth{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return deviceAuth{}, fmt.Errorf("device authorization request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return deviceAuth{}, parseTokenError(resp)
	}
	var da deviceAuth
	if err := decodeJSON(resp.Body, &da); err != nil {
		return deviceAuth{}, fmt.Errorf("decode device authorization: %w", err)
	}
	if da.DeviceCode == "" || da.UserCode == "" {
		return deviceAuth{}, fmt.Errorf("device authorization response was incomplete")
	}
	return da, nil
}
