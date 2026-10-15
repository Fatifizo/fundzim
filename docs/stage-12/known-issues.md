# Stage 12 — Known issues

| ID | Issue | Impact | Status |
|---|---|---|---|
| KI-S12-01 | Acceptance of Stages 1–12 not recorded; Stage 9 PARTIAL; Stages 10 and 11 PASS WITH CONDITIONS (recommendations) | Isolated engineering only | OPEN — owner |
| KI-S12-02 | **No fee model decided** (PD-01, LR-028): every policy is synthetic TEST configuration; no commercial rate exists | No fee may be charged | BLOCKED |
| KI-S12-03 | **Tax treatment unknown** (LR-016, LR-045, LR-059): no VAT, income tax, IMTT or DSWT is calculated; fees are not treated as taxable or exempt | Production fees and invoicing BLOCKED | OPEN — TAX_REVIEW_REQUIRED |
| KI-S12-04 | **Revenue recognition** follows the Stage 1/10 capture-time credit only in the TEST ledger (synthetic fixtures) | Production recognition BLOCKED | OPEN — ACCOUNTING_REVIEW_REQUIRED (KI-S10-02, LR-002) |
| KI-S12-05 | **No provider fee is known** (ADR-040; PCR-005/007/013/016): schedules are SYNTHETIC or UNVERIFIED; VERIFIED refused; provider rounding UNKNOWN | Provider-dependent totals UNKNOWN or estimates | BLOCKED (provider) |
| KI-S12-06 | Donor-covered charges are recorded on the intent but not sent to the simulator (the payment amount is bound to the intent amount); fee pass-through to donors is unconfirmed | Charge ≠ payment amount for donor-covered TEST intents | OPEN — provider confirmation |
| KI-S12-07 | Fees are computed on the donation amount, never grossed up on the donor charge (the provider's fee basis is unknown) | Estimates may differ from a provider that charges on the gross | OPEN — PCR-016 |
| KI-S12-08 | Real (test) payments are assessed but never posted (ADR-042); fee journals exist only for TEST_ONLY fixtures and TEST payouts | Ledger revenue reflects fixtures only | BY DESIGN (Stage 12) |
| KI-S12-09 | Refunds and chargebacks cannot be executed (KI-S9-07; no dispute source): reversals run on fixtures; provider refund/chargeback fees UNKNOWN and never assumed refundable | — | BLOCKED (provider) |
| KI-S12-10 | One chargeback-fee assessment per synthetic donation (a second chargeback reuses it) | Fixture limitation | OPEN |
| KI-S12-11 | Payout fee estimates are captured at request time and not re-quoted if evidence changes before completion | Stale estimate possible | OPEN (Stage 13+) |
| KI-S12-12 | A fee reconciliation run scans the latest 5 000 assessments (no paging) | Must page before production | OPEN |
| KI-S12-13 | Manual fee adjustments never post; a ledger correction is a separate Stage 10 maker-checker adjustment referenced by id (not enforced to exist) | Two-step correction | OPEN |
| KI-S12-14 | A fee change has one checker; no dual approval or value threshold for high-impact changes | Governance | OPEN — operational-controls |
| KI-S12-15 | Policy scopes are permanent (unique forever, even after retirement); a scope is reused by adding versions | Operational nuance | ACCEPTED |
| KI-S12-16 | Organiser fee view lists policies for platform-wide, category and campaign scopes but not method/provider-specific ones per method | Disclosure granularity | OPEN |
| KI-S12-17 | No automated fee-manipulation detection (Stage 13); reconciliation recomputation is the control | — | DEFERRED (Stage 13) |
| KI-S12-18 | `fees.quote.user` = 60/hour is an operational starting value, not an approved limit record | — | ACCEPTED |
| KI-S12-19 | Development-database residue: integration tests leave TEST policies (campaign-scoped or retired), retired SYNTHETIC schedules, quotes, assessments and TEST_ONLY fee journals | Shared dev DB only | ACCEPTED |
| KI-S12-20 | Fee quotes p50 ≈ 155 ms (campaign eligibility and provider routing per quote); the staff policy list loads versions per policy (N+1, p50 ≈ 59 ms) | Performance | OPEN |
| KI-S12-21 | The UI has no tier editor (TIERED terms through the API) and no draft editing (PUT through the API); a payout fee amount is shown only after a request (no estimate endpoint) | UI completeness | OPEN |
