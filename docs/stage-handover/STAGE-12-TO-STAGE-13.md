# Stage 12 → Stage 13 Handover: Trust, Fraud & Risk Engine

> Stage 12 is implemented on branch `stage-12/fees` and **awaits acceptance** (Stages 1–11 too; Stage 9 PARTIAL; Stages
> 10 and 11 PASS WITH CONDITIONS). Do not start Stage 13 until the owner accepts and asks for it. This document prepares
> Stage 13; it implements nothing. Production financial operations remain BLOCKED.

Stage 12 docs: [implementation](../stage-12/implementation.md) · [fee architecture](../stage-12/fee-architecture.md) ·
[security review](../stage-12/security-review.md) · [known issues](../stage-12/known-issues.md). ADR-044.
Stage 1 specifications that Stage 13 refines: [aml-risk-framework.md](../compliance/aml-risk-framework.md),
[transaction-monitoring.md](../compliance/transaction-monitoring.md), [sanctions-screening.md](../compliance/sanctions-screening.md),
[compliance-case-management.md](../compliance/compliance-case-management.md), [operational-controls.md](../compliance/operational-controls.md).

## Preparation topics (brief §62)

| Topic | What exists after Stage 12 | What Stage 13 must decide or obtain |
|---|---|---|
| Donation risk signals | Donation intents (test payments), per-donor and per-campaign intent rate limits, donation limits in `risk.limits` (none approved), fee quotes per user | Signals for velocity, amount anomalies, many donors from one instrument fingerprint (`payments` keeps HMAC fingerprints only), self-donation; thresholds as approved limits |
| Payment risk signals | Payment state machine, UNKNOWN outcomes, contradictory provider evidence (`payment_provider_evidence`, `CONTRADICTORY_EVIDENCE`), late success, provider health | Which payment events raise `risk.risk_signals`; card-testing patterns once a provider exists |
| Provider risk evidence | Provider registry, derived health, no provider selected (ADR-040) | Provider fraud tooling and evidence feeds (PCR-005: 3-D Secure, screening, chargeback process) |
| Campaign risk indicators | Campaign review, policies, reports, compliance restrictions, freeze; `risk` module (signals, versioned model, routing decisions) since Stage 5 | Content and behavioural indicators; review routing by risk |
| Organiser verification | KYC/KYB levels (BASIC/VERIFIED), linked staff identities, compliance restrictions | Re-verification triggers; identity-risk signals |
| Beneficiary verification | Beneficiaries with verification decisions; SELF beneficiary; third-party payees not implemented (LR-024) | Risk of beneficiary substitution; institution payees |
| Payout destination risk | Destination verification, cooling-off, step-up on change, approval invalidation, Stage 11 indicators (recent destination change, multiple destinations) | Scored destination risk; shared-destination detection across campaigns |
| Withdrawal velocity | Stage 11 indicators (count/amount per 7 days against configurable limits; unconfigured = indicator ⇒ REVIEW + DUAL) | Approved thresholds (LR-030); dynamic reserves (LR-020) |
| Refund patterns | Refund requests (cannot execute), refund fee adjustments (TEST fixtures) | Refund-abuse signals once refunds execute |
| Chargeback exposure | Chargeback fee foundations: exposure and recovery status per synthetic donation; no dispute source | Dispute ingestion; exposure-based holds; recovery (LR-080) |
| Fee manipulation risks | Maker-checker on every fee change, terms hashed at submission and bound at approval, quotes from approved versions only, no client-supplied rates, reconciliation recomputes calculations (`ROUNDING_MISMATCH`), per-currency locking | Monitoring of fee changes (frequency, scope narrowing to single campaigns, zero-fee waivers); alerts on reconciliation mismatches |
| Suspicious transaction patterns | Ledger integrity checks, payment/payout/fee reconciliation, audit hash chain | Transaction monitoring rules (round-tripping, structuring, rapid in-and-out), STR preparation (compliance) |
| Compliance restrictions | `compliance.subject_restrictions` projection enforced by campaigns, payments (donors) and payouts | Automatic restrictions from risk decisions (with human review) |
| Risk scoring requirements | `risk` versioned model and decisions (Stage 5), payout eligibility consumes indicators | Model versioning and explainability; owners see coarse reasons only (LR-008) |
| Case management | Compliance cases (maker-checker, notes, links), payout and fee exception/reconciliation queues | Risk case workflow, SLAs, linking risk signals to cases |
| Screening dependencies | No sanctions/PEP screening provider (KI-S5-04, LR-009) | Provider selection; screening at onboarding, payout and periodically |
| Audit requirements | Audit hash chain, evidence records, fee and payout histories | Audit of every risk decision and override; reviewer separation of duties |
| Privacy and retention | Data classification, LR-012 retention open (fee, payout and ledger records never purged) | Retention and minimisation for risk data; DPIA updates (LR-012, data-protection-assessment) |

## Rules Stage 13 must keep

- Automated fraud detection must not be claimed complete before it is built and validated; risk outputs route to human
  review, never auto-approve money movement.
- Fee, payout, payment and ledger controls of Stages 8–12 stay unchanged; TEST environments only until production gates are
  lifted by ADRs with legal sign-off and owner acceptance.
- Owners never see risk scores or compliance detail (LR-008).

## Open blockers carried

Provider selection (ADR-040), settlement evidence (PCR-007), legal/tax items (LR-001, LR-002, LR-016, LR-018, LR-020, LR-024,
LR-028, LR-030, LR-045, LR-056, LR-059, LR-076, LR-080), screening provider (LR-009), accounting review (KI-S10-02), PD-01,
PD-20, PD-23, and the acceptance of Stages 1–12.
