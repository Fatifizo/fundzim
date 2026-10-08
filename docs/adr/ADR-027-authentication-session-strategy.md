# ADR-027: Authentication and session strategy

- **Status:** Accepted
- **Date:** 2026-10-08
- **Deciders:** Technical lead (Stage 2); pending project-owner acceptance
- **Stage:** 2 (design); implementation Stage 4

## Context

SECURITY §4–§6 described OTP-first user login, mandatory phishing-resistant MFA for staff, and opaque server
sessions. Stage 2 must confirm the strategy, choose between session and token authentication, and fix the data
model.

## Decision

1. **Opaque server-side sessions** for browsers (users and staff). The cookie is `__Host-fz_session`
   (HttpOnly, Secure, SameSite=Lax). Only `SHA-256(token)` is stored, in `app.sessions`. **No JWT access or
   refresh tokens** for browser clients. "Refresh" is sliding idle expiry within an absolute lifetime.
   Revocation is a database update, effective immediately.
2. **Users:** primary factor is an OTP to a verified phone or email (HMAC-stored, purpose-bound,
   single-use). An optional password uses Argon2id. Step-up (a fresh OTP or MFA) is required for sensitive
   actions.
3. **Staff:** separate `STAFF` accounts. Mandatory MFA (WebAuthn preferred, TOTP fallback, **no SMS**).
   Shorter sessions. Step-up for approvals.
4. **Machine credentials** (future mobile or partner APIs) need a new ADR. Provider webhooks are
   authenticated by provider signature (ADR-029), never by sessions.
5. **Authorization:** deny-by-default RBAC permissions for staff, plus ownership and organisation-role
   predicates for user resources, evaluated in the service layer. Object access goes through
   repository-scoped queries (IDOR defence).

## Consequences

### Positive
- Instant revocation; no token-leak replay window; simple CSRF model (same-origin).

### Negative / costs
- A database lookup per request (index on `token_hash`; a cache is possible only as a non-authoritative
  optimisation, with revocation checked against the database).
- SMS cost and toll-fraud risk, controlled by rate limits (SECURITY §10.3).

### Follow-up work
- Stage 4 implements this. Staff SSO may replace local staff auth later without changing permissions.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| JWT access + refresh tokens | Revocation and size problems; no need for stateless tokens in a same-origin monolith |
| Third-party IdP for users now | Cost, data residency (LR-011) and OTP/USSD-friendly flows; can be revisited |
| SMS as a staff factor | SIM-swap risk (SECURITY §4.2) |

## Security implications

Central to account-takeover defence. Changing the primary factor revokes all sessions and places a payout hold
plus cooling-off.

## Financial implications

Step-up and maker-checker gate payout and refund actions (ADR-017).

## Related

ADR-017; [authentication-authorization.md](../security/authentication-authorization.md),
[identity-schema.md](../database/identity-schema.md); SECURITY §4–§7.
