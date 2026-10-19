# Stage 15 — Testing

All results below were produced on the development machine in this session (2026-10-16 to 2026-10-19; final validation
2026-10-19 after the finalisation changes; compose stack rebuilt from the final tree). Remote CI is reported in the
completion report.

| Suite | Command | Final result |
|---|---|---|
| Go unit (vet, gofmt) | `go test -count=1 ./apps/api/... ./internal/... ./migrations/...` | 290 top-level tests passed, 0 failed; vet and gofmt clean |
| Go race | same, `-race`, in `golang:1.27.2` | passed (36 packages, no race reports) |
| Go integration (full, perf on, as CI) | `FUNDZIM_IT_PERF=1 go test -tags integration ./tests/integration/...` (stack lock) | 189 passed, 1 failed, 5 skipped (environment-gated). `TestNotifications*` 13, `TestPayout*` 12, `TestPayments*` 25, `TestFee*` 13, `TestRisk*` 13, `TestOperations*` 9. The failure (`TestVerificationSelfReviewAndDuplicateIdentity`, PHONE_IN_USE) was a test-data collision: the notification tests drew synthetic numbers from the same +26377 range as `verifiedUser`; they now use +26371 and both tests pass. That run also exposed the snapshot defect below |
| Re-run after the last fixes | `-run "TestNotifications|TestVerificationSelfReview…|TestPayoutSubmitRetry|TestPayoutHistorical"` and the perf test | all passed; perf 0 errors in every scenario |
| Notifications performance | `TestNotificationsPerf` (now fails on any failed call) | passed; table below |
| Web unit (vitest) | `npx vitest run` | 55 files, 542 tests passed |
| Web lint / typecheck | `npm run lint`, `npm run typecheck` | clean |
| E2E (mock API, desktop + Pixel 7, axe) | `npx playwright test` (fresh build) | 195 passed, 1 skipped (pre-existing), 0 failed |
| Staff-payments flake check | `staff-payments.spec.ts --repeat-each=6` | 72/72 passed after the fix (KI-S15-15) |
| E2E real stack (compose, no mocks) | `flock … npm run test:e2e:real` | 50 passed |
| OpenAPI | Redocly 2.54.3 lint | valid; 2 pre-existing warnings |
| Migrations | down / up on the development database | `20261018090000_notifications`, `20261019090000_payout_submit_retry_budget` and `20261019090100_notification_ops_indexes` round-trip; CI runs the destructive round trip |
| Secrets | `scripts/check-secrets.sh`, gitleaks (staged) | see the completion report |

### Defects found by the final validation

* **Operations snapshot failed under load on a large dataset** (`TestNotificationsPerf`: 53 of 100 snapshot calls failed,
  `could not resize shared memory segment … No space left on device`). Cause: the conflicting-evidence count and the
  consent-enforcement check scanned every delivery/notification with parallel hash joins, exhausting the container's
  64 MB `/dev/shm` when eight snapshots ran at once. Fix: partial indexes (migration `20261019090100`), the SMS/WhatsApp
  branch rewritten to a primary-key lookup per sent delivery (37 buffers instead of full scans), and the compose
  PostgreSQL given `shm_size: 256mb`. Verified with the index fix alone first (0 errors at 115k notifications), then with
  the shared-memory change. The perf test now fails on any failed call (it used to only count them).

## Stage 15 tests

