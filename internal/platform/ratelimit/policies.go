package ratelimit

import (
	"math"
	"time"
)

// Policies holds the named Stage 4 limits in one place so they can be tuned together.
//
// Every value here is an INTERNAL_RISK starting point (CLAUDE.md "Regulatory rules": limits are
// configuration, not legal thresholds). Source: docs/api/api-design.md §8 RL classes and SECURITY §10,
// adapted for Stage 4 identity endpoints. Approval owner: security lead. Review: Stage 18 load and
// abuse testing. They are not regulatory values.
//
// All policies use GCRA: Limit events may happen at once, then capacity refills continuously at
// Limit/Window, so a throttled key always recovers (no permanent lockouts). All are Protective: on a
// Valkey outage they keep limiting per replica through the in-process fallback.
//
// Keys: "ip" policies use ByClientIP (or IPKey); "account" policies use the normalised email address
// (the caller normalises; it is hashed before storage); per-user policies use the user ID; OTP-by-phone
// uses the E.164 number (hashed before storage).
type Policies struct {
	GlobalIP                 Policy // global.ip — coarse per-IP ceiling on every public request
	AuthLoginIP              Policy // auth.login.ip — login attempts per IP
	AuthLoginAccount         Policy // auth.login.account — login attempts per normalised email
	AuthRegisterIP           Policy // auth.register.ip — registrations per IP
	AuthPasswordResetIP      Policy // auth.password_reset.ip — forgot-password requests per IP
	AuthPasswordResetAccount Policy // auth.password_reset.account — forgot-password per normalised email
	AuthVerifyResendAccount  Policy // auth.verify_resend.account — resend verification per normalised email
	AuthMFAChallenge         Policy // auth.mfa.challenge — MFA code attempts per (challenge ID + user ID)
	AuthOTPSendPhone         Policy // auth.otp.send.phone — SMS OTP sends per destination phone
	AuthOTPSendUser          Policy // auth.otp.send.user — SMS OTP sends per user
	AuthOTPVerify            Policy // auth.otp.verify — OTP verification attempts per user (or challenge)
	AuthStepUp               Policy // auth.step_up — step-up verification attempts per user
	AuthTokenIP              Policy // auth.token.ip — link-token submissions (verify email, reset, staff invitation) per IP
	AuthVerifyResendIP       Policy // auth.verify_resend.ip — resend-verification requests per IP
}

// DefaultPolicies returns the Stage 4 starting values.
func DefaultPolicies() Policies {
	return Policies{
		// 40 at once, 20/s sustained: the Stage 3 defaults (RATE_LIMIT_RPS=20, RATE_LIMIT_BURST=40).
		// Use GlobalIPFromRate to derive it from config instead.
		GlobalIP:                 Policy{Name: "global.ip", Limit: 40, Window: 2 * time.Second, Protective: true},
		AuthLoginIP:              Policy{Name: "auth.login.ip", Limit: 20, Window: 5 * time.Minute, Protective: true},
		AuthLoginAccount:         Policy{Name: "auth.login.account", Limit: 5, Window: 15 * time.Minute, Protective: true},
		AuthRegisterIP:           Policy{Name: "auth.register.ip", Limit: 5, Window: time.Hour, Protective: true},
		AuthPasswordResetIP:      Policy{Name: "auth.password_reset.ip", Limit: 5, Window: time.Hour, Protective: true},
		AuthPasswordResetAccount: Policy{Name: "auth.password_reset.account", Limit: 3, Window: time.Hour, Protective: true},
		AuthVerifyResendAccount:  Policy{Name: "auth.verify_resend.account", Limit: 3, Window: time.Hour, Protective: true},
		AuthMFAChallenge:         Policy{Name: "auth.mfa.challenge", Limit: 5, Window: 5 * time.Minute, Protective: true},
		AuthOTPSendPhone:         Policy{Name: "auth.otp.send.phone", Limit: 3, Window: time.Hour, Protective: true},
		AuthOTPSendUser:          Policy{Name: "auth.otp.send.user", Limit: 5, Window: time.Hour, Protective: true},
		AuthOTPVerify:            Policy{Name: "auth.otp.verify", Limit: 5, Window: 15 * time.Minute, Protective: true},
		AuthStepUp:               Policy{Name: "auth.step_up", Limit: 5, Window: 15 * time.Minute, Protective: true},
		AuthTokenIP:              Policy{Name: "auth.token.ip", Limit: 20, Window: 15 * time.Minute, Protective: true},
		AuthVerifyResendIP:       Policy{Name: "auth.verify_resend.ip", Limit: 10, Window: time.Hour, Protective: true},
	}
}

// All lists every policy (validation at start-up, documentation tests).
func (ps Policies) All() []Policy {
	return []Policy{ps.GlobalIP, ps.AuthLoginIP, ps.AuthLoginAccount, ps.AuthRegisterIP, ps.AuthPasswordResetIP,
		ps.AuthPasswordResetAccount, ps.AuthVerifyResendAccount, ps.AuthMFAChallenge, ps.AuthOTPSendPhone,
		ps.AuthOTPSendUser, ps.AuthOTPVerify, ps.AuthStepUp, ps.AuthTokenIP, ps.AuthVerifyResendIP}
}

// Validate checks every policy.
func (ps Policies) Validate() error {
	for _, p := range ps.All() {
		if err := p.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// GlobalIPFromRate builds the global.ip policy from a sustained rate and a burst (config
// RATE_LIMIT_RPS / RATE_LIMIT_BURST): Limit = burst, Window = burst/rps.
func GlobalIPFromRate(rps float64, burst int) Policy {
	if burst < 1 {
		burst = 1
	}
	if rps <= 0 || math.IsNaN(rps) || math.IsInf(rps, 0) {
		rps = 1
	}
	w := time.Duration(float64(burst) / rps * float64(time.Second))
	if w < time.Millisecond {
		w = time.Millisecond
	}
	return Policy{Name: "global.ip", Limit: burst, Window: w, Protective: true}
}
