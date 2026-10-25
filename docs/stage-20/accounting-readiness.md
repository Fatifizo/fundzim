# Stage 20 — Accounting and financial-operations readiness

**Area result: BLOCKED.** The double-entry engine is technically sound (financial-certification.md), but the
accounting model it implements has **no professional review**: KI-S10-02 (chart of accounts, posting rules and donation
accounting model) is OPEN with owner responsibility. No accounting policy is invented here.

| Area | Implemented (TEST only) | Decision required from the accountant / financial owner | Status |
|---|---|---|---|
| Chart of accounts | Stage 10 chart (`ledger.account_class_signature()`), per-campaign liabilities, provider clearing/settled accounts, suspense | approve the chart and the liability treatment of donor funds under Model A | BLOCKED (KI-S10-02) |
| Revenue and fee treatment | Stage 12 TEST fee policies, assessed vs recognised fees, `PAYOUT_FEE` expenses | fee model (PD-01), recognition point, tax treatment | BLOCKED (no fee model; `fees.ProductionFeesEnabled = false`) |
| Refunds and reversals | refund requests maker-checker, not executable; reversal journals maker-checker | refund accounting and who bears provider fees | BLOCKED |
| Provider fees | evidence SYNTHETIC/UNVERIFIED only | treatment of provider fees per provider contract | BLOCKED (ADR-040) |
| Settlement matching | Stage 17 matching engine on canonical TEST statements; settlement posting disabled | settlement policy and statement formats | BLOCKED (`SettlementPostingEnabled = false`, PCR-007/016) |
| Unresolved payments | UNKNOWN / POLL_BUDGET_EXHAUSTED → reconciliation references | treatment of unresolved items at period end | BLOCKED |
| Payout accounting | reserve → submit → complete/fail journals | approval of payable/pending/in-transit model | BLOCKED |
| Reconciliation exceptions | maker-checker resolutions, adjustments | materiality and resolution policy (no SLA invented) | BLOCKED |
| Accounting-period closure | maker-checker period close, prior-period postings | close calendar and approvers | BLOCKED |
| Audit evidence | immutable journals, audit chain, evidence records | what auditors require; retention (LR-012) | BLOCKED |
| Financial reporting | TEST revenue report per currency (no cross-currency totals) | statutory and management reports required | BLOCKED |
| Failed and ambiguous transfers | UNKNOWN never FAILED; FAILED releases reservations | accounting for in-transit items under ambiguity | BLOCKED |

Production financial operations stay blocked until these decisions are recorded (with the decider, date and evidence)
and implemented as new posting-rule versions where needed.
