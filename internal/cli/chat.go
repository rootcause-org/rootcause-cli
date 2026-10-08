package cli

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

func newChatCmd(e *env, version string) *cobra.Command {
	cmd := &cobra.Command{Use: "chat", Short: "Configure, diagnose, and smoke-test embedded chat"}
	cmd.AddCommand(newBagGetCmd(e, "/api/v1/chat"), newBagSetCmd(e, "/api/v1/chat"), chatSecretCmd(e), chatTokenCmd(e), chatSendCmd(e), chatSessionCmd(e), chatCardDecideCmd(e), chatCardStatusCmd(e), chatDoctorCmd(e, version), chatBriefCmd(e))
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
			out, raw, err := c.ChatSecretAction(e.ctx(), e.scopeProject(), action)
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			_, _ = fmt.Fprintln(e.out, out.Secret)
			if out.RotatedBy != "" && out.RotatedAt != "" {
				_, _ = fmt.Fprintf(e.err, "Rotated by %s at %s\n", out.RotatedBy, out.RotatedAt)
			}
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
		out, raw, err := c.ChatToken(e.ctx(), e.scopeProject(), client.ChatTokenRequest{
			Origin: origin, PrincipalKind: kind, ExternalID: principalID, Tenant: e.scopeTenant(),
		})
		if err != nil {
			return err
		}
		if e.jsonOut() {
			return render.JSON(e.out, raw)
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

// chatLane is the --lane selector shared by `send` and `session`: "" keeps the embed plane (an embed
// token + origin), "setup"/"data" switch to the member dashboard plane over the OAuth bearer — the
// same conversation lists a logged-in member sees under "Set up & improve" / "Ask your data".
type chatLane struct {
	lane, intent, integration, action, tier string
}

func (l *chatLane) flags(cmd *cobra.Command, withOpen bool) {
	cmd.Flags().StringVar(&l.lane, "lane", "", "dashboard lane over the OAuth login instead of an embed token: setup (Set up & improve) or data (Ask your data)")
	if withOpen {
		cmd.Flags().StringVar(&l.intent, "intent", "", "seed the new --lane setup conversation like chat/new?intent=…: integration_setup, action_create, action_modify")
		cmd.Flags().StringVar(&l.integration, "integration", "", "connector key for --intent integration_setup (e.g. clickup)")
		cmd.Flags().StringVar(&l.action, "action", "", "action slug for --intent action_modify")
		cmd.Flags().StringVar(&l.tier, "tier", "", "session tier for a new --lane conversation: standard (Quick) or pro (Deeper)")
	}
}

func (l chatLane) dashboard() bool { return l.lane != "" }

func (l chatLane) validate() error {
	switch l.lane {
	case "", "setup", "data":
	default:
		return fmt.Errorf("--lane must be setup or data")
	}
	if l.intent != "" && l.lane != "setup" {
		return fmt.Errorf("--intent requires --lane setup")
	}
	if l.tier != "" && l.tier != "standard" && l.tier != "pro" {
		return fmt.Errorf("--tier must be standard or pro")
	}
	return nil
}

// open starts a dashboard conversation: an intent-seeded setup session, or a plain one in the lane.
func (l chatLane) open(e *env, c *client.Client, project, tenant string) (string, error) {
	if l.intent == "" {
		return c.DashboardChatOpen(e.ctx(), project, tenant, l.lane, l.tier)
	}
	params := url.Values{}
	if l.integration != "" {
		params.Set("integration", l.integration)
	}
	if l.action != "" {
		params.Set("action", l.action)
	}
	return c.DashboardChatOpenIntent(e.ctx(), project, tenant, l.intent, params)
}

func chatSendCmd(e *env) *cobra.Command {
	var token, origin, sessionID string
	var answerFlags []string
	var lane chatLane
	cmd := &cobra.Command{Use: "send [message]", Short: "Send one chat turn and print its SSE frames and session + run IDs", Args: cobra.MaximumNArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		if err := lane.validate(); err != nil {
			return err
		}
		if len(args) == 0 && len(answerFlags) == 0 {
			return fmt.Errorf("a message or at least one --answer key=value is required")
		}
		if sessionID == "" && len(answerFlags) > 0 {
			return fmt.Errorf("--session is required with --answer")
		}
		if lane.dashboard() {
			return chatSendDashboard(e, lane, sessionID, args, answerFlags)
		}
		token, origin, project, err := chatEmbedScope(e, token, origin)
		if err != nil {
			return err
		}
		c, err := e.newClient()
		if err != nil {
			return err
		}
		if sessionID == "" {
			sessionID, err = c.ChatOpen(e.ctx(), project, origin, token)
			if err != nil {
				return err
			}
		}
		parts, err := chatTurnParts(args, answerFlags, func() (json.RawMessage, error) {
			return c.ChatSession(e.ctx(), project, origin, token, sessionID)
		})
		if err != nil {
			return err
		}
		runID, sendErr := c.ChatSend(e.ctx(), project, origin, token, sessionID, randomMessageID(), parts, e.out)
		printChatHandles(e, sessionID, runID)
		return sendErr
	}}
	cmd.Flags().StringVar(&token, "token", "", "embed chat token (default: RC_CHAT_TOKEN)")
	cmd.Flags().StringVar(&origin, "origin", "", "embedding-page origin (default: token origin claim)")
	cmd.Flags().StringVar(&sessionID, "session", "", "existing chat session ID (opens a new session when omitted)")
	cmd.Flags().StringArrayVar(&answerFlags, "answer", nil, "answer the latest data question as key=value (repeat for multiple answers)")
	lane.flags(cmd, true)
	return cmd
}

// chatSendDashboard is the --lane branch of `send`: the OAuth login + the brain/--project/--tenant
// scope pick the conversation list, exactly like the logged-in page.
func chatSendDashboard(e *env, lane chatLane, sessionID string, args, answerFlags []string) error {
	c, err := e.newClient()
	if err != nil {
		return err
	}
	project, tenant := e.scopeProject(), e.scopeTenant()
	if project == "" {
		return fmt.Errorf("--lane needs a project: run inside a brain checkout or pass --project")
	}
	if sessionID == "" {
		sessionID, err = lane.open(e, c, project, tenant)
		if err != nil {
			return err
		}
	} else if lane.intent != "" {
		return fmt.Errorf("--intent opens a new conversation; drop --session")
	}
	parts, err := chatTurnParts(args, answerFlags, func() (json.RawMessage, error) {
		return c.DashboardChatSession(e.ctx(), project, tenant, sessionID)
	})
	if err != nil {
		return err
	}
	runID, sendErr := c.DashboardChatSend(e.ctx(), project, tenant, sessionID, randomMessageID(), lane.tier, parts, e.out)
	printChatHandles(e, sessionID, runID)
	return sendErr
}

// chatTurnParts assembles the typed parts of one turn: the text, plus a data-answers part built
// against the session's latest unanswered question set when --answer is given.
func chatTurnParts(args, answerFlags []string, transcript func() (json.RawMessage, error)) ([]map[string]any, error) {
	parts := make([]map[string]any, 0, 2)
	if len(args) == 1 && strings.TrimSpace(args[0]) != "" {
		parts = append(parts, map[string]any{"type": "text", "text": args[0]})
	}
	if len(answerFlags) > 0 {
		raw, err := transcript()
		if err != nil {
			return nil, err
		}
		answerPart, err := chatAnswerPart(raw, answerFlags)
		if err != nil {
			return nil, err
		}
		parts = append(parts, answerPart)
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("message must not be empty")
	}
	return parts, nil
}

// printChatHandles prints the session id (the handle for every follow-up: `--session`, `rc project
// chat session`, the card decision routes — the frames never carry it) beside the run id.
func printChatHandles(e *env, sessionID, runID string) {
	_, _ = fmt.Fprintf(e.out, "session_id: %s\n", sessionID)
	if runID != "" {
		_, _ = fmt.Fprintf(e.out, "run_id: %s\n", runID)
	}
}

// chatEmbedScope resolves the embed-plane triple every chat verb needs: the bearer (flag or
// RC_CHAT_TOKEN), the exact origin and the project, the latter two decoded from the token's own claims
// when not given — the token was minted for exactly one origin and one project.
func chatEmbedScope(e *env, token, origin string) (string, string, string, error) {
	if token == "" {
		token = os.Getenv("RC_CHAT_TOKEN")
	}
	if token == "" {
		return "", "", "", fmt.Errorf("--token or RC_CHAT_TOKEN is required")
	}
	if origin == "" {
		origin = jwtStringClaim(token, "origin")
	}
	if origin == "" {
		return "", "", "", fmt.Errorf("--origin is required when the token origin cannot be decoded")
	}
	project := e.scopeProject()
	if project == "" {
		project = jwtStringClaim(token, "iss")
	}
	if project == "" {
		return "", "", "", fmt.Errorf("--project is required when the token issuer cannot be decoded")
	}
	return token, origin, project, nil
}

// chatSessionCmd re-reads a session the way the widget does on reopen: the persisted transcript with
// every card hydrated to its current lifecycle state. Distinct from the live `send` stream — this is
// the read-after-park path (PII resolved from the sealed session vault, not the live container).
func chatSessionCmd(e *env) *cobra.Command {
	var token, origin string
	var lane chatLane
	cmd := &cobra.Command{Use: "session <id>", Short: "Print a session's persisted transcript as the widget would reopen it", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		if err := lane.validate(); err != nil {
			return err
		}
		if lane.dashboard() {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			project := e.scopeProject()
			if project == "" {
				return fmt.Errorf("--lane needs a project: run inside a brain checkout or pass --project")
			}
			raw, err := c.DashboardChatSession(e.ctx(), project, e.scopeTenant(), strings.TrimSpace(args[0]))
			if err != nil {
				return err
			}
			return render.JSON(e.out, raw)
		}
		token, origin, project, err := chatEmbedScope(e, token, origin)
		if err != nil {
			return err
		}
		c, err := e.newClient()
		if err != nil {
			return err
		}
		raw, err := c.ChatSession(e.ctx(), project, origin, token, strings.TrimSpace(args[0]))
		if err != nil {
			return err
		}
		return render.JSON(e.out, raw)
	}}
	cmd.Flags().StringVar(&token, "token", "", "embed chat token (default: RC_CHAT_TOKEN)")
	cmd.Flags().StringVar(&origin, "origin", "", "embedding-page origin (default: token origin claim)")
	lane.flags(cmd, false)
	return cmd
}

