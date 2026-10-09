// Package httpx holds FundZim's HTTP conventions: the response envelope, error rendering and the
// middleware chain (ARCHITECTURE §5–§6, docs/api/api-design.md).
package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/logging"
)

// Meta is the envelope metadata.
type Meta struct {
	RequestID string `json:"request_id"`
}

type successBody struct {
	Data any  `json:"data"`
	Meta Meta `json:"meta"`
}

type errorObject struct {
	Code      string        `json:"code"`
	Message   string        `json:"message"`
	Retryable bool          `json:"retryable"`
	Details   []errs.Detail `json:"details,omitempty"`
}

type errorBody struct {
	Error errorObject `json:"error"`
	Meta  Meta        `json:"meta"`
}

// WriteJSON writes v as a raw JSON body (no envelope). Used only for infrastructure probes.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "no-store") // default; handlers of public data choose their own policy
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteData writes a success envelope.
func WriteData(w http.ResponseWriter, r *http.Request, status int, data any) {
	WriteJSON(w, status, successBody{Data: data, Meta: Meta{RequestID: RequestID(r.Context())}})
}

// WriteError renders err as an error envelope. Internal details (the cause) are logged with the
// request-scoped logger and never sent to the client.
func WriteError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	e := errs.As(err)
	status := e.Kind.Status()
	l := logging.FromContext(r.Context(), logger)
	attrs := []any{slog.String("error_code", e.Code), slog.Int("status", status)}
	if e.Cause != nil {
		attrs = append(attrs, slog.String("error", e.Cause.Error()))
	}
	if status >= 500 {
		l.Error("request failed", attrs...)
	} else {
		l.Debug("request rejected", attrs...)
	}
	var ra interface{ RetryAfter() time.Duration }
	if errors.As(err, &ra) && ra.RetryAfter() > 0 && w.Header().Get("Retry-After") == "" {
		secs := int((ra.RetryAfter() + time.Second - 1) / time.Second) // the limiter's real wait, rounded up
		w.Header().Set("Retry-After", strconv.Itoa(secs))
	}
	if e.Kind == errs.RateLimited || e.Kind == errs.Unavailable || e.Kind == errs.Timeout {
		if w.Header().Get("Retry-After") == "" {
			w.Header().Set("Retry-After", strconv.Itoa(5))
		}
	}
	w.Header().Set("Cache-Control", "no-store") // errors are never cacheable (a public 404 must not stick)
	w.Header().Del("ETag")
	WriteJSON(w, status, errorBody{
		Error: errorObject{Code: e.Code, Message: e.Message, Retryable: e.Retryable, Details: e.Details},
		Meta:  Meta{RequestID: RequestID(r.Context())},
	})
}
