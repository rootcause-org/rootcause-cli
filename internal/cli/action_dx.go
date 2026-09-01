package cli

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

const embassyErrorsBase = "https://github.com/rootcause-org/rootcause-embassy/blob/main/docs/integrator/errors.md#"

func actionProbeCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "probe",
		Short: "Probe the configured Embassy from ReplyPen",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			result, raw, err := c.ActionProbe(e.ctx(), e.scopeProject())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				if err := render.JSON(e.out, raw); err != nil {
					return err
				}
			} else if result.Code == "" {
				version := "unknown"
				if result.Health != nil {
					version = result.Health.Version
				}
				_, _ = fmt.Fprintf(e.out, "Embassy reachable (HTTP %d, version %s, %d ms)\n", result.Status, version, result.LatencyMs)
			}
			if result.Code != "" {
				writeActionDiagnostic(e, actionDiagnostic{Code: result.Code, Hint: result.Hint, Docs: result.Docs})
				return silentActionFailure(result.Code)
			}
			return nil
		},
	}
}

func actionReverseSecretCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "reverse-secret", Short: "Manage the Embassy reverse-channel secret"}
	cmd.AddCommand(&cobra.Command{
		Use:   "rotate",
		Short: "Generate, store, and print a new reverse-channel secret once",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			secretBytes := make([]byte, 32)
			if _, err := rand.Read(secretBytes); err != nil {
				return fmt.Errorf("generate reverse secret: %w", err)
			}
			secret := hex.EncodeToString(secretBytes)
			c, err := e.newClient()
			if err != nil {
				return err
			}
			if _, err := c.PatchBag(e.ctx(), "/api/v1/action", map[string]any{"action_reverse_secret": secret}, e.scopeProject()); err != nil {
				return err
			}
			if e.jsonOut() {
				raw, _ := json.Marshal(map[string]any{"action_reverse_secret": secret, "shown_once": true})
				return render.JSON(e.out, raw)
			}
			_, _ = fmt.Fprintln(e.err, "Sensitive value: shown once; store it in the Embassy environment now.")
			_, _ = fmt.Fprintln(e.out, secret)
			return nil
		},
	})
	return cmd
}

type actionDiagnostic struct {
	Code string `json:"code"`
	Hint string `json:"hint"`
	Docs string `json:"docs"`
}

type actionDoctorStep struct {
	Name       string                 `json:"name"`
	OK         bool                   `json:"ok"`
	Diagnostic actionDiagnostic       `json:"diagnostic,omitempty"`
	Details    map[string]interface{} `json:"details,omitempty"`
}

type actionDoctorConfig struct {
	ActionsEnabled          bool   `json:"actions_enabled"`
	Mode                    string `json:"mode,omitempty"`
	RunnerURLConfigured     bool   `json:"runner_url_configured"`
	ReverseSecretConfigured bool   `json:"reverse_secret_configured"`
}

type actionDoctorBundle struct {
	Project        string                      `json:"project,omitempty"`
	Tenant         string                      `json:"tenant,omitempty"`
	Action         string                      `json:"action"`
	RCVersion      string                      `json:"rc_version"`
	HubSHA         string                      `json:"hub_sha,omitempty"`
	EmbassyVersion string                      `json:"embassy_version,omitempty"`
	Config         actionDoctorConfig          `json:"config"`
	LastRejects    []actionDiagnostic          `json:"last_rejects"`
	Probe          *client.ActionProbeResponse `json:"probe,omitempty"`
	Steps          []actionDoctorStep          `json:"steps"`
	GeneratedAt    string                      `json:"generated_at"`
}