| Test | Covers |
|---|---|
| `TestRouteChannelRules`, `TestNoFallbackAndPreferences` | the routing policy: mandatory security, preferences, consent states, marketing, unverified/missing contacts, suppression, synthetic sources, live messaging in production, missing provider/template channel, WhatsApp approval, staff in-app only except security e-mail, no fallback |
| `TestRenderIsPlaceholderOnlyAndSafe` | allow-listed placeholders, stray braces, missing values, HTML escaping (template text and values), subject line breaks, control characters, `[TEST]` label |
| `TestSMSSegmentsAndPhones` | GSM-7 / UCS-2 segment counting, the three-segment limit, E.164 normalisation (ZW default) and invalid numbers |
| `TestBackoffGrowsCapsAndHonoursRetryAfter` | exponential backoff, cap, jitter bounds, Retry-After |
| `TestCallbackPrecedenceIsDeterministic`, `TestDeliveryEdgesMatchMigration` | callback precedence and conflicts; Go edges identical to the SQL state machine |
| `TestSimulatorScenariosAndWebhookVerification` | scenario selection, signatures, timestamp window, tampering |
| `TestFinancialEventMapping` | which payment/payout events produce which notices; synthetic detection |
| `TestNotificationsOutboxCreatesOnceAndDelivers` | a real domain event through the outbox and the compose worker (in-app + Mailpit); 100 duplicate and 100 concurrent deliveries of one event → one notification |
| `TestNotificationsFinancialSafety` | no success notice from an intent, UNKNOWN/FAILED payment or payout, or an unverified webhook; TEST money in-app only with `[TEST]`; confirmation only from `payments.payment_succeeded` |
| `TestNotificationsConsentAndPreferences` | mandatory security, SMS opt-in, consent withdrawn before the send (re-checked), re-grant, expiry, reconfirmation after a policy change, marketing suppressed whatever the records, unverified e-mail never used, no fallback, marketing grant refused |
| `TestNotificationsDeliveryScenarios` | delivered, bounce (+ suppression), rejection, temporary failures to the dead letter, timeout resolved by status lookup without resend, duplicate and out-of-order callbacks, rate limiting |
| `TestNotificationsWebhookSecurityAndCrashRecovery` | forged, stale and unknown-provider callbacks refused and recorded; expired lease → UNKNOWN → lookup |
| `TestNotificationsDeadLetterReplayAndTemplates` | replay re-checks suppression; concurrent replays → one winner; template maker-checker with step-up, frozen after submit, self-approval refused; activation during creation (every notification references a version that was ACTIVE) |
| `TestNotificationsInboxIsolationAndStaffRBAC` | cross-user isolation (404), unread counts, read-all, staff notifications from operations events, `communications.*` permissions per route |
| `TestNotificationsFailureInjection` | a missing template fails the event without partial records and succeeds after recovery; a sweep with an unreachable database changes nothing |
| `TestNotificationsQueueIsolation` | notification work (fan-out, sweep, simulator callbacks) only on the `notifications` queue and nothing else there (payout jobs included); one fan-out job per (consumer, event); attempts recorded |
| `TestNotificationsConsentWithdrawnDuringRetry` | a temporarily failed SMS waiting for its retry is suppressed `CONSENT_WITHDRAWN`, not resent, after withdrawal |
| `TestNotificationsConsentLookupFailureFailsClosed` | fault confined to the consent read (locked table, 300 ms `lock_timeout`, 55P03 asserted): no notification and no partial records; created once after recovery |
| `TestNotificationsFinancialSafety` (extended) | payout SUBMITTED gives "sent to the provider" only; provider-confirmed COMPLETED gives one TEST-labelled in-app notice; a verified non-TEST success in a production process stays in-app (`LIVE_MESSAGING_DISABLED`) |
| `TestPayoutSubmitRetryBudgetHaltsAndResumes` | KI-S15-19: four concurrent dispatchers per round → exactly the budget (5) of CREATE_PAYOUT attempts, all DEFINITELY_NOT_SENT; SEV2 exception; payout, reservation and in-transit journal unchanged; further rounds and a restarted worker make no attempt; the database refuses a CREATE_PAYOUT while halted; resolution (audited) grants a fresh budget (10 attempts in all); ledger integrity |
| `TestPayoutHistoricalExcessiveAttemptsHaltedWithoutAnotherCall` | a payout already over budget (history as on the development database) is halted on its next dispatch without calling the provider |
| `TestPayoutUnknownOutcomeKeepsReservationAndIsNeverResubmitted` (scoped) | the UNKNOWN payout gets no new CREATE_PAYOUT attempt and stays UNKNOWN |
| `TestNotificationsConfig`, `TestPayoutSubmitRetryConfig`, `TestRelayPlanRoutesConsumerQueues` (unit) | messaging config refusals; retry budget bounds; per-consumer outbox queue routing |
| `notifications-components.test.tsx` (vitest) | inbox labels and actions, generic errors, unread link never 0 on failure, preferences locking and minimal changes, consent give/withdraw and no marketing, dead-letter replay with step-up and blocked reason, suppression validation, draft form body and error mapping |
| `e2e/notifications.spec.ts` (Playwright, desktop + mobile) | notification centre, unread count, mark read / all, preferences, consent; staff dashboard, dead-letter replay with step-up, template maker-checker across two staff accounts, consent page RBAC (SUPPORT gets 404), axe and CSP checks |

## Brief §79 failure injection — coverage

| Case | Covered by |
|---|---|
| database outage | failure-injection sweep with an unreachable pool |
| worker crash | expired-lease recovery (webhook/crash test) |
| outbox failure | the Stage 3 outbox tests (relay retries, dead letters); the engine's consumer is idempotent (duplicate delivery test) |
| provider timeout, rate limit | delivery scenarios |
| webhook failure, duplicate / out-of-order callbacks | webhook and delivery scenario tests |
| consent service failure | creation path fault-injected (`TestNotificationsConsentLookupFailureFailsClosed`); send-time path code-reviewed (KI-S15-12) |
| template lookup failure | failure injection (missing template) |
| backlog | performance drain of 400 deliveries |

## Performance (measured, development stack, final)

measured 2026-10-19T09:58:21Z UTC; GOMAXPROCS=16; development stack (compose PostgreSQL 17.11, default settings); in-process service calls (simulators); dataset after the run: 116049 notifications, 147672 deliveries, 5045 attempts.

| scenario | c | n | p50 ms | p95 ms | max ms | ops/s | errors |
|---|---:|---:|---:|---:|---:|---:|---:|
| notification creation (recipient resolution, routing for 4 channels, in-app render) | 8 | 400 | 8.4 | 12.0 | 56.7 | 841 | 0 |
| template rendering (e-mail text + HTML) | 8 | 5000 | 0.0 | 0.0 | 1.3 | 572768 | 0 |
| delivery sweeps + simulator callbacks: 400 external deliveries drained | 1 | 400 | — | — | 5280.6 (total) | 76 | 0 |
| in-app inbox page (25 of 200) | 8 | 500 | 0.7 | 2.1 | 10.4 | 8589 | 0 |
| unread count | 8 | 1000 | 0.4 | 0.6 | 1.5 | 16962 | 0 |
| webhook verification + evidence (unknown message) | 8 | 500 | 2.0 | 3.4 | 5.2 | 3687 | 0 |
| communications dashboard | 8 | 100 | 164.9 | 189.2 | 214.6 | 49 | 0 |
| communications report (30 days) | 8 | 50 | 328.0 | 353.0 | 396.6 | 22 | 0 |
| operations snapshot (notifications) | 8 | 100 | 80.9 | 92.7 | 107.0 | 94 | 0 |

Measured on a development machine against the compose stack whose database had accumulated the history of many test runs (see the dataset line); not a capacity commitment. The dashboard and the 30-day report recompute over that history and grow with it (KI-S15-20).
