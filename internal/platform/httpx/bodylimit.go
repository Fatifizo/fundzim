package httpx

import (
	"log/slog"
	"net/http"

	"github.com/Fatifizo/fundzim/internal/platform/errs"
)

// BodyLimitExemption raises the body limit for the requests a ServeMux pattern matches (the same pattern
// syntax and matching as Router.Handle, e.g. "POST /api/v1/verification/documents"). Max is the hard cap for
// those requests; the handler is expected to apply its own tighter, per-purpose cap as well.
type BodyLimitExemption struct {
	Pattern string
	Max     int64
}

// BodyLimitWithExemptions is BodyLimit (global cap max, 413 PAYLOAD_TOO_LARGE) except that requests matching
// an exemption pattern get that exemption's cap instead. Matching uses an internal http.ServeMux, so the
// exemption is route-level: "POST /x" does not exempt "PUT /x" or "/x/y". It panics on an invalid or
// duplicate pattern or a non-positive cap (programming errors at start-up).
func BodyLimitWithExemptions(max int64, exemptions []BodyLimitExemption, logger *slog.Logger) Middleware {
	mux := http.NewServeMux()
	caps := map[string]int64{}
	for _, e := range exemptions {
		if e.Max <= 0 {
			panic("httpx: body limit exemption " + e.Pattern + " needs a positive cap")
		}
		caps[e.Pattern] = e.Max
		mux.Handle(e.Pattern, http.NotFoundHandler()) // never served; used only for matching
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limit := max
			if len(caps) > 0 {
				if _, pattern := mux.Handler(r); pattern != "" {
					if c, ok := caps[pattern]; ok {
						limit = c
					}
				}
			}
			if r.ContentLength > limit {
				WriteError(w, r, logger, errs.New(errs.TooLarge, errs.CodePayloadTooLarge, "Request body is too large."))
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}
