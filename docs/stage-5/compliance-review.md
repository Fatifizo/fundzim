# Stage 5 — Compliance Cases and the Risk Foundation

> Code: `internal/compliance`, `internal/risk`, migrations `20261009150500_risk.sql`, `20261009150600_compliance.sql`.
> Design inputs: [compliance/aml-risk-framework.md](../compliance/aml-risk-framework.md) and siblings.
> Nothing here is suspicious-transaction reporting or a regulatory filing; reporting duties are
> LEGAL_REVIEW_REQUIRED (see the open legal questions register).

## 1. Compliance cases

The compliance module runs **only** on the `fundzim_compliance` database role. It owns the `compliance`
schema, reads the risk outputs (SELECT only), and writes audit events and outbox events only through the
SECURITY DEFINER gateways (prefix allow-list `compliance.*` / `risk.*`).

**State machine `compliance_case`** (Go `compliance.Transitions` = `app.status_transitions`):

```
OPEN → ASSIGNED → IN_REVIEW → AWAITING_INFORMATION → IN_REVIEW
                      IN_REVIEW → ESCALATED → IN_REVIEW | RESOLVED
                      IN_REVIEW → RESOLVED → CLOSED ;  RESOLVED | CLOSED → IN_REVIEW (reopen)
```

**Opening:** from staff (`POST /admin/compliance/cases`, `case.create`) or automatically from the outbox
events `kyc.case_escalated`, `beneficiaries.verification_escalated` and `risk.escalation_recommended`
(consumer `compliance.open_case`; one case per trigger even under duplicate delivery — dedupe in
`compliance.compliance_case_triggers`; a new trigger for a subject with an open case links to it).
Cases have a human case number, severity `S1`–`S3`, links to subjects (`USER`, `ORGANISATION`,
`BENEFICIARY`, `PAYOUT_DESTINATION`, `KYC_CASE`, `KYB_CASE`) and encrypted notes (AES-256-GCM with
`COMPLIANCE_FIELD_ENCRYPTION_LOCAL_KEY`; never logged, never returned outside staff endpoints).

**Resolution decisions:** `CLEARED`, `EDD_CONDITIONS`, `RESTRICT`, `SUSPEND`, `OFFBOARD`, `CONFIRMED_FRAUD`.
`RESTRICT`, `SUSPEND`, `OFFBOARD` and `CONFIRMED_FRAUD` are **maker-checker**: the resolution is PROPOSED and
takes effect only when a different staff member approves it (`approve-resolution`). A resolution does not
yet act on accounts or funds in Stage 5 — it is recorded and emitted (`compliance.case_resolved`); enforcement
belongs to later stages.

**Who may act:** `compliance.case.view` to read, `case.manage` to act (step-up checked in the handler for
resolve / approve / close / reopen). A staff member linked to a subject (directly, through their personal
account, or as a member of a subject organisation) cannot assign themselves, decide or approve
(`403 SELF_DECISION_FORBIDDEN`), and the checker is never the decider — both in code and in database
triggers. If the conflict check cannot run, the action fails (`503 SUBJECT_CHECK_UNAVAILABLE`), never opens.
Cases flagged `RESTRICTED_STR` are hidden by **row-level security** unless the transaction was granted STR
access after a fresh step-up by a holder of `str.prepare`.

## 2. Verification-to-compliance link

A verification reviewer's `escalate` moves the KYC/KYB case to `ESCALATED` and emits `kyc.case_escalated`; the
compliance case is opened asynchronously by the worker. `return` brings the verification case back to
review. Compliance outcomes do not change verification cases automatically in Stage 5.

## 3. Reviewer roles (seeded permissions)

| Role | Verification | Compliance |
|---|---|---|
| KYC_REVIEWER | queue, documents (assigned + step-up), KYC/KYB/beneficiary/destination decisions, `risk.view` | — |
| COMPLIANCE | queue, documents, KYC decisions, destination verification, **identity-number reveal**, verification-policy maker and checker (different people) | cases (view, manage), overrides |
| SUPPORT, ADMIN, SUPER_ADMIN | **none** of the verification or KYC permissions | none |

## 4. Risk foundation: signal ≠ score ≠ decision

| Layer | Table | Meaning |
|---|---|---|
| **RISK_SIGNAL** | `risk.risk_signals` | An observation from a domain event (`DOCUMENT_REJECTED`, `KYC_REJECTED`, `KYB_REJECTED`, `DUPLICATE_IDENTITY`, `BENEFICIARY_REJECTED`, `BENEFICIARY_ESCALATED`, `DESTINATION_CHANGED`, `DESTINATION_REJECTED`). Append-only; deduplicated on `(source_event_id, signal_type)` |
| **RISK_SCORE** | `risk.risk_assessments` | Explainable rule-weighted score of model `risk-model-v1`: each rule counts signals in a window, fires at its threshold and adds its weight; the rating band is `LOW`/`STANDARD`/`ENHANCED`/`RESTRICTED`. Every assessment stores the model version, the rule-by-rule explanation and the limit versions used |
| **RISK_DECISION** | `risk.risk_decisions` | Routing only: `NO_ACTION`, `MANUAL_REVIEW` (ENHANCED), `ESCALATE` (RESTRICTED → `risk.escalation_recommended` → compliance case) |

**A score is never proof of fraud.** No rule, code or decision is named as a fraud finding; decisions only
route subjects to humans, and fraud conclusions exist only as a human compliance resolution
(`CONFIRMED_FRAUD`, maker-checker). Rule codes describe observations.

**No hard-coded thresholds:** weights, thresholds, the window and rating bands are `risk.limits` rows of type
`INTERNAL_RISK` (the brief's INTERNAL_POLICY), separate from `REGULATORY` and `PROVIDER` limits, with
source, approval owner, effective dates and review date. Changes go through `risk.limit_change_requests`
(maker-checker). The model combines limit types by "most restrictive" (routes to humans sooner). If any
required limit is missing the model **fails closed** (`MODEL_LIMITS_UNAVAILABLE` → manual review), never
open. The baseline values are a `MIGRATION_BASELINE` starting point, not calibrated thresholds.

Reviewers with `risk.view` see the latest assessment and signals for USER/ORGANISATION subjects in the case
detail. Full fraud/risk engine work is Stage 13.
