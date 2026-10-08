package httpx

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"regexp"

	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// HeaderRequestID is the request correlation header (OBSERVABILITY.md).
const HeaderRequestID = "X-Request-ID"

type requestIDKey struct{}

// validInboundID limits what we accept from a trusted proxy: printable, short, no spaces or separators
// that could forge log fields.
var validInboundID = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,64}$`)

// RequestID returns the request ID stored in ctx, or "" if none.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// WithRequestID returns ctx carrying id.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestIDMiddleware assigns every request an ID. An inbound X-Request-ID is accepted only when the
// direct peer is a trusted proxy and the value is well-formed; otherwise a new UUIDv7 is generated.
// The ID is echoed in the response header.
func RequestIDMiddleware(trusted []netip.Prefix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := ""
			if in := r.Header.Get(HeaderRequestID); in != "" && validInboundID.MatchString(in) && peerTrusted(r, trusted) {
				id = in
			}
			if id == "" {
				id = ids.New()
			}
			w.Header().Set(HeaderRequestID, id)
			next.ServeHTTP(w, r.WithContext(WithRequestID(r.Context(), id)))
		})
	}
}

// ClientIP returns the client address. X-Forwarded-For is honoured only when the direct peer is a
// trusted proxy; the right-most untrusted address is used.
func ClientIP(r *http.Request, trusted []netip.Prefix) netip.Addr {
	peer := peerAddr(r)
	if !inPrefixes(peer, trusted) {
		return peer
	}
	xff := r.Header.Values("X-Forwarded-For")
	var hops []string
	for _, v := range xff {
		for _, part := range splitComma(v) {
			hops = append(hops, part)
		}
	}
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(hops[i])
		if err != nil {
			break
		}
		a = a.Unmap()
		if !inPrefixes(a, trusted) {
			return a
		}
	}
	return peer
}

func peerTrusted(r *http.Request, trusted []netip.Prefix) bool {
	return inPrefixes(peerAddr(r), trusted)
}

func peerAddr(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return a.Unmap()
}

func inPrefixes(a netip.Addr, ps []netip.Prefix) bool {
	if !a.IsValid() {
		return false
	}
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func splitComma(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			part := s[start:i]
			for len(part) > 0 && part[0] == ' ' {
				part = part[1:]
			}
			for len(part) > 0 && part[len(part)-1] == ' ' {
				part = part[:len(part)-1]
			}
			if part != "" {
				out = append(out, part)
			}
			start = i + 1
		}
	}
	return out
}
