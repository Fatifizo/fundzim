# Stage 15 — Consent

LR-023 (consent for marketing, transactional SMS/WhatsApp and WhatsApp Business policies) is **open**: no Zimbabwean
rule was assumed. The engineering stance until counsel answers is the stricter one for channels that reach a phone, and
the Stage 4–6 behaviour for e-mail service messages. **LEGAL_REVIEW_REQUIRED**, **PRIVACY_REVIEW_REQUIRED**.

| Purpose | Channel | Needed to send |
|---|---|---|
| SERVICE_MESSAGES | in-app | nothing (the account's own inbox) |
| SERVICE_MESSAGES | e-mail | a verified address (no consent record — pending LR-023) |
| SERVICE_MESSAGES | SMS, WhatsApp | a GRANTED consent record with the current policy version |
| MARKETING | any | not possible: `MarketingEnabled = false`; a grant is refused (`MARKETING_NOT_AVAILABLE`) |

## Records

`app.communication_consents` (current state per user, channel and purpose) and `app.communication_consent_events`
(append-only history). States: `NOT_REQUESTED`, `GRANTED`, `WITHDRAWN`, `EXPIRED`, `REQUIRES_RECONFIRMATION`.
Each grant stores the **policy version** (`consent-v1-engineering-placeholder` — the wording is a placeholder pending
legal review) and an **evidence reference** (the server-assigned request id of the user's own settings request).

* **Only the user grants.** The grant source is `USER_SETTINGS` (database CHECK); there is no staff, import or API
  path that records consent for someone. Staff with `communications.consent.view` (COMPLIANCE) can read the overview
  (`/admin/communications/consent`), not change it.
* **Withdrawal is immediate:** the next routing decision (also re-run before each send and each retry) suppresses
  `CONSENT_WITHDRAWN`, including a delivery already queued. Tested: "consent withdrawal during delivery".
* **Policy change:** a grant under an older policy version is treated as `REQUIRES_RECONFIRMATION` (suppressed until the
  user confirms again; the UI shows "Confirm again").
* **Expiry:** an `expires_at` column exists; no expiry period is set (none was confirmed).
* A consent or suppression lookup error fails closed. At creation the whole notification fails and the outbox
  redelivers the event (fault-injected in `TestNotificationsConsentLookupFailureFailsClosed`); before a send the delivery
  becomes FAILED_RETRYABLE `ROUTING_CHECK_FAILED` and nothing is sent (`deliver.go`; code review — KI-S15-12).
* Consent changes are rate-limited (`notifications.consent.user`, 30 per hour) and audited.
* Consent records are never deleted; retention is LR-012.

## Monitoring

`notifications.consent_violations` counts external SMS/WhatsApp/marketing deliveries sent in the last 24 h without a
GRANTED record — it should be 0, and a non-zero value raises the SEV1 alert `NOTIFICATION_CONSENT_ENFORCEMENT`.