func newActionDoctorCmd(e *env, version string) *cobra.Command {
	var paramsJSON string
	var bundle bool
	cmd := &cobra.Command{
		Use:   "doctor <action-id>",
		Short: "Diagnose action resolution, Embassy health, and preflight",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			params, err := parseActionParams(paramsJSON)
			if err != nil {
				return err
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			out := actionDoctorBundle{
				Project: e.scopeProject(), Tenant: e.scopeTenant(), Action: args[0], RCVersion: version,
				LastRejects: []actionDiagnostic{}, GeneratedAt: time.Now().UTC().Format(time.RFC3339),
			}
			human := !bundle && !e.jsonOut()
			failed := false
			show, showErr := c.ActionShow(e.ctx(), args[0], e.scopeProject(), e.scopeTenant())
			if showErr != nil {
				d := actionDiagnosticForError(showErr)
				out.Steps = append(out.Steps, actionDoctorStep{Name: "resolve", Diagnostic: d})
				writeActionDiagnostic(e, d)
				failed = true
			} else {
				if out.Project == "" {
					out.Project = show.Project
				}
				out.Steps = append(out.Steps, actionDoctorStep{Name: "resolve", OK: true, Details: map[string]interface{}{"digest": show.Digest}})
				if human {
					_, _ = fmt.Fprintf(e.out, "resolve: ok (%s)\n", show.Digest)
				}
			}

			probe, _, probeErr := c.ActionProbe(e.ctx(), e.scopeProject())
			if probeErr != nil {
				d := actionDiagnosticForError(probeErr)
				out.Steps = append(out.Steps, actionDoctorStep{Name: "probe", Diagnostic: d})
				writeActionDiagnostic(e, d)
				failed = true
			} else {
				out.Probe = probe
				if probe.Health != nil {
					out.EmbassyVersion = probe.Health.Version
				}
				if probe.Code != "" {
					d := actionDiagnostic{Code: probe.Code, Hint: probe.Hint, Docs: probe.Docs}
					out.Steps = append(out.Steps, actionDoctorStep{Name: "probe", Diagnostic: d})
					writeActionDiagnostic(e, d)
					failed = true
				} else {
					out.Steps = append(out.Steps, actionDoctorStep{Name: "probe", OK: true, Details: map[string]interface{}{"latency_ms": probe.LatencyMs}})
					if human {
						_, _ = fmt.Fprintf(e.out, "probe: ok (%d ms)\n", probe.LatencyMs)
					}
				}
			}

			if showErr == nil {
				preflight, preflightErr := c.ActionPreflight(e.ctx(), args[0], client.ActionExecRequest{Params: params}, e.scopeProject(), e.scopeTenant())
				if preflightErr != nil {
					d := actionDiagnosticForError(preflightErr)
					out.Steps = append(out.Steps, actionDoctorStep{Name: "preflight", Diagnostic: d})
					writeActionDiagnostic(e, d)
					failed = true
				} else if d, failedPreflight := actionPreflightFailure(preflight); failedPreflight {
					out.Steps = append(out.Steps, actionDoctorStep{Name: "preflight", Diagnostic: d, Details: map[string]interface{}{"status": preflight.Status, "duration_ms": preflight.DurationMs}})
					writeActionDiagnostic(e, d)
					failed = true
				} else {
					out.Steps = append(out.Steps, actionDoctorStep{Name: "preflight", OK: true, Details: map[string]interface{}{"status": preflight.Status, "duration_ms": preflight.DurationMs}})
					if human {
						_, _ = fmt.Fprintf(e.out, "preflight: ok (%s)\n", preflight.Status)
					}
				}
			} else {
				d := actionDiagnosticForCode("ACTION_RESOLVE_FAILED", "Fix action resolution before running preflight.")
				out.Steps = append(out.Steps, actionDoctorStep{Name: "preflight", Diagnostic: d})
				writeActionDiagnostic(e, d)
			}

			if settings, settingsErr := c.GetBag(e.ctx(), "/api/v1/action", e.scopeProject()); settingsErr == nil {
				out.Config = doctorActionConfig(*settings)
			}
			if bundle || e.jsonOut() {
				raw, marshalErr := json.Marshal(out)
				if marshalErr != nil {
					return marshalErr
				}
				if err := render.JSON(e.out, raw); err != nil {
					return err
				}
			}
			if failed {
				return silentActionFailure("ACTION_DOCTOR_FAILED")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&paramsJSON, "params", "", "JSON object of action params")
	cmd.Flags().BoolVar(&bundle, "bundle", false, "print a redacted JSON escalation bundle")
	return cmd
}

func newProjectActionCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "action", Short: "Author action drafts"}
	draft := &cobra.Command{Use: "draft", Short: "Create, inspect, test, and promote action drafts"}
	draft.AddCommand(actionDraftNewCmd(e), actionDraftSimpleCmd(e, "show", http.MethodGet), actionDraftSimpleCmd(e, "validate", http.MethodPost), actionDraftTestCmd(e), actionDraftSimpleCmd(e, "submit", http.MethodPost), actionDraftSimpleCmd(e, "approve", http.MethodPost))
	cmd.AddCommand(draft)
	return cmd
}

