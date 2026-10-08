package app

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/health"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/logging"
	"github.com/Fatifizo/fundzim/internal/platform/ratelimit"
)

// NewRouter registers every route. Each route declares an access policy (deny by default).
func NewRouter(d *Deps, checker *health.Checker) *httpx.Router {
	r := httpx.NewRouter(d.Logger)

	// Infrastructure probes (outside /api/v1, bodies per OpenAPI HealthStatus; no dependency details).
	r.HandleFunc("GET /healthz", httpx.PolicyPublic, func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.HandleFunc("GET /readyz", httpx.PolicyPublic, func(w http.ResponseWriter, req *http.Request) {
		if ready(d, checker, req) {
			httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
			return
		}
		w.Header().Set("Retry-After", "5")
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
	})

	// Public API equivalents (enveloped).
	r.HandleFunc("GET /api/v1/health", httpx.PolicyPublic, func(w http.ResponseWriter, req *http.Request) {
		httpx.WriteData(w, req, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.HandleFunc("GET /api/v1/ready", httpx.PolicyPublic, func(w http.ResponseWriter, req *http.Request) {
		if ready(d, checker, req) {
			httpx.WriteData(w, req, http.StatusOK, map[string]string{"status": "ready"})
			return
		}
		httpx.WriteError(w, req, d.Logger, errs.New(errs.Unavailable, errs.CodeServiceUnavailable, "The service is not ready. Please retry shortly."))
	})
	r.HandleFunc("GET /api/v1/version", httpx.PolicyPublic, func(w http.ResponseWriter, req *http.Request) {
		httpx.WriteData(w, req, http.StatusOK, d.Version)
	})

	if d.Auth != nil {
		r.SetAuthorizer(d.Auth)
		registerIdentityRoutes(r, d.Auth, d.Orgs, d.Idempotency, d.Logger)
	}
	return r
}

// ready runs the readiness checks, records metrics and logs failures (with causes) server-side only.
func ready(d *Deps, checker *health.Checker, req *http.Request) bool {
	rep := checker.RunCached(req.Context())
	l := logging.FromContext(req.Context(), d.Logger)
	for _, res := range rep.Results {
		d.Metrics.SetDependency(res.Name, res.OK)
		if !res.OK {
			l.Warn("readiness check failed", slog.String("check", res.Name), slog.Bool("critical", res.Critical),
				slog.String("error_category", "dependency"), slog.String("error", errString(res.Err)))
		}
	}
	return rep.Ready
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// PublicHandler wraps the router in the middleware chain (ARCHITECTURE §5 order): request ID, trusted
// client IP, access log, panic recovery, security headers, CORS, body limit, timeout, the distributed
// global per-IP rate limit, session resolution and CSRF. Authorization runs per route in the router;
// idempotency and per-route limits are route options.
func PublicHandler(d *Deps, router http.Handler) http.Handler {
	cfg := d.Config
	mws := []httpx.Middleware{
		httpx.RequestIDMiddleware(cfg.HTTP.TrustedProxyCIDRs),
		httpx.ClientIPMiddleware(cfg.HTTP.TrustedProxyCIDRs),
		httpx.AccessLog(d.Logger, d.Metrics),
		httpx.Recover(d.Logger, d.Metrics.PanicRecovered),
		httpx.SecurityHeaders(!cfg.App.Env.IsLocal()),
		httpx.CORS(cfg.HTTP.CORSAllowedOrigins),
		httpx.BodyLimit(cfg.HTTP.MaxBodyBytes, d.Logger),
		httpx.Timeout(cfg.HTTP.RequestTimeout),
	}
	if cfg.RateLimit.Enabled {
		lim := d.Limiter
		if lim == nil {
			lim = ratelimit.New(nil, ratelimit.Options{Logger: d.Logger})
		}
		mws = append(mws, ratelimit.Middleware(lim, ratelimit.GlobalIPFromRate(cfg.RateLimit.RequestsPerS, cfg.RateLimit.Burst),
			ratelimit.ByClientIP, d.Logger))
	}
	if d.Auth != nil {
		mws = append(mws, d.Auth.SessionMiddleware(), d.Auth.CSRFMiddleware())
	}
	return httpx.Chain(router, mws...)
}

// InternalHandler serves metrics and detailed readiness on the internal listener only. It must never be
// exposed through the public proxy.
func InternalHandler(d *Deps, checker *health.Checker) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", d.Metrics.Handler())
	mux.HandleFunc("GET /internal/readiness", func(w http.ResponseWriter, req *http.Request) {
		rep := checker.Run(req.Context())
		type item struct {
			Name     string `json:"name"`
			Critical bool   `json:"critical"`
			OK       bool   `json:"ok"`
			Error    string `json:"error,omitempty"`
			Millis   int64  `json:"duration_ms"`
		}
		out := struct {
			Ready  bool   `json:"ready"`
			Checks []item `json:"checks"`
		}{Ready: rep.Ready}
		for _, r := range rep.Results {
			out.Checks = append(out.Checks, item{Name: r.Name, Critical: r.Critical, OK: r.OK, Error: errString(r.Err), Millis: r.Duration.Milliseconds()})
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if !rep.Ready {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	return mux
}
