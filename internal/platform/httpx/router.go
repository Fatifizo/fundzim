package httpx

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/Fatifizo/fundzim/internal/platform/errs"
)

// Policy declares who may call a route. Every route must declare one (deny by default, SECURITY §5.1).
// Stage 3 has public routes only; Authenticated/Permission policies arrive with Stage 4.
type Policy string

const (
	// PolicyPublic routes need no authentication (health, version, public reads).
	PolicyPublic Policy = "public"
)

// Route describes one registered route, for documentation and contract tests.
type Route struct {
	Pattern string // e.g. "GET /api/v1/health"
	Policy  Policy
}

// Router wraps http.ServeMux, records route templates for logs and metrics, requires a policy per
// route, and renders enveloped 404/405 responses.
type Router struct {
	mux    *http.ServeMux
	routes []Route
	logger *slog.Logger
}

// NewRouter returns an empty router.
func NewRouter(logger *slog.Logger) *Router {
	return &Router{mux: http.NewServeMux(), logger: logger}
}

// Handle registers a route. pattern must include the method ("GET /api/v1/health"). It panics on an
// empty policy, so a route can never be added without an explicit access decision.
func (rt *Router) Handle(pattern string, policy Policy, h http.Handler) {
	if policy == "" {
		panic("httpx: route " + pattern + " registered without a policy")
	}
	if !strings.Contains(pattern, " ") {
		panic("httpx: route " + pattern + " must include an HTTP method")
	}
	rt.routes = append(rt.routes, Route{Pattern: pattern, Policy: policy})
	rt.mux.Handle(pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetRoute(r.Context(), trimPattern(pattern))
		h.ServeHTTP(w, r)
	}))
}

// HandleFunc registers a handler function.
func (rt *Router) HandleFunc(pattern string, policy Policy, f func(http.ResponseWriter, *http.Request)) {
	rt.Handle(pattern, policy, http.HandlerFunc(f))
}

// Routes lists the registered routes.
func (rt *Router) Routes() []Route { return append([]Route(nil), rt.routes...) }

var probeMethods = []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

// ServeHTTP dispatches to the matching route, or renders an enveloped 404/405.
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, pattern := rt.mux.Handler(r); pattern != "" {
		rt.mux.ServeHTTP(w, r)
		return
	}
	var allow []string
	for _, m := range probeMethods {
		if m == r.Method {
			continue
		}
		probe := r.Clone(r.Context())
		probe.Method = m
		if _, p := rt.mux.Handler(probe); p != "" {
			allow = append(allow, m)
		}
	}
	if len(allow) > 0 {
		w.Header().Set("Allow", strings.Join(allow, ", "))
		WriteError(w, r, rt.logger, errs.New(errs.MethodNotAllowed, errs.CodeMethodNotAllowed, "Method not allowed on this route."))
		return
	}
	NotFound(rt.logger).ServeHTTP(w, r)
}
