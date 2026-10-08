package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/Fatifizo/fundzim/internal/platform/errs"
)

// DecodeJSON strictly decodes a JSON request body into v: Content-Type must be application/json, unknown
// fields are rejected (no mass assignment, SECURITY §5.4), trailing data is rejected, and the body size is
// bounded by the BodyLimit middleware.
func DecodeJSON(r *http.Request, v any) error {
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if ct != "application/json" {
		return errs.New(errs.UnsupportedMediaType, errs.CodeUnsupportedMedia, "Content-Type must be application/json.")
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return errs.New(errs.TooLarge, errs.CodePayloadTooLarge, "Request body is too large.")
		}
		if strings.HasPrefix(err.Error(), "json: unknown field") {
			field := strings.Trim(strings.TrimPrefix(err.Error(), "json: unknown field "), `"`)
			e := errs.New(errs.Unprocessable, errs.CodeValidationFailed, "The request contains an unknown field.")
			e.Details = []errs.Detail{{Field: field, Code: "UNKNOWN_FIELD"}}
			return e
		}
		return errs.New(errs.Invalid, errs.CodeMalformedRequest, "The request body is not valid JSON for this operation.")
	}
	if _, err := dec.Token(); err != io.EOF {
		return errs.New(errs.Invalid, errs.CodeMalformedRequest, "Unexpected data after the JSON body.")
	}
	return nil
}

// Validation builds a 422 VALIDATION_FAILED error from field/code pairs.
func Validation(details ...errs.Detail) error {
	e := errs.New(errs.Unprocessable, errs.CodeValidationFailed, "Some fields are invalid.")
	e.Details = details
	return e
}
