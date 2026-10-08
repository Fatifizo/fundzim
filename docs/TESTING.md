# FundZim — Testing Strategy

> **Status:** Stage 0 strategy. **No automated tests exist yet** beyond what the Next.js scaffold ships with
> (none). Test suites are introduced stage by stage from Stage 3 onward. This document defines what "tested"
> means for FundZim so every stage builds toward it.

Related: [MONEY.md](MONEY.md) · [LEDGER.md](LEDGER.md) · [PAYMENTS.md](PAYMENTS.md) ·
[SECURITY.md](SECURITY.md) · [DEVELOPMENT.md](DEVELOPMENT.md) · [ROADMAP.md](ROADMAP.md)

---

## 1. Principles

1. **Tests ship with the feature.** A feature without tests is not done ([DEVELOPMENT.md](DEVELOPMENT.md),
   Definition of Done).
2. **Never disable, skip, delete, or weaken a test to make a build pass.** A failing test is a finding.
   Quarantining a genuinely flaky test requires an issue, an owner, a deadline, and must never apply to
   financial-invariant, idempotency, or security tests.
3. **Financial correctness is tested as invariants, not examples.** Example-based tests show a donation
   posts correctly; property-based and concurrency tests show *no sequence* of events breaks the books.
4. **Real dependencies where it matters.** Ledger, idempotency and concurrency behaviour depends on
   PostgreSQL semantics (constraints, isolation, locking). These are tested against real PostgreSQL in
   containers, never mocked or substituted with SQLite.
5. **Deterministic by default.** Inject the clock (`platform/clock`), ID generator and randomness; seed
   property tests and record seeds on failure.
6. **No real PII or credentials in tests** (see §6).
7. **Report honestly.** Stage reports list exactly which suites ran and their results. A suite that does
   not exist is reported as "not yet implemented", never as passing.

---

## 2. Test pyramid and ownership

| Layer | Scope | Tooling | Location | Runs in CI |
|---|---|---|---|---|
| Unit | Pure functions, domain logic, state machines, money arithmetic | Go `testing`; Vitest | Beside code (`*_test.go`, `*.test.ts`) | Every push |
| Property / fuzz | Money, allocation, ledger balancing, parsers, webhook payload parsing | Go native fuzzing (`go test -fuzz`), `pgregory.net/rapid` (or equivalent); `fast-check` for TS money formatting | Beside code | Every push (bounded); extended nightly |
| Database | Migrations, constraints, triggers, grants, queries | Go + testcontainers-go (PostgreSQL, same major version as prod) | Beside repository code | Every push |
| Integration | Module + DB + outbox/job queue; provider adapters against sandbox fake | Go + testcontainers-go (Postgres, Redis, MinIO) | `internal/**` and `tests/integration` | Every push |
| API | HTTP contract: envelopes, error codes, authz, idempotency headers | Go `httptest` + real router; contract checks vs OpenAPI (when introduced) | `apps/api/**`, `tests/api` | Every push |
| Component | React components, accessibility of components | Vitest + React Testing Library + `jest-axe`/axe-core | `apps/web/src/**` | Every push |
| E2E | User journeys through web + API + sandbox provider | Playwright (mobile viewport first) + axe | `tests/e2e` | PRs to `main`, nightly |
| Security | Authn/authz, IDOR, CSRF, injection, headers, upload handling, webhook auth | Dedicated Go tests, Playwright, ZAP baseline (later), govulncheck, npm audit, gitleaks, Trivy | `tests/security` + CI jobs | Every push (fast); nightly (scans) |
| Concurrency | Races on donations, payouts, webhooks, idempotency | Go tests with parallel goroutines against real Postgres; `-race` | `tests/financial`, module tests | Every push (bounded), nightly (extended) |
| Financial invariants | Ledger balance, projection recompute, reconciliation | Go invariant checker run after every integration/E2E suite | `tests/financial` | Every push |
| Failure injection | Provider/network/DB failures | Sandbox provider fault scripts, toxiproxy (or equivalent), DB fault hooks | `tests/financial`, `tests/integration` | Every push (core set), nightly (full) |
| Performance | Latency/throughput of campaign pages, donation initiation, webhook ingestion | k6; Lighthouse CI for web | `tests/perf` | Nightly / pre-release |

`tests/` holds only cross-cutting suites (E2E, financial, security, perf, shared fixtures). Unit and module
tests live beside the code they test.

---

## 3. Test types in detail

