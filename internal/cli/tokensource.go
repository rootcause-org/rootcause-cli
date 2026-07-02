package cli

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rootcause-org/rootcause-cli/internal/oauth"
	"github.com/rootcause-org/rootcause-cli/internal/token"
)

// refreshSkew refreshes an access token this long before it actually expires, so a request never races
// the expiry boundary (and the 401-retry path stays a rare backstop, not the common case).
const refreshSkew = 60 * time.Second

// liveSource is the production client.TokenSource: it reads the profile's token from the store, refreshes
// it via OAuth when it's near expiry (or on a forced 401 retry), and PERSISTS the rotated pair so the
// next command starts fresh. All refresh policy lives here — the client stays oblivious to OAuth.
type liveSource struct {
	profile string
	baseURL string
	oauth   *oauth.Client
	mu      sync.Mutex // serializes refresh so two goroutines don't double-rotate one refresh token
}

// newLiveSource builds the token source for a profile against the issuer at baseURL.
func newLiveSource(profile, baseURL string) *liveSource {
	return &liveSource{profile: profile, baseURL: baseURL, oauth: oauth.NewClient(baseURL)}
}

// Token returns a valid access token, refreshing pre-emptively when it's within refreshSkew of expiry.
func (s *liveSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok, err := token.Load(s.profile)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", reauthError(s.profile)
	}
	if t.Expired(time.Now(), refreshSkew) {
		return s.refreshLocked(ctx, t)
	}
	return t.AccessToken, nil
}

// Refresh forces a rotation after the server rejected the access token mid-flight (a 401).
func (s *liveSource) Refresh(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok, err := token.Load(s.profile)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", reauthError(s.profile)
	}
	return s.refreshLocked(ctx, t)
}

// refreshLocked exchanges the stored refresh token for a fresh access token and persists the result.
// A dead/expired refresh (invalid_grant) surfaces as a "run `rc login`" prompt. Must hold s.mu.
//
// Cross-process safety: the in-process mutex only serializes ONE liveSource. Two concurrent `rc`
// processes (parallel agents on one machine) can both present the same refresh token; with a ROTATING
// grant the server invalidates it on first use, so the slower process gets invalid_grant even though the
// login is healthy. So on invalid_grant we re-read the store: if a sibling already rotated it (the stored
// refresh differs), adopt its result rather than forcing a spurious re-login. Bounded to two attempts.
func (s *liveSource) refreshLocked(ctx context.Context, t token.Token) (string, error) {
	for attempt := 0; attempt < 2; attempt++ {
		if t.RefreshToken == "" {
			return "", reauthError(s.profile)
		}
		res, err := s.oauth.Refresh(ctx, t.RefreshToken)
		if err == nil {
			next := token.Token{
				AccessToken:  res.AccessToken,
				RefreshToken: t.RefreshToken, // a non-rotating grant returns none → keep the one we have
				ExpiresAt:    time.Now().Add(time.Duration(res.ExpiresIn) * time.Second),
				BaseURL:      s.baseURL,
			}
			if res.RefreshToken != "" {
				next.RefreshToken = res.RefreshToken // rotating grant: store the new refresh
			}
			if serr := token.Save(s.profile, next); serr != nil {
				return "", serr
			}
			return next.AccessToken, nil
		}
		if !oauth.IsInvalidGrant(err) {
			return "", fmt.Errorf("refresh access token: %w", err)
		}
		// invalid_grant — maybe a sibling process already rotated this refresh token. Re-read the store.
		fresh, ok, lerr := token.Load(s.profile)
		if lerr != nil {
			return "", lerr
		}
		if !ok || fresh.RefreshToken == t.RefreshToken {
			return "", reauthError(s.profile) // genuinely dead, not a race
		}
		if !fresh.Expired(time.Now(), refreshSkew) {
			return fresh.AccessToken, nil // the sibling already minted a usable access token
		}
		t = fresh // rotated but already near expiry → retry once with the sibling's newer refresh
	}
	return "", reauthError(s.profile)
}

// reauthError is the shared "session can't be refreshed — log in again" error.
func reauthError(profile string) error {
	return fmt.Errorf("session expired (profile %q) — run `rc login`", profile)
}
