package httpx

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Fatifizo/fundzim/internal/platform/errs"
)

// Optimistic-concurrency errors shared by the campaign family of APIs (ADR-038 §7).
var (
	ErrPreconditionRequired = errs.New(errs.PreconditionRequired, "PRECONDITION_REQUIRED",
		"Send If-Match with the version you are changing.")
	ErrPreconditionFailed = errs.New(errs.PreconditionFailed, "PRECONDITION_FAILED",
		"This item changed since you loaded it. Reload and try again.")
)

// RequireIfMatch returns the version in If-Match (a quoted or bare positive integer, as sent in ETag), or
// ErrPreconditionRequired when it is missing or unparsable.
func RequireIfMatch(r *http.Request) (int, error) {
	v := strings.TrimSpace(r.Header.Get("If-Match"))
	v = strings.TrimPrefix(v, "W/")
	v = strings.Trim(v, `"`)
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, ErrPreconditionRequired
	}
	return n, nil
}
