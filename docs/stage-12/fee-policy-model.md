# Stage 12 — Fee policy model

## 1. Entities

| Entity | Table | Notes |
|---|---|---|
| FeePolicy | `app.fee_policies` | Identity + immutable scope: category, transaction type, currency, optional payment method, provider, campaign category, campaign. `scope_key` unique: a scope belongs to one policy forever. Only `name` and `description` may change. |
| FeePolicyVersion | `app.fee_policy_versions` | Terms (model, rate_bps, fixed/min/max, tiers, rounding mode), responsibility (+ donor share for SHARED), refund treatment (platform fee only), effective window, `algorithm_version`, `terms_hash`, lifecycle and the maker/checker/activator columns. |
| FeeComponent | `app.fee_components` | One computed fee of a calculation (immutable). |
| FeeCalculation | `app.fee_calculations` | Canonical inputs, result, SHA-256 of both, algorithm version (immutable). |
| FeeQuote / FeeAssessment / FeeAdjustment | `app.fee_quotes` / `app.fee_assessments` / `app.fee_adjustments` | See [fee-calculation.md](fee-calculation.md), [refund-adjustments.md](refund-adjustments.md). |
| FeeAllocation | `donor_part_minor`, `organiser_part_minor`, `platform_part_minor` on components and assessments | Who bears each fee; parts always sum to the fee. |
| FeeAccountingReference | `app.fee_accounting_references` | Fee subject ↔ ledger journal (cross-schema FK). |
| FeeDisclosure | quote and organiser views | [fee-disclosure.md](fee-disclosure.md). |
| FeeAuditEvent | `app.fee_audit_events` | Append-only history. |
| FeeReconciliationReference | `app.fee_reconciliation_references` (+ runs) | [reconciliation.md](reconciliation.md). |

Quoted, assessed, provider-reported, confirmed, recognised and refunded fees are separate values (bases `QUOTED`,
`ESTIMATED`, `PROVIDER_REPORTED`, `PROVIDER_CONFIRMED`, `RECONCILED`, `UNKNOWN`, `NOT_CONFIGURED`; recognised = TEST ledger;
refunded = adjustments).

## 2. Categories and models

| Category | Transaction | Models | Responsibility |
|---|---|---|---|
| `PLATFORM_FEE` | DONATION | ZERO_FEE, PERCENTAGE, FIXED, PERCENTAGE_PLUS_FIXED, TIERED, CAPPED, MINIMUM_FEE | DONOR_COVERED, ORGANISER_COVERED, SHARED (never PLATFORM_ABSORBED: waive with ZERO_FEE); refund treatment required |
| `PAYMENT_PROCESSING_FEE` | DONATION | PROVIDER_PASS_THROUGH (amount from provider evidence) | any of the four |
| `PAYOUT_PROCESSING_FEE` | PAYOUT | PROVIDER_PASS_THROUGH | ORGANISER_COVERED, PLATFORM_ABSORBED |
| `REFUND_FEE` | REFUND | PROVIDER_PASS_THROUGH | ORGANISER_COVERED, PLATFORM_ABSORBED |
| `CHARGEBACK_FEE` | CHARGEBACK | PROVIDER_PASS_THROUGH | ORGANISER_COVERED, PLATFORM_ABSORBED |
| `CURRENCY_CONVERSION_FEE` | — | cannot be created (FundZim never converts currencies) | — |
| `OTHER_APPROVED_FEE` | — | cannot be created (no posting rule) | — |

Model parameters (mirrored by `ck_fee_policy_versions_shape` and `Terms.Validate`): PERCENTAGE needs `rate_bps`; FIXED
needs `fixed_minor`; PERCENTAGE_PLUS_FIXED both; TIERED 1–20 ascending tiers, the last open-ended; CAPPED `rate_bps` +
`max_fee_minor`; MINIMUM_FEE `rate_bps` + `min_fee_minor`; ZERO_FEE and PROVIDER_PASS_THROUGH nothing. Optional min/max apply
to the other models too. `rate_bps` 0–10 000; amounts non-negative; `max ≥ min`.

## 3. Lifecycle (machine `fee_policy_version`, DB-guarded)

```
DRAFT ──submit──▶ PENDING_APPROVAL ──approve──▶ APPROVED ──activate──▶ ACTIVE ⇄ SUSPENDED
  │                    └──reject──▶ REJECTED (final)       │              │         │
  └────────retire──────────────────────────────────────────┴──retire──▶ RETIRED ◀──┘
```

| Step | Who (permission) | Rules |
|---|---|---|
| create policy / draft / edit draft | maker `fee.config.request` (FINANCE), step-up | one DRAFT or PENDING version per policy; only the author edits a draft |
| submit | `fee.config.request` | terms frozen (trigger) and hashed (`terms_hash`); `effective_from` not in the past (no retroactive fees) |
| approve / reject | checker `fee.config.approve` (BUSINESS_APPROVER), fresh step-up recorded | never the maker or submitter or anyone linked to them (`app.actor_identities`, Go and trigger); optional `terms_hash` binding (`FEE_TERMS_CHANGED`) |
| activate / suspend / retire | `fee.config.request`, step-up | activation invalidates ACTIVE quotes in the currency; two ACTIVE versions of a policy cannot share a start |
| reactivate | `fee.config.approve` | never the suspender or anyone linked |

No transition goes DRAFT → APPROVED/ACTIVE, and nothing leaves REJECTED or RETIRED. Published versions are never edited
or deleted; history is in `app.fee_audit_events`.

## 4. Selection (precedence)

A version applies at time *t* if it is ACTIVE and `effective_from ≤ t < effective_to`. Among matching policies the most
specific wins (campaign 16 + campaign category 8 + provider 4 + payment method 2); within a policy the latest
`effective_from` wins. Because `scope_key` is unique, equal specificity between two policies cannot occur; `Resolve` still
refuses it (`FEE_POLICY_AMBIGUOUS`). Historical calculations keep the version they used (stored in the inputs, the
components and the journal's `fee_schedule_version_id`).

No policy for a category: the platform fee is `NOT_CONFIGURED` (nothing is charged); a provider cost's responsibility is
undecided, so totals that depend on it are `UNKNOWN`.
