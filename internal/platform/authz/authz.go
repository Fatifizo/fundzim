// Package authz carries the authenticated principal through the request context and defines the
// authorization policies routes declare. It holds no business rules: the auth module resolves sessions
// into Principals and implements the Authorizer; ownership (ABAC) checks stay in each module's service.
package authz

import (
	"context"
	"net/netip"
	"time"
)

// AccountKind distinguishes personal and staff accounts (SECURITY §4.2).
type AccountKind string

const (
	KindUser  AccountKind = "USER"
	KindStaff AccountKind = "STAFF"
)

// Principal is the authenticated actor of a request. A STAFF principal always has completed MFA
// (sessions for staff cannot exist otherwise, ADR-027/ADR-032).
type Principal struct {
	UserID        string
	SessionID     string
	Kind          AccountKind
	MFAVerifiedAt time.Time // zero when the session was created without a second factor
	StepUpAt      time.Time // last fresh factor (password or TOTP); zero if none since login
	Permissions   map[string]bool
	EmailVerified bool
}

// Has reports whether the principal holds a staff permission.
func (p *Principal) Has(perm string) bool { return p != nil && p.Permissions[perm] }

// StepUpFresh reports whether a fresh factor was presented within maxAge of now.
func (p *Principal) StepUpFresh(now time.Time, maxAge time.Duration) bool {
	return p != nil && !p.StepUpAt.IsZero() && now.Sub(p.StepUpAt) <= maxAge
}

type principalKey struct{}
type clientIPKey struct{}

// WithPrincipal stores p in ctx.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the principal, or nil for anonymous requests.
func PrincipalFrom(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey{}).(*Principal)
	return p
}

// WithClientIP stores the resolved client address (trusted-proxy aware) in ctx.
func WithClientIP(ctx context.Context, ip netip.Addr) context.Context {
	return context.WithValue(ctx, clientIPKey{}, ip)
}

// ClientIPFrom returns the resolved client address (invalid Addr if unknown).
func ClientIPFrom(ctx context.Context) netip.Addr {
	ip, _ := ctx.Value(clientIPKey{}).(netip.Addr)
	return ip
}
