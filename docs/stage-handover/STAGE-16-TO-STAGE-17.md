# Stage 16 → Stage 17 handover (Financial Reconciliation & Settlement Operations)

Status of Stage 16: complete, **awaiting acceptance** (`docs/governance/stage-acceptance-register.md`, row 16
NOT_RECORDED). Stage 17 must not start until the owner asks for it. This document prepares it; it implements nothing.
Stage 16 touched no financial path (no ledger, payment, payout or fee code; discovery never reads money and never
attributes a donation).

## 1. What Stage 17 inherits

| Area | Already built | Where | State |
|---|---|---|---|
| Reconciliation design | `recon` schema owner, immutable deduplicated imports, matches, discrepancy types, postings only through named ledger rules, payouts gated on reconciliation freshness, SEV1 → provider hold | ADR-030, `docs/database/reconciliation-schema.md` | design only; schema `recon` exists in grants, no tables built |
| Payment reconciliation references | `payments.openRecon` opens references (UNKNOWN horizon, not found, amount/currency mismatch, anomaly, late/duplicate success, parked deadline, unmapped status); `AwaitSettlement` | `internal/payments/recon.go` | references open; **nothing is ever marked reconciled** — needs settlement data |
| Ledger reconciliation | integrity and balance recompute checks; `SettlementReconciliation: NOT_PERFORMED (PCR-007, PCR-016; Stage 17)` | `internal/ledger/reconcile.go` | internal consistency only |
| Payout reconciliation | provider status vs local state, exceptions with investigate/resolve, references | `internal/payouts/reconcile.go` | simulator only; settlement NOT_PERFORMED |
| Fee reconciliation | recompute from stored canonical inputs, compare with assessments and fee journals | `internal/fees/reconcile.go` | TEST fees only; provider fee evidence SYNTHETIC/UNVERIFIED |
| Unknown outcomes | UNKNOWN payments and payouts resolved only by authoritative status, webhook or reconciliation; payouts never resubmitted while UNKNOWN; retry budget halts DEFINITELY_NOT_SENT loops (KI-S11-20) | Stages 8, 11, 15 | built |
| Operations portal | module snapshots, exception center (resolution stays in modules), reports (bounded, audited exports) | Stage 14 | built |
| Evidence | `audit.evidence_records` with hashes and legal holds | Stage 3/5 | built |

## 2. Scope to settle in Stage 17

| Topic | Starting point / open question |
|---|---|
| Provider settlement imports | file/API formats unknown (PCR-007, PCR-045, PCR-051); imports must be immutable, hashed, deduplicated by file and external line id, stored as C2/C3 evidence in the private bucket |
| Payment reconciliation | match provider transactions to payments by provider reference + amount + currency; close or escalate the existing `payments` references; never infer a match at query time |
| Ledger reconciliation | trial balance per currency; settlement postings only through new versioned ledger rules (`SETTLEMENT_MATCHED`, `SETTLEMENT_DISCREPANCY`); the ledger stays TEST until an ADR, accounting review and legal sign-off |
| Payout reconciliation | disbursement statements vs payout attempts and provider references; UNKNOWN payouts resolved only by statement or status evidence |
| Fee reconciliation | provider fee evidence from statements (PCR-016) vs assessments; VERIFIED evidence only after provider selection (ADR-040) |
| Unknown outcome resolution | statement evidence as an authoritative resolver; contradictory evidence recorded (`app.payment_provider_evidence`), escalated, never applied blindly |
| Bank statement matching | only FundZim's own platform-fee account under Model A (ADR-013); donor funds never settle to FundZim |
| Discrepancy detection | missing on either side, duplicate, amount/currency/fee/payout/status mismatch, settlement short/over (ADR-030 §4), with severity, owner queue and age-based escalation |
| Exception workflows | in the owning module, surfaced through Stage 14 exceptions; maker-checker for anything that moves money (ledger adjustments) |
| Financial audit evidence | every import, match, discrepancy and resolution referenced to evidence records; reports bounded, step-up, audited |
| Operational dashboards | reconciliation freshness per provider and currency; open discrepancies by age and severity; never present TEST data as business performance; currencies never combined |
| Concurrency safety | idempotent imports (unique constraints), one active match per line, advisory locking per provider/currency/period where needed; re-import of the same file is a no-op |

## 3. Dependencies and blockers

| ID | Item |
|---|---|
| ADR-040 | no payment or payout provider is selected: real statements cannot exist; only synthetic fixtures (labelled TEST) are possible |
| PCR-007, PCR-016, PCR-045, PCR-051 | settlement schedules, report formats and fee reporting are unconfirmed |
| KI-S10-02 | accounting review of the chart and posting rules |
| KI-S10-04 / KI-S11-03 | settlement reconciliation NOT_PERFORMED; production available funds BLOCKED |
| LR-001, LR-002, LR-003, LR-012, LR-076 | custody, accounting and retention questions that constrain settlement postings and record keeping |
| Gate B | the roadmap's Gate B lifts only for reconciled rails |

## 4. Constraints carried forward

- Production financial operations stay BLOCKED; no real statements, settlement evidence or provider integrations may be
  fabricated. Synthetic fixtures must be labelled TEST_ONLY and never shown as real funds.
- Only `internal/ledger` writes ledger tables; postings go through versioned rules; corrections are maker-checker.
- Unknown ≠ failed; a payout in UNKNOWN is never resubmitted.
- Currencies are never summed, netted or converted.
- Stage 16 discovery must keep ignoring money: no "raised" figure may be added to discovery, trending or analytics as a
  side effect of settlement data (ADR-048 §7).

## 5. Open Stage 16 items relevant to Stage 17

None blocks Stage 17. KI-S16-08 (retire the Stage 7 `/public/events` counters) and KI-S16-04 (analytics retention, LR-012)
are scheduled for Stage 18; see [../stage-16/known-issues.md](../stage-16/known-issues.md).
