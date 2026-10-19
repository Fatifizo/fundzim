# Stage 15 — Notification preferences

`/dashboard/notifications/preferences` (`GET/PUT /api/v1/notifications/preferences`, users only, rate-limited).
Preferences replace the Stage 6 `/me/notification-preferences` endpoint (removed from the contract; the web app had
no page for it).

| Category | Channels | Default on | Cannot be turned off |
|---|---|---|---|
| SECURITY | in-app, e-mail, SMS | in-app, e-mail | in-app, e-mail |
| TRANSACTIONAL | in-app, e-mail | in-app, e-mail | in-app |
| CAMPAIGN, VERIFICATION, PAYMENT, WITHDRAWAL, PAYOUT | in-app, e-mail, SMS, WhatsApp | in-app, e-mail | in-app |
| SUPPORT | in-app, e-mail | in-app, e-mail | in-app |
| MARKETING | (none available) | — | — (marketing is disabled) |
| RISK, COMPLIANCE, ADMINISTRATIVE | staff in-app only | — | not shown to users |

Rules:

* A mandatory channel cannot be turned off: the API answers 422 `NOTIFICATION_MANDATORY`, and the
  `notification_preferences` table has a CHECK that refuses a disabled mandatory row.
* Turning SMS or WhatsApp on is not enough: those channels also need a consent record ([consent.md](consent.md)); the
  form says "Also needs your consent below".
* Every change is stored in `notification_preference_events` (old and new value, actor, time) and audited.
* Preferences are re-checked before every send, so turning a channel off stops queued and retrying deliveries
  (`PREFERENCE_DISABLED`).
* Quiet hours and digests are not available (not required in this stage; no timing rule was invented).
