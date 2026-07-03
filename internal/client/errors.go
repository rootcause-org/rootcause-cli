// This file carries the API's error envelope through to the user verbatim. The contract is that the
// server owns the vocabulary (codes, messages, field errors); the CLI must surface code+message
// exactly as sent and exit non-zero — it never paraphrases or invents an error. APIError is the typed
// carrier so the command layer can format INVALID_SETTINGS field lines without re-parsing.
package client

import (
	"encoding/json"
	"fmt"
	"sort"
)

// FieldError is one entry in an INVALID_SETTINGS envelope: which key failed and why.
type FieldError struct {
	Key     string `json:"key"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

// errorEnvelope is the decode target for a non-2xx body:
// {"error":{code,message,details}}. fields is kept for released servers.
type errorEnvelope struct {
	Error struct {
		Code    string       `json:"code"`
		Message string       `json:"message"`
		Details []FieldError `json:"details"`
		Fields  []FieldError `json:"fields"`
	} `json:"error"`
}

// validationFailedEnvelope is the SECOND non-2xx shape, used by the tenant-settings editing surface:
// {"error":"validation_failed","field_errors":{"<key>":"<msg>"}}. It differs from errorEnvelope (error
// is a STRING, not an object; per-field errors are a map, not an array), so the client tries it after
// the standard envelope fails to yield a code. Mapped onto the same APIError (Code/Fields) so the
// command layer renders it through the one verbatim-surfacing path.
type validationFailedEnvelope struct {
	Error       string            `json:"error"`
	FieldErrors map[string]string `json:"field_errors"`
}

// APIError carries the server's verbatim error so the command layer can print code/message to stderr
// and exit 1. A zero Code means we got a non-2xx with no decodable envelope (a plain-text 404/405 from
// a proxy or an older server that lacks the endpoint); the caller still treats it as a failure but
// renders it generically — Method/Path/BaseURL give the user enough to see WHAT was hit WHERE, which a
// bare "HTTP 405" doesn't.
type APIError struct {
	Status  int          // HTTP status, for the no-envelope fallback
	Code    string       // server error code, verbatim (e.g. INVALID_SETTINGS)
	Message string       // server message, verbatim
	Fields  []FieldError // populated for INVALID_SETTINGS
	Method  string       // request method, for the no-envelope fallback (e.g. GET)
	Path    string       // request path, for the no-envelope fallback (e.g. /api/v1/runs)
	BaseURL string       // base URL the request went to, so the user can spot a wrong/default host
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("HTTP %d", e.Status)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// decodeAPIError turns a non-2xx (status + verbatim body) into a typed APIError. It tries the standard
// {error:{code,message,details}} envelope first, then the tenant-settings {error,field_errors} shape,
// and finally falls back to a no-envelope error that still carries method/path/baseURL so a plain-text
// 404/405 (proxy, or an older server missing the endpoint) is diagnosable rather than a bare "HTTP N".
// Shared by the JSON path (do) and the multipart path (doRaw).
func decodeAPIError(status int, method, path, baseURL string, data []byte) *APIError {
	apiErr := &APIError{Status: status}
	var env errorEnvelope
	var vfe validationFailedEnvelope
	switch {
	case json.Unmarshal(data, &env) == nil && env.Error.Code != "":
		apiErr.Code = env.Error.Code
		apiErr.Message = env.Error.Message
		apiErr.Fields = normalizeFieldErrors(env.Error.Details)
		if len(apiErr.Fields) == 0 {
			apiErr.Fields = normalizeFieldErrors(env.Error.Fields)
		}
	case json.Unmarshal(data, &vfe) == nil && vfe.Error != "":
		apiErr.Code = vfe.Error
		apiErr.Message = "settings rejected"
		apiErr.Fields = sortedFieldErrors(vfe.FieldErrors)
	default:
		apiErr.Method = method
		apiErr.Path = pathOnly(path)
		apiErr.BaseURL = baseURL
	}
	return apiErr
}

func normalizeFieldErrors(in []FieldError) []FieldError {
	if len(in) == 0 {
		return nil
	}
	out := make([]FieldError, 0, len(in))
	for _, f := range in {
		if f.Key == "" {
			f.Key = f.Field
		}
		out = append(out, f)
	}
	return out
}

// sortedFieldErrors flattens the tenant-settings field_errors map into the []FieldError the command
// layer prints, sorted by key so the output is deterministic (map iteration order is not).
func sortedFieldErrors(m map[string]string) []FieldError {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]FieldError, 0, len(keys))
	for _, k := range keys {
		out = append(out, FieldError{Key: k, Message: m[k]})
	}
	return out
}