### 3.1 Unit tests
- Every domain state machine (campaign lifecycle, payment intent, payout, KYC level) has a table-driven test
  covering **every** valid transition and asserting rejection of **every** invalid one (generate the
  invalid set from the full state×state matrix, so new states cannot silently skip coverage).
- Money: arithmetic, overflow, currency mismatch errors, parsing/formatting, allocation.
- Fee calculation: rounding at the single documented point; allocation sums exactly to the whole.
- Frontend: money formatting from string minor units (no `Number` conversion of amounts), form validation.

### 3.2 Integration tests
- Run against PostgreSQL via testcontainers-go with migrations applied from `migrations/` — the same
  migrations as production.
- Each test uses an isolated schema/database or a transaction rolled back at the end; tests must be
  parallel-safe.
- Cover outbox → job → side effect pipelines, webhook inbox processing, module public interfaces.

### 3.3 API tests
- Envelope shape on success and error; stable `UPPER_SNAKE` error codes; `request_id` echoed.
- Authorization matrix: for each endpoint × role × ownership (own / other user's / other org's), assert the
  expected allow/deny. IDOR tests are mandatory for every resource endpoint.
- Idempotency: same `Idempotency-Key` + same body → same response, no second side effect; same key +
  different body → `409 IDEMPOTENCY_KEY_REUSED`; missing key on required endpoints → `400`.
- Money in JSON: `amount_minor` always a string; floats rejected on input.

### 3.4 Database tests
- Migrations apply cleanly from empty and are forward-only; schema matches expectation.
- Constraints fire: check constraints (`amount_minor > 0`, currency FK), unique constraints (provider
  transaction IDs, idempotency keys), foreign keys.
- Ledger trigger rejects an unbalanced transaction at commit; rejects mixed-currency balance; rejects
  entries whose currency differs from the account currency.
- **Grant tests:** connect as the application role and assert `UPDATE`/`DELETE` on ledger and audit tables
  fail; assert the app role cannot read the `kyc` schema; assert the kyc role cannot read unrelated schemas.

### 3.5 End-to-end tests
- Playwright, mobile viewport (e.g. 360×800) first, then desktop. Include throttled-network profiles.
- Journeys (as stages deliver them): sign-up/OTP, create/submit campaign, review/approve, donate via sandbox
  provider (success, decline, pending-then-success), anonymous donation display, share preview metadata,
  payout request and maker-checker approval, suspension/freeze effects.
- Every E2E run ends with the financial invariant checker (§4).

### 3.6 Security tests
- Authentication: brute-force/rate-limit behaviour, OTP expiry and reuse, session fixation (rotation on
  login), logout invalidation, idle/absolute timeout.
- Authorization: IDOR (above), privilege escalation (role grant by non-SUPER_ADMIN, self-approval in
  maker-checker), ADMIN cannot view KYC documents without the specific permission.
- CSRF: state-changing requests without valid token/origin are rejected.
- XSS: campaign content rendered escaped; CSP present with nonces.
- SQL injection: parameterised queries everywhere (static check + fuzzed inputs).
- SSRF: user-supplied URLs are not fetched outside the allow-listed fetcher.
- Uploads: wrong magic bytes, oversized files, polyglots, EICAR test file blocked by scanner, EXIF stripped
  from public media, KYC objects not publicly reachable.
- Webhooks: invalid signature, expired timestamp (replay), replayed valid event (dedupe).
- Headers: HSTS, CSP, frame-ancestors, nosniff, Referrer-Policy present.
- Logging: tests assert that secrets/PII in requests do not appear in captured log output (redaction).
- Scanners in CI: `govulncheck`, `npm audit` (high/critical gate with documented exceptions), gitleaks,
  Trivy image scan, later OWASP ZAP baseline against a staging deployment.

### 3.7 Concurrency tests
- N concurrent donations to the same campaign: all posted exactly once, balances equal sum.
- Concurrent payout requests against the same available balance: never exceeds available; at most one
  succeeds where funds suffice for only one.
- Concurrent duplicate webhook deliveries processed in parallel workers: exactly one ledger posting.
- Concurrent requests with the same `Idempotency-Key`: one executes, others wait or receive the stored
  response / `409` in-progress — never two executions.
- Run with Go `-race`; use barriers to force interleavings rather than relying on luck.

### 3.8 Financial invariant tests
The invariant checker (built in Stage 10, used everywhere after) asserts:
- For every ledger transaction and each currency: Σ debits = Σ credits.
- Every entry currency equals its account currency; every amount > 0.
- No USD/ZWG cross-currency postings.
- Materialised balance projections equal a full recompute from entries.
- Every SUCCEEDED payment has exactly one donation posting; every COMPLETED payout exactly one completion posting (plus its reserve and submit postings);
  reversals reference originals.
