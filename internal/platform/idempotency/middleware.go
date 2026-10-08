package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/logging"
)

// Mode says whether the Idempotency-Key header is mandatory on a route.
type Mode int

const (
	Optional Mode = iota // mutating routes: the header is honoured when present
	Required             // financial mutations: missing header → 400 IDEMPOTENCY_KEY_REQUIRED
)

// Header names.
const (
	HeaderKey      = "Idempotency-Key"
	HeaderReplayed = "Idempotent-Replayed"
)

// Error codes (docs/api/error-model.md, api-design §7).
const (
	CodeKeyRequired = "IDEMPOTENCY_KEY_REQUIRED"
	CodeKeyInvalid  = "IDEMPOTENCY_KEY_INVALID"
	CodeInProgress  = "IDEMPOTENCY_REQUEST_IN_PROGRESS"
	CodeKeyReused   = "IDEMPOTENCY_KEY_REUSED"
)

// Middleware enforces Idempotency-Key semantics on one route. The stored scope is
// "<scope>:user:<user id>" for signed-in principals or "<scope>:anon:<client ip>" otherwise, so two
// principals can never see each other's records. Mount it after authorization (httpx.With) and after
// any rate limit, so throttled requests never claim a key.
//
// Responses with status < 500 (except 408 and 429) are stored and replayed; 5xx/408/429 release the key
// so the client can retry with it. Stored bodies must be JSON (the API envelope) or empty. Response
// headers other than Content-Type are not stored, so routes that set cookies or Location must not rely on
// replays to deliver them.
func Middleware(store *Store, mode Mode, scope string, logger *slog.Logger) httpx.Middleware {
	if store == nil {
		panic("idempotency: nil store")
	}
	if scope == "" || len(scope) > 180 || strings.ContainsAny(scope, " \t\r\n") {
		panic("idempotency: scope must be 1-180 characters without whitespace")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			vals := r.Header.Values(HeaderKey)
			if len(vals) == 0 || (len(vals) == 1 && vals[0] == "") {
				if mode == Required {
					httpx.WriteError(w, r, logger, errs.New(errs.Invalid, CodeKeyRequired, "An Idempotency-Key header is required for this request."))
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			key := strings.ToLower(strings.TrimSpace(vals[0]))
			if len(vals) != 1 || !ids.Valid(key) {
				httpx.WriteError(w, r, logger, errs.New(errs.Invalid, CodeKeyInvalid, "Idempotency-Key must be a UUID."))
				return
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				var mbe *http.MaxBytesError
				if errors.As(err, &mbe) {
					httpx.WriteError(w, r, logger, errs.New(errs.TooLarge, errs.CodePayloadTooLarge, "Request body is too large."))
					return
				}
				httpx.WriteError(w, r, logger, errs.Wrap(err, errs.Invalid, errs.CodeMalformedRequest, "The request body could not be read."))
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			fullScope := scope + ":" + actor(r)
			hash := Fingerprint(r.Method, httpx.RouteFrom(r.Context()), r.URL.EscapedPath(), r.URL.RawQuery, body)

			res, err := store.begin(r.Context(), fullScope, key, hash)
			if err != nil {
				// Fail closed: without the record we cannot guarantee at-most-once execution.
				httpx.WriteError(w, r, logger, errs.Wrap(err, errs.Unavailable, errs.CodeServiceUnavailable, "The service is temporarily unavailable. Please retry."))
				return
			}
			switch res.outcome {
			case replay:
				writeReplay(w, res)
				return
			case inProgress:
				w.Header().Set("Retry-After", strconv.Itoa(ceilSeconds(res.retry)))
				e := errs.New(errs.Conflict, CodeInProgress, "A request with this Idempotency-Key is still being processed. Retry with the same key.")
				e.Retryable = true
				httpx.WriteError(w, r, logger, e)
				return
			case reused:
				httpx.WriteError(w, r, logger, errs.New(errs.Conflict, CodeKeyReused, "This Idempotency-Key was already used for a different request."))
				return
			}
			run(store, res, w, r, next, logger)
		})
	}
}

// run executes the handler as the key owner, then stores or releases the record.
func run(store *Store, res beginResult, w http.ResponseWriter, r *http.Request, next http.Handler, logger *slog.Logger) {
	l := logging.FromContext(r.Context(), logger)
	bg := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	}
	cw := &captureWriter{header: w.Header()}
	finished := false
	defer func() {
		if finished {
			return
		}
		// Panic in the handler: release the key, then let Recover render the 500.
		ctx, cancel := bg()
		defer cancel()
		if err := store.release(ctx, res.id, res.lease); err != nil {
			l.Error("idempotency: release after panic failed", slog.String("error", err.Error()))
		}
	}()
	next.ServeHTTP(cw, r)
	finished = true

	status := cw.status
	if status == 0 {
		status = http.StatusOK
	}
	body := cw.buf.Bytes()
	ctx, cancel := bg()
	defer cancel()
	switch {
	case status >= 500 || status == http.StatusTooManyRequests || status == http.StatusRequestTimeout:
		if err := store.release(ctx, res.id, res.lease); err != nil {
			l.Error("idempotency: release failed", slog.String("error", err.Error()))
		}
	case len(body) > 0 && !json.Valid(body):
		l.Error("idempotency: response is not JSON and cannot be stored; key released", slog.Int("status", status))
		if err := store.release(ctx, res.id, res.lease); err != nil {
			l.Error("idempotency: release failed", slog.String("error", err.Error()))
		}
	default:
		var stored []byte
		if len(body) > 0 {
			stored = body
		}
		if err := store.complete(ctx, res.id, res.lease, status, stored); err != nil {
			// The handler's effect has happened. The record stays IN_PROGRESS until its lease expires
			// (blocking duplicates meanwhile); domain uniqueness constraints remain the last guard.
			l.Error("idempotency: storing the response failed", slog.String("error", err.Error()))
		}
	}
	if status != http.StatusNoContent && status != http.StatusNotModified && len(body) > 0 {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func writeReplay(w http.ResponseWriter, res beginResult) {
	h := w.Header()
	h.Set(HeaderReplayed, "true")
	if res.body != nil {
		h.Set("Content-Type", "application/json")
		h.Set("Content-Length", strconv.Itoa(len(res.body)))
	}
	w.WriteHeader(res.status)
	if res.body != nil {
		_, _ = w.Write(res.body)
	}
}

func actor(r *http.Request) string {
	if p := authz.PrincipalFrom(r.Context()); p != nil && p.UserID != "" {
		return "user:" + p.UserID
	}
	if ip := authz.ClientIPFrom(r.Context()); ip.IsValid() {
		return "anon:" + ip.String()
	}
	return "anon:unknown"
}

// Fingerprint is the request hash: SHA-256 over the method, route template, escaped path, raw query and
// canonical body, each length-prefixed. A JSON body is canonicalised (object keys sorted, insignificant
// whitespace removed, numbers kept verbatim); any other body is hashed as raw bytes.
func Fingerprint(method, route, path, query string, body []byte) []byte {
	h := sha256.New()
	field := func(tag byte, b []byte) {
		var n [9]byte
		n[0] = tag
		binary.BigEndian.PutUint64(n[1:], uint64(len(b)))
		h.Write(n[:])
		h.Write(b)
	}
	field('m', []byte(method))
	field('r', []byte(route))
	field('p', []byte(path))
	field('q', []byte(query))
	if c, ok := canonicalJSON(body); ok {
		field('j', c)
	} else {
		field('b', body)
	}
	return h.Sum(nil)
}

func canonicalJSON(body []byte) ([]byte, bool) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false // trailing data
	}
	out, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	return out, true
}

func ceilSeconds(d time.Duration) int {
	s := int((d + time.Second - 1) / time.Second)
	if s < 1 {
		s = 1
	}
	return s
}

// captureWriter buffers the handler's response so exactly what is stored is what is sent. It shares the
// real header map, so headers set by outer middleware and the handler both reach the client.
type captureWriter struct {
	header http.Header
	status int
	buf    bytes.Buffer
}

func (c *captureWriter) Header() http.Header { return c.header }

func (c *captureWriter) WriteHeader(code int) {
	if c.status == 0 {
		c.status = code
	}
}

func (c *captureWriter) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	return c.buf.Write(b)
}
