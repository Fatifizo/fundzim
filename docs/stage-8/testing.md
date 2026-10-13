# Stage 8 — Testing

What was added, what it proves, and what was run. No test was skipped, disabled or weakened to get a green run.

## 0. Final runs (2026-10-13, final tree)

| Gate | Command | Result |
|---|---|---|
| Go format, vet | `gofmt -l apps internal migrations tests`; `go vet` (incl. `-tags integration` and the perf harness overlay) | clean |
| Go unit, race detector | `go test -race -count=1 ./apps/api/... ./internal/... ./migrations/...` (in `golang:1.27.2`) | 32 packages ok; 223 tests + 60 subtests passed, 0 failed |
| Integration | `go test -tags integration -count=1 ./tests/integration/...` with `FUNDZIM_IT_API_URL`, `FUNDZIM_IT_WEB_URL`, `FUNDZIM_IT_PERF=1`, under the host lock, after rebuilding API/worker/web | 115 passed, 0 failed, 2 skipped (env-gated: multi-replica API profile; stopping the Valkey container); all 16 `TestPayments*` passed |
| Real-stack Playwright | `npm run test:e2e:real`, twice, under the host lock | 47 / 47 passed in both runs (projects staff-desktop, desktop-chromium, mobile-chromium; unchanged suite — no checkout exists) |
| Web lint, typecheck | `npm run lint`, `npm run typecheck` | 0 errors |
| Vitest | `npx vitest run` | 471 / 471 passed (48 files) |
| Mock-API Playwright | `npm run test:e2e` | 155 passed, 1 skipped (existing browser-conditional skip in `public.spec.ts`) |
| Dependencies | `npm audit --omit=dev` | 0 vulnerabilities |
| OpenAPI | `redocly lint` | valid, 2 warnings (baseline) |
| Migrations | down + up of both Stage 8 migrations on the development database; CI destructive round trip | ok locally; CI: §6 |
| Secrets | `scripts/check-secrets.sh`; gitleaks history | none; history clean |

Remote CI: §6.

## 1. Go unit tests (new)

| Test | Proves |
|---|---|
| `TestPaymentEdgesMatchMigration` | The Go edge list equals the 42 `payment_intent` rows in the migration (payment-state-machine §2) |
| `TestRanksMatchMigration` | Go ranks equal `app.payment_status_rank` |
| `TestRefundRequestHasNoExecutionEdge` | No edge leaves `APPROVED`; execution impossible in Stage 8 |
| `TestSourceRules` | `SUCCEEDED` only from SYNC/WEBHOOK/POLL/RECON; `UNKNOWN` only INTERNAL; `FAILED` never INTERNAL except from CREATED, never STAFF without evidence; a donor (browser) source never changes a payment |
| `TestEdgesRespectPrecedence` | Every lower-rank edge is a listed exception; REFUNDED/CHARGED_BACK final; no SUCCEEDED → FAILED |
| `TestPollBackoffIsBounded`, `TestSafeRedirect`, `TestCleanReason`, `TestDonorMessagesNeverClaimMoneyBeforeSuccess`, `TestAmountFormat` | Poll backoff ≤ 10 min; open-redirect refusal; reason validation; donor messages; money format |
| `TestVerify` (sandbox) | Rotation overlap (current and previous slot); absent, forged and altered signature headers; unknown kid; tampered body; stale and future timestamps |
| `TestParseRedactsAndValidates`, `TestMappingNeverGuesses`, `TestScenarioTable`, `TestLimitsAreSandboxContractOnly` | Strict parsing and redaction, livemode never simulated, unknown types kept as OTHER, unmapped statuses never guessed, scenario table, no ZWG |
| `TestLiveExecutionIsDisabled`, `TestEnvVarFor`, `TestEnvResolver`, `TestMethodCategories`, `TestCursor`, `TestVerifyReasonMapping` (psp) | Execution constant false; secret references; method categories (no ZIPIT code); cursors; sensitive headers never kept |
| `TestPaymentsSandboxRefusedInProduction` (config) | Sandbox refused in production; PRODUCTION accounts only in production; sandbox off by default |
| `TestDefaultPolicyValues` (ratelimit) | The four payment policies |
| `TestEveryRouteHasAPolicyAndIsInOpenAPI` (app) | Every Stage 8 route has a policy and is in the OpenAPI document |
| archtest | `psp` imports platform/audit only; `payments` imports platform/audit/campaigns/risk/psp; each module touches only its own tables |

## 2. Integration tests (`tests/integration/payments_test.go`, real PostgreSQL, Valkey, outbox)