func actionDraftNewCmd(e *env) *cobra.Command {
	var mode, runtime, displayName, description, customerDescription, risk string
	var params []string
	var connections []string
	cmd := &cobra.Command{
		Use:   "new <id>",
		Short: "Scaffold a new action draft",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			parsedParams := make([]map[string]any, 0, len(params))
			for _, raw := range params {
				var p map[string]any
				if err := json.Unmarshal([]byte(raw), &p); err != nil {
					return fmt.Errorf("parse --param JSON: %w", err)
				}
				parsedParams = append(parsedParams, p)
			}
			body := map[string]any{"id": args[0], "mode": mode, "runtime": runtime, "display_name": displayName, "description": description, "customer_description": customerDescription, "risk": risk, "params": parsedParams, "connections": connections}
			return runActionDraftRequest(e, http.MethodPost, "/api/v1/actions/drafts", body, "action-draft-new-"+args[0])
		},
	}
	cmd.Flags().StringVar(&mode, "mode", "", "action mode: hosted|embassy")
	cmd.Flags().StringVar(&runtime, "runtime", "", "action runtime")
	cmd.Flags().StringVar(&displayName, "display-name", "", "customer-facing action name")
	cmd.Flags().StringVar(&description, "description", "", "agent-facing action description")
	cmd.Flags().StringVar(&customerDescription, "customer-description", "", "customer-facing action description")
	cmd.Flags().StringVar(&risk, "risk", "", "action risk level")
	cmd.Flags().StringArrayVar(&params, "param", nil, "parameter JSON object (repeatable)")
	cmd.Flags().StringSliceVar(&connections, "connection", nil, "required connection name (repeatable)")
	return cmd
}

func actionDraftSimpleCmd(e *env, verb, method string) *cobra.Command {
	return &cobra.Command{
		Use:   verb + " <id>",
		Short: strings.ToUpper(verb[:1]) + verb[1:] + " an action draft",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path := "/api/v1/actions/drafts/" + url.PathEscape(args[0])
			if verb != "show" {
				path += "/" + verb
			}
			return runActionDraftRequest(e, method, path, map[string]any{}, "action-draft-"+verb+"-"+args[0])
		},
	}
}

func actionDraftTestCmd(e *env) *cobra.Command {
	var paramsJSON string
	cmd := &cobra.Command{
		Use:   "test <id>",
		Short: "Mint a human-confirmed test run for an action draft",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			params, err := parseActionParams(paramsJSON)
			if err != nil {
				return err
			}
			body := map[string]any{"params": params}
			if tenant := e.scopeTenant(); tenant != "" {
				body["tenant"] = tenant
			}
			return runActionDraftRequest(e, http.MethodPost, "/api/v1/actions/drafts/"+url.PathEscape(args[0])+"/test", body, "action-draft-test-"+args[0])
		},
	}
	cmd.Flags().StringVar(&paramsJSON, "params", "", "JSON object of action params")
	return cmd
}

func runActionDraftRequest(e *env, method, path string, body map[string]any, label string) error {
	c, err := e.newClient()
	if err != nil {
		return err
	}
	raw, err := c.RawScoped(e.ctx(), method, path, body, e.scopeProject(), "")
	if err != nil {
		return err
	}
	return e.renderJSON(label, raw)
}

func parseActionParams(raw string) (map[string]any, error) {
	params := map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return params, nil
	}
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		return nil, fmt.Errorf("parse --params JSON: %w", err)
	}
	return params, nil
}

