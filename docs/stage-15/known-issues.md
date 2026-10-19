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
| KI-S15-12 | Consent-lookup failure injection | test depth | **PARTLY FIXED**: `TestNotificationsConsentLookupFailureFailsClosed` injects a fault confined to the consent read (table locked, engine connections with a 300 ms `lock_timeout`, error 55P03 asserted): creation fails as a whole with no partial records and succeeds once after recovery. The send-time path (`ROUTING_CHECK_FAILED` → FAILED_RETRYABLE, nothing sent) is covered by code review only: on the shared database the compose worker would race the test for the delivery | OPEN (send-time path) |
| KI-S15-13 | Staff receive in-app notifications only (except security e-mail about their own account); no paging for incidents | KI-S14-11 partly remains | DEFERRED (provider) |
| KI-S15-14 | The mock E2E API (`e2e/mock-api/notifications.mjs`) is a simplified stand-in shared by parallel projects; the specs use their own records; real coverage is `TestNotifications*` | E2E realism | ACCEPTED |
| KI-S15-15 | `staff-payments.spec.ts` (desktop) timed out under parallel load. Cause (measured): one test ran nine full-page axe scans of the staff shell (≈ 1.5–2.4 s each, ≈ 15 s of 20 s when idle) under the 30 s budget, and the shell grew each stage; a second, latent cause was that the test expected its seeded UNKNOWN payment on page 1 of an oldest-first queue shared by parallel tests | flaky under load | **FIXED**: the test is split in two independent tests (same assertions and scans; 10–17 s each) and follows "Next page" to its own record; 72/72 repeat runs passed. No timeout was raised |
| KI-S15-16 | Guest donors (no account) receive no donation notification (no recipient; e-mail to an unverified guest address is not sent) | donor experience | DEFERRED (needs a verified guest-contact design) |
| KI-S15-17 | No tax receipts: tax-deductibility of donations is not established | no receipt wording claims deductibility | LEGAL_REVIEW_REQUIRED |
| KI-S15-18 | Stage 13 / 14 external blockers remain (screening provider, STR duties, SLA targets, access-review cadence) | production readiness | EXTERNAL_BLOCKER |
| KI-S15-19 | Stage 11: TEST payouts refused with DEFINITELY_NOT_SENT were resubmitted every 10 minutes without an effective limit (720 attempts on one development payout; none could have reached the provider) | unbounded provider calls | **FIXED** in the Stage 15 finalisation — see KI-S11-20: configurable budget with jittered backoff, SEV2 `SUBMISSION_RETRIES_EXHAUSTED` exception, dispatcher and database halt, operator resume with a fresh budget; reservations, journals and the UNKNOWN rule unchanged. All 10 historical payouts on the development database were halted on their next dispatch with no further provider call |
| KI-S15-20 | The communications dashboard and 30-day report recompute counts over the whole notification history on each request: p50 ≈ 165 ms / 330 ms at ≈ 130k notifications on the development stack (concurrency 8), growing with history | admin latency at scale | OPEN (Stage 17 scale; as KI-S14-05) |
| KI-S15-21 | (Found and fixed in the final validation.) The notifications operations snapshot scanned every delivery and notification; concurrent snapshots on a large dataset failed with "could not resize shared memory segment" (53 of 100 calls) | operations portal showed the source as failed | **FIXED**: partial indexes (migration `20261019090100`), a primary-key-lookup form of the consent-enforcement check, compose PostgreSQL `shm_size: 256mb`; the perf test now fails on failed calls |
| KI-S15-22 | Synthetic phone numbers assigned by earlier local runs of the notification tests remain in the development database in the +26377 range (new runs use +26371); a `verifiedUser` number can still collide with one of them locally | local test flake only (CI databases are fresh) | ACCEPTED (local) |

## Stage 14 items addressed in Stage 15

| ID | Resolution |
|---|---|
| KI-S14-02 | PARTLY FIXED — the notification error rate is measured from `notification_attempts` (NOT_AVAILABLE without attempts); application-wide error rates remain NOT_AVAILABLE |
| KI-S14-10 | FIXED (in-app) — staff tasks (assignments, approvals waiting, escalations, payout exceptions) create persisted in-app notifications; external delivery waits for providers |
| KI-S14-11 | PARTLY FIXED — incident declarations notify incident managers in-app; paging and status pages remain deferred |
