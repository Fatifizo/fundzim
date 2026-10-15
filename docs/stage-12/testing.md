# Stage 12 — Testing

All results below were produced in this session on 2026-10-15 (local stack). Remote CI results are in §6.

## 1. Unit and property-based tests (`internal/fees`)

- `calc_test.go`: every model (zero, percentage, fixed, percentage + fixed, tiered, capped, minimum, min/max combinations,
  0 % and 100 %), tier boundaries, rounding boundaries under all four modes (16.65, 16.5, 17.5, 16.1, exact, sub-unit),
  terms validation (13 invalid shapes), overflow (MaxInt64 + fixed; donor charge above MaxInt64) and underflow (zero and
  negative basis), SHARED splits (largest remainder, ties to the donor), all responsibility combinations on the brief's
  synthetic USD 100.00 / 5 % / 2 % example, UNKNOWN provider fees never 0, organiser-borne fees ≥ amount refused (donation
  and payout), refund reversals (proportional, except-fixed, non-refundable, cumulative partial refunds with no drift,
  refunds above the original refused), determinism and reproducibility of `Compute` (same hashes; algorithm version checked).
- Property-based (reproducible seeds; ≈ 180 000 cases): fees deterministic, non-negative, within min/max, fixed part ≤ fee,
  rounding within one minor unit; donation breakdowns reconcile exactly (`charge = allocation + platform net + provider`),
  parts sum to the fee, allocation positive; reversals never negative and a fully refunded donation reverses exactly the
  refundable fee under every treatment and rounding mode.
- `http_test.go`: the quote throttle uses `fees.quote.user` per user and fails closed without a limiter; every staff route
  has a fee permission, every staff mutation a step-up permission (or `fee.fixtures`), every creation is idempotent, no route
  posts journals.
- Go unit suite: all packages pass; `-race` (golang:1.27.2 container): **35 packages ok**; `go vet`, `gofmt` clean;
  `internal/archtest` (fees module, imports, 13 owned tables) passes.

## 2. Integration tests (`tests/integration/fees_test.go`, real stack)

| Test | What it proves |
|---|---|
| `TestFeePolicyMakerCheckerLifecycleAndImmutability` | disabled categories refused; one policy per scope; invalid terms refused; only the author edits a draft; no approval/activation of a draft; terms frozen at submission (DB); FINANCE cannot approve; maker self-approval refused (service and DB); DRAFT → ACTIVE refused (DB); terms-hash binding; activation, suspension, reactivation by a checker only; append-only history; retirement final; no deletes |
| `TestFeePolicyLinkedAccountCannotApprove` | a checker linked to the maker is refused over HTTP and by the database trigger |
| `TestFeePolicyPrecedenceAndSelection` | campaign scope beats method scope; method scope applies elsewhere; never before its effective start; retroactive versions refused |
| `TestFeeQuotesAndDonationIntentBinding` | the brief's example quote (500 / 200 / 10 000 / 9 300 / 500); no method ⇒ provider fee UNKNOWN (not 0); client fee override refused; owner-only quotes; float amounts and ZWG refused; idempotent replay and key reuse; intent binding, single use, stale amount, foreign quote, invalidation by suspension, expiry, system quotes |
| `TestFeeAssessmentOnTestPaymentIsNeverPosted` | donor-covered quote and intent charge; simulated payment SUCCEEDED; 10 concurrent duplicate deliveries ⇒ 2 assessments; QUOTED/ESTIMATED bases and parts; `NOT_POSTED_TEST_PAYMENT`; no journal for the payment; DB refuses POSTED |
| `TestFeeAccountingFixturesRefundsAndChargebacks` | synthetic USD 100.00 donation: capture (Dr 100.00 / Cr 95.00 / Cr revenue 5.00, policy version on the journal), provider fee 2.00, settlement 98.00, release 93.00 — all fees/SIMULATED/TEST; replay posts nothing new; partial refund 40.00 ⇒ reversal 2.00 with exact journal lines; replay by reversal id; cumulative second refund; over-refund refused; net fee; chargeback 20.00 ⇒ reversal 1.00 and DISPUTE_FEE 15.00 recovered; small donation ⇒ UNRECOVERED exposure; ledger integrity passes |
| `TestFeeAdjustmentsMakerCheckerAndImmutability` | REFUND not typed in; bounds; maker cannot approve (403 and service); checker approves; original fee kept, net reduced; organiser view shows the adjustment without notes, staff ids or internal references; assessments/adjustments immutable; DB refuses a net below zero |
| `TestFeeConcurrency` | 100 concurrent duplicate fixture runs ⇒ 2 assessments and 2 journals; two activations with the same start ⇒ one wins (`FEE_POLICY_CONFLICT`); quotes created during a suspension ⇒ none stays ACTIVE on the suspended version (found a race, fixed with the per-currency lock) |
| `TestFeePayoutFeesNeverDeductTwice` | platform-absorbed payout fee estimate 100 (immutable), reservation = amount, owner view receives 10 000, completion ⇒ `PAYOUT_FEE` expense journal, campaign payable falls by exactly the payout; organiser-borne ⇒ beneficiary receives 4 900; fee ≥ amount refused |
| `TestFeeSecurity` | 401/403/404 matrix, fixtures ADMIN only, organiser view owner only, no identifiers in quotes, VERIFIED refused (API and DB), quote rate limit (429), production rows refused, app role cannot write ledger |
| `TestFeeReconciliationAndRevenueReport` | provider-reported mismatch and a tampered calculation detected (AMOUNT_MISMATCH, ROUNDING_MISMATCH); reruns do not duplicate; resolve once; revenue report TEST, per currency, cash NOT_AVAILABLE, no cross-currency total |
| `TestFeeFailureInjection` | posting refused after assessment (frozen account = crash between assessment and posting) ⇒ PENDING_POSTING, reconciliation MISSING_POSTING, replay posts once; database down ⇒ quote fails; ledger refusal of a refund ⇒ no adjustment recorded |

