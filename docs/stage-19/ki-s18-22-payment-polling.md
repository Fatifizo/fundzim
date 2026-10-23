# KI-S18-22 — Bounded payment status polling

Status: **FIXED** (Stage 19, ADR-051). Severity before the fix: MEDIUM (unbounded provider traffic and audit/attempt
growth for payments that never resolve; no financial misstatement).

## 1. Original behaviour (`internal/payments/poll.go`, `apply.go`, Stage 8/9/18)

| Status | Before Stage 19 |
|---|---|
| UNKNOWN | bounded: `poll_horizon_at = unknown_since + UnknownHorizon (72 h)`; past it, UNKNOWN_HORIZON_EXCEEDED (or NOT_FOUND_AFTER_HORIZON) reference and polling stops |
| CREATED, PENDING, REQUIRES_ACTION, AUTHORISED | **unbounded**: `next_poll_at = now + pollDelay(poll_attempts)` (5 s doubling, capped at 10 min) forever |
| Backoff | deterministic; no jitter — payments made due together poll in lockstep |
| Multiple sweepers | `Sweep` lists due payments then calls `PollPayment`, which locks with `FOR UPDATE SKIP LOCKED` but did **not** re-check that the payment was still due; a sweeper that listed a payment could query it again right after another had (window: listing → lock) |
| Process environment | `Sweep` selected due payments of **every** execution environment; a process without that environment's adapter recorded a RETRYABLE_BEFORE_SEND "query" (nothing sent) and advanced the backoff |
| Worker restart | the lease (`next_poll_at = now + deadline + grace` before the call) and the in-flight sweeper make a restarted worker resume after the lease; no double query from a restart |

Reproduction before the fix (`prefix-poll.log`, this session, budget and due checks disabled in the test process):

```
financial_payment_poll_test.go:71: the provider was queried after the horizon (1 queries)
--- FAIL: TestFinancialPaymentPollBudgetExhaustionAndLateSuccess
```

The double-poll race did not reproduce in one round of 8 concurrent sweeps (the window between listing and locking is
milliseconds); its fix is by construction and the post-fix test asserts at most one query per due period.

## 2. Policy (effective values, from code)

| Parameter | Value | Source / status |
|---|---|---|
| Backoff | 5 s, doubling per poll, capped at 10 min | `pollDelay` (Stage 8, unchanged) |
| Jitter | ±20 % uniform (`PollJitter = 0.2`) on routine polls | Stage 19; `TestPollJitterStaysWithinBounds` |
| Elapsed bound, awaiting statuses | `PollHorizon = 72 h` from the payment's creation | Stage 19 internal safety default, equal to `UnknownHorizon`; **not a provider-documented or approved value** (PCR-010) |
| Elapsed bound, UNKNOWN | `UnknownHorizon = 72 h` from `unknown_since` | Stage 8 (PCR-010) |
| Attempt bound | `MaxStatusPolls = 500` status queries per payment (routine and staff) | Stage 19; independent second stop |
| Effective polls within the horizon | **438** at the unjittered backoff (`TestPollBudgetHorizonBindsBeforeAttemptCap`), so the horizon binds first |
| Provider rate limits / minimum intervals | none configured: no provider is selected (ADR-040); RATE_LIMITED answers follow the same backoff (no Retry-After is available from the adapters) | PCR-010 / provider selection |
| Per-sweep batch | 25 due payments per 5 s sweep per worker (Stage 8) | unchanged — also bounds the burst after an outage |

## 3. Unknown-outcome handling

Budget exhausted (horizon passed for an awaiting status, or `MaxStatusPolls` reached):

- the payment **keeps its status** (PENDING stays PENDING; UNKNOWN stays UNKNOWN) — an exhausted budget is never
  evidence of failure, success or non-existence;
- `needs_reconciliation = true` and one POLL_BUDGET_EXHAUSTED reference (`ON CONFLICT (payment_intent_id, reason)`:
  never multiplied by repeated sweeps), counted in `payments.reconciliation.open` on the operations portal and listed
  in the staff payment reconciliation queue;
- `next_poll_at = NULL`: no routine poll and no staff status query is sent any more (the budget applies to explicit
  queries too, like the UNKNOWN horizon);
