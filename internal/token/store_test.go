package token

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

func TestCrossProcessSavesPreserveEveryProfile(t *testing.T) {
	isolate(t)
	barrier := filepath.Join(t.TempDir(), "start")
	const workers = 12
	cmds := make([]*exec.Cmd, 0, workers)
	for i := 0; i < workers; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestStoreProcessHelper$")
		cmd.Env = append(os.Environ(),
			"RC_STORE_HELPER=1",
			"RC_STORE_BARRIER="+barrier,
			"RC_STORE_PROFILE=profile-"+strconv.Itoa(i),
			"RC_STORE_TOKEN=token-"+strconv.Itoa(i),
		)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmds = append(cmds, cmd)
	}
	if err := os.WriteFile(barrier, []byte("go"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("helper failed: %v", err)
		}
	}

	profiles, err := List()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < workers; i++ {
		profile := "profile-" + strconv.Itoa(i)
		if got := profiles[profile].RefreshToken; got != "token-"+strconv.Itoa(i) {
			t.Errorf("%s refresh token = %q", profile, got)
		}
	}
}

func TestStoreProcessHelper(t *testing.T) {
	if os.Getenv("RC_STORE_HELPER") != "1" {
		return
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(os.Getenv("RC_STORE_BARRIER")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for store barrier")
		}
		time.Sleep(5 * time.Millisecond)
	}
	profile, refresh := os.Getenv("RC_STORE_PROFILE"), os.Getenv("RC_STORE_TOKEN")
	if profile == "" || refresh == "" {
		t.Fatal("missing helper profile/token")
	}
	if err := Save(profile, Token{RefreshToken: refresh}); err != nil {
		t.Fatal(err)
	}
}
