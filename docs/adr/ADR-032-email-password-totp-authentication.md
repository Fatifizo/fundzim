# ADR-032: Email + password primary login with TOTP MFA (amends ADR-027)

- **Status:** Accepted
- **Date:** 2026-10-08
- **Deciders:** Project owner (Stage 4 brief), technical lead
- **Stage:** 4

## Context

ADR-027 and SECURITY §4 made a one-time code to a verified phone or email the users' primary factor, with an
optional password, and required WebAuthn (TOTP fallback) for staff. The Stage 4 brief, issued later by the
project owner, specifies email + password registration and login, email verification, password reset, TOTP
MFA with recovery codes, and phone verification through a provider abstraction. No SMS provider is selected
(Stage 15), so OTP login would depend on a development fake in every environment.

## Decision

1. **Users:** the primary factor is **email + password** (Argon2id, PHC string). The email must be verified
   for features that need it (organisation creation in Stage 4; more later). Optional **TOTP MFA** with ten
   one-time recovery codes. OTP **login** (phone or email) is deferred; the Stage 2 `/auth/otp*` endpoints
   stay PLANNED.
2. **Phone numbers** are verified with a one-time SMS code through `notifications.SMSSender` (development
   provider only). A verified phone is contact information: it does **not** imply identity or payout
   verification and is not a login factor (SIM-swap risk, SECURITY §4.1).
3. **Staff:** password + **mandatory TOTP**. A staff session cannot exist without MFA (database CHECK).
   WebAuthn is preferred by I-6 but deferred; TOTP is the accepted fallback. Staff accounts are created only
   by invitation (or the audited bootstrap command), and MFA enrolment is part of accepting the invitation.
4. **Sessions** are unchanged from ADR-027: opaque server-side sessions, SHA-256 stored, HttpOnly cookie, idle
   and absolute expiry, rotation at login and after MFA. A login that needs MFA creates **no session**: it creates
   a short-lived MFA challenge (HttpOnly cookie, hashed server-side, attempt-limited).
5. **CSRF:** Origin check on every unsafe request, plus a session-bound token (cookie + `X-CSRF-Token`) when a
   session cookie is present.

## Consequences

### Positive
- Works without an SMS provider; standard, password-manager-friendly flows; MFA available to every user.

### Negative / costs
- Passwords become a primary credential: Argon2id cost, breached-password screening, throttling and reset flows
  are now in scope (implemented in Stage 4).
- Low-literacy, phone-first users are served less well than with OTP login; revisit when an SMS provider exists.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Keep OTP-first login (ADR-027) | No real SMS channel; contradicts the owner's Stage 4 brief |
| Passkeys/WebAuthn for everyone now | Larger scope; device support for the target users unverified; planned later |

## Security implications

Password guessing becomes the main attack: per-account and per-IP throttles (distributed), generic errors,
breached-password checks, Argon2id with bounded concurrency, and MFA. No permanent lockouts (DoS); throttles
decay.

## Related

ADR-017, ADR-027 (amended), docs/stage-4/authentication-architecture.md, SECURITY §4–§6.
