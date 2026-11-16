package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	adapter "github.com/matcra587/jira-cli/internal/adapters/jira"
)

// ErrReadOnly classifies requests refused by the read-only transport policy.
var ErrReadOnly = adapter.ErrReadOnly

// ErrResponseTooLarge identifies an explicitly truncated capture.
var ErrResponseTooLarge = adapter.ErrAPIResponseTooLarge

// ErrResponseIncomplete identifies a failed body capture with retained bytes.
var ErrResponseIncomplete = adapter.ErrAPIResponseIncomplete

// Error provides a safe request-failure summary. The response returned alongside
// it retains status and captured bytes. Neither failure nor cancellation proves
// a write had no effect. Unwrapped causes may contain sensitive remote data.
type Error struct {
	Code       string        `json:"code"`
	StatusCode int           `json:"status_code,omitempty"`
	RetryAfter time.Duration `json:"retry_after,omitempty"`
	cause      error
}

func (e Error) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("jira request failed: %s (HTTP %d)", e.Code, e.StatusCode)
	}
	return "jira request failed: " + e.Code
}

// String keeps untrusted remote messages out of ordinary diagnostics.
func (e Error) String() string { return e.Error() }

// GoString keeps provider and transport diagnostics out of %#v output.
func (e Error) GoString() string { return e.Error() }

// MarshalJSON exposes reviewed summary fields only.
func (e Error) MarshalJSON() ([]byte, error) {
	type summary struct {
		Code       string        `json:"code"`
		StatusCode int           `json:"status_code,omitempty"`
		RetryAfter time.Duration `json:"retry_after,omitempty"`
	}
	return json.Marshal(summary{Code: e.Code, StatusCode: e.StatusCode, RetryAfter: e.RetryAfter})
}

// Unwrap preserves cancellation and typed cause inspection. Treat underlying
// diagnostic text as sensitive rather than forwarding it to logs or users.
func (e Error) Unwrap() error { return e.cause }

func requestError(cause error) *Error {
	failure := &Error{Code: "request_failed", cause: cause}
	remote, remoteFailure := errors.AsType[*adapter.APIError](cause)
	_, invalidInput := errors.AsType[*adapter.APIRequestValidationError](cause)
	switch {
	case errors.Is(cause, context.Canceled), errors.Is(cause, context.DeadlineExceeded):
		failure.Code = "canceled"
	case errors.Is(cause, ErrReadOnly):
		failure.Code = "read_only"
	case errors.Is(cause, ErrResponseTooLarge):
		failure.Code = "response_too_large"
	case errors.Is(cause, ErrResponseIncomplete):
		failure.Code = "response_incomplete"
	case invalidInput, errors.Is(cause, adapter.ErrUnsafePath):
		failure.Code = "invalid_request"
	case remoteFailure:
		failure.StatusCode = remote.StatusCode
		failure.RetryAfter = time.Duration(remote.RetryAfterSeconds) * time.Second
		switch remote.StatusCode {
		case 401:
			failure.Code = "authentication_required"
		case 403:
			failure.Code = "forbidden"
		case 429:
			failure.Code = "rate_limited"
		default:
			failure.Code = "http_error"
		}
	}
	return failure
}
