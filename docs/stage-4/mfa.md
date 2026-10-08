# Stage 4 Multi-Factor Authentication and Phone Verification

Code: `internal/auth/mfa.go`, `internal/auth/phone.go`, `internal/platform/crypto`. Schema: `app.mfa_methods`,
`app.recovery_codes`, `app.mfa_login_challenges`, `app.otp_challenges` (migration `20261008140100`).

## 1. TOTP

- RFC 6238, SHA-1, 6 digits, 30-second steps (authenticator-app compatible); ±1 step tolerated.
- **Replay protection:** each method stores `totp_last_used_step`; a code for a step at or before it is
  rejected, so an observed code cannot be reused (tested: the integration helper must wait for fresh steps).
- **Secret storage:** AES-256-GCM with the field-encryption key; additional authenticated data = the method's
  row ID, so a ciphertext cannot be moved to another row. The key ID is stored with it. The secret is returned
  **once** at enrolment (and as an `otpauth://` URI); the web page renders the QR code locally.
- **Enrolment:** `POST /me/mfa/enroll` (step-up) creates an unconfirmed method (a previous unconfirmed one is
  discarded); `POST /me/mfa/confirm` with a valid code activates it, disables any older confirmed method,
  issues ten recovery codes, revokes the user's other sessions and emails a security alert.
- **Disable:** personal accounts only, step-up plus a current TOTP or recovery code; staff get
  `403 MFA_REQUIRED_FOR_ROLE`. Revokes other sessions; alert email.

## 2. Login with MFA

1. Correct password → no session; a challenge (`app.mfa_login_challenges`, token hash) and an HttpOnly,
   SameSite=Strict `fz_mfa` cookie; response `{status: "mfa_required", methods: ["totp","recovery_code"]}`.
2. `POST /auth/mfa/verify {code}` or `/auth/mfa/recovery {recovery_code}` → session (rotated), audit event.
3. Each wrong answer counts on the challenge; the **fifth** failure invalidates it (DB CHECK caps attempts at
   5), plus the `auth.mfa.challenge` throttle. A dead challenge never succeeds, even with a correct code
   (tested). Password reset and other security changes invalidate pending challenges.

## 3. Recovery codes

Ten codes `xxxxx-xxxxx` (≈ 50 bits each), stored as `HMAC(blind-index key, "recovery_code", user_id, code)`,
single use (atomic `UPDATE … WHERE used_at IS NULL`; two concurrent uses → exactly one succeeds, tested).
Regenerating (`POST /me/mfa/recovery-codes`, step-up) supersedes the batch. Using a code emails an alert.

## 4. Phone verification (development provider only)

- Input normalised with libphonenumber (`nyaruka/phonenumbers`), default region **ZW**; only valid mobile
  (or fixed-line-or-mobile) numbers; stored as E.164.
- `POST /me/phone/verify-request {phone}` → 6-digit code via `notifications.SMSSender` (`dev_mailpit`: the SMS
  becomes an email to `<digits>@sms.dev.invalid` in Mailpit; production refuses this provider). The code is
  stored as an HMAC; the destination as an HMAC. ≤ 5 minutes, ≤ 5 attempts, a resend invalidates the previous
  code, throttles per user and per number. The SMS is sent **after** the challenge commits (never inside a
  transaction); if sending fails the challenge is invalidated and the caller gets 503.
- `POST /me/phone/verify-confirm {phone, code}` → the number becomes the verified primary phone; a number
  verified by another account → `409 PHONE_IN_USE` (unique index).
- A verified phone is contact data only — not a login factor, not identity or payout verification.

## 5. Not in Stage 4

WebAuthn/passkeys (preferred for staff by baseline I-6; TOTP is the accepted fallback per ADR-032), SMS as a
second factor (not planned: SIM-swap risk), real SMS provider (Stage 15), KMS-held keys (Stage 18).