| Test | Proves |
|---|---|
| `TestPaymentsSandboxSuccessAndIdempotency` | Intent idempotency (replay, reused key, missing key); attempt replay makes no second provider call; PAYMENT_IN_PROGRESS; webhook → SUCCEEDED; donation PAID; DONATION_ALREADY_PAID; codes P1,P2,P9; one success event and one `payment_succeeded`; raw vs mapped status; **no ledger tables**; donor status view `no-store`; cross-user 404 |
| `TestPaymentsDeclineThenRetry` | Definite decline → FAILED; retry is a new payment with a new reference |
| `TestPaymentsTimeoutIsUnknownNeverFailed` | Timeout → UNKNOWN (reason TIMEOUT); second attempt refused while unknown; resolved to SUCCEEDED; not-created timeout stays UNKNOWN through repeated not-found polls, never FAILED, same reference on re-create |
| `TestPaymentsCrashBetweenCallAndResultIsRecovered` | Worker crash after an IN_FLIGHT call: the sweeper records OUTCOME_UNKNOWN / CRASH_DURING_CREATE without changing the payment |
| `TestPaymentsWebhookOrderingAndDuplicates` | Duplicate webhook stored and applied once; out-of-order (`refunded` parked, `paid` applied and releases it, late `pending` LOWER_RANK); late success after expiry (P11, P15) with reconciliation reference |
| `TestPaymentsAnomaliesAreRecordedNotApplied` | Unmapped status → inbox UNRECOGNISED + reconciliation; amount mismatch never applied; provider unavailable → FAILED pre-submit (DEFINITELY_NOT_SENT) |
| `TestPaymentsWebhookSecurity` | Unsigned, forged, tampered, stale, future, wrong content type, unknown provider, oversize → refused with rejection rows and nothing stored; valid stored once, replay acknowledged as duplicate; previous key accepted (slot recorded); 20 concurrent duplicate deliveries → one row; bad-signature scenario resolved by status query |
| `TestPaymentsRedirectSessionNeverConfirms` | Card redirect host allow-listed; return URL carries the session; foreign donor and wrong token refused; a valid return never confirms and schedules a poll; token single use |
| `TestPaymentsEligibilityAndCurrency` | ZWG refused; malformed amounts; unknown and draft campaigns refused; provider limits; unverified and merchant-settlement methods never offered or routed; staff cannot donate; a restricted donor (real maker-checker compliance case) gets DONATION_NOT_AVAILABLE without the reason |
| `TestPaymentsConcurrentAttemptsYieldOnePayment` | 20 concurrent attempts → one payment; 100 concurrent requests with one key → one intent, one id |
| `TestPaymentsRefundRequestsNeverExecute` | Refund refused before success; over-refund refused (request and remainder); maker cannot approve; FINANCE approves with step-up (`execution: DISABLED`); APPROVED → EXECUTING impossible in SQL; payment unchanged; no `refund_transactions` table |
| `TestPaymentsStaffOperations` | All staff views 200 + `no-store` without secrets or tokens; providers view shows all gates false and secrets resolvable; donors and KYC reviewers refused; audited status query; SUPPORT cannot operate; no manual success/fail endpoints |
| `TestPaymentsDatabaseInvariants` | Under `fundzim_app`: event history and webhook raw columns immutable, no deletes, no status change without an event, amount immutable, completed calls immutable, capabilities append-only, INTERNAL success event refused; no payment on a non-routable capability; sandbox PRODUCTION account, unselected enabled provider and raw secrets refused |
| `TestPaymentsProviderOutageOpensCircuit` | Open circuit → 503 PAYMENT_PROVIDER_UNAVAILABLE and nothing stored; recovery after close |
| `TestPaymentsPostingRequestsExactlyOnce` | Webhook racing status polls (4 × concurrent) → one SUCCEEDED; at most one posting request per `payment:{id}:capture` (test-only recorder) across duplicates, the race and a late success |
| `TestPaymentsDeadLetterAndRequeue` | Injected handler failure (database unavailable) → FAILED_RETRYING ×7 → DEAD_LETTERED, payment unchanged (rollback); staff re-queue (step-up, audited, only once) → PROCESSED → SUCCEEDED |

## 3. Failure injection and concurrency (brief §43, §44, §46; TESTING.md §3.9)

| Scenario | Covered by |
|---|---|
| Provider timeout / delayed success / unknown | sandbox 03, 05, 20, 23; `TestPaymentsTimeoutIsUnknownNeverFailed` |
| Provider outage | sandbox 19 (not sent); `TestPaymentsProviderOutageOpensCircuit` |
| Database outage during processing (F7) | injected handler failure: `TestPaymentsDeadLetterAndRequeue` (rollback, retry, dead letter) |
| Worker crash (F16) | `TestPaymentsCrashBetweenCallAndResultIsRecovered` (sweeper); outbox redelivery is idempotent |
| Redis/Valkey outage (F15) | existing Stage 4 test `TestRateLimitFallbackWhenValkeyContainerStopped` (env-gated, skipped locally and in CI) — not re-run for payments |
| Duplicate / out-of-order / replayed webhooks (F1–F3) | sandbox 06, 07, 15; `TestPaymentsWebhookSecurity`, `TestPaymentsWebhookOrderingAndDuplicates` |
| Invalid / expired signature (F14) | sandbox 14, 15; `TestPaymentsWebhookSecurity`; `TestVerify` |
| Successful payment after client timeout (F6) / redirect claims success (F13) | sandbox 16 (`webhook_never`), session return test |
| Reconciliation failure | amount mismatch, unmapped status, horizon logic (reconciliation references) |
| 100 concurrent duplicate requests; concurrent webhooks; multiple dispatchers; poll racing webhook; duplicate refund requests | `TestPaymentsConcurrentAttemptsYieldOnePayment`, `TestPaymentsWebhookSecurity`, `TestPaymentsPostingRequestsExactlyOnce`, perf harness (4 dispatchers), over-refund DB guard |
| Network timeout on CreatePayment (F4), status-query 5xx (F5) | sandbox 03/05/20; the poll records INDETERMINATE without changing state |

Not covered: a real provider (none exists); chargebacks/disputes (deferred); refund execution (disabled); payouts.

## 4. Web (staff payment operations UI)

Vitest: normalisers, money input (no floating point), labels and components (incl. axe and the step-up retry).
Mock Playwright `e2e/staff-payments.spec.ts` (desktop + mobile): navigation, attempt detail (provider calls, raw vs
mapped status, timeline), status query with step-up, simulator panel, webhook re-queue, rejections, refund approve
showing execution DISABLED, maker sees only Withdraw, 404 for donors / KYC reviewers / ADMIN-only staff, no button
marks a payment succeeded or failed, axe + CSP. The public campaign page still asserts the disabled donation button.

## 5. Performance

[performance.md](performance.md) (measured; harness `tests/perf/payments/`).

## 6. Remote CI

CI_RESULT
