package token

import (
	"os"
	"testing"
	"time"
)

// isolate points XDG at a temp dir so each test gets its own token store.
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func TestSaveLoadDelete(t *testing.T) {
	isolate(t)

	if _, ok, err := Load("default"); err != nil || ok {
		t.Fatalf("empty store: ok=%v err=%v", ok, err)
	}

	a := Token{AccessToken: "rcoa_a", RefreshToken: "rcor_a", ExpiresAt: time.Now().Add(time.Hour), BaseURL: "https://a"}
	b := Token{AccessToken: "rcoa_b", RefreshToken: "rcor_b", ExpiresAt: time.Now().Add(time.Hour), BaseURL: "https://b"}
	if err := Save("default", a); err != nil {
		t.Fatal(err)
	}
	if err := Save("acme", b); err != nil {
		t.Fatal(err)
	}

	got, ok, err := Load("default")
	if err != nil || !ok {
		t.Fatalf("load default: ok=%v err=%v", ok, err)
	}
	if got.AccessToken != "rcoa_a" || got.BaseURL != "https://a" {
		t.Errorf("loaded wrong token: %+v", got)
	}

	// Deleting one profile must leave the other intact (per-profile keying).
	if err := Delete("default"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := Load("default"); ok {
		t.Error("default should be gone after delete")
	}
	if _, ok, _ := Load("acme"); !ok {
		t.Error("acme must survive deleting default")
	}
}

func TestStoreMode0600(t *testing.T) {
	isolate(t)
	if err := Save("default", Token{AccessToken: "x"}); err != nil {
		t.Fatal(err)
	}
	p, _ := Path()
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("token store mode = %o, want 600", perm)
	}
}

func TestExpired(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		exp  time.Time
		skew time.Duration
		want bool
	}{
		{"future, no skew", now.Add(time.Hour), 0, false},
		{"past", now.Add(-time.Minute), 0, true},
		{"within skew", now.Add(30 * time.Second), time.Minute, true},
		{"zero expiry treated as expired", time.Time{}, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := (Token{ExpiresAt: c.exp}).Expired(now, c.skew); got != c.want {
				t.Errorf("Expired = %v, want %v", got, c.want)
			}
		})
	}
}
