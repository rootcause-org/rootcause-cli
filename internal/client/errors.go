// This file carries the API's error envelope through to the user verbatim. The contract is that the
// server owns the vocabulary (codes, messages, field errors); the CLI must surface code+message
// exactly as sent and exit non-zero — it never paraphrases or invents an error. APIError is the typed
// carrier so the command layer can format INVALID_SETTINGS field lines without re-parsing.
package client

import (
	"fmt"
	"sort"
)

// FieldError is one entry in an INVALID_SETTINGS envelope: which key failed and why.
type FieldError struct {
	Key     string `json:"key"`
	Message string `json:"message"`
}

// errorEnvelope is the decode target for a non-2xx body: {"error":{code,message,fields}}.
type errorEnvelope struct {
	Error struct {
		Code    string       `json:"code"`
		Message string       `json:"message"`
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