- No available balance is negative.
- Ledger and audit rows were never updated or deleted (compare counts/hashes before/after; audit hash chain
  verifies once implemented).

Any invariant failure fails the build **and** is reported as a stop-the-line incident (CLAUDE.md rule 11).

### 3.9 Failure-injection tests (mandatory scenarios)

The sandbox provider (Stage 8) is scriptable per test. Each scenario asserts final payment/payout state,
ledger postings, audit events, and that the invariant checker passes.

**Staging of assertions.** In Stage 8 the ledger does not exist yet: `payments` posts through the `ledger.Post`
interface backed by a test-only stub, so Stage 8 asserts **state, idempotency and the posting requests
recorded by the stub** (exactly one request per idempotency key). The **ledger-level assertions** (entries
written, balances, invariant checker) for F1, F6 and F7 are added in Stage 10 against the real ledger.

| # | Scenario | Expected behaviour |
|---|---|---|
| F1 | **Duplicate webhook** (same event delivered twice, sequentially and concurrently) | Second delivery deduplicated by `UNIQUE(provider, provider_event_id)`; one state change, one ledger posting. |
| F2 | **Webhook retry** after our endpoint returned 5xx/timeout | Retry accepted and processed once; no double posting. |
| F3 | **Webhook out of order** (e.g. `succeeded` before `pending`, or `refunded` before `succeeded`) | State precedence table prevents regression; late `pending` ignored after `SUCCEEDED`; early `refunded` held/reprocessed once prerequisite state arrives, never lost. |
| F4 | **Network timeout** between FundZim and provider on CreatePayment | Payment moves to `UNKNOWN` (ADR-020), never `FAILED`; any retry uses the same provider reference/idempotency key; resolved by status query or reconciliation — no duplicate charge. |
| F5 | **Provider timeout / 5xx** on status query | Backoff and retry; no state change on error; alert after threshold. |
| F6 | **Successful payment after client timeout** (donor closed browser / redirect never returned) | Webhook or reconciliation marks `SUCCEEDED` and posts ledger; donor receives confirmation; redirect absence irrelevant. |
| F7 | **Database transaction failure** mid-processing (injected error after state update, before ledger posting) | Whole transaction rolls back; job retries; final state consistent; outbox event emitted once. |
| F8 | **Concurrent donations** | See §3.7. |
| F9 | **Concurrent withdrawals** | See §3.7; available balance never overdrawn. |
| F10 | **Duplicate payout request** (same idempotency key; and double-click with distinct keys) | Same key → one payout. Distinct keys → second rejected if exceeds available balance; hold/confirmation UX prevents accidental double. |
| F11 | **Payout failure** (provider rejects / returns funds later) | Payout `FAILED`/`REVERSED` (ADR-020); the failure or reversal journal restores available balance (or `campaign_held` if frozen); a payout in `UNKNOWN` is never resubmitted; owner notified; no silent retry to a different destination. |
| F12 | **Reconciliation mismatch** (provider statement has a transaction we lack, lacks one we have, or differs in amount/currency) | Mismatch recorded as an exception case; alert raised; no automatic ledger "fix"; resolution via audited adjusting entry under maker-checker. |
| F13 | Redirect claims success but provider says failed | Payment not marked succeeded; UI shows provider truth. |
| F14 | Webhook with invalid signature / stale timestamp | Rejected (4xx), logged without payload secrets, metric incremented; no processing. |
| F15 | Redis unavailable | Non-financial degradation only (rate limiting falls back to conservative mode); no financial data loss. |
| F16 | Worker crash mid-job | Job lease expires and is retried; idempotent handler produces a single effect. |

### 3.10 Performance tests
- k6 scripts for: public campaign page (via Next.js SSR), donation initiation, webhook ingestion burst
  (e.g. 10× normal rate, verifying fast 2xx ack and backlog drain), payout listing for staff.
- Budgets defined in [FRONTEND.md](FRONTEND.md) (web) and per-endpoint p95 targets set in Stage 2.
- Lighthouse CI on key pages with mobile emulation and slow-4G throttling.
- Performance tests run against production-like data volumes generated synthetically.

---

## 4. Money and ledger: property-based testing

Properties (examples, to be implemented Stage 3 for money, Stage 10 for ledger):

