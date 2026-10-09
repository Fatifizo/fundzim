package httpx

import (
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/errs"
)

func TestRequireIfMatch(t *testing.T) {
	for in, want := range map[string]int{`"3"`: 3, `7`: 7, `W/"12"`: 12, ` "4" `: 4} {
		r := httptest.NewRequest("PATCH", "/", nil)
		r.Header.Set("If-Match", in)
		if got, err := RequireIfMatch(r); err != nil || got != want {
			t.Errorf("%q: %d %v", in, got, err)
		}
	}
	for _, bad := range []string{"", `"x"`, `"0"`, `"-1"`, `*`, `"1", "2"`} {
		r := httptest.NewRequest("PATCH", "/", nil)
		if bad != "" {
			r.Header.Set("If-Match", bad)
		}
		if _, err := RequireIfMatch(r); !errors.Is(err, ErrPreconditionRequired) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestCachePolicyRespectedAndErrorsNeverCached(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	w.Header().Set("Cache-Control", "public, no-cache")
	WriteData(w, r, 200, map[string]int{"a": 1})
	if got := w.Header().Get("Cache-Control"); got != "public, no-cache" {
		t.Fatalf("handler cache policy overwritten: %q", got)
	}
	w = httptest.NewRecorder()
	WriteData(w, r, 200, nil)
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("default must be no-store: %q", got)
	}
	w = httptest.NewRecorder()
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("ETag", `"x"`)
	WriteError(w, r, slog.New(slog.NewTextHandler(io.Discard, nil)), ErrPreconditionFailed)
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("ETag") != "" || w.Code != 412 {
		t.Fatalf("error response cacheable: %d %v", w.Code, w.Header())
	}
}

type waitErr struct{ e *errs.Error }

func (w waitErr) Error() string           { return w.e.Error() }
func (waitErr) RetryAfter() time.Duration { return 721 * time.Second }
func (w waitErr) Unwrap() error           { return w.e }

func TestRetryAfterFromError(t *testing.T) {
	w := httptest.NewRecorder()
	WriteError(w, httptest.NewRequest("POST", "/", nil), slog.New(slog.NewTextHandler(io.Discard, nil)),
		waitErr{e: errs.New(errs.RateLimited, errs.CodeRateLimited, "slow down")})
	if w.Code != 429 || w.Header().Get("Retry-After") != "721" {
		t.Fatalf("Retry-After: %d %q", w.Code, w.Header().Get("Retry-After"))
	}
}
