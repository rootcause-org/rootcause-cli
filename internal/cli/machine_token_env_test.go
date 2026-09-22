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
	t.Setenv("CLAUDE_CODE_REMOTE", "true")
	res := config.Resolved{
		Profile: "acme",
		Brain:   &config.Brain{Project: "acme", MachineTokenEnv: "RC_REFRESH_TOKEN_ACME"},
	}

	_, ok, err := loadResolvedToken(res, "https://app.replypen.com")
	if err == nil || ok || !strings.Contains(err.Error(), "RC_REFRESH_TOKEN_ACME") {
		t.Fatalf("expected named missing-env error, ok=%v err=%v", ok, err)
	}
}

func TestLoadResolvedTokenMissingBrainEnvAllowsLocalDefaultFallback(t *testing.T) {
	isolatedConfig(t)
	res := config.Resolved{
		Profile: "acme",
		Brain:   &config.Brain{Project: "acme", MachineTokenEnv: "RC_REFRESH_TOKEN_ACME"},
	}

	_, ok, err := loadResolvedToken(res, config.DefaultBaseURL)
	if err != nil || ok {
		t.Fatalf("local marker without named token should allow default fallback, ok=%v err=%v", ok, err)
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

func TestLoadResolvedTokenIgnoresCachedMachineTokenAfterEnvRemovalLocally(t *testing.T) {
	isolatedConfig(t)
	seedToken(t, "acme", token.Token{
		RefreshToken: "rcor_machine", MachineTokenEnv: "RC_REFRESH_TOKEN_ACME",
	})
	res := config.Resolved{
		Profile: "acme",
		Brain:   &config.Brain{Project: "acme", MachineTokenEnv: "RC_REFRESH_TOKEN_ACME"},
	}

	got, ok, err := loadResolvedToken(res, config.DefaultBaseURL)
	if err != nil || ok || got.RefreshToken != "" {
		t.Fatalf("stale machine cache must be ignored locally (default fallback), got=%+v ok=%v err=%v", got, ok, err)
	}
	if _, exists, loadErr := token.Load("acme"); loadErr != nil || !exists {
		t.Fatalf("stale entry should stay in the store for when the variable returns, exists=%v err=%v", exists, loadErr)
	}
}

func TestLoadResolvedTokenRejectsCachedMachineTokenAfterEnvRemovalInCloud(t *testing.T) {
	isolatedConfig(t)
	t.Setenv("CLAUDE_CODE_REMOTE", "true")
	seedToken(t, "acme", token.Token{
		RefreshToken: "rcor_machine", MachineTokenEnv: "RC_REFRESH_TOKEN_ACME",
	})
	res := config.Resolved{
		Profile: "acme",
		Brain:   &config.Brain{Project: "acme", MachineTokenEnv: "RC_REFRESH_TOKEN_ACME"},
	}

	_, ok, err := loadResolvedToken(res, config.DefaultBaseURL)
	if err == nil || ok || !strings.Contains(err.Error(), "cached machine credentials are disabled") {
		t.Fatalf("expected removed-env refusal in cloud, ok=%v err=%v", ok, err)
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

func TestLoadResolvedTokenIgnoresCachedMachineTokenAfterMarkerDeclarationRemoval(t *testing.T) {
	isolatedConfig(t)
	t.Setenv("RC_REFRESH_TOKEN_ACME", "rcor_machine")
	seedToken(t, "acme", token.Token{
		RefreshToken: "rcor_machine", MachineTokenEnv: "RC_REFRESH_TOKEN_ACME",
	})
	for _, brain := range []*config.Brain{{Project: "acme"}, nil} {
		res := config.Resolved{Profile: "acme", Brain: brain}
		got, ok, err := loadResolvedToken(res, config.DefaultBaseURL)
		if err != nil || ok || got.RefreshToken != "" {
			t.Fatalf("brain=%+v: cached machine token without its marker must be ignored, got=%+v ok=%v err=%v", brain, got, ok, err)
		}
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

// alsoMarker binds the checkout to acme-staff and declares a second, separately minted acme-support
// identity — the KampAdmin cloud shape (chat runs live in a different project than the checkout).
const alsoMarker = `project = "acme-staff"
machine_token_env = "RC_REFRESH_TOKEN_ACME_STAFF"

[[also]]
project = "acme-support"
machine_token_env = "RC_REFRESH_TOKEN_ACME_SUPPORT"
`

// inAlsoBrain chdirs into a checkout carrying alsoMarker.
func inAlsoBrain(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, config.MarkerFileName), []byte(alsoMarker), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	return dir
}

func TestSecondaryBindingSeedsItsOwnProfileFromItsOwnEnv(t *testing.T) {
	isolatedConfig(t)
	inAlsoBrain(t)
	t.Setenv("RC_REFRESH_TOKEN_ACME_STAFF", "rcor_staff")
	t.Setenv("RC_REFRESH_TOKEN_ACME_SUPPORT", "rcor_support")

	res, err := config.LoadFor("", "acme-support")
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := loadResolvedToken(res, config.DefaultBaseURL)
	if err != nil || !ok || got.RefreshToken != "rcor_support" {
		t.Fatalf("secondary seed = %+v ok=%v err=%v", got, ok, err)
	}
	persisted, exists, err := token.Load("acme-support")
	if err != nil || !exists || persisted.MachineTokenEnv != "RC_REFRESH_TOKEN_ACME_SUPPORT" {
		t.Fatalf("persisted = %+v exists=%v err=%v", persisted, exists, err)
	}
	if _, staffExists, _ := token.Load("acme-staff"); staffExists {
		t.Fatal("selecting the secondary binding must not touch the primary profile")
	}
}

func TestSecondaryBindingMissingEnvFailsClosedInCloud(t *testing.T) {
	isolatedConfig(t)
	inAlsoBrain(t)
	t.Setenv("CLAUDE_CODE_REMOTE", "true")
	t.Setenv("RC_REFRESH_TOKEN_ACME_STAFF", "rcor_staff")

	res, err := config.LoadFor("", "acme-support")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := loadResolvedToken(res, config.DefaultBaseURL); err == nil || ok ||
		!strings.Contains(err.Error(), "RC_REFRESH_TOKEN_ACME_SUPPORT") {
		t.Fatalf("expected the secondary variable to fail closed, ok=%v err=%v", ok, err)
	}
}

func TestSecondaryBindingIgnoresStaleCacheLocally(t *testing.T) {
	isolatedConfig(t)
	inAlsoBrain(t)
	seedToken(t, "acme-support", token.Token{
		RefreshToken: "rcor_support", MachineTokenEnv: "RC_REFRESH_TOKEN_ACME_SUPPORT",
	})

	res, err := config.LoadFor("", "acme-support")
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := loadResolvedToken(res, config.DefaultBaseURL)
	if err != nil || ok || got.RefreshToken != "" {
		t.Fatalf("stale secondary cache must be ignored locally, got=%+v ok=%v err=%v", got, ok, err)
	}
}

func TestNewClientPinsSecondaryBindingToItsOwnProject(t *testing.T) {
	for _, tc := range []struct {
		name      string
		whoami    string
		projects  string
		wantErr   string
		wantScope string
	}{
		{
			name:      "pinned to the requested project",
			whoami:    `{"all_projects":false,"project":{"id":"p2","name":"acme-support"}}`,
			projects:  `{"projects":[{"id":"p2","name":"acme-support"}]}`,
			wantScope: "acme-support",
		},
		{
			name:     "still pinned to the checkout's primary project",
			whoami:   `{"all_projects":false,"project":{"id":"p1","name":"acme-staff"}}`,
			projects: `{"projects":[{"id":"p1","name":"acme-staff"}]}`,
			wantErr:  `is bound to project "acme-staff", but this checkout requires "acme-support"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolatedConfig(t)
			inAlsoBrain(t)
			t.Setenv("RC_REFRESH_TOKEN_ACME_SUPPORT", "rcor_support")
			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/v1/whoami", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.whoami))
			})
			mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.projects))
			})
			redirectToStub(t, mux)
			// A cached, unexpired credential for the SECONDARY profile keeps the case offline of /oauth.
			seedToken(t, "acme-support", token.Token{
				AccessToken: "rcoa_support", RefreshToken: "rcor_support", ExpiresAt: time.Now().Add(time.Hour),
				BaseURL: config.DefaultBaseURL, MachineTokenEnv: "RC_REFRESH_TOKEN_ACME_SUPPORT",
			})

			e := &env{project: "acme-support"}
			_, err := e.newClient()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if e.resolved.Profile != "acme-support" || e.resolved.BindingKind != config.BindingAlso {
				t.Fatalf("resolved = %+v, want the [[also]] binding's profile", e.resolved)
			}
			if e.scopeProject() != tc.wantScope {
				t.Fatalf("scope project = %q, want %q", e.scopeProject(), tc.wantScope)
			}
		})
	}
}

// redirectToStub points every outbound request at the stub server for the duration of the test, so the
// production base URL (the only one a machine token may be sent to) still reaches a local handler.
func redirectToStub(t *testing.T, handler http.Handler) {
	t.Helper()
	srv := httptest.NewServer(handler)
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
}
