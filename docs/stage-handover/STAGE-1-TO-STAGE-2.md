# Stage 1 → Stage 2 Handover

**From:** Stage 1 — Regulatory, Compliance, Funds-Flow & Payment Operating Architecture (design only).
**To:** Stage 2 — System Architecture & Database Design ([ROADMAP.md](../ROADMAP.md)).
**Date:** 2026-10-08. **Status of inputs:** design-complete. Nothing here has been reviewed by counsel, a
provider or a regulator. A Stage 1 PASS is **not** permission to operate or to move real money.

This document is an index and checklist for Stage 2. It does not restate the specifications; where this
document and a linked specification differ, **the specification wins**. Stage 2 must not reopen a Stage 1
decision without a superseding ADR.

---

## 1. Decisions Stage 2 must design to

| # | Decision | Source | What Stage 2 must do |
|---|---|---|---|
| D1 | **Operating Model A (PSP-mediated)** is provisional for the MVP. A licensed PSP collects, holds, settles and disburses. FundZim orchestrates and keeps records, and never receives donor money. Model C (direct beneficiary settlement) is kept open for verified organisations only. Model B is not selected. If neither A nor C is available, launch is **blocked**. | [operating-model-decision.md](../compliance/operating-model-decision.md), [ADR-013](../adr/ADR-013-regulatory-operating-model.md) | Model `provider_capabilities.custody_model` (`PSP_POOL` / `MERCHANT_SETTLEMENT` / `SPLIT_DIRECT`) and `campaigns.settlement_model` (`MODEL_A` / `MODEL_C`). Routing must **refuse** `MERCHANT_SETTLEMENT` for campaign donations. |
| D2 | **Accounting is separate from custody.** Ledger balances are claims and obligations, not FundZim cash and not spendable stored value. There are no wallets and no user-to-user transfers. | [settlement-and-custody-model.md](../ledger/settlement-and-custody-model.md), [ADR-014](../adr/ADR-014-accounting-separated-from-custody.md) | Use the Stage 1 chart of accounts (§3.2 of that doc), which supersedes LEDGER §3.2. Plan DB enforcement for SC-1 to SC-6. |
| D3 | **Risk-based identity verification**: levels and status are orthogonal fields, and gates are policy configuration. | [kyc-architecture.md](../compliance/kyc-architecture.md), [kyb-architecture.md](../compliance/kyb-architecture.md), [ADR-015](../adr/ADR-015-risk-based-identity-verification.md) | Design the `kyc` schema separation, the read-only level/status mirrors on `users`/`organisations`, and `kyc_gate_policy`. |
| D4 | **Beneficiary is modelled separately from the owner**, and must be verified before payout. Fundraising authority (PVO Act) is a gate. | [beneficiary-verification.md](../compliance/beneficiary-verification.md), [ADR-016](../adr/ADR-016-beneficiary-verification-before-payout.md) | Design `campaign_beneficiaries`, `institution_payees`, `payout_destinations` and `kyc.fundraising_authorities`, with the "exactly one payee reference" constraint. |
| D5 | **Payouts fail closed, under maker-checker and segregation of duties.** | [payout-eligibility-and-controls.md](../payments/payout-eligibility-and-controls.md), [operational-controls.md](../compliance/operational-controls.md), [ADR-017](../adr/ADR-017-payout-approval-segregation-of-duties.md) | Enforce approver ≠ requester and DUAL distinctness with DB triggers. Design the pending-approval object with expiry. |
| D6 | **Currency isolation; FundZim never converts.** Every journal is single-currency, and a rail is offered only if it settles in the donation currency. | [currency-and-fx-policy.md](../payments/currency-and-fx-policy.md), [ADR-018](../adr/ADR-018-currency-isolation-and-fx.md) | Every money column pair is `amount_minor bigint` + `currency char(3)`, and every account is per currency. There are no FX tables in the MVP; the future FX record shape is in §6 of the policy, for reference only. |
| D7 | **Audit events reference evidence; they never embed it.** | [audit-evidence-model.md](../compliance/audit-evidence-model.md), [ADR-019](../adr/ADR-019-regulatory-evidence-management.md) | Design `evidence_records` and `evidence_holds` (append-only, hash-verified) alongside the AUDIT.md hash chain. |
| D8 | **Revised payment and payout state models**: `UNKNOWN` is a state, `AUTHORISED` exists, and payout states are renamed. | [ADR-020](../adr/ADR-020-payment-payout-state-model-revision.md) | Use the state names and ranks below. Stage 0 `outcome_unknown`, `PAID`, `RETURNED` and the payout `REQUESTED` and `UNDER_REVIEW` names are retired. Campaign `UNDER_REVIEW` remains. |

