package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

const (
	exitOK        = 0
	exitUsage     = 1
	exitAuth      = 2
	exitTruncated = 3
	exitRemote    = 4
	exitServer    = 5
)

type commandError struct {
	code    int
	name    string
	message string
	silent  bool
	err     error
}

func (e *commandError) Error() string {
	if e.message != "" {
		return e.message
	}
	return e.err.Error()
}

func (e *commandError) Unwrap() error { return e.err }

func truncationError(message string) error {
	return &commandError{code: exitTruncated, name: "TRUNCATED", message: message}
}

func authenticationError(message string) error {
	return &commandError{code: exitAuth, name: "AUTH", message: message}
}

func remoteExitError() error {
	return &commandError{code: exitRemote, name: "REMOTE_FAILED", silent: true, message: "remote command exited non-zero or timed out"}
}

func exitCodeFor(err error) int {
	if err == nil {
		return exitOK
	}
	var ce *commandError
	if errors.As(err, &ce) {
		return ce.code
	}
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		if apiErr.Status == http.StatusUnauthorized || apiErr.Status == http.StatusForbidden {
			return exitAuth
		}
		if apiErr.Status == http.StatusTooManyRequests || apiErr.Status >= 500 || apiErr.Status == 0 {
			return exitServer
		}
		return exitUsage
	}
	var transport *client.TransportError
	if errors.As(err, &transport) {
		return exitServer
	}
	return exitUsage
}

type jsonErrorEnvelope struct {
	Error jsonErrorBody `json:"error"`
}

type jsonErrorBody struct {
	Code    string              `json:"code"`
	Message string              `json:"message"`
	Status  int                 `json:"status"`
	Fields  []client.FieldError `json:"fields"`
	Hint    string              `json:"hint,omitempty"`
	Docs    string              `json:"docs,omitempty"`
}

func writeJSONError(w io.Writer, err error) error {
	body := jsonErrorBody{Code: "USAGE", Message: err.Error(), Fields: []client.FieldError{}}
	var ce *commandError
	var apiErr *client.APIError
	var transport *client.TransportError
	switch {
	case errors.As(err, &ce):
		body.Code = ce.name
	case errors.As(err, &apiErr):
		body.Code = apiErr.Code
		if body.Code == "" {
			body.Code = "HTTP_ERROR"
		}
		body.Message = apiErr.Message
		if body.Message == "" {
			body.Message = fmt.Sprintf("HTTP %d %s", apiErr.Status, http.StatusText(apiErr.Status))
		}
		body.Status = apiErr.Status
		body.Fields = apiErr.Fields
		body.Hint = apiErr.Hint
		body.Docs = apiErr.Docs
	case errors.As(err, &transport):
		body.Code = "NETWORK_ERROR"
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(jsonErrorEnvelope{Error: body})
}

// asAPIError unwraps err into *client.APIError if the chain contains one. A thin wrapper over
// errors.As so the call sites in this package read cleanly.
func asAPIError(err error, target **client.APIError) bool {
	return errors.As(err, target)
}
