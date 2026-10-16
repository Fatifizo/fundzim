# Stage 13 — Testing

Every result below was run in this session on the development machine (Go 1.27.2, Node 24.21.0, compose stack with
PostgreSQL 17.11). Remote CI is recorded in the completion report.

## 1. Go unit, property and race tests (`internal/risk`, integrations)

| Test | What it proves |
|---|---|
| `TestConditionKinds`, `TestDefinitionValidation` | every condition type; rule safety (unknown fields/signals, depth, size, windows, value types) |
| `TestScoringExplanationAndActions` | score = capped sum; bands; HIGH→REVIEW / CRITICAL→ESCALATE floors; explanation and confidence |
| `TestPropertyDeterministicBoundedAndOrderIndependent` | property test, 4 seeds × 3 000 cases: deterministic, 0–100, order-independent, stable inputs hash |
| `TestSignalFromV2Events` | event → signal mapping for every Stage 13 source; malformed payloads refused; raw UA/IP never kept |
| `TestScreeningCallbackSignature` | HMAC, tampering, replay window, malformed header, unknown fields, missing key; synthetic TEST_ONLY matches |
| `TestRuleVersionValidation` | contribution, automated hold types/durations, code injection, canonical definitions, definition hash coverage |
| `TestStateMachinesAreClosed` | terminal states; RESOLVED only from DECISION_PENDING |
| `TestEvidenceHashIsCanonical` | hashes verify against the jsonb form; tampering detected |
| `TestCheckHoldsFailsClosed` (payments), `TestRiskFactsFailClosedOnOutage` (payouts) | holds enforced; a registry outage stops donations and payouts |
| payouts `TestEvaluateEachBlockingCondition` (+7 Stage 13 cases) | EC-12 risk holds at request and submission; EC-15 potential/confirmed/pending/error; EC-16 classification |

Results: `go test -count=1 ./apps/api/... ./internal/... ./migrations/...` — all 35 packages with tests **ok**; `go test -race`
in `golang:1.27.2` over the same packages — **no failures**; `gofmt -l` clean; `go vet` (incl. `-tags integration`) clean.

## 2. Integration tests (`tests/integration`, real stack, `TestRisk*`)

| Test | Covers |
|---|---|
| `TestRiskRuleLifecycleAndSafety` | unsafe definitions refused; fraud wording refused; activation bypass refused; maker self-approval refused (route, service, **database**); definition immutable in the database; hash binding; FINANCE read-only, SUPPORT none; suspend/reactivate separation; superseding activation (one ACTIVE version); outbox and history |
| `TestRiskEvaluationActionsAndHolds` | pipeline: ALLOW, monitoring, HIGH → case + automated ≤ 7-day withdrawal hold; dedup to one case/hold; redelivery idempotent; payout refused (EC-12) while held; owner cannot reach risk routes; false-positive review, double review refused; case maker-checker releases the hold (proposer = maker, approver = checker); payout succeeds after release |
| `TestRiskStaffHoldsMakerChecker` | REQUESTED donation hold not enforced until a second person activates it; donations refused while held and while release is pending; database refuses a self-release; linked accounts can neither request nor approve a release; no hold on yourself; campaign hold blocks publication until released |
| `TestRiskCaseWorkflowAndEscalation` | no case about yourself; assignee permission; working edges; immutable evidence (database), supersession, hash verification, access log; escalation → compliance RISK_REVIEW case linked to the RISK_CASE, redelivery a no-op; resolve/close/reopen with history; fraud outcome refused (database) |
| `TestRiskScreeningSimulator` | all nine scenarios; NO_MATCH_REPORTED never CLEARED; requester cannot decide; confirmed match escalates; duplicate and out-of-order callbacks; invalid signature monitored and recovered by polling; provider error and timeout are ERROR (never cleared) and retryable; delayed results; webhook rejects bad, stale and unknown callbacks; capabilities report production BLOCKED |
| `TestRiskConcurrentSignalsAndReleases` | 20 concurrent signals → 1 case, 1 hold, 20 evaluations; 5 concurrent release approvals → exactly 1 release |
| `TestRiskReconciliationAndReports` | Go state machines equal the database's; orphaned hold found, nothing changed; support cannot open a case; evaluation scores immutable (database); overview and quality say NOT_MEASURED / BLOCKED |
| `TestRiskSignalSources` | `identity.signed_in` carries a keyed UA hash, never the raw UA or IP, and maps to a SIGNED_IN signal |
| `TestRiskRuleEngineErrorIsAtomic` | failure injection: an ACTIVE rule the engine cannot run makes signal recording fail as a whole (no partial rows), reconciliation reports it, retiring it restores recording |