func actionDiagnosticForError(err error) actionDiagnostic {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) && apiErr.Code != "" {
		return actionDiagnosticForCode(apiErr.Code, actionHint(apiErr.Code, apiErr.Message))
	}
	return actionDiagnosticForCode("ACTION_EXECUTOR_UNAVAILABLE", "ReplyPen could not reach the action service; retry, then include the doctor bundle when escalating.")
}

func actionPreflightFailure(resp *client.ActionExecResponse) (actionDiagnostic, bool) {
	if resp == nil {
		return actionDiagnosticForCode("ACTION_FAILED", "The action preflight returned no result; run doctor again and escalate if it persists."), true
	}
	status := strings.ToLower(strings.TrimSpace(resp.Status))
	failed := status == "failed" || status == "uncertain" || status == "preflight_failed"
	type resultError struct {
		Code string `json:"code"`
		Hint string `json:"hint"`
		Docs string `json:"docs"`
	}
	type resultEnvelope struct {
		OK    *bool        `json:"ok"`
		Error *resultError `json:"error"`
	}
	var envelope resultEnvelope
	if len(resp.Result) > 0 && json.Unmarshal(resp.Result, &envelope) == nil && envelope.OK != nil && !*envelope.OK {
		failed = true
	}
	var topError resultError
	if len(resp.Error) > 0 {
		_ = json.Unmarshal(resp.Error, &topError)
	}
	if topError.Code == "" && envelope.Error != nil {
		topError = *envelope.Error
	}
	if !failed {
		return actionDiagnostic{}, false
	}
	if topError.Code != "" && topError.Hint != "" && topError.Docs != "" {
		return actionDiagnostic{Code: topError.Code, Hint: topError.Hint, Docs: topError.Docs}, true
	}
	return actionDiagnosticForCode("ACTION_FAILED", "The action preflight failed; fix its manifest, parameters, or preflight checks before retrying."), true
}

func actionDiagnosticForCode(code, hint string) actionDiagnostic {
	return actionDiagnostic{Code: code, Hint: hint, Docs: embassyErrorsBase + strings.ToLower(code)}
}

func actionHint(code, fallback string) string {
	switch code {
	case "TENANT_REQUIRED":
		return "Select the tenant that this action should run against with --tenant."
	case "ACTIONS_DISABLED":
		return "Enable actions in project action settings before retrying."
	case "UNKNOWN_ACTION", "ACTION_RESOLVE_FAILED":
		return "Check the action ID and publish a valid action manifest in the project brain."
	case "INVALID_PARAMS", "BAD_PARAMS":
		return "Fix --params to match the action manifest schema."
	case "FORBIDDEN":
		return "Use a project token with the action settings and console scopes required by this command."
	}
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	return "Run the action doctor with --bundle and use the linked troubleshooting steps."
}

func writeActionDiagnostic(e *env, d actionDiagnostic) {
	_, _ = fmt.Fprintf(e.err, "%s: %s — %s\n", d.Code, d.Hint, d.Docs)
}

func silentActionFailure(code string) error {
	return &commandError{code: exitUsage, name: code, silent: true, message: code}
}

func doctorActionConfig(settings client.Settings) actionDoctorConfig {
	return actionDoctorConfig{
		ActionsEnabled:          settingBool(settings, "actions_enabled"),
		Mode:                    settingString(settings, "action_mode"),
		RunnerURLConfigured:     settingConfigured(settings, "action_runner_url"),
		ReverseSecretConfigured: settingConfigured(settings, "action_reverse_secret"),
	}
}

func settingBool(settings client.Settings, key string) bool {
	var value bool
	_ = json.Unmarshal(settingValue(settings[key]), &value)
	return value
}

func settingString(settings client.Settings, key string) string {
	var value string
	_ = json.Unmarshal(settingValue(settings[key]), &value)
	return value
}

func settingConfigured(settings client.Settings, key string) bool {
	raw := settingValue(settings[key])
	if len(raw) == 0 || string(raw) == "null" || string(raw) == `""` {
		return false
	}
	var value interface{}
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return strings.TrimSpace(v) != ""
	default:
		return value != nil
	}
}

func settingValue(field client.SettingField) json.RawMessage {
	if len(field.Effective) > 0 {
		return field.Effective
	}
	return field.Value
}
