package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/logging"
)

// Middleware wraps a handler.
type Middleware func(http.Handler) http.Handler

// Chain applies middleware so that the first listed is the outermost.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// routeInfo is shared through the context so outer middleware (access log, metrics) can learn the matched
// route template after the mux has run. It never contains raw paths with IDs.
type routeInfo struct{ pattern string }

type routeKey struct{}

func withRouteInfo(ctx context.Context) (context.Context, *routeInfo) {
	ri := &routeInfo{pattern: "unmatched"}
	return context.WithValue(ctx, routeKey{}, ri), ri
}

// SetRoute records the matched route template for logs and metrics.
func SetRoute(ctx context.Context, pattern string) {
	if ri, ok := ctx.Value(routeKey{}).(*routeInfo); ok {
		ri.pattern = pattern
	}
}

// RouteFrom returns the matched route template ("unmatched" when no route matched).
func RouteFrom(ctx context.Context) string {
	if ri, ok := ctx.Value(routeKey{}).(*routeInfo); ok {
		return ri.pattern
	}
	return "unmatched"
}

// statusRecorder captures the response status and size.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Observer receives one call per finished request (metrics).
type Observer interface {
	ObserveRequest(method, route string, status int, duration time.Duration)
}

// AccessLog logs one line per request with the route template, status and duration, and stores a
// request-scoped logger (request_id, correlation_id) in the context. Bodies are never logged.
func AccessLog(base *slog.Logger, obs Observer) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ctx, ri := withRouteInfo(r.Context())
			reqID := RequestID(ctx)
			l := base.With(slog.String("request_id", reqID), slog.String("correlation_id", reqID))
			ctx = logging.WithContext(ctx, l)
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r.WithContext(ctx))
			if rec.status == 0 {
				rec.status = http.StatusOK
			}
			d := time.Since(start)
			if obs != nil {
				obs.ObserveRequest(r.Method, ri.pattern, rec.status, d)
			}
			level := slog.LevelInfo
			if rec.status >= 500 {
				level = slog.LevelError
			} else if ri.pattern == "GET /healthz" || ri.pattern == "GET /readyz" {
				level = slog.LevelDebug // probes are frequent; metrics still count them
			}
			l.Log(ctx, level, "http request",
				slog.String("operation", ri.pattern),
				slog.String("method", r.Method),
				slog.Int("status", rec.status),
				slog.Int64("duration_ms", d.Milliseconds()),
				slog.Int("response_bytes", rec.bytes),
			)
		})
	}
}

// Recover converts a panic into a 500 INTERNAL_ERROR envelope and logs the stack.
func Recover(base *slog.Logger, onPanic func()) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					if v == http.ErrAbortHandler {
						panic(v)
					}
					if onPanic != nil {
						onPanic()
					}
					logging.FromContext(r.Context(), base).Error("panic recovered",
						slog.String("error_category", "panic"),
						slog.String("panic", fmt.Sprint(v)),
						slog.String("stack", string(debug.Stack())))
					WriteError(w, r, base, errs.New(errs.Internal, errs.CodeInternal, "An unexpected error occurred."))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// SecurityHeaders sets headers appropriate for a JSON API. HSTS is sent only outside local
// environments (it would pin localhost to HTTPS in browsers).
func SecurityHeaders(hsts bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
			h.Set("Cross-Origin-Resource-Policy", "same-origin")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			h.Set("Cache-Control", "no-store")
			if hsts {
				h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// BodyLimit rejects bodies larger than max with 413 PAYLOAD_TOO_LARGE. Declared lengths are checked
// up front; chunked bodies are capped by http.MaxBytesReader while handlers read them.
func BodyLimit(max int64, logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > max {
				WriteError(w, r, logger, errs.New(errs.TooLarge, errs.CodePayloadTooLarge, "Request body is too large."))
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, max)
			next.ServeHTTP(w, r)
		})
	}
}

// Timeout bounds request processing time through the context. Handlers and dependencies must honour
// ctx; the server's WriteTimeout is the hard backstop.
func Timeout(d time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// CORS allows cross-origin requests only from explicitly configured origins. The main web app is
// same-origin, so the default (no origins) sends no CORS headers at all. Credentials are never allowed
// cross-origin in Stage 3 (cookie sessions arrive in Stage 4 and stay same-origin, ADR-027).
func CORS(allowed []string) Middleware {
	return func(next http.Handler) http.Handler {
		if len(allowed) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				w.Header().Add("Vary", "Origin")
			}
			if origin == "" || !slices.Contains(allowed, origin) {
				next.ServeHTTP(w, r)
				return
			}
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Expose-Headers", HeaderRequestID+", Retry-After")
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
				h.Set("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key, "+HeaderRequestID)
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimiter limits requests per client key. Implementations must fail open only when the limiter
// itself is broken, never by default.
type RateLimiter interface {
	Allow(key string) (ok bool, retryAfter time.Duration)
}

// RateLimit applies a coarse per-client-IP limit. Infrastructure probes are exempt.
func RateLimit(l RateLimiter, trusted []netip.Prefix, logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
				next.ServeHTTP(w, r)
				return
			}
			ip := ClientIP(r, trusted)
			if ok, retry := l.Allow(ip.String()); !ok {
				secs := int(retry.Round(time.Second) / time.Second)
				if secs < 1 {
					secs = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(secs))
				WriteError(w, r, logger, errs.New(errs.RateLimited, errs.CodeRateLimited, "Too many requests. Please slow down."))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// MemoryRateLimiter is an in-process token bucket per key. It is the safe fallback when Redis is absent
// (ARCHITECTURE §8.2); with several API replicas the effective limit is per replica.
type MemoryRateLimiter struct {
	mu      sync.Mutex
	rate    float64 // tokens per second
	burst   float64
	buckets map[string]*bucket
	now     func() time.Time
	maxKeys int
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewMemoryRateLimiter returns a limiter allowing rps sustained and burst at once per key.
func NewMemoryRateLimiter(rps float64, burst int, now func() time.Time) *MemoryRateLimiter {
	if now == nil {
		now = time.Now
	}
	return &MemoryRateLimiter{rate: rps, burst: float64(burst), buckets: map[string]*bucket{}, now: now, maxKeys: 100_000}
}

// Allow consumes one token for key.
func (m *MemoryRateLimiter) Allow(key string) (bool, time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	b, ok := m.buckets[key]
	if !ok {
		if len(m.buckets) >= m.maxKeys {
			m.evict(now)
		}
		b = &bucket{tokens: m.burst, last: now}
		m.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * m.rate
	if b.tokens > m.burst {
		b.tokens = m.burst
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	need := (1 - b.tokens) / m.rate
	return false, time.Duration(need * float64(time.Second))
}

// evict drops full (idle) buckets to bound memory.
func (m *MemoryRateLimiter) evict(now time.Time) {
	for k, b := range m.buckets {
		if b.tokens+now.Sub(b.last).Seconds()*m.rate >= m.burst {
			delete(m.buckets, k)
		}
	}
}

// NotFound renders the enveloped 404 for unknown routes.
func NotFound(logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, logger, errs.New(errs.NotFound, errs.CodeRouteNotFound, "No such route."))
	})
}

// IsClientGone reports whether err means the client disconnected or the deadline passed.
func IsClientGone(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// trimPattern removes the host part of a ServeMux pattern ("GET example.com/x" → "GET /x").
func trimPattern(p string) string {
	method, path, ok := strings.Cut(p, " ")
	if !ok {
		return p
	}
	if i := strings.IndexByte(path, '/'); i > 0 {
		path = path[i:]
	}
	return method + " " + path
}
