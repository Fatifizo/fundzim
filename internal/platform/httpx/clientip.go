package httpx

import (
	"net/http"
	"net/netip"
	"strings"

	"github.com/Fatifizo/fundzim/internal/platform/authz"
)

// maxForwardedHops bounds how many X-Forwarded-For entries are examined. A legitimate chain through
// FundZim's own proxies is a handful of hops; anything longer is attacker-supplied padding.
const maxForwardedHops = 16

// ClientIPMiddleware resolves the client address once per request (docs/stage-4/interface-contracts.md
// §3.1) and stores it with authz.WithClientIP. Every component that needs the client IP (rate limits,
// audit, sessions) reads authz.ClientIPFrom(ctx) instead of parsing headers itself.
//
// Rules: only X-Forwarded-For is considered, and only when the direct peer is inside trusted.
// X-Real-IP and Forwarded are always ignored (any client can send them).
func ClientIPMiddleware(trusted []netip.Prefix) Middleware {
	trusted = append([]netip.Prefix(nil), trusted...)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := ClientIP(r, trusted)
			next.ServeHTTP(w, r.WithContext(authz.WithClientIP(r.Context(), ip)))
		})
	}
}

// ClientIP resolves the client address of r without consulting the context.
//
//   - Direct peer not in trusted: the peer address is the client; all forwarding headers are ignored.
//   - Direct peer trusted: walk X-Forwarded-For right to left (all header lines, in order), skipping
//     addresses inside trusted; the first untrusted address is the client.
//   - A malformed entry stops the walk: the last trusted hop's view wins, i.e. the last valid address
//     examined (or the peer if none was). The same applies when every entry is trusted or the hop cap
//     is reached.
//
// The result is unmapped (::ffff:a.b.c.d → a.b.c.d) and carries no IPv6 zone. It is the invalid Addr
// only when RemoteAddr itself is unparsable (never for real net/http connections).
func ClientIP(r *http.Request, trusted []netip.Prefix) netip.Addr {
	peer := peerAddr(r)
	if !inPrefixes(peer, trusted) {
		return peer
	}
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(v, ",")...)
	}
	best := peer
	examined := 0
	for i := len(hops) - 1; i >= 0 && examined < maxForwardedHops; i-- {
		h := strings.TrimSpace(hops[i])
		examined++
		a, ok := parseForwardedAddr(h)
		if !ok {
			break
		}
		if !inPrefixes(a, trusted) {
			return a
		}
		best = a
	}
	return best
}

// parseForwardedAddr accepts a bare IPv4/IPv6 address or an address with a port ("1.2.3.4:80",
// "[2001:db8::1]:443"). Zones, empty strings, hostnames, "unknown" and obfuscated identifiers are
// malformed.
func parseForwardedAddr(s string) (netip.Addr, bool) {
	if s == "" || len(s) > 64 {
		return netip.Addr{}, false
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		ap, err2 := netip.ParseAddrPort(s)
		if err2 != nil {
			return netip.Addr{}, false
		}
		a = ap.Addr()
	}
	if a.Zone() != "" {
		return netip.Addr{}, false
	}
	a = a.Unmap()
	if !a.IsValid() || a.IsUnspecified() {
		return netip.Addr{}, false
	}
	return a, true
}