// chatCardTarget is what `decide` and `card` share: the plane (embed token or --lane), the card route
// (--card, verbatim) and the session + card ids. The CLI knows no per-card fields; the server validates
// the route and the outcome and returns the card projection, printed as JSON.
type chatCardTarget struct {
	token, origin, card string
	lane                chatLane
}

func (t *chatCardTarget) flags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&t.token, "token", "", "embed chat token (default: RC_CHAT_TOKEN)")
	cmd.Flags().StringVar(&t.origin, "origin", "", "embedding-page origin (default: token origin claim)")
	cmd.Flags().StringVar(&t.card, "card", "website-changes", "card route: website-changes, actions, memories, brain-changes, outbound-batches")
	t.lane.flags(cmd, false)
}

// run resolves the plane and hands the matching call to embed or dashboard, then prints the body.
func (t *chatCardTarget) run(e *env,
	embed func(c *client.Client, project, origin, token string) (json.RawMessage, error),
	dashboard func(c *client.Client, project, tenant string) (json.RawMessage, error),
) error {
	if err := t.lane.validate(); err != nil {
		return err
	}
	var raw json.RawMessage
	if t.lane.dashboard() {
		project := e.scopeProject()
		if project == "" {
			return fmt.Errorf("--lane needs a project: run inside a brain checkout or pass --project")
		}
		c, err := e.newClient()
		if err != nil {
			return err
		}
		if raw, err = dashboard(c, project, e.scopeTenant()); err != nil {
			return err
		}
	} else {
		token, origin, project, err := chatEmbedScope(e, t.token, t.origin)
		if err != nil {
			return err
		}
		c, err := e.newClient()
		if err != nil {
			return err
		}
		if raw, err = embed(c, project, origin, token); err != nil {
			return err
		}
	}
	return render.JSON(e.out, raw)
}

