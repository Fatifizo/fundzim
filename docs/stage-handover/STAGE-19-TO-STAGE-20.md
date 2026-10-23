# Stage 19 → Stage 20 handover (Production / Pilot Certification)

Status of Stage 19: complete, **awaiting acceptance** (`docs/governance/stage-acceptance-register.md`, row 19
NOT_RECORDED). Stage 20 must not start until the owner asks for it. This document prepares it; it implements nothing.
Stage 20 is a certification and go/no-go stage: it requires decisions and evidence that engineering cannot produce.

## 1. Verified prerequisites (engineering)

| Item | Evidence |
|---|---|
| Stage 17–19 code on one stacked branch | `stage-19/financial-concurrency` (from `801babe`); nothing merged to `main` (Stage 0) |
| Financial invariants documented and tested under concurrency, retries, duplicates, ambiguous outcomes, crashes | docs/stage-19/financial-invariants.md, concurrency-test-plan.md, fault-injection.md |
| KI-S18-25 and KI-S18-22 fixed | ki-s18-25-payout-reservation-race.md, ki-s18-22-payment-polling.md, ADR-051 |
| Ledger lock-order deadlock fixed (F-S19-01) | database-contention.md |
| Financial gates still off | `TestFinancialGateConstantsStayOff`, `TestSecurityFinancialGatesInDatabase` |
| Load and endurance baselines on the development host | benchmark-results.md (not production capacity) |

## 2. Unresolved Stage 19 issues

docs/stage-19/known-issues.md §2: KI-S19-01 (audit chain lock caps posting throughput; hot-account convoys),
KI-S19-04 (internal poll budget values need provider semantics, PCR-010), KI-S19-09 (payout reconciliation is a
sequential full scan with provider queries — needs incremental runs before production volumes), test-coverage limits
KI-S19-05 to -08, KI-S19-10 (no 10⁵–10⁶ payment/payout volume), KI-S19-11 (no approved targets).

## 3. Required owner decisions

- Formal acceptance (or rejection) of Stages 0–19, in order.
- Capacity and performance targets (none exists; Stage 19 numbers are baselines only).
- RPO/RTO approval (Stage 18 values are proposals).
- Approval of the poll budget values (72 h, 500) or provider-specific replacements.
- Pilot limits: users, amounts, rails, duration.
- Named approvers for the go/no-go record; named compliance officer and DPO (ROADMAP Stage 20).

## 4. Required legal, accounting and provider decisions

- Provider selection superseding ADR-040, with due diligence, sandbox certification (conformance suite) and contract
  (PCR-* items, including status/finality semantics PCR-009/PCR-010 and statement formats PCR-007/016/045/051).
- Accounting review before any settlement posting (KI-S10-02) and of the chart of accounts / fee recognition.
- Every P0 `LR-xxx` item (docs/compliance/open-legal-questions.md §5), retention periods (LR-012, LR-074), consent
  rules (LR-010, LR-011, LR-012, LR-023), POTRAZ data-controller licence (LR-010, LR-057).

## 5. Required operational infrastructure

Production and staging environments (Stage 18 reference architecture is a proposal), KMS/HSM, managed PostgreSQL
with backups and point-in-time recovery, a process supervisor with health-based restarts, alert delivery (paging),
on-call, off-host immutable backups, TLS termination, WAF/DDoS protection, IaC.

## 6. Required security evidence

Independent penetration test; WebAuthn for staff, MFA reset and break-glass workflows (F-S18-03, -04, -12);
authenticated internal metrics (F-S18-05 residual); artifact signing; production secret-manager deployment.

## 7. Tests to repeat after any change to financial code

`go test -race` (all packages); the full integration suite including every `TestFinancial*`, `TestPayments*`,
`TestPayout*`, `TestLedger*`, `TestFee*`, `TestReconciliation*`, `TestRisk*`, `TestSecurity*`; the worker-crash
exercise; `TestFinancialLoad` tiers; the ledger volume harness on a scratch database; the restore drill; remote CI.

## 8. Explicit production blockers

Production financial operations remain **BLOCKED**: no provider is selected; `psp.LiveExecutionEnabled`,
`payouts.LiveExecutionEnabled`, `ledger.ProductionPostingEnabled`, `fees.ProductionFeesEnabled`,
`reconciliation.ProductionImportsEnabled` / `SettlementPostingEnabled` / `ProductionAdjustmentsEnabled`, messaging and
referral gates are false and CHECK-backed; no accounting review, legal sign-off, production infrastructure, penetration
test or stage acceptance exists. A Stage 19 PASS (with conditions) is not permission to operate.
