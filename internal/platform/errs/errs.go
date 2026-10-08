// Package errs is FundZim's application error model.
//
// A *Error carries a Kind (which fixes the HTTP status at the boundary), a stable UPPER_SNAKE Code that
// clients branch on, a message that is safe to show to anyone, and an optional wrapped cause that is only
// ever logged. Unknown errors are reported to clients as INTERNAL_ERROR with no internals
// (docs/api/error-model.md).
package errs

import (
	"errors"
	"net/http"
)

// Kind classifies an error. Names follow docs/architecture/go-module-design.md §3.3.
type Kind int

const (
	Internal Kind = iota
	Invalid
	Unauthenticated
	Forbidden
	NotFound
	MethodNotAllowed
	Conflict
	Unprocessable
	TooLarge
	UnsupportedMediaType
	RateLimited
	Unavailable
	Timeout
)

// Status maps a Kind to its HTTP status.
func (k Kind) Status() int {
	switch k {
	case Invalid:
		return http.StatusBadRequest
	case Unauthenticated:
		return http.StatusUnauthorized
	case Forbidden:
		return http.StatusForbidden
	case NotFound:
		return http.StatusNotFound
	case MethodNotAllowed:
		return http.StatusMethodNotAllowed
	case Conflict:
		return http.StatusConflict
	case Unprocessable:
		return http.StatusUnprocessableEntity
	case TooLarge:
		return http.StatusRequestEntityTooLarge
	case UnsupportedMediaType:
		return http.StatusUnsupportedMediaType
	case RateLimited:
		return http.StatusTooManyRequests
	case Unavailable:
		return http.StatusServiceUnavailable
	case Timeout:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// Detail describes one invalid field.
type Detail struct {
	Field string `json:"field"`
	Code  string `json:"code"`
}

// Error is an application error.
type Error struct {
	Kind      Kind
	Code      string
	Message   string // safe for clients
	Retryable bool
	Details   []Detail
	Cause     error // logged, never sent to clients
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return e.Code + ": " + e.Message + ": " + e.Cause.Error()
	}
	return e.Code + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.Cause }

// New creates an error without a cause.
func New(kind Kind, code, message string) *Error {
	return &Error{Kind: kind, Code: code, Message: message, Retryable: defaultRetryable(kind)}
}

// Wrap creates an error carrying a cause for logs.
func Wrap(cause error, kind Kind, code, message string) *Error {
	e := New(kind, code, message)
	e.Cause = cause
	return e
}

func defaultRetryable(k Kind) bool {
	return k == Internal || k == Unavailable || k == RateLimited || k == Timeout
}

// As extracts an *Error from err. Anything else becomes INTERNAL_ERROR with err as the logged cause.
func As(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return Wrap(err, Internal, CodeInternal, "An unexpected error occurred.")
}

// Platform codes (docs/api/error-model.md §4.1–§4.2).
const (
	CodeMalformedRequest   = "MALFORMED_REQUEST"
	CodeValidationFailed   = "VALIDATION_FAILED"
	CodeRouteNotFound      = "ROUTE_NOT_FOUND"
	CodeMethodNotAllowed   = "METHOD_NOT_ALLOWED"
	CodePayloadTooLarge    = "PAYLOAD_TOO_LARGE"
	CodeUnsupportedMedia   = "UNSUPPORTED_MEDIA_TYPE"
	CodeRateLimited        = "RATE_LIMITED"
	CodeInternal           = "INTERNAL_ERROR"
	CodeServiceUnavailable = "SERVICE_UNAVAILABLE"
)
