# Stage 15 — Testing

All results below were produced in this session (2026-10-16 to 2026-10-19, development machine; compose stack rebuilt
from the Stage 15 tree). Remote CI is reported in the completion report.

| Suite | Command | Result |
|---|---|---|
| Go unit (vet, gofmt) | `go test -count=1 ./apps/api/... ./internal/... ./migrations/...` | 287 top-level tests passed, 0 failed |
| Go race | same, `-race`, in `golang:1.27.2` | passed (after fixing a time-of-check race in the Stage 4 SMTP sender's error wrapping, which made `TestSMTPTimeoutAndUnreachable` flaky under `-race`) |
| Go integration | `go test -tags integration ./tests/integration/...` (under the stack lock) | last full run: 176 passed, 1 failed, 13 skipped (opt-in perf and environment-gated); `TestNotifications*` 9, `TestPayments*` 25, `TestPayout*` 8, `TestFee*` 12, `TestRisk*` 12, `TestOperations*` 8. The failure, `TestPayoutUnknownOutcomeKeepsReservationAndIsNeverResubmitted`, counted other tests' due payouts in the shared database (KI-S15-19); the assertion now checks its own payout and passed 3/3. Earlier full runs found and fixed `TestIdentityRegistrationVerificationAndLogin` (existing-account warning to an unverified address) and `TestStaffPersonalLink` (staff security e-mail) |
| Notifications performance | `FUNDZIM_IT_PERF=1 … -run TestNotificationsPerf` | passed; table below |
| Web unit (vitest) | `npm test` | 55 files, 542 tests passed (incl. `notifications-components.test.tsx`, 11 tests) |
| Web lint / typecheck | `npm run lint`, `npm run typecheck` | clean |
| E2E (mock API, desktop + Pixel 7, axe) | `npx playwright test` | 193 passed, 1 skipped (pre-existing), 0 failed (fresh build); an earlier run timed out once in `staff-payments.spec.ts` under load, then 30/30 repeats passed (KI-S15-15) |
| E2E real stack (compose, no mocks) | `flock … npm run test:e2e:real` | 50 passed |
| OpenAPI | Redocly 2.54.3 lint | valid; 2 pre-existing warnings |
| Migrations | down / up on the development database | `20261018090000_notifications` round-trips (re-applied after the deep-link CHECK change) |
| Secrets | `scripts/check-secrets.sh`, gitleaks (staged) | see the completion report |

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
| consent service failure | **not separately injected** (KI-S15-12); fail-closed code path `ROUTING_CHECK_FAILED` |
| template lookup failure | failure injection (missing template) |
| backlog | performance drain of 400 deliveries |

## Performance (measured, development stack)

measured 2026-10-19T06:35:45Z UTC; GOMAXPROCS=16; development stack (compose PostgreSQL 17.11, default settings); in-process service calls (simulators); dataset after the run: 25206 notifications, 30156 deliveries, 1373 attempts.

| scenario | c | n | p50 ms | p95 ms | max ms | ops/s | errors |
|---|---:|---:|---:|---:|---:|---:|---:|
| notification creation (recipient resolution, routing for 4 channels, in-app render) | 8 | 400 | 7.9 | 12.5 | 57.3 | 863 | 0 |
| template rendering (e-mail text + HTML) | 8 | 5000 | 0.0 | 0.0 | 2.3 | 532410 | 0 |
| delivery sweeps + simulator callbacks: 400 external deliveries drained | 1 | 400 | — | — | 4869.5 (total) | 82 | 0 |
| in-app inbox page (25 of 200) | 8 | 500 | 0.9 | 2.3 | 6.6 | 7127 | 0 |
| unread count | 8 | 1000 | 0.4 | 0.5 | 1.2 | 21432 | 0 |
| webhook verification + evidence (unknown message) | 8 | 500 | 1.9 | 3.2 | 7.7 | 3909 | 0 |
| communications dashboard | 8 | 100 | 28.7 | 72.5 | 75.2 | 234 | 0 |
| communications report (30 days) | 8 | 50 | 62.2 | 254.2 | 262.2 | 80 | 0 |
| operations snapshot (notifications) | 8 | 100 | 26.0 | 32.9 | 37.0 | 296 | 0 |

Measured on a development machine against the compose stack; not a capacity commitment. Rendering is sub-millisecond; the report query (30 days) is the slowest operation (p95 ≈ 254 ms at concurrency 8).