- `parse(format(m)) == m` for all valid `m` per currency.
- `a.Add(b).Sub(b) == a` when no overflow; overflow returns an error, never wraps.
- `Add` of different currencies always errors.
- `Allocate(total, weights)` parts sum exactly to `total` and each part differs from its exact proportional
  share by < 1 minor unit.
- Fee rounding is deterministic and applied once.
- For any random sequence of valid ledger operations (donations, fees, refunds, payouts, reversals,
  failures), all invariants in §3.8 hold after each step.
- JSON round-trip preserves `amount_minor` exactly for values up to `math.MaxInt64` (string encoding).
- Fuzz: decimal-string parser rejects floats-in-disguise (`1e2`, `0.1000000001` beyond minor units,
  `NaN`, `Infinity`, locale separators unless explicitly supported).

---

## 5. CI gates

From Stage 3 (when Go code and CI exist), a PR to `main` cannot merge unless:

1. Build succeeds (Go and web).
2. Lint passes: `gofmt`, `go vet`, `golangci-lint`, ESLint, TypeScript `tsc --noEmit`.
3. Unit, property (bounded), DB, integration, API and component tests pass.
4. Financial invariant and core failure-injection suites pass (from Stage 10; payments set from Stage 8).
5. `go test -race` passes for packages with concurrency.
6. Secrets scan (gitleaks) passes.
7. Dependency scan: no new high/critical vulnerabilities without a documented, time-limited exception.
8. Coverage thresholds (§5.1) met for financial modules.
9. Migrations apply cleanly on an empty database and on a snapshot of the previous schema.
10. No test was deleted, skipped (`t.Skip`, `.skip`, `test.fixme`) or had assertions weakened without
    explicit reviewer approval recorded in the PR.

Nightly: extended fuzzing/property runs, full failure-injection matrix, E2E on all viewports, container
scans, performance tests, and audit hash-chain verification once implemented.

### 5.1 Coverage expectations

Coverage is a floor, not a goal; meaningful assertions matter more than percentages.

| Area | Line/branch coverage target |
|---|---|
| `internal/platform/money`, `internal/ledger`, `internal/payments`, `internal/payouts`, `internal/fees`, `internal/reconciliation` | ≥ 90% lines, ≥ 85% branches, **100% of state transitions** and **every invariant check** covered |
| `internal/auth`, `internal/kyc`, authorization middleware | ≥ 85% lines; 100% of permission checks covered by API authz matrix |
| Other Go modules | ≥ 75% lines |
| `apps/web` | Component tests for every form and money display; E2E for every primary journey |

---

## 6. Test data rules

- **No real personal data**, ever: no real names + IDs combinations, no real phone numbers, no real
  National ID or passport numbers, no real bank/mobile-money account numbers, no real identity document images.
- Use clearly synthetic generators: phone numbers from reserved/test ranges agreed with providers' sandboxes,
  names from a fixture list, ID numbers in an obviously invalid/test format, generated placeholder images
  for KYC documents watermarked "TEST".
- No production database copies in development or CI. If production-like data is ever needed, it must go
  through an approved anonymisation pipeline (Stage 18) and be classified accordingly.
- Provider sandbox credentials are non-production and still treated as secrets (never committed; injected
  via CI secrets).
- Fixture amounts use both USD and ZWG, including edge values (1 minor unit, large values near limits).
- E2E fixtures reset between runs; tests never depend on ordering.

---

## 7. Stage-by-stage introduction

| Stage | Testing deliverables |
|---|---|
| 0 | This strategy. Validation is documentation review and secrets scan only. |
| 3 | Go test harness, testcontainers setup, money package unit/property/fuzz tests, CI pipeline with gates 1–3, 6, 7, 9. Web: Vitest + RTL setup. |
| 4–7 | Auth/KYC/campaign API authz matrices, state-machine tests, Playwright E2E foundation with axe. |
| 8 | Sandbox provider with scripted faults; payment state machine and webhook tests (F1–F7, F13, F14) with state/idempotency assertions against the test-only `ledger.Post` stub; donation-flow E2E against the sandbox. |
| 10 | Ledger property tests, DB trigger/grant tests, invariant checker wired into all suites; ledger-level assertions added to F1, F6, F7. |
| 11 | Payout concurrency and failure tests (F9–F11). |
| 17 | Reconciliation mismatch tests (F12). |
| 18 | Security test suite expansion, ZAP baseline, container scanning gates. |
| 19 | Full financial validation: complete failure-injection matrix, extended concurrency, performance at target load. |
| 20 | Evidence pack for pilot certification (test results, coverage, scan reports). |