// chatCardDecideCmd settles a chat card the way its widget button does (e.g. publish a website change).
func chatCardDecideCmd(e *env) *cobra.Command {
	var t chatCardTarget
	cmd := &cobra.Command{Use: "decide <session-id> <card-id> <outcome>", Short: "Settle a chat card (e.g. publish or decline a website change) and print its projection", Args: cobra.ExactArgs(3), RunE: func(_ *cobra.Command, args []string) error {
		sessionID, cardID, outcome := strings.TrimSpace(args[0]), strings.TrimSpace(args[1]), strings.TrimSpace(args[2])
		return t.run(e, func(c *client.Client, project, origin, token string) (json.RawMessage, error) {
			return c.ChatCardDecide(e.ctx(), project, origin, token, sessionID, t.card, cardID, outcome)
		}, func(c *client.Client, project, tenant string) (json.RawMessage, error) {
			return c.DashboardChatCardDecide(e.ctx(), project, tenant, sessionID, t.card, cardID, outcome)
		})
	}}
	t.flags(cmd)
	return cmd
}

// chatCardStatusCmd reads a card's current projection — what the widget polls (e.g. while a website
// change builds).
func chatCardStatusCmd(e *env) *cobra.Command {
	var t chatCardTarget
	cmd := &cobra.Command{Use: "card <session-id> <card-id>", Short: "Print a chat card's current projection (poll it while it settles)", Args: cobra.ExactArgs(2), RunE: func(_ *cobra.Command, args []string) error {
		sessionID, cardID := strings.TrimSpace(args[0]), strings.TrimSpace(args[1])
		return t.run(e, func(c *client.Client, project, origin, token string) (json.RawMessage, error) {
			return c.ChatCardStatus(e.ctx(), project, origin, token, sessionID, t.card, cardID)
		}, func(c *client.Client, project, tenant string) (json.RawMessage, error) {
			return c.DashboardChatCardStatus(e.ctx(), project, tenant, sessionID, t.card, cardID)
		})
	}}
	t.flags(cmd)
	return cmd
}

