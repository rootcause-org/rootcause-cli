package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootcause-org/rootcause-cli/internal/config"
	"github.com/rootcause-org/rootcause-cli/internal/token"
)

func TestLoadResolvedTokenSeedsProjectProfileFromBrainEnv(t *testing.T) {
	isolatedConfig(t)
	t.Setenv("RC_REFRESH_TOKEN_ACME", "rcor_machine")
	res := config.Resolved{
		Profile: "acme",
		Brain:   &config.Brain{Project: "acme", MachineTokenEnv: "RC_REFRESH_TOKEN_ACME"},
	}

	got, ok, err := loadResolvedToken(res, "https://app.replypen.com")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got.RefreshToken != "rcor_machine" || !got.ExpiresAt.IsZero() {
		t.Fatalf("seeded token = %+v, ok=%v", got, ok)
	}
	persisted, exists, err := token.Load("acme")
	if err != nil || !exists || persisted.RefreshToken != "rcor_machine" {
		t.Fatalf("persisted token = %+v, exists=%v, err=%v", persisted, exists, err)
	}
}

func TestLoadResolvedTokenKeepsStoredLoginWhenBrainEnvIsAbsent(t *testing.T) {
	isolatedConfig(t)
	want := token.Token{AccessToken: "rcoa_user", RefreshToken: "rcor_user", ExpiresAt: time.Now().Add(time.Hour)}
	seedToken(t, "acme", want)
	res := config.Resolved{
		Profile: "acme",
		Brain:   &config.Brain{Project: "acme", MachineTokenEnv: "RC_REFRESH_TOKEN_ACME"},
	}

	got, ok, err := loadResolvedToken(res, "https://app.replypen.com")
	if err != nil || !ok || got.RefreshToken != want.RefreshToken {
		t.Fatalf("stored login = %+v, ok=%v, err=%v", got, ok, err)
	}
}

func TestLoadResolvedTokenMissingBrainEnvFailsBeforeDefaultFallback(t *testing.T) {
	isolatedConfig(t)
	res := config.Resolved{
		Profile: "acme",
		Brain:   &config.Brain{Project: "acme", MachineTokenEnv: "RC_REFRESH_TOKEN_ACME"},
	}

	_, ok, err := loadResolvedToken(res, "https://app.replypen.com")
	if err == nil || ok || !strings.Contains(err.Error(), "RC_REFRESH_TOKEN_ACME") {
		t.Fatalf("expected named missing-env error, ok=%v err=%v", ok, err)
	}
}

func TestLoadResolvedTokenRefusesCustomBaseURL(t *testing.T) {
	isolatedConfig(t)
	t.Setenv("RC_REFRESH_TOKEN_ACME", "rcor_production")
	res := config.Resolved{
		Profile: "acme",
		Brain:   &config.Brain{Project: "acme", MachineTokenEnv: "RC_REFRESH_TOKEN_ACME"},
	}

	_, ok, err := loadResolvedToken(res, "https://staging.example")
	if err == nil || ok || !strings.Contains(err.Error(), "refusing to send machine token") {
		t.Fatalf("expected custom-base refusal, ok=%v err=%v", ok, err)
	}
	if _, exists, loadErr := token.Load("acme"); loadErr != nil || exists {
		t.Fatalf("custom-base refusal must not persist token, exists=%v err=%v", exists, loadErr)
	}
}

func TestLoadResolvedTokenUsesMatchingCustomBaseLogin(t *testing.T) {
	isolatedConfig(t)
	t.Setenv("RC_REFRESH_TOKEN_ACME", "rcor_production")
	want := token.Token{
		AccessToken: "rcoa_staging", RefreshToken: "rcor_staging",
		ExpiresAt: time.Now().Add(time.Hour), BaseURL: "https://staging.example",
	}
	seedToken(t, "acme", want)
	res := config.Resolved{
		Profile: "acme",
		Brain:   &config.Brain{Project: "acme", MachineTokenEnv: "RC_REFRESH_TOKEN_ACME"},
	}

	got, ok, err := loadResolvedToken(res, "https://staging.example")
	if err != nil || !ok || got.RefreshToken != "rcor_staging" {
		t.Fatalf("matching custom login = %+v, ok=%v err=%v", got, ok, err)
	}
}

