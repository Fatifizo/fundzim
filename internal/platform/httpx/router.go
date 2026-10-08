package httpx

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/Fatifizo/fundzim/internal/platform/errs"
)

// PolicyKind is the class of an access policy.
type PolicyKind string

const (
	// KindPublic routes need no authentication (health, version, login, registration).
	KindPublic PolicyKind = "public"
	// KindAuthenticated routes need any fully authenticated session (USER or STAFF).
	KindAuthenticated PolicyKind = "authenticated"
	// KindUser routes need a USER (personal) session.
	KindUser PolicyKind = "user"
	// KindStaff routes need a STAFF session (MFA is guaranteed for staff sessions).
	KindStaff PolicyKind = "staff"
	// KindPermission routes need a STAFF session holding a named permission.
	KindPermission PolicyKind = "permission"
)

// Policy declares who may call a route. Every route must declare one (deny by default, SECURITY §5.1).
type Policy struct {
	Kind       PolicyKind
	Permission string // KindPermission only
}

// Policy constructors.
func Public() Policy                { return Policy{Kind: KindPublic} }
func Authenticated() Policy         { return Policy{Kind: KindAuthenticated} }
func User() Policy                  { return Policy{Kind: KindUser} }
func Staff() Policy                 { return Policy{Kind: KindStaff} }
func Permission(code string) Policy { return Policy{Kind: KindPermission, Permission: code} }
func (p Policy) String() string {
	if p.Kind == KindPermission {
		return "permission:" + p.Permission
	}
	return string(p.Kind)
}

// PolicyPublic is kept for readability at call sites.
var PolicyPublic = Public()

// Authorizer decides whether the request's principal satisfies a policy. It returns nil to allow or an
// *errs.Error (401/403/404) to deny. The auth module implements it.
type Authorizer interface {
	Authorize(r *http.Request, p Policy) error
}

// AuthorizerFunc adapts a function.
type AuthorizerFunc func(r *http.Request, p Policy) error

func (f AuthorizerFunc) Authorize(r *http.Request, p Policy) error { return f(r, p) }

// denyNonPublic is the default authorizer: only public routes pass until an Authorizer is installed.
var denyNonPublic = AuthorizerFunc(func(_ *http.Request, p Policy) error {
	if p.Kind == KindPublic {
		return nil
	}
	return errs.New(errs.Unauthenticated, "AUTHENTICATION_REQUIRED", "Sign in to continue.")
})

// RouteOption customises one route.
type RouteOption func(*routeConfig)

type routeConfig struct{ mws []Middleware }

// With adds route-specific middleware, applied after authorization and before the handler (first listed
// is outermost). Used for per-route rate limits and idempotency.
func With(mws ...Middleware) RouteOption {
	return func(c *routeConfig) { c.mws = append(c.mws, mws...) }
}

// Route describes one registered route, for documentation and contract tests.
type Route struct {
	Pattern string // e.g. "GET /api/v1/health"
	Policy  Policy
}

// Router wraps http.ServeMux, records route templates for logs and metrics, requires a policy per
// route, enforces it through the Authorizer, and renders enveloped 404/405 responses.
type Router struct {
	mux        *http.ServeMux
	routes     []Route
	logger     *slog.Logger
	authorizer Authorizer
}

// NewRouter returns an empty router that allows only public routes until SetAuthorizer is called.
func NewRouter(logger *slog.Logger) *Router {
	return &Router{mux: http.NewServeMux(), logger: logger, authorizer: denyNonPublic}
}

// SetAuthorizer installs the policy decision function used for every non-public route.
func (rt *Router) SetAuthorizer(a Authorizer) { rt.authorizer = a }

// Handle registers a route. pattern must include the method ("GET /api/v1/health"). It panics on an
// empty policy kind or an unnamed permission, so a route can never exist without an access decision.
func (rt *Router) Handle(pattern string, policy Policy, h http.Handler, opts ...RouteOption) {
	switch policy.Kind {
	case KindPublic, KindAuthenticated, KindUser, KindStaff:
	case KindPermission:
		if policy.Permission == "" {
			panic("httpx: route " + pattern + " has a permission policy without a permission")
		}
	default:
		panic("httpx: route " + pattern + " registered without a valid policy")
	}
	if !strings.Contains(pattern, " ") {
		panic("httpx: route " + pattern + " must include an HTTP method")
	}
	var cfg routeConfig
	for _, o := range opts {
		o(&cfg)
	}
	inner := Chain(h, cfg.mws...)
	rt.routes = append(rt.routes, Route{Pattern: pattern, Policy: policy})
	template := trimPattern(pattern)
	rt.mux.Handle(pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetRoute(r.Context(), template)
		if policy.Kind != KindPublic {
			if err := rt.authorizer.Authorize(r, policy); err != nil {
				WriteError(w, r, rt.logger, err)
				return
			}
		}
		inner.ServeHTTP(w, r)
	}))
}

// HandleFunc registers a handler function.
func (rt *Router) HandleFunc(pattern string, policy Policy, f func(http.ResponseWriter, *http.Request), opts ...RouteOption) {
	rt.Handle(pattern, policy, http.HandlerFunc(f), opts...)
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
