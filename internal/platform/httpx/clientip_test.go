package httpx

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/Fatifizo/fundzim/internal/platform/authz"
)

func TestClientIPResolution(t *testing.T) {
	trusted := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.30.0.10/32"),
		netip.MustParsePrefix("fd00::/8"),
	}
	cases := []struct {
		name  string
		peer  string
		xff   []string // one entry per header line
		other map[string]string
		want  string
	}{
		{name: "untrusted peer, no headers", peer: "203.0.113.5:4000", want: "203.0.113.5"},
		{name: "untrusted peer spoofs XFF", peer: "203.0.113.5:4000", xff: []string{"1.1.1.1"}, want: "203.0.113.5"},
		{name: "untrusted peer spoofs X-Real-IP and Forwarded", peer: "203.0.113.5:4000",
			other: map[string]string{"X-Real-IP": "1.1.1.1", "Forwarded": "for=1.1.1.1"}, want: "203.0.113.5"},
		{name: "untrusted peer spoofs trusted-looking XFF", peer: "203.0.113.5:4000", xff: []string{"10.0.0.9"}, want: "203.0.113.5"},
		{name: "trusted peer ignores X-Real-IP and Forwarded", peer: "10.0.0.1:80",
			other: map[string]string{"X-Real-IP": "1.1.1.1", "Forwarded": "for=1.1.1.1"}, want: "10.0.0.1"},
		{name: "trusted peer, single hop", peer: "172.30.0.10:80", xff: []string{"198.51.100.7"}, want: "198.51.100.7"},
		{name: "client-supplied left entries are not trusted", peer: "10.0.0.1:80",
			xff: []string{"1.2.3.4, 198.51.100.7, 10.0.0.2"}, want: "198.51.100.7"},
		{name: "multiple trusted proxies across header lines", peer: "10.0.0.1:80",
			xff: []string{"6.6.6.6, 198.51.100.7", "172.30.0.10", "10.0.0.3"}, want: "198.51.100.7"},
		{name: "IPv6 client through trusted IPv6 proxy", peer: "[fd00::1]:443",
			xff: []string{"2001:db8::42, fd00::2"}, want: "2001:db8::42"},
		{name: "IPv4-mapped IPv6 is unmapped", peer: "[::ffff:10.0.0.1]:80", xff: []string{"::ffff:198.51.100.7"}, want: "198.51.100.7"},
		{name: "entry with port", peer: "10.0.0.1:80", xff: []string{"198.51.100.7:5555"}, want: "198.51.100.7"},
		{name: "IPv6 entry with port", peer: "10.0.0.1:80", xff: []string{"[2001:db8::42]:5555"}, want: "2001:db8::42"},
		{name: "malformed right-most entry: peer wins", peer: "10.0.0.1:80", xff: []string{"1.1.1.1, garbage"}, want: "10.0.0.1"},
		{name: "malformed behind trusted hop: last trusted hop wins", peer: "10.0.0.1:80",
			xff: []string{"1.1.1.1, unknown, 10.0.0.7"}, want: "10.0.0.7"},
		{name: "empty entry is malformed", peer: "10.0.0.1:80", xff: []string{"1.1.1.1, , 10.0.0.7"}, want: "10.0.0.7"},
		{name: "empty header", peer: "10.0.0.1:80", xff: []string{""}, want: "10.0.0.1"},
		{name: "zone is malformed", peer: "10.0.0.1:80", xff: []string{"fe80::1%eth0"}, want: "10.0.0.1"},
		{name: "unspecified is malformed", peer: "10.0.0.1:80", xff: []string{"0.0.0.0"}, want: "10.0.0.1"},
		{name: "hostname is malformed", peer: "10.0.0.1:80", xff: []string{"evil.example"}, want: "10.0.0.1"},
		{name: "all hops trusted: left-most trusted hop", peer: "10.0.0.1:80", xff: []string{"10.0.0.5, 10.0.0.6"}, want: "10.0.0.5"},
		{name: "peer zone stripped", peer: "[fe80::1%eth0]:80", want: "fe80::1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tc.peer
			for _, v := range tc.xff {
				req.Header.Add("X-Forwarded-For", v)
			}
			for k, v := range tc.other {
				req.Header.Set(k, v)
			}
			if got := ClientIP(req, trusted); got.String() != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestClientIPHopCap(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:80"
	xff := "198.51.100.7"
	for i := 0; i < 40; i++ {
		xff += ", 10.0.0.9"
	}
	req.Header.Set("X-Forwarded-For", xff)
	if got := ClientIP(req, trusted).String(); got != "10.0.0.9" {
		t.Fatalf("hop cap: got %s", got)
	}
}

func TestClientIPMiddlewareStoresAddress(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	var got netip.Addr
	h := ClientIPMiddleware(trusted)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = authz.ClientIPFrom(r.Context())
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "203.0.113.5:1"
	req.Header.Set("X-Forwarded-For", "1.1.1.1")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got.String() != "203.0.113.5" {
		t.Fatalf("spoofed: got %s", got)
	}
	req = httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:1"
	req.Header.Set("X-Forwarded-For", "1.1.1.1")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got.String() != "1.1.1.1" {
		t.Fatalf("trusted: got %s", got)
	}
}