func TestLoadResolvedTokenRejectsCachedMachineTokenAfterEnvRemoval(t *testing.T) {
	isolatedConfig(t)
	seedToken(t, "acme", token.Token{
		RefreshToken: "rcor_machine", MachineTokenEnv: "RC_REFRESH_TOKEN_ACME",
	})
	res := config.Resolved{
		Profile: "acme",
		Brain:   &config.Brain{Project: "acme", MachineTokenEnv: "RC_REFRESH_TOKEN_ACME"},
	}

	_, ok, err := loadResolvedToken(res, config.DefaultBaseURL)
	if err == nil || ok || !strings.Contains(err.Error(), "cached machine credentials are disabled") {
		t.Fatalf("expected removed-env refusal, ok=%v err=%v", ok, err)
	}
}

func TestLoadResolvedTokenRotatesWhenDeclaredMachineSecretChanges(t *testing.T) {
	isolatedConfig(t)
	t.Setenv("RC_REFRESH_TOKEN_ACME", "rcor_new")
	seedToken(t, "acme", token.Token{
		RefreshToken: "rcor_old", MachineTokenEnv: "RC_REFRESH_TOKEN_ACME",
	})
	res := config.Resolved{
		Profile: "acme",
		Brain:   &config.Brain{Project: "acme", MachineTokenEnv: "RC_REFRESH_TOKEN_ACME"},
	}

	got, ok, err := loadResolvedToken(res, config.DefaultBaseURL)
	if err != nil || !ok || got.RefreshToken != "rcor_new" || got.MachineTokenEnv != "RC_REFRESH_TOKEN_ACME" {
		t.Fatalf("rotated machine token = %+v, ok=%v err=%v", got, ok, err)
	}
}

func TestLoadResolvedTokenRejectsCachedMachineTokenAfterMarkerDeclarationRemoval(t *testing.T) {
	isolatedConfig(t)
	t.Setenv("RC_REFRESH_TOKEN_ACME", "rcor_machine")
	seedToken(t, "acme", token.Token{
		RefreshToken: "rcor_machine", MachineTokenEnv: "RC_REFRESH_TOKEN_ACME",
	})
	res := config.Resolved{Profile: "acme", Brain: &config.Brain{Project: "acme"}}

	_, ok, err := loadResolvedToken(res, config.DefaultBaseURL)
	if err == nil || ok || !strings.Contains(err.Error(), "require the same machine_token_env") {
		t.Fatalf("expected removed-marker refusal, ok=%v err=%v", ok, err)
	}
}

func TestLoadResolvedTokenRejectsCachedMachineTokenOnMatchingCustomBase(t *testing.T) {
	isolatedConfig(t)
	t.Setenv("RC_REFRESH_TOKEN_ACME", "rcor_machine")
	seedToken(t, "acme", token.Token{
		RefreshToken: "rcor_machine", BaseURL: "https://staging.example",
		MachineTokenEnv: "RC_REFRESH_TOKEN_ACME",
	})
	res := config.Resolved{
		Profile: "acme",
		Brain:   &config.Brain{Project: "acme", MachineTokenEnv: "RC_REFRESH_TOKEN_ACME"},
	}

	_, ok, err := loadResolvedToken(res, "https://staging.example")
	if err == nil || ok || !strings.Contains(err.Error(), "refusing to send machine token") {
		t.Fatalf("expected custom-base refusal, ok=%v err=%v", ok, err)
	}
}

func TestNewClientRejectsMachineTokenProjectMismatchBeforeCommandRequest(t *testing.T) {
	isolatedConfig(t)
	t.Setenv("RC_REFRESH_TOKEN_ACME", "rcor_machine")
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, config.MarkerFileName), []byte(
		"project = \"acme\"\nmachine_token_env = \"RC_REFRESH_TOKEN_ACME\"\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	var commandRequests int
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/whoami", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"all_projects":false,"project":{"id":"p2","name":"other"}}`))
	})
	mux.HandleFunc("GET /api/v1/runs", func(w http.ResponseWriter, _ *http.Request) {
		commandRequests++
		_, _ = w.Write([]byte(`{"runs":[]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	originalTransport := http.DefaultTransport
	host := strings.TrimPrefix(srv.URL, "http://")
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		clone := req.Clone(req.Context())
		clone.URL.Scheme = "http"
		clone.URL.Host = host
		return originalTransport.RoundTrip(clone)
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	seedToken(t, "acme", token.Token{
		AccessToken: "rcoa_machine", RefreshToken: "rcor_machine", ExpiresAt: time.Now().Add(time.Hour),
		BaseURL: config.DefaultBaseURL, MachineTokenEnv: "RC_REFRESH_TOKEN_ACME",
	})

	e := &env{}
	_, err := e.newClient()
	if err == nil || !strings.Contains(err.Error(), `bound to project "other"`) {
		t.Fatalf("expected project mismatch, got %v", err)
	}
	if commandRequests != 0 {
		t.Fatalf("target endpoint called %d times before project validation", commandRequests)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