- no new payment is created, no CREATE is re-sent, no reservation, credit or posting happens;
- the provider reference, idempotency context and attempts are untouched;
- **resolution**: a later verified webhook applies through `apply()`; when it makes the payment terminal the
  reference becomes RESOLVED_BY_PROVIDER_STATUS (`apply.go`, POLL_BUDGET_EXHAUSTED added to the resolvable reasons);
  Stage 17 statement evidence resolves through an approved maker-checker resolution (source RECON).

Migration `20261024090000_financial_concurrency.sql` adds POLL_BUDGET_EXHAUSTED to
`ck_payment_reconciliation_references_reason` (Down restores the previous list NOT VALID; references are never deleted).

## 4. Tests (all run after the fix in this session: PASS)

| Brief §6.4 case | Test |
|---|---|
| 1 success on first query | `TestPaymentsSandboxSuccessAndIdempotency` (Stage 8, rerun); `TestFinancialPaymentPollErrorClassesNeverFail` (final authoritative answer) |
| 2 failure on first query | `TestPaymentsDeclineThenRetry` (rerun) |
| 3 pending until the budget is exhausted | `TestFinancialPaymentPollBudgetExhaustionAndLateSuccess` (horizon), `TestFinancialPaymentPollAttemptCap` (exactly 500 queries, then none) |
| 4 repeated timeouts, 5 `429`, 6 `5xx`, 7 malformed data | `TestFinancialPaymentPollErrorClassesNeverFail` (scripted in-test fake: RATE_LIMITED, DEFINITELY_NOT_SENT/503, OUTCOME_UNKNOWN/TIMEOUT, unmapped status → UNMAPPED_STATUS reference; status never FAILED, always rescheduled) |
| 8 worker crash between queries, 9 restart after the deadline | lease + in-flight sweeper (`TestPaymentsCrashBetweenCallAndResultIsRecovered`, rerun); the budget is evaluated from stored facts (creation time, attempt count), so a restarted worker cannot extend it |
| 10 multiple workers on one intent, 16 scheduled twice | `TestFinancialPaymentConcurrentSweepsPollOnce` (8 concurrent sweeps + the compose worker: one query) |
| 11 webhook while polling | `TestFinancialPaymentPollErrorClassesNeverFail` (8 concurrent explicit queries while the answer becomes paid: one SUCCEEDED event, one success notice); `TestPaymentsWebhookOrderingAndDuplicates` (rerun) |
| 12 success webhook after review state | `TestFinancialPaymentPollBudgetExhaustionAndLateSuccess` (signed simulator webhook after exhaustion → SUCCEEDED, reference resolved, no query) |
| 13 delayed failure after another authoritative event | `TestPaymentsWebhookOrderingAndDuplicates` (out-of-order scenario 07), contradiction log (Stage 9) |
| 14 status query resolves an earlier ambiguous outcome | `TestPaymentsTimeoutIsUnknownNeverFailed` (rerun) |
| 15 extended provider unavailability | error-class test (503 answers keep the payment scheduled); exhaustion test (budget ends polling without a failure) |
| Environment scoping | the compose worker (LOCAL_SIMULATOR) no longer polls PROVIDER_SANDBOX payments of the error-class test |

## 5. Operational evidence (from the tests above, this session)

| Measure | Observed |
|---|---|
| Effective maximum attempts | 500 (`TestFinancialPaymentPollAttemptCap`: 520 calls → 500 queries) |
| Effective maximum duration | 72 h from creation (awaiting) / from `unknown_since` (UNKNOWN) |
| Payments processed by the four Stage 19 polling tests | 4 |
| Resolved normally | 2 (scripted paid answer; late signed webhook after exhaustion) |
| Escalated | 2 (POLL_BUDGET_EXHAUSTED: horizon, cap) |
| Left unresolved by design | 2 (the cap test's payment stays PENDING with an open reference; the concurrent-sweeps payment (pending_forever) stays PENDING inside its budget) |
| Duplicate financial effects | 0 (one SUCCEEDED event and one `payment_succeeded` per payment) |
| Queries after the budget expired | 0 (shifted-clock attempts after exhaustion: 0; total unchanged across further sweeps) |
| Audit growth | 500 identical answers produced ≤ 2 status-poll audit events (Stage 18 dedupe) |

## 6. Limitations

- 72 h and 500 are internal safety defaults, not provider-approved values; a selected provider's documented status
  semantics (PCR-010) must replace them by configuration with an owner's approval.
- No Retry-After is honoured because no adapter reports one; a real adapter that does should feed it into the next poll.
- The budget does not distinguish "provider down" from "payment pending": both end in a reconciliation reference,
  which is the safe outcome.
