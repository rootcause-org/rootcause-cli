package cli

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/rootcause-org/rootcause-cli/internal/render"
)

const errorDocsBase = "https://github.com/rootcause-org/rootcause-embassy/blob/main/docs/integrator/errors.md#"

func newChatCmd(e *env, version string) *cobra.Command {
	cmd := &cobra.Command{Use: "chat", Short: "Configure, diagnose, and smoke-test embedded chat"}
	cmd.AddCommand(newBagGetCmd(e, "/api/v1/chat"), newBagSetCmd(e, "/api/v1/chat"), chatSecretCmd(e), chatTokenCmd(e), chatSendCmd(e), chatDoctorCmd(e, version), chatBriefCmd(e))
	return cmd
}

func chatBriefCmd(e *env) *cobra.Command {
	var target, locale, scheme string
	cmd := &cobra.Command{Use: "brief", Short: "Print the secret-free chat implementation brief", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
		if target != "bubble" && target != "page" {
			return fmt.Errorf("--target must be bubble or page")
		}
		if !containsCLI([]string{"", "en", "nl", "fr"}, locale) {
			return fmt.Errorf("--locale must be en, nl, or fr")
		}
		if !containsCLI([]string{"", "light", "dark"}, scheme) {
			return fmt.Errorf("--color-scheme must be light or dark")
		}
		c, err := e.newClient()
		if err != nil {
			return err
		}
		return c.ChatBrief(e.ctx(), e.scopeProject(), e.scopeTenant(), target, locale, scheme, e.out)
	}}
	cmd.Flags().StringVar(&target, "target", "bubble", "widget presentation: bubble or page")
	cmd.Flags().StringVar(&locale, "locale", "", "widget locale override: en, nl, or fr")
	cmd.Flags().StringVar(&scheme, "color-scheme", "", "widget color scheme override: light or dark")
	return cmd
}

func chatSecretCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "secret", Short: "Manage the dedicated chat signing secret"}
	for _, action := range []string{"rotate", "reveal"} {
		action := action
		label := strings.ToUpper(action[:1]) + action[1:]
		cmd.AddCommand(&cobra.Command{Use: action, Short: label + " the chat signing secret (printed once)", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			raw, err := c.ChatRaw(e.ctx(), http.MethodPost, e.scopeProject(), "/secret/"+action, map[string]any{})
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			var out struct {
				Secret string `json:"secret"`
			}
			if err := json.Unmarshal(raw, &out); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(e.out, out.Secret)
			return nil
		}})
	}
	return cmd
}

func chatTokenCmd(e *env) *cobra.Command {
	var origin, kind, principalID string
	cmd := &cobra.Command{Use: "token", Short: "Mint a five-minute server chat token", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
		c, err := e.newClient()
		if err != nil {
			return err
		}
		body := map[string]any{"origin": origin, "principal_kind": kind, "external_id": principalID}
		if tenant := e.scopeTenant(); tenant != "" {
			body["tenant"] = tenant
		}
		raw, err := c.ChatRaw(e.ctx(), http.MethodPost, e.scopeProject(), "/token", body)
		if err != nil {
			return err
		}
		if e.jsonOut() {
			return render.JSON(e.out, raw)
		}
		var out struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(e.out, out.Token)
		return nil
	}}
	cmd.Flags().StringVar(&origin, "origin", "", "exact embedding-page origin")
	cmd.Flags().StringVar(&kind, "principal-kind", "", "declared principal kind")
	cmd.Flags().StringVar(&principalID, "principal-id", "", "principal external ID")
	_ = cmd.MarkFlagRequired("origin")
	return cmd
}

func chatSendCmd(e *env) *cobra.Command {
	var token, origin string
	cmd := &cobra.Command{Use: "send <message>", Short: "Open an embed session and print one turn's SSE frames", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		if token == "" {
			token = os.Getenv("RC_CHAT_TOKEN")
		}
		if token == "" {
			return fmt.Errorf("--token or RC_CHAT_TOKEN is required")
		}
		if origin == "" {
			origin = jwtStringClaim(token, "origin")
		}
		if origin == "" {
			return fmt.Errorf("--origin is required when the token origin cannot be decoded")
		}
		project := e.scopeProject()
		if project == "" {
			project = jwtStringClaim(token, "iss")
		}
		if project == "" {
			return fmt.Errorf("--project is required when the token issuer cannot be decoded")
		}
		c, err := e.newClient()
		if err != nil {
			return err
		}
		sessionID, err := c.ChatOpen(e.ctx(), project, origin, token)
		if err != nil {
			return err
		}
		return c.ChatSend(e.ctx(), project, origin, token, sessionID, randomMessageID(), args[0], e.out)
	}}
	cmd.Flags().StringVar(&token, "token", "", "embed chat token (default: RC_CHAT_TOKEN)")
	cmd.Flags().StringVar(&origin, "origin", "", "embedding-page origin (default: token origin claim)")
	return cmd
}

