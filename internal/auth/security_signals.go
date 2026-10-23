package auth

// Stage 18 (docs/stage-18/security-monitoring.md): authentication and privilege signals for the security scan. Counts
// over the identity module's own tables only (app.security_events, app.break_glass_grants, app.role_assignment_requests);
// no identifiers, addresses or user agents leave this function. A package function, not a method: the worker builds
// the security scan without the full auth service.

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/platform/opsview"
)

// SecuritySignalsPermission is the permission a viewer needs to see these metrics.
const SecuritySignalsPermission = "security.alert.view"

// SecuritySignals counts recent authentication failures, rate limiting, MFA changes, recovery-code use, active
// break-glass grants and approved role changes.
func SecuritySignals(ctx context.Context, pool *pgxpool.Pool, now time.Time) (opsview.Snapshot, error) {
	const m = "identity_security"
	snap := opsview.Snapshot{Module: m, Metrics: []opsview.Metric{}, Approvals: []opsview.Approval{}, Exceptions: []opsview.Exception{}}
	var loginFailed, targeted, mfaFailed, rateLimited, mfaRemoved, recoveryUsed, breakGlass, roleChanges int64
	err := pool.QueryRow(ctx, `WITH p AS (SELECT $1::timestamptz AS now) SELECT
		(SELECT count(*) FROM app.security_events WHERE event_type IN ('LOGIN_FAILED','PASSWORD_FAILED') AND occurred_at > (SELECT now FROM p) - interval '15 minutes'),
		(SELECT count(*) FROM (SELECT user_id FROM app.security_events WHERE event_type IN ('LOGIN_FAILED','PASSWORD_FAILED')
		   AND user_id IS NOT NULL AND occurred_at > (SELECT now FROM p) - interval '15 minutes' GROUP BY user_id HAVING count(*) >= 10) x),
		(SELECT count(*) FROM app.security_events WHERE event_type IN ('MFA_FAILED','STEP_UP_FAILED','OTP_VERIFY_FAILED')
		   AND occurred_at > (SELECT now FROM p) - interval '15 minutes'),
		(SELECT count(*) FROM app.security_events WHERE event_type IN ('RATE_LIMITED','LOGIN_BLOCKED') AND occurred_at > (SELECT now FROM p) - interval '15 minutes'),
		(SELECT count(*) FROM app.security_events WHERE event_type = 'MFA_REMOVED' AND occurred_at > (SELECT now FROM p) - interval '24 hours'),
		(SELECT count(*) FROM app.security_events WHERE event_type = 'RECOVERY_CODE_USED' AND occurred_at > (SELECT now FROM p) - interval '24 hours'),
		(SELECT count(*) FROM app.break_glass_grants WHERE revoked_at IS NULL AND expires_at > (SELECT now FROM p)),
		(SELECT count(*) FROM app.role_assignment_requests WHERE status = 'APPROVED' AND decided_at > (SELECT now FROM p) - interval '24 hours')`,
		now).Scan(&loginFailed, &targeted, &mfaFailed, &rateLimited, &mfaRemoved, &recoveryUsed, &breakGlass, &roleChanges)
	if err != nil {
		return snap, err
	}
	p := SecuritySignalsPermission
	snap.Metrics = append(snap.Metrics,
		opsview.Count(m, "auth.security.login_failed_15m", "Failed sign-ins and password checks (15 min)", loginFailed, p,
			"app.security_events LOGIN_FAILED, PASSWORD_FAILED", ""),
		opsview.Count(m, "auth.security.accounts_targeted_15m", "Accounts with ≥ 10 failed sign-ins (15 min)", targeted, p,
			"app.security_events per account", ""),
		opsview.Count(m, "auth.security.second_factor_failed_15m", "Failed MFA, step-up and OTP checks (15 min)", mfaFailed, p,
			"app.security_events MFA_FAILED, STEP_UP_FAILED, OTP_VERIFY_FAILED", ""),
		opsview.Count(m, "auth.security.rate_limited_15m", "Requests refused by rate limits (15 min)", rateLimited, p,
			"app.security_events RATE_LIMITED, LOGIN_BLOCKED", ""),
		opsview.Count(m, "auth.security.mfa_removed_24h", "MFA methods removed (24 h)", mfaRemoved, p, "app.security_events MFA_REMOVED", ""),
		opsview.Count(m, "auth.security.recovery_codes_used_24h", "Recovery codes used (24 h)", recoveryUsed, p,
			"app.security_events RECOVERY_CODE_USED", ""),
		opsview.Count(m, "auth.security.break_glass_active", "Active break-glass grants", breakGlass, p,
			"app.break_glass_grants not revoked, not expired", "/admin/staff"),
		opsview.Count(m, "auth.security.role_changes_24h", "Approved role changes (24 h)", roleChanges, p,
			"app.role_assignment_requests APPROVED", "/admin/staff"),
	)
	return snap, nil
}
