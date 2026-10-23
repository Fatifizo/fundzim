//go:build integration

package integration

// Stage 18 authentication hardening (docs/stage-18/authentication-security.md).

import (
	"os"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/config"
)

// F-S18-01: staff step up with TOTP only, so a password change must neither work on a stale step-up nor refresh it.
func TestSecurityStaffPasswordChangeNeedsTOTPStepUp(t *testing.T) {
	need(t, "DATABASE_MIGRATION_URL")
	s := newITServer(t, nil)
	f := staffFixture(t, s)
	a, b := s.browser(t), s.browser(t)
	a.staffLogin(f.adminA.email, f.adminA.secret)
	b.staffLogin(f.adminB.email, f.adminB.secret)
	staff := inviteStaff(t, s, a, b, "Password Change Staff", "") // its own account: the shared fixtures keep their passwords

	short := newITServer(t, func(c *config.Config) { c.Auth.StepUpMaxAge = 1500 * time.Millisecond })
	c := short.browser(t)
	c.staffLogin(staff.email, staff.secret)
	time.Sleep(2 * time.Second) // the login's step-up is now stale

	mig := pool(t, os.Getenv("DATABASE_MIGRATION_URL"))
	stepUpAt := func() time.Time {
		var at time.Time
		if err := mig.QueryRow(ctx(t), `SELECT step_up_at FROM app.sessions WHERE user_id = $1 AND revoked_at IS NULL
			ORDER BY created_at DESC LIMIT 1`, staff.id).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}
	before := stepUpAt()
	newPW := itPassword + "-rotated"
	c.expect(c.do("POST", "/me/password", map[string]string{"current_password": itPassword, "new_password": newPW}), 403, "STEP_UP_REQUIRED")
	if !stepUpAt().Equal(before) {
		t.Fatal("a refused password change refreshed the step-up")
	}
	// password-only step-up stays refused for staff
	c.expect(c.do("POST", "/auth/step-up/verify", map[string]string{"password": itPassword}), 401, "STEP_UP_FAILED")

	// the login consumed this 30 s TOTP step (replay protection): wait for the next one
	step := time.Now().Unix() / 30
	for time.Now().Unix()/30 == step {
		time.Sleep(250 * time.Millisecond)
	}
	c.expect(c.do("POST", "/auth/step-up/verify", map[string]string{"code": totpCode(t, staff.secret)}), 200, "")
	stepped := stepUpAt()
	c.expect(c.do("POST", "/me/password", map[string]string{"current_password": itPassword, "new_password": newPW}), 204, "")
	if !stepUpAt().Equal(stepped) {
		t.Fatal("a staff password change refreshed the step-up (password must not stand in for TOTP)")
	}
	time.Sleep(2 * time.Second)
	c.expect(c.do("POST", "/me/password", map[string]string{"current_password": newPW, "new_password": itPassword}), 403, "STEP_UP_REQUIRED")
}