Full integration suite (containers rebuilt first): **152 passed, 0 failed, 10 skipped** (perf tests without
`FUNDZIM_IT_PERF` and environment-dependent tests, as before). Two test-harness fixes were needed and made: the TOTP helper
could hand out a previous-step code at a step boundary (`MFA_CODE_INVALID` during the staff fixture), and reused staff
sessions now refresh their step-up after 5 minutes (the verification tests ran later than before and their login-time
step-up had passed `STEP_UP_MAX_AGE`). Neither weakens an assertion.

Failure injection not run for fees (stated): cache outage (quotes use the Stage 4 limiter fallback) and outbox delivery
failure (the fee consumer is idempotent; duplicate delivery tested). Accounting period closure is handled by the Stage 10
posting engine (fee journals post at `now()` into the current open period).

## 3. Web

- Typecheck and lint clean; vitest **52 files, 505 tests passed** (includes `src/__tests__/fees-components.test.tsx`).
- Mock-API Playwright under `CI=1`: **179 passed, 1 skipped** (the existing mobile-only skip); new `e2e/staff-fees.spec.ts`
  (3 tests) and `e2e/owner-fees.spec.ts` (2 tests), fee assertions in `e2e/owner-withdrawals.spec.ts`; the new specs passed
  70/70 with `--repeat-each=5`.
- Real-stack Playwright: **50 passed**, including the new fee test in `e2e-real/staff-ledger.spec.ts` (COMPLIANCE sees TEST
  fees and production BLOCKED; revenue report TEST with cash NOT_AVAILABLE; a reviewer gets 403/404).
- Web production build: passes (container image rebuilt).

## 4. OpenAPI and migrations

- `redocly lint`: valid, 2 warnings (pre-existing). Route coverage test (`TestEveryRouteHasAPolicyAndIsInOpenAPI`) passes.
- Migration applied, rolled back and re-applied on the development database twice (once with fee data and fee journals
  present).

## 5. Performance (measured; nothing extrapolated)

`TestFeesPerf` (FUNDZIM_IT_PERF=1), in-process service calls against the development stack:

measured 2026-10-15T12:44:45Z UTC; GOMAXPROCS=16; development stack (compose PostgreSQL 17.11, default settings); in-process service calls; dataset after the run: 1138 fee quotes, 529 assessments, 87 fee policies, 559 fee ledger journals.

| scenario | c | n | p50 ms | p95 ms | max ms | ops/s | errors |
|---|---:|---:|---:|---:|---:|---:|---:|
| fee quote (policy selection + exact calculation + store), c=1 | 1 | 400 | 155.3 | 168.7 | 213.4 | 6 | 0 |
| fee quote (policy selection + exact calculation + store), c=8 | 8 | 400 | 176.8 | 208.6 | 314.5 | 44 | 0 |
| policy lookup (Resolve: platform fee, most specific scope) | 8 | 1000 | 0.5 | 0.8 | 3.7 | 14601 | 0 |
| fee assessment + fee journals (fixture donation: 2 assessments, capture + provider-fee journals) | 8 | 200 | 224.2 | 276.7 | 741.1 | 32 | 0 |
| staff policy list | 8 | 200 | 59.4 | 69.6 | 81.4 | 132 | 0 |
| staff assessment list (100) | 8 | 300 | 2.4 | 4.6 | 7.8 | 2846 | 0 |
| staff quote list (50) | 8 | 300 | 2.9 | 5.4 | 7.1 | 2523 | 0 |
| revenue report (12 months, per currency) | 8 | 50 | 9.1 | 24.8 | 25.0 | 656 | 0 |
| fee reconciliation run (latest ≤ 5000 assessments) | 1 | 3 | 1555.3 | 1743.9 | 1743.9 | 1 | 0 |

Notes: a quote's cost is mostly the campaign eligibility evaluation and provider routing (policy lookup alone is p50
0.5 ms); the staff policy list loads versions per policy (N+1; KI-S12-20); reconciliation grows with the number of
assessments (it recomputes every calculation and reads every journal).

## 6. Remote CI

Recorded after the push (see the completion report).