Results: all 9 Stage 13 tests and the 3 Stage 5 risk tests **pass** (`go test -tags integration -run TestRisk`, 129 s incl. perf).
Full suite (`go test -tags integration ./tests/integration/`, 925 s): every test passed except
`TestPublicExperienceEndpoints` (a campaign review needed a second approval at `public_experience_test.go:295`); it
**passed when rerun alone** and exercises no risk code path — recorded as KI-S13-20 pending the CI run.

### Failure-injection coverage

| Brief §65 | Where |
|---|---|
| database outage | `TestRiskFactsFailClosedOnOutage`, `TestCheckHoldsFailsClosed` |
| screening timeout | `TestRiskScreeningSimulator` (PROVIDER_TIMEOUT) |
| duplicate / out-of-order events | redelivery in `TestRiskEvaluationActionsAndHolds`; DUPLICATE/OUT_OF_ORDER callbacks |
| partial case creation, rule engine error, outbox failure | `TestRiskRuleEngineErrorIsAtomic` (one transaction: signal, evaluation, case, hold, outbox and audit commit together or not at all) |
| hold enforcement failure | fail-closed tests above |
| worker crash | jobs are idempotent and re-runnable (expiry `FOR UPDATE SKIP LOCKED`; simulator delivery claims a row before sending; polling recovers a lost callback — INVALID_SIGNATURE scenario); not killed mid-run in a test |
| cache outage | risk uses no cache; the webhook rate limit uses the platform limiter (fails as configured, Stage 3) — not tested here |

## 3. Web

| Suite | Result |
|---|---|
| `npm run lint`, `npm run typecheck` | clean |
| `npm test` (vitest) | 53 files, 513 tests passed (incl. `risk-components.test.tsx`: wording, TEST labels, normalisers, error mapping, rule approval with hash binding and step-up, release refusal, outcome choices, axe) |
| `npm run build` | succeeded; 12 risk routes |
| `npx playwright test` (mock API, desktop + mobile) | 183 passed, 1 skipped; `staff-risk.spec.ts`: overview, rule approval, case assignment, hold release, screening wording, FINANCE refused, axe and CSP |

## 4. Performance (opt-in `FUNDZIM_IT_PERF=1`, `TestRiskPerf`)


measured 2026-10-16T06:33:41Z UTC; GOMAXPROCS=16; development stack (compose PostgreSQL 17.11, default settings); in-process service calls; dataset after the run: 1423 signals, 1050 evaluations, 24 cases, 20 holds.

| scenario | c | n | p50 ms | p95 ms | max ms | ops/s | errors |
|---|---:|---:|---:|---:|---:|---:|---:|
| signal → evaluation (5 active rules, no action), c=1 | 1 | 400 | 9.0 | 14.0 | 47.4 | 105 | 0 |
| signal → evaluation (5 active rules, no action), c=8 | 8 | 400 | 7.5 | 12.3 | 92.3 | 866 | 0 |
| signal → evaluation → case + automated hold (first) / link (rest) | 8 | 200 | 16.6 | 103.9 | 240.5 | 311 | 0 |
| hold lookup at an enforcement point (WithdrawalHolds) | 8 | 2000 | 0.4 | 0.7 | 3.2 | 18719 | 0 |
| case queue (open, 100) | 8 | 300 | 0.6 | 1.3 | 2.7 | 11844 | 0 |
| evaluation list (100) | 8 | 300 | 1.7 | 5.4 | 571.0 | 512 | 0 |
| risk overview | 8 | 100 | 4.2 | 21.3 | 26.0 | 1364 | 0 |
| risk reconciliation run | 1 | 3 | 24.7 | 27.2 | 27.2 | 42 | 0 |

Screening simulator processing is covered functionally, not timed (the simulator is a local stub; its latency says nothing
about a provider). Velocity rules are measured inside "signal → evaluation" (the five rules include counts, distinct counts
and a sequence over a 1-day window).
