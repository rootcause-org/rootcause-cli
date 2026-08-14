// Package token is the on-disk OAuth credential store: ~/.config/rootcause/tokens.json (0600), a
// per-profile record of {access_token, refresh_token, expiry, base_url}. The CLI is OAuth-only: it
// holds no static API key, only short-lived access tokens refreshed from a stored refresh token.
//
// The store is intentionally dumb: it persists and returns what it's given. WHO refreshes (and the
// pre-expiry/401 policy) lives one layer up, in the CLI's token source — this package only owns the
// file format, the 0600 mode, and the per-profile keying.
package token

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/rootcause-org/rootcause-cli/internal/config"
)

// fileName is the token store under the rootcause config dir.
const fileName = "tokens.json"

// storeMode is owner-only: the file holds live OAuth refresh + access tokens.
const storeMode = 0o600

var storeMu sync.Mutex

const (
	lockWait  = 5 * time.Second
	lockStale = 30 * time.Second
)

// Token is one profile's stored credential. ExpiresAt is the access token's absolute expiry (computed
// at mint/refresh time from the server's expires_in). RefreshToken is empty for a non-rotating machine
// credential that never returned one — but in practice the CLI always logs in with a rotating grant, so
// it's set. BaseURL records the issuer used at login/latest refresh for diagnostics and revocation; it
// does not override normal command URL resolution.
type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	BaseURL      string    `json:"base_url"`
	// MachineTokenEnv records provenance only (never the secret) so removing a declared cloud secret
	// disables its cached machine credential without breaking ordinary OAuth profiles.
	MachineTokenEnv string `json:"machine_token_env,omitempty"`
}

// Expired reports whether the access token is at/after its expiry, with a skew margin so a token about
// to expire is treated as already expired (refresh it before the request, not after a 401). A zero
// ExpiresAt (unknown) is treated as expired so the caller refreshes rather than sending a stale token.
func (t Token) Expired(now time.Time, skew time.Duration) bool {
	if t.ExpiresAt.IsZero() {
		return true
	}
	return !now.Add(skew).Before(t.ExpiresAt)
}

// storeFile is the JSON envelope: profile name → token.
type storeFile struct {
	Profiles map[string]Token `json:"profiles"`
}

// Path is the resolved tokens.json location (exported for diagnostics/messages).
func Path() (string, error) {
	dir, err := config.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fileName), nil
}

// Load returns the token stored for profile, with ok=false when there is none (no file, or no such
// profile). A malformed store is an error so a corrupted file surfaces loudly rather than silently
// looking "logged out".
func Load(profile string) (Token, bool, error) {
	sf, err := read()
	if err != nil {
		return Token{}, false, err
	}
	t, ok := sf.Profiles[profile]
	return t, ok, nil
}

// Save writes (or replaces) the token for profile, preserving every other profile's entry, at 0600.
func Save(profile string, t Token) error {
	return mutate(func(sf *storeFile) {
		if sf.Profiles == nil {
			sf.Profiles = map[string]Token{}
		}
		sf.Profiles[profile] = t
	})
}

// Delete removes the token for profile (a no-op if absent). Used by `rc auth logout`.
func Delete(profile string) error {
	return mutate(func(sf *storeFile) { delete(sf.Profiles, profile) })
}

// List returns every stored profile→token (a copy). Used by diagnostics/whoami.
func List() (map[string]Token, error) {
	sf, err := read()
	if err != nil {
		return nil, err
	}
	return sf.Profiles, nil
}

// read loads the store; a missing file is an empty store, not an error.
func read() (storeFile, error) {
	path, err := Path()
	if err != nil {
		return storeFile{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return storeFile{Profiles: map[string]Token{}}, nil
	}
	if err != nil {
		return storeFile{}, fmt.Errorf("read %s: %w", path, err)
	}
	var sf storeFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return storeFile{}, fmt.Errorf("parse %s: %w (delete it and `rc auth login` again)", path, err)
	}
	if sf.Profiles == nil {
		sf.Profiles = map[string]Token{}
	}
	return sf, nil
}

func mutate(change func(*storeFile)) error {
	storeMu.Lock()
	defer storeMu.Unlock()

	unlock, err := acquireLock()
	if err != nil {
		return err
	}
	defer unlock()

	sf, err := read()
	if err != nil {
		return err
	}
	change(&sf)
	return write(sf)
}

// acquireLock serializes cross-process read-modify-write without platform-specific syscalls. A dead
// process can leave the lock file behind; a 30-second-old lock is stale because store mutations are
// only a small local JSON write.
func acquireLock() (func(), error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}
	lockPath := path + ".lock"
	deadline := time.Now().Add(lockWait)
	for {
		f, openErr := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, storeMode)
		if openErr == nil {
			_, _ = f.WriteString(strconv.Itoa(os.Getpid()))
			_ = f.Close()
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !os.IsExist(openErr) {
			return nil, fmt.Errorf("lock token store: %w", openErr)
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > lockStale {
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("token store is busy (lock %s)", lockPath)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// write persists the store atomically-ish (write a temp file, then rename) at 0600, creating the config
// dir if needed. The rename keeps a concurrent reader from ever seeing a half-written file.
func write(sf storeFile) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return fmt.Errorf("encode token store: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tokens-*.tmp")
	if err != nil {
		return fmt.Errorf("create token temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(storeMode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod %s: %w", tmpPath, err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}
