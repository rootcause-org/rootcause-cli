package client

import (
	"bufio"
	"bytes"
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
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, nil
}

func (c *Client) ChatOpen(ctx context.Context, project, origin, token string) (string, error) {
	path := "/chat/v1/session?project=" + url.QueryEscape(project)
	resp, data, err := c.embedRequest(ctx, http.MethodPost, path, origin, token, []byte(`{}`))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", decodeAPIError(resp.StatusCode, http.MethodPost, path, c.baseURL, data)
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
	resp, data, err := c.embedRequest(ctx, http.MethodGet, path, origin, token, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeAPIError(resp.StatusCode, http.MethodGet, path, c.baseURL, data)
	}
	return data, nil
}

func (c *Client) ChatSend(ctx context.Context, project, origin, token, sessionID, messageID string, parts []map[string]any, out io.Writer) (string, error) {
	body, err := json.Marshal(map[string]any{"session_id": sessionID, "message": map[string]any{"id": messageID, "parts": parts}})
	if err != nil {
		return "", err
	}
	path := "/chat/v1/message?project=" + url.QueryEscape(project)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-RC-Embed-Origin", origin)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", &TransportError{Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return "", decodeAPIError(resp.StatusCode, http.MethodPost, path, c.baseURL, data)
	}

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

func (c *Client) embedRequest(ctx context.Context, method, path, origin, token string, body []byte) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-RC-Embed-Origin", origin)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, &TransportError{Err: err}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, nil, &TransportError{Err: err}
	}
	return resp, data, nil
}
