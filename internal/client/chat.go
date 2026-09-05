package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

func (c *Client) ChatRaw(ctx context.Context, method, project, suffix string, body map[string]any) (json.RawMessage, error) {
	return c.Raw(ctx, method, chatProjectPath(project, suffix), body)
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

func (c *Client) PrincipalsRaw(ctx context.Context, method, project string, body map[string]any) (json.RawMessage, error) {
	return c.Raw(ctx, method, principalsPath(project), body)
}

func (c *Client) PrincipalResolveRaw(ctx context.Context, project string, body map[string]any) (json.RawMessage, error) {
	return c.Raw(ctx, http.MethodPost, principalsPath(project)+"/resolve", body)
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
