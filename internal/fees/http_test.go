package fees

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ratelimit"
)

type fakeLimiter struct {
	allow  bool
	policy string
	key    string
}

func (f *fakeLimiter) Allow(_ context.Context, p ratelimit.Policy, key string) (ratelimit.Decision, error) {
	f.policy, f.key = p.Name, key
	return ratelimit.Decision{Allowed: f.allow, RetryAfter: 30 * time.Second}, nil
}

func TestThrottleUsesTheFeeQuotePolicyPerUser(t *testing.T) {
	lim := &fakeLimiter{allow: false}
	h := &HTTP{S: &Service{}, Limiter: lim}
	r := httptest.NewRequest("POST", "/", nil)
	r = r.WithContext(authz.WithPrincipal(r.Context(), &authz.Principal{UserID: "u1", Kind: authz.KindUser}))
	var rl rateLimited
	if err := h.throttle(r); !errors.As(err, &rl) || rl.RetryAfter() != 30*time.Second {
		t.Fatalf("throttled request: %v", err)
	}
	if lim.policy != "fees.quote.user" || lim.key != "u1" {
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

// Every staff route needs a fee permission; every staff mutation one that requires step-up (fee.config.*,
// fee.adjust.*) or the fixture permission; user routes are personal-account routes; no public route exists and no
// route posts free-form journals.
func TestRoutePolicies(t *testing.T) {
	stepUp := map[string]bool{"fee.config.request": true, "fee.config.approve": true, "fee.adjust.request": true, "fee.adjust.approve": true}
	for _, rt := range (&HTTP{S: &Service{}}).Routes() {
		method, path, _ := strings.Cut(rt.Pattern, " ")
		admin := strings.HasPrefix(path, "/api/v1/admin/")
		switch {
		case admin && rt.Policy.Kind != httpx.KindPermission:
			t.Errorf("%s: staff route without a permission", rt.Pattern)
		case admin && method != "GET" && !stepUp[rt.Policy.Permission] && rt.Policy.Permission != "fee.fixtures":
			t.Errorf("%s: staff mutation with %s (needs a step-up permission)", rt.Pattern, rt.Policy.Permission)
		case admin && method == "GET" && rt.Policy.Permission != "fee.view":
			t.Errorf("%s: staff read with %s", rt.Pattern, rt.Policy.Permission)
		case !admin && rt.Policy.Kind != httpx.KindUser:
			t.Errorf("%s: user route with policy %s", rt.Pattern, rt.Policy)
		}
		if strings.Contains(path, "journal") || strings.Contains(path, "posting") {
			t.Errorf("%s: no fee route may post free-form journals", rt.Pattern)
		}
		if method == "POST" && (strings.HasSuffix(path, "/quotes") || strings.HasSuffix(path, "/policies") || strings.HasSuffix(path, "/versions")) &&
			!rt.Idempotent {
			t.Errorf("%s: creation must be idempotent", rt.Pattern)
		}
	}
}
