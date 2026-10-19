# Stage 15 — Known issues

| ID | Issue | Impact | Status |
|---|---|---|---|
| KI-S15-01 | Acceptance of Stages 1–14 not recorded (Stage 9 PARTIAL; 10–14 PASS WITH CONDITIONS proposed) | governance | EXTERNAL_BLOCKER (owner) |
| KI-S15-02 | No e-mail, SMS or WhatsApp provider selected; `LiveMessagingEnabled = false`; provider rows CHECK-limited to TEST | no message leaves FundZim outside development | EXTERNAL_BLOCKER (LR-010, LR-011, LR-023, PCR) |
| KI-S15-03 | Consent policy text `consent-v1-engineering-placeholder` is not legal wording; whether service SMS/WhatsApp needs opt-in and whether e-mail service messages need consent are open | consent records would need reconfirmation after the wording changes (supported) | LEGAL_REVIEW_REQUIRED (LR-023), PRIVACY_REVIEW_REQUIRED |
| KI-S15-04 | Marketing is disabled in code; no marketing consent can be given | by design until LR-023 | EXTERNAL_BLOCKER |
| KI-S15-05 | No retention period for notifications, deliveries, attempts, provider events or consent records; nothing is purged | storage grows | LEGAL_REVIEW_REQUIRED (LR-012) |
| KI-S15-06 | English only; no Shona or Ndebele versions (no reviewed translations) | accessibility of language | OPEN (content owner) |
| KI-S15-07 | Retry, backlog, error-rate and dead-letter thresholds are engineering defaults ([retries.md](retries.md)) | not service commitments | OPEN (as KI-S14-03) |
| KI-S15-08 | SMTP (development) has no idempotency key: an ambiguous SMTP outcome can duplicate a development e-mail | dev only; production adapters must support keys or lookup | ACCEPTED (dev) |
| KI-S15-09 | Inbound opt-out keywords (e.g. STOP) are not handled: they depend on the provider | consent withdrawal is in settings only | DEFERRED (provider) |
| KI-S15-10 | Unread counts are polled every 60 s (no push) | up to 60 s staleness | ACCEPTED |
| KI-S15-11 | No quiet hours, digests or per-user locale | not required in Stage 15 | DEFERRED |
| KI-S15-12 | A failure confined to the consent query is not separately fault-injected; the fail-closed path (`ROUTING_CHECK_FAILED`) is covered by code review and the database-outage case | test depth | OPEN |
| KI-S15-13 | Staff receive in-app notifications only (except security e-mail about their own account); no paging for incidents | KI-S14-11 partly remains | DEFERRED (provider) |
| KI-S15-14 | The mock E2E API (`e2e/mock-api/notifications.mjs`) is a simplified stand-in shared by parallel projects; the specs use their own records; real coverage is `TestNotifications*` | E2E realism | ACCEPTED |
| KI-S15-15 | One run of the full mock Playwright suite timed out `staff-payments.spec.ts` (desktop) under load; 30/30 repeat runs passed | flaky under load | OPEN — monitor |
| KI-S15-16 | Guest donors (no account) receive no donation notification (no recipient; e-mail to an unverified guest address is not sent) | donor experience | DEFERRED (needs a verified guest-contact design) |
| KI-S15-17 | No tax receipts: tax-deductibility of donations is not established | no receipt wording claims deductibility | LEGAL_REVIEW_REQUIRED |
| KI-S15-18 | Stage 13 / 14 external blockers remain (screening provider, STR duties, SLA targets, access-review cadence) | production readiness | EXTERNAL_BLOCKER |
| KI-S15-19 | Stage 11 observation: 11 TEST payouts (amount 1009, created 2026-10-14) keep being redispatched by the compose worker — 720 CREATE_PAYOUT attempts on one, all `DEFINITELY_NOT_SENT` (safe to retry under the Stage 11 rules; no UNKNOWN payout was resubmitted). There is no retry cap for definitely-not-sent attempts. `TestPayoutUnknownOutcomeKeepsReservationAndIsNeverResubmitted` counted these in `DispatchDue` and failed once; it now checks its own payout | payouts module (not changed in Stage 15) | OPEN (payouts owner: retry cap) |

## Stage 14 items addressed in Stage 15

| ID | Resolution |
|---|---|
| KI-S14-02 | PARTLY FIXED — the notification error rate is measured from `notification_attempts` (NOT_AVAILABLE without attempts); application-wide error rates remain NOT_AVAILABLE |
| KI-S14-10 | FIXED (in-app) — staff tasks (assignments, approvals waiting, escalations, payout exceptions) create persisted in-app notifications; external delivery waits for providers |
| KI-S14-11 | PARTLY FIXED — incident declarations notify incident managers in-app; paging and status pages remain deferred |
