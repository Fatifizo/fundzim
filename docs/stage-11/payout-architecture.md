# Stage 11 — Payout architecture

Decision: [ADR-043](../adr/ADR-043-withdrawals-and-payouts.md). Specifications implemented: Stage 1
[payout-lifecycle.md](../payments/payout-lifecycle.md) (Y1–Y14) and
[payout-eligibility-and-controls.md](../payments/payout-eligibility-and-controls.md) (EC-01–EC-22); Stage 2 draft
`design/sql/0016_payouts.sql`.

## 1. Module

`internal/payouts` (one package, extending the Stage 5 destinations module; imports platform, audit, organisations, kyc,
beneficiaries, campaigns, ledger, psp, risk — design-baseline §4; never payments).

| File | Responsibility |
|---|---|
| `destinations.go`, `review.go` | Stage 5 destinations; Stage 11: `payout_readiness`, approval invalidation on change/suspension/expiry |
| `eligibility.go` | The pure eligibility engine (22 checks, outcome, tier, owner-safe reason category) |
| `gather.go` | Facts from owning modules' public APIs; limit resolution from `risk.limits` |
| `requests.go` | Domain types, transitions with events/audit/outbox, exceptions, request (slot → reservation), cancel |
| `orchestration.go` | Approvals, recheck, submission, dispatch, observations, polling, webhooks, `syncLedger`, recovery |
| `reconcile.go` | Reconciliation run, mismatch references, exception workflow |
| `views.go` | Owner and staff read models, availability preview, overview |
| `http.go` | API (owner, staff, sandbox controls) |
| `jobs.go` | `payouts.submit` (2 s), `payouts.poll` (5 s), `payouts.recover` (30 s), `payouts.reconcile` (15 min) |

Provider side: `internal/psp/payouts.go` (`PayoutProvider`, payout capabilities, `RoutePayout`) and
`internal/psp/sandbox/payouts.go` (the simulator). Ledger read API: `internal/ledger/payouts.go`
(`CampaignBalances`, `JournalByKey`, `IntegrityConfirmed`, TEST-only `FundCampaign` fixture).

## 2. Data (schema `app`, migration `20261014090000_payouts.sql`)

| Table | Kind | Notes |
|---|---|---|
| `payout_requests` | status-bearing (machine `payout`) | the withdrawal request; amount/parties/destination snapshot/environment immutable; single in-flight index |
| `payout_reservations` | status-bearing (machine `payout_reservation`) | ACTIVE/BLOCKED/CONSUMED/RELEASED/EXPIRED; FKs to ledger journals |
| `payout_approvals` | append-only | FINANCE decisions per round; SoD guard |
| `payout_eligibility_decisions` | append-only | 22 checks, hashed; validity re-checked by the database |
| `payout_attempts` | complete-once | every provider call; resubmission guard |
| `payout_events` | append-only | applied transitions and not-applied observations |
| `payout_reconciliation_references` | append-only | mismatches found by runs |
| `payout_exceptions` (+ `payout_exception_events`) | status-bearing (+ append-only history) | the operations queue |
| `sandbox_provider_payouts` (psp) | simulator state | masked synthetic destination only |

Every status change of a payout requires an applied `payout_events` row in the same transaction (deferred trigger).
Runtime grants: `fundzim_app` (no DELETE/TRUNCATE; UPDATE only on status-bearing tables); the payout CHECK helper
functions are granted explicitly.

## 3. Flow

```
owner ──POST /campaigns/{c}/payout-requests──▶ gather facts ─▶ Evaluate (REQUEST) ─▶ refused: decision recorded, 422
                                                      │ accepted
                                                      ▼
  app tx: payout PAYOUT_REQUESTED (+decision, events, audit, outbox)   ── claims the single in-flight slot
  ledger (own pool): PAYOUT_RESERVED  payable → payout_pending        ── row lock; refuses overdraw
  app tx: reservation ACTIVE (FK journal) + PAYOUT_REQUESTED → PENDING_REVIEW
FINANCE approvals (step-up; SoD; tier SINGLE/DUAL) ─▶ APPROVED
job payouts.submit: Evaluate (PRE_SUBMIT) ─▶ SUBMITTED  ─▶ ledger PAYOUT_SUBMITTED  pending → in_transit
                                         ─▶ attempt IN_FLIGHT ─▶ simulator CreatePayout (outside any transaction)
observations (sync, webhook via inbox, poll) ─▶ PROCESSING / UNKNOWN / COMPLETED / FAILED / REVERSED
syncLedger: COMPLETED in_transit → psp_settled · FAILED in_transit → payable · REJECTED/CANCELLED pending → payable ·
            REVERSED psp_settled → payable
```

## 4. Environments

TEST payouts only (ADR-043 §1). `PAYOUTS_EXECUTION_ENVIRONMENT=DISABLED` (default) refuses requests
(`PAYOUTS_DISABLED`) and runs no submission, dispatch or polling. `LOCAL_SIMULATOR` needs `PAYMENTS_SANDBOX_ENABLED` and
routes only to a sandbox provider with a routable payout capability (seeded: ECOCASH/USD v2 with payouts to
ECOCASH, ONEMONEY and BANK_TRANSFER — simulator contract facts).