type chatQuestion struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

func chatAnswerPart(raw json.RawMessage, flags []string) (map[string]any, error) {
	var session struct {
		Messages []struct {
			Parts []json.RawMessage `json:"parts"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &session); err != nil {
		return nil, fmt.Errorf("decode chat session: %w", err)
	}
	answered := map[string]bool{}
	for _, message := range session.Messages {
		for _, rawPart := range message.Parts {
			var part struct {
				Type string `json:"type"`
				Data struct {
					QuestionSetID string `json:"question_set_id"`
				} `json:"data"`
			}
			if json.Unmarshal(rawPart, &part) == nil && part.Type == "data-answers" && part.Data.QuestionSetID != "" {
				answered[part.Data.QuestionSetID] = true
			}
		}
	}
	var questionSetID string
	var questions []chatQuestion
	for mi := len(session.Messages) - 1; mi >= 0 && questionSetID == ""; mi-- {
		parts := session.Messages[mi].Parts
		for pi := len(parts) - 1; pi >= 0; pi-- {
			var part struct {
				Type string `json:"type"`
				Data struct {
					QuestionSetID string         `json:"question_set_id"`
					Questions     []chatQuestion `json:"questions"`
				} `json:"data"`
			}
			if json.Unmarshal(parts[pi], &part) == nil && part.Type == "data-questions" && part.Data.QuestionSetID != "" && !answered[part.Data.QuestionSetID] {
				questionSetID, questions = part.Data.QuestionSetID, part.Data.Questions
				break
			}
		}
	}
	if questionSetID == "" {
		return nil, fmt.Errorf("session has no unanswered data question")
	}

	values := map[string][]string{}
	for _, flag := range flags {
		key, value, ok := strings.Cut(flag, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || key == "" || value == "" {
			return nil, fmt.Errorf("--answer must be key=value")
		}
		values[key] = append(values[key], value)
	}
	byID := make(map[string]chatQuestion, len(questions))
	for _, q := range questions {
		byID[q.ID] = q
	}
	answers := map[string]any{}
	for id, selections := range values {
		q, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("unknown answer key %q", id)
		}
		switch q.Kind {
		case "single_select":
			if len(selections) != 1 {
				return nil, fmt.Errorf("answer %q accepts one value", id)
			}
			answers[id] = map[string]any{"value": selections[0]}
		case "multi_select":
			answers[id] = map[string]any{"values": selections}
		case "free_text":
			if len(selections) != 1 {
				return nil, fmt.Errorf("answer %q accepts one value", id)
			}
			answers[id] = map[string]any{"text": selections[0]}
		default:
			return nil, fmt.Errorf("question %q has unsupported kind %q", id, q.Kind)
		}
	}
	return map[string]any{"type": "data-answers", "data": map[string]any{"question_set_id": questionSetID, "answers": answers}}, nil
}

// doctorRejectLimit is how many recent rejects the doctor pulls — enough to see a pattern in the
// window, small enough to stay one page.
const doctorRejectLimit = 100

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
	Since      string              `json:"since"`
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
	Kind      string    `json:"kind,omitempty"`
	Origin    string    `json:"origin,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	Stale     bool      `json:"stale,omitempty"`
}

func chatDoctorCmd(e *env, version string) *cobra.Command {
	var origin, kind, since string
	var bundle bool
	cmd := &cobra.Command{Use: "doctor", Short: "Diagnose embedded-chat configuration and recent rejects", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
		if !containsCLI([]string{"30m", "1h", "24h"}, since) {
			return fmt.Errorf("--since must be 30m, 1h, or 24h")
		}
		sinceDuration, _ := time.ParseDuration(since)
		c, err := e.newClient()
		if err != nil {
			return err
		}
		project := e.scopeProject()
		b := doctorBundle{Project: project, RCVersion: version, Timestamp: time.Now().UTC(), Since: since, Config: map[string]any{}, Principals: map[string]any{}, Secret: map[string]any{}, Branding: map[string]any{}, Probes: map[string]any{}}
		decodeDoctor := func(raw json.RawMessage, dst any) { _ = json.Unmarshal(raw, dst) }
		// The bundle carries the bag verbatim (raw passthrough); the checks read the SAME bytes through
		// the typed settings shape, so a field's Effective-then-Value ladder is decoded in one place.
		settings, raw, err := c.ChatSettings(e.ctx(), project)
		if err != nil {
			return err
		}
		decodeDoctor(raw, &b.Config)
		config := *settings
		manifest, _, err := c.Principals(e.ctx(), project)
		if err != nil {
			return err
		}
		kinds := manifest.KindNames()
		b.Principals = map[string]any{
			"configured":              len(kinds) > 0,
			"kinds":                   kinds,
			"email_lookup_configured": manifest.HasEmailLookup(),
		}
		raw, err = c.ChatSecretStatus(e.ctx(), project)
		if err != nil {
			return err
		}
		decodeDoctor(raw, &b.Secret)
		// Branding is a diagnostic input, not a precondition: a doctor that aborts on it reports nothing
		// about the chat wiring the operator actually came to check.
		brandingBag, _, brandingErr := c.GetBag(e.ctx(), "/api/v1/branding", project)
		var branding client.Settings
		if brandingErr == nil {
			branding = *brandingBag
		}
		b.Branding = map[string]any{
			"reachable":                brandingErr == nil,
			"name_configured":          settingString(branding, "name") != "",
			"primary_color_configured": settingString(branding, "primary_color") != "",
		}
		raw, err = c.ChatRejects(e.ctx(), project, doctorRejectLimit)
		if err != nil {
			return err
		}
		var rejects struct {
			Rejects []doctorReject `json:"rejects"`
		}
		decodeDoctor(raw, &rejects)
		cutoff := b.Timestamp.Add(-sinceDuration)
		for i := range rejects.Rejects {
			rejects.Rejects[i].Stale = rejects.Rejects[i].Timestamp.Before(cutoff)
		}
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
				docs = embassyDocsFor(code)
			}
			b.Findings = append(b.Findings, chatDoctorFinding{Status: status, Check: check, Code: code, Hint: hint, Docs: docs})
		}
		warn := func(code, hint string) {
			b.Findings = append(b.Findings, chatDoctorFinding{Status: "warning", Check: code, Code: code, Hint: hint, Docs: embassyDocsFor(code)})
		}
		add(settingBool(config, "chat_enabled"), "CHAT_ENABLED", "CHAT_DISABLED", "Enable chat for this project.")
		origins := settingStrings(config, "chat_origins")
		if origin != "" {
			add(containsCLI(origins, origin), "ORIGIN_ALLOWED", "ORIGIN_NOT_ALLOWED", "Add this exact origin to chat_origins.")
		} else {
			add(len(origins) > 0, "ORIGINS_CONFIGURED", "ORIGIN_NOT_ALLOWED", "Configure at least one exact chat origin.")
		}
		for _, o := range unresolvableOrigins(e.ctx(), origins, net.DefaultResolver.LookupHost) {
			warn("ORIGIN_UNRESOLVABLE", fmt.Sprintf("chat_origins entry %s has no DNS record — likely a typo of the real app host.", o))
		}
		if origin != "" && !containsCLI(origins, origin) {
			if near, ok := nearestOrigin(origin, origins); ok {
				warn("ORIGIN_NEAR_MISS", fmt.Sprintf("%s is not registered, but %s is — one of them is probably a typo.", origin, near))
			}
		}
		for rejected, near := range nearMissRejects(b.Rejects, origins) {
			if rejected == origin {
				continue
			}
			warn("ORIGIN_NEAR_MISS", fmt.Sprintf("recent rejects came from %s, which is not registered; %s is — one of them is probably a typo.", rejected, near))
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
		// A run-time principal failure is invisible in CONFIG: the manifest validates, the token mints, and
		// only the recent rejects show that the asserted identity never verified against the project's data.
		if code, principalKind, n := recentPrincipalRejects(b.Rejects); n > 0 {
			scope := ""
			if principalKind != "" {
				scope = " for principal kind " + principalKind
			}
			warn(code, fmt.Sprintf("last %d turn(s)%s failed principal verification — check the asserted external ID against the manifest's verify query.", n, scope))
		}
		add(brandingErr == nil, "BRANDING_REACHABLE", "BRANDING_UNAVAILABLE", "Check the branding API and project scope.")
		add(probeErr == nil && loaderStatus == http.StatusOK, "WIDGET_LOADER_REACHABLE", "WIDGET_SCRIPT_BLOCKED", "Allow the ReplyPen loader URL through network and CSP policy.")

		// The verdict is a property of the findings, not of the output mode: fold it BEFORE the mode
		// branch so a piped `rc project chat doctor | jq` exits non-zero on broken wiring too.
		failed := false
		for _, f := range b.Findings {
			if f.Status == "failed" {
				failed = true
				break
			}
		}
		if bundle || e.jsonOut() {
			encoded, marshalErr := json.Marshal(b)
			if marshalErr != nil {
				return marshalErr
			}
			if err := render.JSON(e.out, encoded); err != nil {
				return err
			}
		} else {
			for _, f := range b.Findings {
				_, _ = fmt.Fprintf(e.out, "%-7s %-28s %s\n", f.Status, f.Check, f.Hint)
			}
			for _, reject := range b.Rejects {
				state := "recent"
				if reject.Stale {
					state = "stale"
				}
				_, _ = fmt.Fprintf(e.out, "%-7s %-28s %s\n", state, reject.Code, reject.Timestamp.Format(time.RFC3339))
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
	cmd.Flags().StringVar(&since, "since", "24h", "reject warning window: 30m, 1h, or 24h")
	return cmd
}

func newPrincipalsCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "principals", Short: "Read or replace the project principal manifest"}
	cmd.AddCommand(&cobra.Command{Use: "get", Short: "Show the principal manifest", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
		c, err := e.newClient()
		if err != nil {
			return err
		}
		_, raw, err := c.Principals(e.ctx(), e.scopeProject())
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
		raw, err := c.SetPrincipals(e.ctx(), e.scopeProject(), body)
		if err != nil {
			return err
		}
		return render.JSON(e.out, raw)
	}})
	var kind, email, externalID string
	resolve := &cobra.Command{Use: "resolve", Short: "Resolve an email or pass through an external principal ID", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
		if strings.TrimSpace(kind) == "" {
			return fmt.Errorf("--kind is required")
		}
		if (strings.TrimSpace(email) == "") == (strings.TrimSpace(externalID) == "") {
			return fmt.Errorf("exactly one of --email or --external-id is required")
		}
		c, err := e.newClient()
		if err != nil {
			return err
		}
		out, raw, err := c.ResolvePrincipal(e.ctx(), e.scopeProject(), client.PrincipalResolveRequest{
			Kind: kind, Email: email, ExternalID: externalID, Tenant: e.scopeTenant(),
		})
		if err != nil {
			return err
		}
		if e.jsonOut() {
			return render.JSON(e.out, raw)
		}
		_, _ = fmt.Fprintln(e.out, out.ExternalID)
		return nil
	}}
	resolve.Flags().StringVar(&kind, "kind", "", "declared principal kind")
	resolve.Flags().StringVar(&email, "email", "", "authenticated user's email")
	resolve.Flags().StringVar(&externalID, "external-id", "", "already-canonical principal external ID")
	cmd.AddCommand(resolve)
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

// recentPrincipalRejects returns the DOMINANT (code, principal kind) pair in the recent rejects window
// and its own count — not the total across codes — so the doctor names the ONE failure an integrator can
// act on, with the kind whose verify query to check. Ties break on code then kind for determinism (map
// iteration order is random).
func recentPrincipalRejects(rejects []doctorReject) (string, string, int) {
	type failure struct{ code, kind string }
	counts := map[failure]int{}
	for _, r := range rejects {
		if r.Stale {
			continue
		}
		switch r.Code {
		case "PRINCIPAL_UNVERIFIED", "PRINCIPAL_LOOKUP_FAILED":
			counts[failure{code: r.Code, kind: r.Kind}]++
		}
	}
	best, total := failure{}, 0
	for f, n := range counts {
		if total == 0 || n > total || (n == total && (f.code < best.code || (f.code == best.code && f.kind < best.kind))) {
			best, total = f, n
		}
	}
	return best.code, best.kind, total
}
