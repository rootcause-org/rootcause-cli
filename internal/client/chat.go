package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func chatProjectPath(project, suffix string) string {
	if project == "" {
		return "/api/v1/chat" + suffix
	}
	return "/api/v1/projects/" + url.PathEscape(project) + "/chat" + suffix
}

func principalsPath(project string) string {
	if project == "" {
		return "/api/v1/principals"
	}
	return "/api/v1/projects/" + url.PathEscape(project) + "/principals"
}

// One named method per chat control-plane endpoint: the path, verb and body shape stay in this
// package, and each returns the verbatim body (plus a typed view where there is one) so a command
// fetches ONCE and picks the shape by output mode.

// ChatSettings fetches the project's chat config bag (GET /chat).
func (c *Client) ChatSettings(ctx context.Context, project string) (*Settings, json.RawMessage, error) {
	return fetchBoth[Settings](ctx, c, http.MethodGet, chatProjectPath(project, ""), nil)
}

// ChatSecretAction runs POST /chat/secret/{rotate|reveal} — the secret is returned once, in the clear.
func (c *Client) ChatSecretAction(ctx context.Context, project, action string) (*ChatSecretResponse, json.RawMessage, error) {
	return fetchBoth[ChatSecretResponse](ctx, c, http.MethodPost, chatProjectPath(project, "/secret/"+url.PathEscape(action)), map[string]any{})
}

// ChatSecretStatus fetches GET /chat/secret — where the signing secret comes from (dedicated vs the
// webhook fallback), never the secret itself. Raw only: the diagnostic bundle carries it verbatim.
func (c *Client) ChatSecretStatus(ctx context.Context, project string) (json.RawMessage, error) {
	return c.Raw(ctx, http.MethodGet, chatProjectPath(project, "/secret"), nil)
}

// ChatToken mints a five-minute embed token (POST /chat/token).
func (c *Client) ChatToken(ctx context.Context, project string, req ChatTokenRequest) (*ChatTokenResponse, json.RawMessage, error) {
	return fetchBoth[ChatTokenResponse](ctx, c, http.MethodPost, chatProjectPath(project, "/token"), req)
}

// ChatRejects fetches GET /chat/rejects?limit= — why recent turns were refused. Raw only: the reject
// rows are server-owned and the doctor bundle passes them through untouched.
func (c *Client) ChatRejects(ctx context.Context, project string, limit int) (json.RawMessage, error) {
	suffix := "/rejects"
	if limit > 0 {
		suffix += "?limit=" + strconv.Itoa(limit)
	}
	return c.Raw(ctx, http.MethodGet, chatProjectPath(project, suffix), nil)
}

// ChatBrief streams the server-owned secret-free Markdown handoff.
func (c *Client) ChatBrief(ctx context.Context, project, tenant, target, locale, scheme string, out io.Writer) error {
	q := url.Values{}
	if tenant != "" {
		q.Set("tenant", tenant)
	}
	if target != "" {
		q.Set("target", target)
	}
	if locale != "" {
		q.Set("locale", locale)
	}
	if scheme != "" {
		q.Set("scheme", scheme)
	}
	path := chatProjectPath(project, "/brief")
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return c.Download(ctx, path, out)
}

// Principals fetches GET /principals — the project's principal manifest.
func (c *Client) Principals(ctx context.Context, project string) (*PrincipalManifest, json.RawMessage, error) {
	return fetchBoth[PrincipalManifest](ctx, c, http.MethodGet, principalsPath(project), nil)
}

// SetPrincipals replaces the manifest (PATCH /principals). The body is server-validated and freeform,
// so it rides through as given and the verbatim response comes back.
func (c *Client) SetPrincipals(ctx context.Context, project string, body map[string]any) (json.RawMessage, error) {
	return c.Raw(ctx, http.MethodPatch, principalsPath(project), body)
}

// ResolvePrincipal posts POST /principals/resolve — email (or pass-through external id) → the
// canonical principal external id.
func (c *Client) ResolvePrincipal(ctx context.Context, project string, req PrincipalResolveRequest) (*PrincipalResolveResponse, json.RawMessage, error) {
	return fetchBoth[PrincipalResolveResponse](ctx, c, http.MethodPost, principalsPath(project)+"/resolve", req)
}

func (c *Client) ProbeWidgetLoader(ctx context.Context) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/chat/widget/v1/loader.js", nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, &TransportError{Err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, nil
}

func (c *Client) ChatOpen(ctx context.Context, project, origin, token string) (string, error) {
	path := "/chat/v1/session?project=" + url.QueryEscape(project)
	data, err := c.fetch(ctx, embedSpec(http.MethodPost, path, origin, token, []byte(`{}`)))
	if err != nil {
		return "", err
	}
	var out struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("decode chat session: %w", err)
	}
	return out.SessionID, nil
}

func (c *Client) ChatSession(ctx context.Context, project, origin, token, sessionID string) (json.RawMessage, error) {
	path := "/chat/v1/session/" + url.PathEscape(sessionID) + "?project=" + url.QueryEscape(project)
	return c.fetch(ctx, embedSpec(http.MethodGet, path, origin, token, nil))
}

func (c *Client) ChatSend(ctx context.Context, project, origin, token, sessionID, messageID string, parts []map[string]any, out io.Writer) (string, error) {
	body, err := json.Marshal(map[string]any{"session_id": sessionID, "message": map[string]any{"id": messageID, "parts": parts}})
	if err != nil {
		return "", err
	}
	path := "/chat/v1/message?project=" + url.QueryEscape(project)
	spec := embedSpec(http.MethodPost, path, origin, token, body)
	spec.accept = "text/event-stream"
	resp, err := c.openStream(ctx, spec)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var runID, streamError string
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		_, _ = fmt.Fprintln(out, line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event struct {
			Type      string `json:"type"`
			MessageID string `json:"messageId"`
			ErrorText string `json:"errorText"`
			Code      string `json:"code"`
		}
		if json.Unmarshal([]byte(data), &event) != nil {
			continue
		}
		if event.Type == "start" {
			runID = event.MessageID
		}
		if event.Type == "error" {
			streamError = event.ErrorText
			if streamError == "" {
				streamError = event.Code
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return runID, &TransportError{Err: err}
	}
	if streamError != "" {
		return runID, fmt.Errorf("chat stream error: %s", streamError)
	}
	return runID, nil
}

// embedSpec is the transport spec for the chat embed plane: the credential is the widget/embed JWT
// rather than the OAuth bearer (a StaticToken, so the shared loop's 401-refresh step is a no-op), plus
// the origin header the server checks. Everything else — timeout, 429/5xx backoff on safe methods,
// verbatim error decoding — comes from the one transport loop.
func embedSpec(method, path, origin, token string, body []byte) sendSpec {
	return sendSpec{
		method:  method,
		path:    path,
		body:    body,
		headers: map[string]string{"X-RC-Embed-Origin": origin},
		tokens:  StaticToken(token),
	}
}