## 2. Financial trust boundaries

Diagram: [funds-flow-architecture.md §1](../payments/funds-flow-architecture.md). Stage 2 must carry these
into the [THREAT-MODEL.md](../THREAT-MODEL.md) update, which is a Stage 2 deliverable:

| Boundary | Rule |
|---|---|
| Donor/owner browser → FundZim | Untrusted. A redirect or client claim never confirms a payment. Every mutation is idempotent (`Idempotency-Key`) and authorised server-side. |
| PSP → FundZim (webhooks) | Authenticated (signature + replay window + dedupe) through the inbox. Where a provider has unsigned callbacks, the status is confirmed only by an authenticated status query ([provider-capability-matrix.md](../payments/provider-capability-matrix.md)). |
| FundZim → PSP | FundZim instructs, and the PSP holds the money. `payment_id`, `payout_id` and `refund_id` are the provider idempotency references and never change on retry. |
| PSP pool ↔ FundZim operating bank | Only platform-fee remittance (and FundZim's own funding or recovery) crosses. SC-5 limits the posting rules that touch `asset:fundzim_operating_bank`. |
| Application ↔ `kyc` schema / `private-kyc` bucket | Only the `kyc` module touches them. Other modules get level/status through its interface. Every access is audited ([identity-data-protection.md](../security/identity-data-protection.md)). |
| Staff console ↔ money movement | Step-up MFA, maker-checker and SoD conflicts are checked at action time ([operational-controls.md §2–§3](../compliance/operational-controls.md)). |

## 3. Domain responsibility matrix

External parties: [financial-responsibility-matrix.md](../compliance/financial-responsibility-matrix.md),
which covers FundZim, the PSP, the bank, the donor, the owner and the beneficiary. Every PSP-side row is
`PROVIDER_CONFIRMATION_REQUIRED` until a contract exists.

Internal module ownership follows the Stage 0 catalogue ([ARCHITECTURE.md §4.2](../ARCHITECTURE.md)). Only
the owner writes its tables, and only `ledger` writes journals, through named posting rules. Stage 1 adds:

| Concern | Owning module (Stage 1 position) | Reads from others via interface |
|---|---|---|
| Payment intents, `payment_events`, adapters, routing, inbox, status polling, refund requests, refunds, dispute cases | `payments` | `campaigns`, `fees`, `risk`, `ledger` (posts via rules) |
| Accounts, journals, projections, pool-integrity checks (SC-1 – SC-6) | `ledger` | — |
| Payouts, approvals, eligibility decisions, holds, recovery cases, payout destinations | `payouts` | `kyc` (level/status only), `ledger`, `campaigns`, `compliance` (blocking cases), `risk` |
| Identity, organisation KYB data, beneficiary C3 evidence, consents, fundraising authorities | `kyc` | — |
| Compliance cases, screening results, STR preparation, evidence records | `compliance` ([refund-and-dispute-architecture.md §1](../payments/refund-and-dispute-architecture.md)) | `kyc` (case-bound, justified), `risk` |
| Beneficiaries, institution payees, review-policy versions, freeze/suspend state | `campaigns` | `kyc`, `risk` |
| Settlement batches, matches, discrepancy items | `reconciliation` (Stage 17) | `ledger` (posts via rules), `payments`, `payouts` |
| Limits registry (REGULATORY / PROVIDER / INTERNAL_RISK) | to be decided in Stage 2 (see below) | read by `payouts`, `payments`, `risk` |

**Ownership questions Stage 2 must settle** (Stage 0 and Stage 1 differ; no behaviour depends on the
answer yet):

1. **Holds.** ARCHITECTURE §4.2 gives `risk` "holds/flags" and `payouts` `payout_holds`. Stage 1 specifies one
   unified `holds` + `hold_events` model owned by `payouts`
   ([payout-eligibility-and-controls.md §5](../payments/payout-eligibility-and-controls.md)), with `risk` and
   `compliance` placing holds through its interface. Stage 2 should confirm this and update ARCHITECTURE.
2. **Evidence records.** [refund-and-dispute-architecture.md §1](../payments/refund-and-dispute-architecture.md)
   puts `evidence_records` in `compliance`, but [audit-evidence-model.md](../compliance/audit-evidence-model.md)
   uses them for every domain (KYC, payments, reconciliation). They may belong in `audit` (or `platform`) so
   that non-compliance modules can create them without depending on `compliance`.
3. **Limits registry.** Used by `payouts`, `payments`, `risk` and `compliance`. To avoid cycles, it probably
   belongs in `platform` (config) or `compliance` with a read-only interface.
4. **Payout destinations.** ARCHITECTURE puts them in `payouts`. Beneficiary verification also writes their
   verification status. The account reference is C3, so confirm whether the encrypted column lives in `kyc`
   with only a token in `payouts`.

Stage 2 must show the result has no circular dependencies (in particular `payouts` ↔ `compliance` and
`campaigns` ↔ `risk`).

## 4. State models (authoritative sources)

| Model | States | Spec |
|---|---|---|
| Payment | `CREATED`, `PENDING`, `REQUIRES_ACTION`, `AUTHORISED`, `UNKNOWN`, `SUCCEEDED`, `FAILED`, `EXPIRED`, `CANCELLED`, `PARTIALLY_REFUNDED`, `REFUNDED`, `DISPUTED`, `CHARGED_BACK` | [transaction-lifecycle.md §3–§5](../payments/transaction-lifecycle.md) (transition table P1–P20, rank table, late-success exceptions) |
| Payout | `PAYOUT_REQUESTED`, `PENDING_REVIEW`, `APPROVED`, `SUBMITTED`, `PROCESSING`, `UNKNOWN`, `COMPLETED`, `FAILED`, `REJECTED`, `CANCELLED`, `REVERSED` | [payout-lifecycle.md §2–§3](../payments/payout-lifecycle.md) (Y1–Y14 with journals) |
| Refund request | `REQUESTED`, `PENDING_APPROVAL`, `ON_HOLD`, `APPROVED`, `EXECUTING`, `COMPLETED`, `FAILED`, `REJECTED`, `WITHDRAWN` | [refund-and-dispute-architecture.md §2.2](../payments/refund-and-dispute-architecture.md) |
| Dispute | `OPENED`, `EVIDENCE_REQUIRED`, `EVIDENCE_SUBMITTED`, `UNDER_PROVIDER_REVIEW`, `WON`, `LOST`, `ACCEPTED`, `EXPIRED` | [refund-and-dispute-architecture.md §3.2](../payments/refund-and-dispute-architecture.md) |
| Individual KYC | level `UNVERIFIED` → `BASIC_VERIFIED` → `IDENTITY_VERIFIED` → `PAYOUT_VERIFIED`; status overlay `ACTIVE` / `PENDING_REVIEW` / `REJECTED` / `SUSPENDED` | [kyc-architecture.md §3](../compliance/kyc-architecture.md) |
| Organisation KYB | `ORG_UNVERIFIED` → `ORG_REGISTERED_VERIFIED` → `ORG_KYB_VERIFIED` → `ORG_PAYOUT_VERIFIED`, with the same overlay | [kyb-architecture.md §2](../compliance/kyb-architecture.md) |
| Compliance case | `OPEN`, `IN_PROGRESS`, `AWAITING_INFO`, `PENDING_APPROVAL`, `DECIDED`, `CLOSED`, `REOPENED` | [compliance-case-management.md §4](../compliance/compliance-case-management.md) |
| Campaign | Stage 0 lifecycle ([PRODUCT.md §6](../PRODUCT.md)) plus the Stage 1 re-review and freeze rules | [campaign-approval-policy.md §7](../compliance/campaign-approval-policy.md) |
| Money states (not a status column) | S1–S9, S5r, S6t and SH, each held in a distinct ledger account | [settlement-and-custody-model.md §2](../ledger/settlement-and-custody-model.md) |

Rules every state model shares (and that Stage 2 must make enforceable):
- every transition appends an `*_events` row (append-only; INSERT/SELECT grants only; a trigger blocks
  UPDATE, DELETE and TRUNCATE);
- the transition and its ledger posting commit in **one DB transaction**;
- an inbound event moves a payment only if the target rank is higher **and** the move is a graph edge;
  otherwise it is parked;
- `UNKNOWN` is never resolved to `FAILED` by assumption, and an `UNKNOWN` payout is never resubmitted.

## 5. Compliance workflow requirements

| Workflow | Spec | Stage 2 design need |
|---|---|---|
| Campaign review pipeline, risk tiers, versioned review policy | [campaign-approval-policy.md §2–§5](../compliance/campaign-approval-policy.md) | Policy-version tables; reviews pinned to a policy version; `campaign_version_id` for material edits |
| KYC/KYB review, re-verification triggers, PEP handling | [kyc-architecture.md §6–§9](../compliance/kyc-architecture.md), [kyb-architecture.md §3–§5](../compliance/kyb-architecture.md) | `kyc.reviews` with `scope_paused[]`; append-only `check_results` with `valid_until` |
| Sanctions and PEP screening, rescreening, false-positive disposition | [sanctions-screening.md](../compliance/sanctions-screening.md) | `screening_results` with freshness; feeds EC-15 |
| Transaction monitoring and risk rules | [transaction-monitoring.md](../compliance/transaction-monitoring.md), [aml-risk-framework.md](../compliance/aml-risk-framework.md) | Alert → case linkage; the risk hook interface (Stage 11 minimal, Stage 13 scored) |
| Case management, holds and STR with tipping-off controls | [compliance-case-management.md](../compliance/compliance-case-management.md) | `compliance_cases` with `blocks_payouts` and confidentiality; `holds` + `hold_events` (append-only); case visibility restricted (LR-008) |
| Payout eligibility (EC-01 – EC-22), approval tiers, holds | [payout-eligibility-and-controls.md §2–§6](../payments/payout-eligibility-and-controls.md) | `eligibility_decisions` storing every check result per evaluation (request **and** pre-submit) |
| Limits registry (REGULATORY / PROVIDER / INTERNAL_RISK) | [payout-eligibility-and-controls.md §4](../payments/payout-eligibility-and-controls.md), [aml-risk-framework.md §5](../compliance/aml-risk-framework.md) | `limits` (versioned, maker-checker, `effective_from/to`, `review_by`, source). An unconfigured limit fails closed. |
| Maker-checker on sensitive actions | [operational-controls.md §3](../compliance/operational-controls.md) | A generic `approval_requests` object (maker, checker, payload hash, expiry, step-up timestamp) with DB checks |
| Incident workflows | [incident-response-workflows.md](../compliance/incident-response-workflows.md) | Kill switches (`PROVIDER_HOLD`, payout freeze) as data, not deploys |

## 6. Currency rules (summary for schema design)

- Money is `amount_minor bigint` + `currency char(3)` with a `CHECK` on the enabled set (`USD`, `ZWG`). In
  JSON, `amount_minor` is a string. There are no floats, including for rates and percentages; basis points
  are integers.
- Every ledger account has an immutable currency. Every transaction has all lines in one currency (L2/L3,
  SC-6).
- Payment currency, refund currency and payout currency equal the currency of the source account. Fees are
  in the donation currency.
- A campaign has exactly one goal currency, and accepted currencies are a per-campaign config (PD-02). Goal
  progress counts goal-currency donations only.
- Limits are per currency and never converted.
- ZiG enablement is gated on LR-043 (P0 feature gate).

## 7. Provider abstraction requirements

Stage 0 interface: [PAYMENTS.md §3–§5](../PAYMENTS.md), [ADR-007](../adr/ADR-007-payment-provider-abstraction.md).
Stage 1 additions the schema and interfaces must carry:

- **Capability flags**: `custody_model`, `payout_api`, `payout_rails`, `split_settlement`,
  `pool_balance_api`, `statement_format`, `separate_capture`, `refund_api` (with partial refund support),
  `signed_webhooks`, `status_api`, `documented_expiry`, and settlement currency per method × currency.
  Anything unverified is **absent** by default ([provider-capability-matrix.md](../payments/provider-capability-matrix.md) rule 2).
- **Routing guard**: refuse `MERCHANT_SETTLEMENT` for donations, and refuse a rail that cannot settle in the
  donation currency.
- **Status confirmation path** for providers without signed webhooks: an authenticated status query is
  mandatory before `SUCCEEDED`.
- **Unknown-outcome classification** of every provider call (`unknown_reason` codes,
  [transaction-lifecycle.md §7.2](../payments/transaction-lifecycle.md)) and a poll schedule
  (`next_poll_at`, `poll_horizon_at`, `needs_reconciliation`).
- **Manual refund fallback** where a provider has no refund API (`execution_method = MANUAL_PAYOUT`).
- **Settlement report ingestion** shape, for T3 matching and discrepancies (Stage 17), designed generically
  until a provider's statement format is known (PCR-007).
- **Provider selection is pending.** There is no PRIMARY, BACKUP or PILOT candidate
  ([provider-comparison.md §5](../payments/provider-comparison.md)). The Stage 8 sandbox provider must be able
  to simulate every custody model and every capability combination above.

## 8. Audit event requirements

- Common fields: actor (`actor_type`, `actor_id`, `actor_role`, `on_behalf_of`), `action`, `target_type`,
  `target_id`, `occurred_at`, `recorded_at`, `correlation_id`, `request_id`, `reason`, `justification`,
  `outcome`, and `metadata` (provider refs, `evidence_record_ids[]`, `ledger_transaction_ids[]`)
  ([audit-evidence-model.md §3](../compliance/audit-evidence-model.md)).
- The action catalogue is a closed Go constant set ([audit-evidence-model.md §4](../compliance/audit-evidence-model.md)).
  Unknown actions fail tests.
- Never-embed list: [audit-evidence-model.md §5](../compliance/audit-evidence-model.md).
- Retention classes are a column, but **periods are pending LR-012**. Do not hard-code them. Legal hold uses
  two-person `evidence_holds`.

## 9. Required database entities

Stage 2 owns the ERD. This list is the minimum Stage 1 implies, in addition to the Stage 0 list in
[ROADMAP.md](../ROADMAP.md) Stage 2 (users, organisations, campaigns, payments, ledger, payouts, fees, kyc,
audit, idempotency, webhook inbox, outbox, jobs). Conceptual column lists are in the linked §.

| Area | Entities | Spec |
|---|---|---|
| Payments | `payments` (+ `status_rank`, `unknown_*`, poll fields, `reversal_kind`), `payment_events` | transaction-lifecycle §3.2 |
| Ledger | Stage 1 chart of accounts; `ledger_transactions` keyed by the posting keys (`payment:{id}:capture`, `settlement:{batch}:{payment}`, `payout:{id}:reserved` …); balance projections per money state | settlement-and-custody-model §3, §5, §6 |
| Settlement | `settlement_batches`, `settlement_lines`, `settlement_matches`, discrepancy items | settlement-and-custody-model §7 |
| Payouts | `payouts`, `payout_approvals`, `payout_events`, `eligibility_decisions`, `holds`, `hold_events`, `limits` | payout-lifecycle §8, payout-eligibility-and-controls §4, §5, §8, §9 |
| Refunds and disputes | `refund_requests`, `refunds`, `dispute_cases`, `recovery_cases` | refund-and-dispute-architecture §2, §3, §9 |
| Identity (`kyc` schema) | `verification_profiles`, `profile_events`, `verification_attempts`, `check_results`, `identities` (C3, blind index), `reviews`, `consents`, `organisations`, `org_persons`, `org_representative_authorities`, `fundraising_authorities`, `org_check_results`, `beneficiary_evidence` | kyc §11, kyb §7, beneficiary §8 |
| Beneficiaries and destinations (`public`) | `campaign_beneficiaries`, `institution_payees`, `payout_destinations` (versioned; account ref encrypted, C3) | beneficiary-verification §8 |
| Compliance | `compliance_cases`, case links and notes, `screening_results`, monitoring alerts, review policy versions | compliance-case-management §2, sanctions-screening, campaign-approval-policy §5 |
| Controls | `approval_requests` (maker-checker), staff conflict declarations, break-glass sessions | operational-controls §3, §5 |
| Evidence | `evidence_records`, `evidence_holds` | audit-evidence-model §2 |

Constraints Stage 2 must plan at DB level (not only in code):
- `UNIQUE (provider, provider_payment_ref)`, plus provider-idempotency uniques for payouts and refunds;
- a partial unique index allowing only one non-terminal payout per (campaign, currency) (EC-11);
- approver ≠ requester and distinct DUAL approvers (trigger);
- append-only triggers on all `*_events`, `check_results`, `consents`, `hold_events` and `evidence_*`
  tables, and on ledger tables;
- balanced, single-currency journals (deferred constraint trigger);
- a guard that only the `campaign_payable` posting rule can debit for a payout reservation (SC-1);
- `fundraising_authorities` single-subject and s8 duration checks;
- a partial unique index on `(id_type, id_number_bidx)` across active profiles.

## 10. API design implications

- All money-moving endpoints take `Idempotency-Key`. Payment, payout and refund IDs are created
  server-side, before any provider call.
- Donor-facing status must distinguish `UNKNOWN` ("don't pay again") from `FAILED`, and must show money
  states separately: there is never a single "balance" field.
- Owner payout API: request, cancel (before `SUBMITTED`) and view. There is no endpoint that changes status
  after submission.
- Refund and dispute endpoints: [refund-and-dispute-architecture.md §7](../payments/refund-and-dispute-architecture.md).
- Staff `/api/v1/admin/*` approvals need step-up MFA, a `justification` field, and an SoD check that
  returns a stable error code (for example `APPROVER_CONFLICT`).
- Hold and STR-related reasons must not leak through owner-facing error codes (tipping-off, LR-008).
- Webhooks: `/api/v1/webhooks/{provider}` → inbox for payment, refund, dispute, reversal and payout events.

## 11. Security controls to carry into the design

- KYC is C3: a separate schema and bucket, envelope encryption with a dedicated key, a blind index for
  duplicate detection, justified and case-bound viewing, and every view audited
  ([identity-data-protection.md](../security/identity-data-protection.md)).
- Payout account numbers are encrypted and masked in every non-`kyc` surface and in logs.
- DB role and grant matrix: the app role has INSERT/SELECT only on append-only tables; ledger tables are
  writable only by the ledger role; KYC tables are reachable only by the `kyc` role.
- Kill switches and provider holds are data-driven and audited.
- Minors' data and medical detail are hidden by default (PD-39 provisional).

## 12. Open business decisions (PD)

All 39 are tracked: PD-01 – PD-34 in [PRODUCT.md §13](../PRODUCT.md) (with the source Stage 1 document named
on each row) and PD-35 – PD-39 in [open-legal-questions.md §7](../compliance/open-legal-questions.md). The ones that change
**schema shape** (as opposed to configuration values) and should be decided, or designed for both options,
during Stage 2:

| PD | Why it affects Stage 2 | Design-for-both default |
|---|---|---|
| PD-01 / PD-19 / PD-20 / PD-32 | Fee model and fee reversal change the posting rules | Versioned fee config with posting rules parameterised; fee bearer as data |
| PD-02 / PD-11 | Multi-currency acceptance and the ZiG launch set | Per-campaign accepted-currency set; all accounts per currency already |
| PD-14 / PD-33 | Release timing and reserves | `campaign_reserve` account and release-rule config exist regardless |
| PD-17 | Single provider vs split collection/payout providers | Payout provider independent of collection provider in the schema |
| PD-23 / PD-24 | Payout mechanics and approval tiers | Approval tier as config; single in-flight payout per campaign and currency enforced |
| PD-36 / PD-38 | Operating entity and data-hosting region | The design must not preclude in-country hosting of `kyc` data |

## 13. Open legal questions

The authoritative register is [open-legal-questions.md](../compliance/open-legal-questions.md) (LR-001 –
LR-089). The P0 (launch-blocking) set is in its §5. **None blocks Stage 2 design work**, because Stage 2
moves no money. They gate Stage 9 (provider contract), Stage 11 (payouts) and Stage 20 (pilot). Stage 2
must keep every LR-dependent value configurable, with the LR id referenced near the configuration.

Provider questions: [provider-questions.md](../payments/provider-questions.md) (PCR-001 – PCR-091).
Regulatory requirements: [regulatory-requirements-register.md](../compliance/regulatory-requirements-register.md)
(REQ-001 – REQ-068).

## 14. Stage 2 entry checklist

- [ ] Stage 1 accepted by the user.
- [ ] Stage 1 work committed on `stage-1/regulatory-architecture` and merged (or Stage 2 branched from it).
- [ ] Stage 2 ERD covers §9 entities and constraints, with C-classification per column
      ([DATA-CLASSIFICATION.md](../DATA-CLASSIFICATION.md)).
- [ ] The module interface sketches confirm the §3 ownership with no cycles.
- [ ] [THREAT-MODEL.md](../THREAT-MODEL.md) is updated for the §2 boundaries.
- [ ] Each §1 decision is traceable to a schema or interface element.
