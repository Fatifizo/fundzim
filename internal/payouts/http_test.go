package payouts

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/ratelimit"
)

type fakeLimiter struct {
	allow  bool
	policy string
	key    string
}

func (f *fakeLimiter) Allow(_ context.Context, p ratelimit.Policy, key string) (ratelimit.Decision, error) {
	f.policy, f.key = p.Name, key
	return ratelimit.Decision{Allowed: f.allow, RetryAfter: 42 * time.Second}, nil
}

func TestThrottleUsesThePayoutRequestPolicyPerUser(t *testing.T) {
	lim := &fakeLimiter{allow: false}
	h := &HTTP{S: &Service{}, Limiter: lim}
	r := httptest.NewRequest("POST", "/", nil)
	r = r.WithContext(authz.WithPrincipal(r.Context(), &authz.Principal{UserID: "u1", Kind: authz.KindUser}))
	err := h.throttle(r)
	var rl rateLimited
	if !errors.As(err, &rl) || rl.RetryAfter() != 42*time.Second {
		t.Fatalf("throttled request: %v", err)
	}
	if lim.policy != "payouts.request.user" || lim.key != "u1" {
		t.Fatalf("policy %s key %s", lim.policy, lim.key)
	}
	lim.allow = true
	if err := h.throttle(r); err != nil {
		t.Fatal(err)
	}
	if err := (&HTTP{S: &Service{}}).throttle(r); err == nil {
		t.Fatal("a missing limiter must fail closed")
	}
}
