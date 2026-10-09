package httpx

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBodyLimitWithExemptions(t *testing.T) {
	read := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	h := BodyLimitWithExemptions(10, []BodyLimitExemption{{Pattern: "POST /api/v1/verification/documents", Max: 100}},
		slog.New(slog.DiscardHandler))(read)
	cases := []struct {
		method, path string
		size         int
		chunked      bool
		want         int
	}{
		{"POST", "/api/v1/verification/documents", 50, false, http.StatusNoContent},
		{"POST", "/api/v1/verification/documents", 50, true, http.StatusNoContent},
		{"POST", "/api/v1/verification/documents", 101, false, http.StatusRequestEntityTooLarge},
		{"POST", "/api/v1/verification/documents", 101, true, http.StatusRequestEntityTooLarge},
		{"PUT", "/api/v1/verification/documents", 50, false, http.StatusRequestEntityTooLarge},    // other method: global cap
		{"POST", "/api/v1/verification/documents/x", 50, false, http.StatusRequestEntityTooLarge}, // other path
		{"POST", "/api/v1/auth/login", 50, true, http.StatusRequestEntityTooLarge},                // global cap, chunked
		{"POST", "/api/v1/auth/login", 10, false, http.StatusNoContent},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, strings.NewReader(strings.Repeat("a", c.size)))
		if c.chunked {
			r.ContentLength = -1
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%s %s %d chunked=%v: %d, want %d", c.method, c.path, c.size, c.chunked, w.Code, c.want)
		}
	}
}

func TestBodyLimitWithoutExemptionsMatchesBodyLimit(t *testing.T) {
	h := BodyLimitWithExemptions(5, nil, slog.New(slog.DiscardHandler))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/x", strings.NewReader("123456")))
	if w.Code != http.StatusRequestEntityTooLarge || !strings.Contains(w.Body.String(), "PAYLOAD_TOO_LARGE") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
