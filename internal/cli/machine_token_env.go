package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/config"
	"github.com/rootcause-org/rootcause-cli/internal/token"
)

// loadResolvedToken loads a profile token and, when the brain names a machine-token environment
// variable, seeds that project profile from it. The marker carries only the variable name; the
// credential stays in the environment and the normal 0600 token store.
func loadResolvedToken(res config.Resolved, baseURL string) (token.Token, bool, error) {
	stored, ok, err := token.Load(res.Profile)
	if err != nil {
		return token.Token{}, false, err
	}
	if ok && stored.MachineTokenEnv != "" {
		if res.Brain == nil || res.Brain.MachineTokenEnv != stored.MachineTokenEnv {
			return token.Token{}, false, fmt.Errorf(
				"cached machine credentials for %q require the same machine_token_env declaration in .rootcause.toml",
				stored.MachineTokenEnv,
			)
		}
		secret := os.Getenv(stored.MachineTokenEnv)
		if secret == "" {
			return token.Token{}, false, fmt.Errorf(
				"machine token environment variable %q is missing; cached machine credentials are disabled",
				stored.MachineTokenEnv,
			)
		}
		if baseURL != config.DefaultBaseURL {
			return token.Token{}, false, fmt.Errorf(
				"refusing to send machine token %q to non-production base URL %q\n  fix: unset ROOTCAUSE_BASE_URL or use `rc auth login` for that environment",
				stored.MachineTokenEnv,
				baseURL,
			)
		}
	}
	if res.Brain == nil || res.Brain.MachineTokenEnv == "" {
		return stored, ok, nil
	}

	secret := os.Getenv(res.Brain.MachineTokenEnv)
	if secret == "" {
		if ok {
			if stored.MachineTokenEnv != "" {
				return token.Token{}, false, fmt.Errorf(
					"machine token environment variable %q is not set; cached machine credentials are disabled",
					res.Brain.MachineTokenEnv,
				)
			}
			return stored, true, nil
		}
		if os.Getenv("CLAUDE_CODE_REMOTE") != "true" {
			return token.Token{}, false, nil
		}
		return token.Token{}, false, fmt.Errorf(
			"machine token environment variable %q is not set\n  fix: set it or run `rc auth login` from this checkout",
			res.Brain.MachineTokenEnv,
		)
	}
	if baseURL != config.DefaultBaseURL {
		if ok && config.CanonicalBaseURL(stored.BaseURL) == baseURL {
			return stored, true, nil
		}
		return token.Token{}, false, fmt.Errorf(
			"refusing to send machine token %q to non-production base URL %q\n  fix: unset ROOTCAUSE_BASE_URL or use `rc auth login` for that environment",
			res.Brain.MachineTokenEnv,
			baseURL,
		)
	}
	if ok && stored.RefreshToken == secret && stored.MachineTokenEnv == res.Brain.MachineTokenEnv {
		return stored, true, nil
	}

	seeded := token.Token{
		RefreshToken:    secret,
		ExpiresAt:       time.Time{}, // force a refresh before the first API request
		BaseURL:         baseURL,
		MachineTokenEnv: res.Brain.MachineTokenEnv,
	}
	if err := token.Save(res.Profile, seeded); err != nil {
		return token.Token{}, false, err
	}
	return seeded, true, nil
}

func machineTokenEnvActive(res config.Resolved) bool {
	return res.Brain != nil && res.Brain.MachineTokenEnv != "" && os.Getenv(res.Brain.MachineTokenEnv) != ""
}

func validateMachineTokenScope(res config.Resolved, scope *client.WhoamiResponse) error {
	if !machineTokenEnvActive(res) {
		return nil
	}
	if scope == nil || scope.AllProjects || scope.Project == nil {
		return fmt.Errorf("machine token %q must be pinned to project %q", res.Brain.MachineTokenEnv, res.Brain.Project)
	}
	if scope.Project.Name != res.Brain.Project && scope.Project.ID != res.Brain.Project {
		return fmt.Errorf(
			"machine token %q is bound to project %q, but this checkout requires %q",
			res.Brain.MachineTokenEnv,
			firstScopeName(scope.Project.Name, scope.Project.ID),
			res.Brain.Project,
		)
	}
	return nil
}

func firstScopeName(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return "unknown"
}
