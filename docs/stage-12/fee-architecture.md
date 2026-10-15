# Stage 12 — Fee architecture

Decision record: [ADR-044](../adr/ADR-044-fees-and-platform-revenue.md).

## 1. Module

`internal/fees` owns the `app.fee_*` tables. It imports `platform`, `audit` and `ledger` only (ADR-044 amends the Stage 2
graph, which had `fees` at layer 2 with no ledger import). Facts from other modules arrive through interfaces implemented in
`internal/app/fees.go`:

| Interface | Implemented over | Used for |
|---|---|---|
| `fees.Campaigns` | `campaigns.Service.Load`, `EvaluateDonation`, `OwnerAccess` | campaign scope (category), donation eligibility, organiser access |
| `fees.Routing` | `psp.Service.AvailableMethods`, `Route`, `Providers` | method → provider for quotes; sandbox check for SYNTHETIC evidence |
| `fees.Identities` | `kyc.Service.ActorIdentities` | linked-account separation of duties |

`payments` and `payouts` import `fees` (allowed by the Stage 2 graph): `payments.FeeQuoter` (quote binding) and
`payouts.PayoutFees` (estimate and assessment). `internal/archtest` registers the module, its imports and its 13 tables.

## 2. Layers inside the package

```
http.go (routes, step-up time from the principal)            ← no business rules
  └─ service files: policies, schedules, quotes, assess, adjustments, fixtures, reconcile, reports
        ├─ calc.go — pure: Terms.Amount, Split, DonationFees, PayoutFees, FeeReversal, Compute(Inputs) → Outcome
        ├─ PostgreSQL (app pool): fee tables, DB guards (CHECKs, triggers, transition machines)
        └─ ledger.Service (its own pool, fundzim_ledger): PostOnce, JournalAmountsByKey, FeeMovements, SyntheticAllowed
```

The calculation engine never touches I/O, the clock or configuration: callers resolve the policy versions and provider
evidence in force, build canonical `Inputs` and call `Compute`. Reconciliation calls the same `Compute` on stored inputs.

## 3. Data flow

| Flow | Steps |
|---|---|
| Quote | authorise user → rate limit (`fees.quote.user`) → campaign scope and eligibility → method → provider → shared currency lock → resolve platform and provider-cost policy versions and provider evidence at *now* → `Compute` → store calculation + components + quote (ACTIVE, expiry) → fee history |
| Intent | payments transaction: `QuoteForIntent` (validate the caller's quote under the currency lock: owner, ACTIVE, unexpired, same campaign/amount/currency, same versions in force — or create a system quote) → insert intent with `fee_quote_id` → `MarkQuoteBound` |
| Assessment (payment) | outbox `payments.payment_succeeded` → consumer `fees.payments` → quote bound to the intent → one assessment per assessable component (`ON CONFLICT DO NOTHING`), `NOT_POSTED_TEST_PAYMENT` |
| Payout | request transaction: `EstimatePayoutFee` stores a calculation, the estimate goes on the payout row (immutable) → completion: `AssessPayoutFee` → platform-absorbed known fee: `PAYOUT_FEE` journal (retried by `fees.maintenance`) |
| Fixtures (TEST_ONLY) | assessments (`PENDING_POSTING`) → ledger journals (deterministic keys) → accounting references + `POSTED`; reversals: journals first, then the adjustment |
| Reconciliation | recompute, compare with journals, provider reports and expected postings → idempotent detections |

## 4. Concurrency

- Quote creation and intent binding take a per-currency transaction advisory lock in SHARED mode; every policy-version and
  provider-schedule lifecycle change takes it EXCLUSIVE (`lockCurrency`). A quote is either committed before a change (and
  invalidated by it) or computed after it. Found by `TestFeeConcurrency` (a quote committed after the invalidation).
- Version lifecycle changes lock the version row (`FOR UPDATE`); two activations with the same start collide on
  `uq_fee_policy_versions_active_start` (`FEE_POLICY_CONFLICT`).
- Assessments, adjustments, accounting references and journals are unique on business identities, so concurrent duplicates
  collapse (`TestFeeConcurrency`: 100 duplicate runs → one set).
- The adjustment guard locks the assessment row before checking the net never goes below zero.

## 5. What stays BLOCKED

Production fee charging, provider fee verification, revenue recognition beyond the TEST ledger, tax, fee pass-through to
providers and settlement-based reconciliation (see [known-issues.md](known-issues.md)).