type chatDoctorFinding struct {
	Status string `json:"status"`
	Check  string `json:"check"`
	Code   string `json:"code"`
	Hint   string `json:"hint"`
	Docs   string `json:"docs"`
}

type doctorBundle struct {
	Project    string              `json:"project"`
	RCVersion  string              `json:"rc_version"`
	Timestamp  time.Time           `json:"timestamp"`
	Config     map[string]any      `json:"config"`
	Principals map[string]any      `json:"principals"`
	Secret     map[string]any      `json:"secret"`
	Branding   map[string]any      `json:"branding"`
	Rejects    []doctorReject      `json:"rejects"`
	Probes     map[string]any      `json:"probes"`
	Findings   []chatDoctorFinding `json:"findings"`
}

type doctorReject struct {
	Code      string    `json:"code"`
	Origin    string    `json:"origin,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

func chatDoctorCmd(e *env, version string) *cobra.Command {
	var origin, kind string
	var bundle bool
	cmd := &cobra.Command{Use: "doctor", Short: "Diagnose embedded-chat configuration and recent rejects", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
		c, err := e.newClient()
		if err != nil {
			return err
		}
		project := e.scopeProject()
		b := doctorBundle{Project: project, RCVersion: version, Timestamp: time.Now().UTC(), Config: map[string]any{}, Principals: map[string]any{}, Secret: map[string]any{}, Branding: map[string]any{}, Probes: map[string]any{}}
		decodeDoctor := func(raw json.RawMessage, dst any) { _ = json.Unmarshal(raw, dst) }
		raw, err := c.ChatRaw(e.ctx(), http.MethodGet, project, "", nil)
		if err != nil {
			return err
		}
		decodeDoctor(raw, &b.Config)
		raw, err = c.PrincipalsRaw(e.ctx(), http.MethodGet, project, nil)
		if err != nil {
			return err
		}
		var principalManifest map[string]any
		decodeDoctor(raw, &principalManifest)
		kinds := principalKinds(principalManifest)
		b.Principals = map[string]any{
			"configured":              len(kinds) > 0,
			"kinds":                   kinds,
			"email_lookup_configured": principalManifest["email_lookup"] != nil,
		}
		raw, err = c.ChatRaw(e.ctx(), http.MethodGet, project, "/secret", nil)
		if err != nil {
			return err
		}
		decodeDoctor(raw, &b.Secret)
		// Branding is a diagnostic input, not a precondition: a doctor that aborts on it reports nothing
		// about the chat wiring the operator actually came to check.
		raw, brandingErr := c.Raw(e.ctx(), http.MethodGet, bagPath("/api/v1/branding", project), nil)
		var branding map[string]any
		if brandingErr == nil {
			decodeDoctor(raw, &branding)
		}
		b.Branding = map[string]any{
			"reachable":                brandingErr == nil,
			"name_configured":          bagString(branding, "name") != "",
			"primary_color_configured": bagString(branding, "primary_color") != "",
		}
		raw, err = c.ChatRaw(e.ctx(), http.MethodGet, project, "/rejects?limit=20", nil)
		if err != nil {
			return err
		}
		var rejects struct {
			Rejects []doctorReject `json:"rejects"`
		}
		decodeDoctor(raw, &rejects)
		b.Rejects = rejects.Rejects
		loaderStatus, probeErr := c.ProbeWidgetLoader(e.ctx())
		b.Probes["widget_loader_status"] = loaderStatus
		if probeErr != nil {
			b.Probes["widget_loader_error"] = "network error"
		}

		add := func(ok bool, good, bad, hint string) {
			status, code := "ok", "OK"
			check := good
			if !ok {
				status, code = "failed", bad
				check = bad
			}
			// A passing check has no anchor in the integrator error index and no fix to suggest.
			docs := ""
			if ok {
				hint = ""
			} else {
				docs = errorDocsBase + strings.ToLower(code)
			}
			b.Findings = append(b.Findings, chatDoctorFinding{Status: status, Check: check, Code: code, Hint: hint, Docs: docs})
		}
		warn := func(code, hint string) {
			b.Findings = append(b.Findings, chatDoctorFinding{Status: "warning", Check: code, Code: code, Hint: hint, Docs: errorDocsBase + strings.ToLower(code)})
		}
		add(bagBool(b.Config, "chat_enabled"), "CHAT_ENABLED", "CHAT_DISABLED", "Enable chat for this project.")
		origins := bagStrings(b.Config, "chat_origins")
		if origin != "" {
			add(containsCLI(origins, origin), "ORIGIN_ALLOWED", "ORIGIN_NOT_ALLOWED", "Add this exact origin to chat_origins.")
		} else {
			add(len(origins) > 0, "ORIGINS_CONFIGURED", "ORIGIN_NOT_ALLOWED", "Configure at least one exact chat origin.")
		}
		source, _ := b.Secret["source"].(string)
		if source == "dedicated" {
			add(true, "CHAT_SECRET_PRESENT", "", "Dedicated chat signing secret is configured.")
		} else {
			warn("CHAT_SECRET_FALLBACK", "Rotate a dedicated chat secret; webhook fallback is temporary.")
		}
		if kind != "" {
			add(containsCLI(kinds, kind), "PRINCIPAL_KIND_DECLARED", "UNKNOWN_PRINCIPAL_KIND", "Add the requested kind to the principal manifest.")
		} else {
			if len(kinds) > 0 {
				add(true, "PRINCIPALS_CONFIGURED", "", "Principal manifest is configured.")
			} else {
				warn("PRINCIPALS_DORMANT", "Declare principal kinds when chat must be user-scoped.")
			}
		}
		add(brandingErr == nil, "BRANDING_REACHABLE", "BRANDING_UNAVAILABLE", "Check the branding API and project scope.")
		add(probeErr == nil && loaderStatus == http.StatusOK, "WIDGET_LOADER_REACHABLE", "WIDGET_SCRIPT_BLOCKED", "Allow the ReplyPen loader URL through network and CSP policy.")

		if bundle || e.jsonOut() {
			encoded, _ := json.Marshal(b)
			return render.JSON(e.out, encoded)
		}
		failed := false
		for _, f := range b.Findings {
			_, _ = fmt.Fprintf(e.out, "%-7s %-28s %s\n", f.Status, f.Check, f.Hint)
			if f.Status == "failed" {
				failed = true
			}
		}
		if failed {
			return &commandError{code: exitUsage, name: "CHAT_DOCTOR_FAILED", silent: true, message: "chat doctor found failures"}
		}
		return nil
	}}
	cmd.Flags().StringVar(&origin, "origin", "", "origin to check against the allowlist")
	cmd.Flags().StringVar(&kind, "principal-kind", "", "principal kind to check")
	cmd.Flags().BoolVar(&bundle, "bundle", false, "print a redacted JSON escalation bundle")
	return cmd
}

func newPrincipalsCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "principals", Short: "Read or replace the project principal manifest"}
	cmd.AddCommand(&cobra.Command{Use: "get", Short: "Show the principal manifest", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
		c, err := e.newClient()
		if err != nil {
			return err
		}
		raw, err := c.PrincipalsRaw(e.ctx(), http.MethodGet, e.scopeProject(), nil)
		if err != nil {
			return err
		}
		return render.JSON(e.out, raw)
	}})
	cmd.AddCommand(&cobra.Command{Use: "set <json-or-yaml-file>", Short: "Replace the validated principal manifest", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		data, err := readCLIFile(e.in, args[0])
		if err != nil {
			return err
		}
		var body map[string]any
		if json.Unmarshal(data, &body) != nil {
			if err := yaml.Unmarshal(data, &body); err != nil {
				return fmt.Errorf("decode principal manifest: %w", err)
			}
		}
		c, err := e.newClient()
		if err != nil {
			return err
		}
		raw, err := c.PrincipalsRaw(e.ctx(), http.MethodPatch, e.scopeProject(), body)
		if err != nil {
			return err
		}
		return render.JSON(e.out, raw)
	}})
	return cmd
}

func readCLIFile(stdin io.Reader, path string) ([]byte, error) {
	if path == "-" {
		if stdin == nil {
			stdin = os.Stdin
		}
		return io.ReadAll(stdin)
	}
	return os.ReadFile(path)
}

func jwtStringClaim(token, key string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims map[string]any
	if json.Unmarshal(raw, &claims) != nil {
		return ""
	}
	v, _ := claims[key].(string)
	return v
}

func randomMessageID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
func containsCLI(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
func bagBool(b map[string]any, key string) bool {
	f, _ := b[key].(map[string]any)
	v, _ := f["effective"].(bool)
	return v
}
func bagStrings(b map[string]any, key string) []string {
	f, _ := b[key].(map[string]any)
	raw, _ := f["effective"].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
func bagString(b map[string]any, key string) string {
	f, _ := b[key].(map[string]any)
	v, _ := f["effective"].(string)
	return v
}
func principalKinds(p map[string]any) []string {
	kinds, _ := p["kinds"].(map[string]any)
	out := make([]string, 0, len(kinds))
	for k := range kinds {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
