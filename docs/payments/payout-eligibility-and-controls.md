# Payout Eligibility Engine and Controls

> **Stage 1 — design specification.** Implemented in Stage 11 (minimal rule-based risk hook) and extended in
> Stage 13 (risk engine) and Stage 14 (operations portal). No live payouts before Stage 20.
> State machine and ledger effects: [payout-lifecycle.md](payout-lifecycle.md). Roles and maker-checker:
> [operational-controls.md](../compliance/operational-controls.md),
> [ADR-017](../adr/ADR-017-payout-approval-segregation-of-duties.md). Beneficiary rules:
> [beneficiary-verification.md](../compliance/beneficiary-verification.md),
> [ADR-016](../adr/ADR-016-beneficiary-verification-before-payout.md). Ledger accounts:
> [settlement-and-custody-model.md](../ledger/settlement-and-custody-model.md).

## 1. Purpose

Decide, deterministically and with a recorded explanation, whether a payout may proceed, and under which
approval policy. The engine:

- is **configurable**: checks, limits and approval tiers are data (versioned policy), not code branches;
- **fails closed**: anything missing, stale or unconfigured on the payout path results in manual review or
  rejection, never auto-approval;
- runs **twice**: when the payout is requested, and again immediately before submission to the provider
  (time-of-check/time-of-use);
- writes an **eligibility decision record** each time, which is the evidence for auditors and regulators.

The engine never moves money itself. It returns a decision; the payouts module applies the state transition and
the ledger posting in the same DB transaction.

## 2. Check catalogue

Result types: `PASS`, `FAIL` (blocking), `REVIEW` (route to PENDING_REVIEW), `DEFER` (cannot decide now —
treated as REVIEW at request time, and blocks submission), `NOT_APPLICABLE`.

Override column: who may override a FAIL/REVIEW, and how. "No" means the check cannot be overridden by anyone;
the underlying condition must change.

| ID | Check | Rule | Data source | On failure | Override | Maker-checker on override |
|---|---|---|---|---|---|---|
| EC-01 | Campaign payout-eligible | Campaign status ∈ {ACTIVE, COMPLETED} | `campaigns` | FAIL | No (CANCELLED-campaign disbursement only via a COMPLIANCE-approved disposition case, LR-019) | — |
| EC-02 | Requester authorised | Requester is the campaign owner, or an `ORG_ADMIN` of the owning organisation; account `verification_status = ACTIVE` | `users`, `organisation_members` | FAIL | No | — |
| EC-03 | Owner verification | Individual owner `PAYOUT_VERIFIED` + status ACTIVE; organisation `ORG_PAYOUT_VERIFIED` + status ACTIVE and the requesting representative `IDENTITY_VERIFIED` | `kyc` module API (level/status only, never documents) | FAIL | No | — |
| EC-04 | Beneficiary verified | Campaign beneficiary `beneficiary_verification = VERIFIED`, or beneficiary type `SELF` with the owner verified | `beneficiaries` | FAIL | COMPLIANCE, only for configured exception types and with LR-024 resolved | Yes (COMPLIANCE + FINANCE) |
| EC-05 | Destination verified | Destination `status = VERIFIED`, ownership/name match passed, holder is the owner, the verified beneficiary, or a verified institution payee | `payout_destinations` | FAIL | No | — |
| EC-06 | Destination cooling-off | Now ≥ destination `verified_at` (or last change) + cooling-off period (INTERNAL_RISK limit) | `payout_destinations`, limits | DEFER | COMPLIANCE (documented urgent case, e.g. funeral) | Yes |
| EC-07 | Destination snapshot unchanged | At submit: live `destination_version` = snapshot version | `payouts`, `payout_destinations` | REVIEW (approvals invalidated) | No | — |
| EC-08 | Currency and rail | Payout currency = ledger account currency; the destination rail supports that currency; the provider has `payouts` capability for rail + currency | ledger, provider capabilities | FAIL | No | — |
| EC-09 | Available balance | Request: amount ≤ `campaign_payable:{c}` in that currency (reserved atomically). Submit: reservation journal exists and is intact | ledger projections (locked) | FAIL | No | — |
| EC-10 | Amount limits | `min_payout ≤ amount ≤ min(provider max per transaction, internal per-payout limit, regulatory limit if any)` | limits (§4) | FAIL (below min / above provider max) or REVIEW (above internal limit) | Internal limit only: FINANCE | Yes (DUAL) |
| EC-11 | Single in-flight | No other non-terminal payout for (campaign, currency) | `payouts` (partial unique index) | FAIL | No | — |
| EC-12 | No blocking holds | No active hold in scope (campaign, owner, organisation, beneficiary, destination, provider+currency) whose type blocks payouts (§5) | `holds` | FAIL (freeze, compliance, provider) or DEFER (destination, dispute) | Only by releasing the hold (§5) | Per hold type |
| EC-13 | No blocking case | No open compliance case marked `blocks_payouts` on the campaign, owner, organisation, beneficiary or destination | `compliance_cases` | REVIEW | COMPLIANCE (closing or re-scoping the case) | Yes |
| EC-14 | Dispute exposure | Open disputes/chargeback exposure for the campaign ≤ reserve + (available − amount), or no open disputes | `dispute_cases`, ledger | REVIEW | FINANCE | Yes |
| EC-15 | Screening current | Sanctions/PEP screening of payee (and owner/organisation principals) completed within freshness window and result `CLEAR`, or `CLEARED_BY_COMPLIANCE` | `screening_results` ([sanctions-screening.md](../compliance/sanctions-screening.md)) | FAIL (confirmed match) / REVIEW (potential match, stale) | COMPLIANCE (false-positive disposition) | Yes |
| EC-16 | Risk signals | Stage 11 minimal rules: campaign age; donation velocity; share of donations from newly created or linked accounts; first payout requested shortly after first donations; destination added/changed recently; owner account/device change recently; reports from users. Stage 13 replaces with a scored model behind the same interface | risk hook | REVIEW (any rule hit) | REVIEWER/COMPLIANCE disposition | Yes for high severity |
| EC-17 | Velocity limits | Payout count and amount per campaign/owner/destination per period ≤ configured limits | limits, `payouts` | REVIEW | FINANCE | Yes |
| EC-18 | Provider available | Provider rail not in an outage circuit-breaker state; no PROVIDER_HOLD | provider health, `holds` | DEFER | No (wait) | — |
| EC-19 | Pool integrity | Pool-integrity invariant holds for provider + currency ([refund-and-reversal-flows.md](refund-and-reversal-flows.md) §7) | ledger | FAIL (blocks all payouts for that provider/currency) | No | — |
| EC-20 | Settlement freshness | The last settlement reconciliation for provider + currency completed within the freshness window, with no open SEV1 discrepancy | reconciliation | DEFER | FINANCE (documented, short window) | Yes |
| EC-21 | Limits configured | Every limit referenced by this policy version has an approved, effective value | limits | REVIEW (and disables AUTO) | — (configure the limit) | — |
| EC-22 | Approvals obtained | (Submit only) approvals required by the approval tier are recorded, valid and not expired; approvers ≠ requester, distinct for DUAL | `payout_approvals` | FAIL (cannot submit) | No | — |

Notes:

- `campaign_payable` only ever contains **settled and released** funds (see the ledger model), so EC-09 already
  enforces "sufficient settled funds". Unsettled and reserved amounts are in other accounts and can never be
  requested.
- Checks read state through each owning module's public interface (Stage 0 module rules). The engine never
  reads KYC tables directly; EC-03 receives a level and status only.

## 3. When the engine runs

### 3.1 At request (`PAYOUT_REQUESTED`)

Runs inside the request's DB transaction, after locking the campaign's balance projections in account-id order.

| Outcome | Effect |
|---|---|
| Any `FAIL` | Request refused with error code `PAYOUT_NOT_ELIGIBLE` and a coarse reason category (no fraud-signal details to the owner — tipping-off risk, LR-008). No payout row is created; an eligibility decision record **is** written (attempt evidence). |
| No FAIL; any `REVIEW`/`DEFER` | Payout created, funds reserved (Y1), → `PENDING_REVIEW` |
| All PASS / N/A and approval tier = AUTO | Payout created, funds reserved, → `APPROVED` |
| All PASS / N/A and tier = SINGLE or DUAL | Payout created, funds reserved, → `PENDING_REVIEW` |

### 3.2 Before submission (`APPROVED → SUBMITTED`)

The submission worker locks the payout row and the campaign projections, then re-runs **all** checks (EC-09 as
"reservation intact", EC-22 included). Any non-PASS result:

- `FAIL` on EC-01/02/03/04/05/08/11/19 → `REJECTED` or back to `PENDING_REVIEW` per check (balance released on
  REJECTED);
- `REVIEW` → `PENDING_REVIEW` with approvals invalidated;
- `DEFER` → stays `APPROVED` and is retried later (bounded; after the configured deferral limit →
  `PENDING_REVIEW`).

Approvals expire if a payout is not submitted within the configured approval-validity window (INTERNAL_RISK
limit); an expired approval behaves like a missing one.

## 4. Limits model

Every threshold the engine uses is a **limit record**. No numeric legal threshold is hard-coded or assumed.

| Field | Meaning |
|---|---|
| `limit_id`, `version` | Immutable versioned record; changes create a new version |
| `limit_key` | e.g. `payout.max_per_transaction`, `payout.auto_approve_max`, `payout.dual_approval_min`, `payout.velocity.amount_per_7d`, `payout.min_amount`, `destination.cooling_off`, `screening.freshness`, `settlement.freshness`, `approval.validity` |
| `limit_type` | `REGULATORY` (from law/regulator), `PROVIDER` (from provider docs/contract), `INTERNAL_RISK` (FundZim risk decision) |
| `scope` | global, provider, rail, currency, campaign category, verification level, organisation type |
| `currency` | ISO code for monetary limits (amount in integer minor units); null for durations/counts |
| `value` | integer minor units, duration, or count |
| `source` | REGULATORY: register entry (REQ-xxx) + citation; PROVIDER: provider doc/contract reference (PCR id until confirmed); INTERNAL_RISK: risk decision record |
| `approval_owner` | Role accountable for the value (COMPLIANCE for REGULATORY/INTERNAL_RISK compliance limits, FINANCE for financial limits, both for payout tiers) |
| `approved_by` (×2) | Maker and checker (maker-checker on every limit change) |
| `effective_from`, `effective_to` | Validity window (UTC) |
| `review_by` | Mandatory review date |

Resolution rules:

1. For a given check, all applicable limits of all three types are evaluated; the **most restrictive** applies.
   Types are never merged into one number: the decision record lists each.
2. **Unconfigured** limit (no effective approved version) on a payout or withdrawal path → the check returns
   REVIEW and **AUTO approval is disabled** for the affected scope (fail closed).
3. Limit past `review_by` → still enforced, an alert goes to the approval owner, and AUTO approval is disabled
   for the affected scope until reviewed.
4. Monetary limits are per currency. A USD limit never applies to ZiG amounts and is never converted.
5. Documentation and tests use placeholders such as `<TBD: LR-030>`; real values are entered only through the
   approved configuration workflow (Stage 14) with their source.

## 5. Holds

A hold is a record that blocks (some) payouts within a scope. Holds are **added** and **released** as
append-only events; history is never edited or deleted.

| Hold type | Placed by | Scope | Blocks | Ledger effect | Released by |
|---|---|---|---|---|---|
| `CAMPAIGN_FREEZE` (campaign → FROZEN) | COMPLIANCE or FINANCE (reason) | campaign | all payouts, new donations | available balance moved `campaign_payable → campaign_held` (`campaign:{id}:freeze:{n}`) | Unfreeze: maker-checker (COMPLIANCE + second approver); inverse journal |
| `COMPLIANCE_HOLD` | COMPLIANCE | campaign / owner / organisation / beneficiary / destination | payouts in scope | campaign scope: payable → held (`hold:{id}:applied`); other scopes: none (enforced by EC-12) | COMPLIANCE with maker-checker; inverse journal if applied |
| `PAYOUT_HOLD` (risk) | System risk hook, REVIEWER, COMPLIANCE | campaign / owner | payouts | none | COMPLIANCE (or REVIEWER for low-severity system holds) |
| `DISPUTE_HOLD` | System on dispute/reversal | campaign | payouts | none (dispute journals handle money) | System on case close, or COMPLIANCE |
| `DESTINATION_HOLD` | System on destination add/change/reversal | destination | payouts to it | none | System after cooling-off **and** re-verification |
| `RECOVERY_HOLD` | FINANCE (open recovery case) | campaign / owner | payouts | none | FINANCE on recovery or write-off |
| `ACCOUNT_HOLD` | COMPLIANCE / SUPPORT (security: suspected takeover) | user | all actions incl. payouts | none | COMPLIANCE (takeover cases: after account recovery) |
| `PROVIDER_HOLD` | FINANCE / system (outage, pool-integrity breach, reconciliation SEV1) | provider + currency | all payouts on that rail | none | FINANCE |

Hold record: `hold_id, hold_type, scope_type, scope_id, reason_code, reason_text, case_id, placed_by,
placed_at, review_by, ledger_transaction_id (nullable), released_by, released_at, release_reason`. A release is
a new `hold_events` row; `released_*` is set once and never cleared.

Owner communication: the owner sees "payouts paused — under review" with a support contact; reasons that could
tip off a subject of a suspicious-transaction report are not disclosed (LR-008). How long a hold may last
without a decision, and what must be communicated, is LR-084.

## 6. Approval policy

| Tier | When (default policy; values are limit records) | Approvers | Constraint |
|---|---|---|---|
| `AUTO` | All checks PASS, amount ≤ `payout.auto_approve_max` (currency-specific, INTERNAL_RISK), campaign has a prior successful payout to the same destination, no flags, all limits configured and in review date | none (system) | Disabled entirely by default for the pilot (PD-24) |
| `SINGLE` | Default for everything not AUTO and below `payout.dual_approval_min` | 1 × FINANCE with `payout.approve` | approver ≠ requester; approver ≠ staff member who last changed or verified the destination |
| `DUAL` | Amount ≥ `payout.dual_approval_min`, **or** any high-severity risk flag, **or** any override applied, **or** first payout of an organisation campaign, **or** CANCELLED-campaign disposition | FINANCE + (COMPLIANCE or second FINANCE) | two distinct people; neither the requester; neither applied the override being approved |

Additional rules:

- The **first payout** of any campaign is never AUTO.
- Approvals bind to the payout **as requested** (amount, currency, destination snapshot). Any change
  invalidates them (Y7).
- Approval actions require step-up authentication (fresh MFA within `STEP_UP_MAX_AGE`, [SECURITY.md](../SECURITY.md)).
- Approver conflicts: an approver may not approve payouts for a campaign they own, donated to above a
  configured amount, or are linked to by a recorded relationship (declared-conflict register, Stage 14).

## 7. Provider timeouts, ambiguous results and retries

Defined in [payout-lifecycle.md](payout-lifecycle.md) §5. Summary of the controls the engine relies on:

- The provider call happens only on `APPROVED → SUBMITTED`, after the in-transit journal commits, outside any DB
  transaction.
- Indeterminate outcome → `UNKNOWN`; money stays in `payout_in_transit`; poll and reconcile; **never resubmit
  with a new reference**; same-reference retry only for `ErrDefinitelyNotSent` or with documented provider
  idempotency (PCR-013 — idempotent payouts).
- FAILED only on authoritative final failure (PCR-026 — finality of "not found").
- While any payout for (campaign, currency) is UNKNOWN, EC-11 blocks new payouts for that pair.

## 8. Duplicate payout prevention — database constraints

```sql
-- Illustrative (Stage 2 finalises). One non-terminal payout per campaign and currency.
CREATE UNIQUE INDEX uq_payouts_one_inflight
    ON payouts (campaign_id, currency)
    WHERE status IN ('PAYOUT_REQUESTED','PENDING_REVIEW','APPROVED','SUBMITTED','PROCESSING','UNKNOWN');

-- Client idempotency and provider reference uniqueness.
ALTER TABLE payouts ADD CONSTRAINT uq_payouts_idempotency_key UNIQUE (idempotency_key);
ALTER TABLE payouts ADD CONSTRAINT uq_payouts_provider_ref   UNIQUE (provider, provider_payout_ref);

-- An approver can approve a payout once; requester-≠-approver enforced by trigger (needs payouts.requested_by).
ALTER TABLE payout_approvals ADD CONSTRAINT uq_payout_approvals UNIQUE (payout_id, approver_id);
```

Plus: ledger idempotency keys per journal (`payout:{id}:reserved|submitted|completed|failed|released|reversed`)
are UNIQUE; state transitions use `UPDATE … WHERE id = $1 AND status = $expected` and check the row count, so
two workers cannot both submit; row locks in account-id order prevent concurrent reservations from
over-spending (ledger L8).

## 9. Eligibility decision record

Written for every evaluation (request, submit, re-check, and refused requests). Append-only, classification C2
(contains identifiers and outcomes, not documents or raw PII). Retention class per LR-012.

| Field | Content |
|---|---|
| `decision_id` | UUIDv7 |
| `payout_id` | nullable (refused requests have none) |
| `campaign_id`, `currency`, `amount_minor`, `requested_by` | Request facts |
| `phase` | `REQUEST`, `SUBMIT`, `RECHECK` |
| `evaluated_at` | UTC |
| `engine_version`, `policy_version` | Code and policy versions used |
| `inputs` | JSON snapshot of *references and values used*: available balance (minor units), verification levels/statuses, destination id + version + hash, hold ids, case ids, screening result ids + timestamps, risk rule ids hit, limit ids + versions + values + types |
| `results` | Array of `{check_id, result, detail_code}` |
| `outcome` | `ELIGIBLE`, `REVIEW`, `DEFERRED`, `REJECTED` |
| `approval_tier` | `AUTO`, `SINGLE`, `DUAL` |
| `overrides` | Array of `{check_id, override_id, by, reason, approved_by}` |
| `request_id`, `correlation_id` | Trace links |
| `record_hash` | SHA-256 over the canonical record (tamper evidence; anchored with audit hash chain in Stage 18) |

An audit event (`payout.eligibility.evaluated`) references `decision_id`; it does not duplicate the inputs.

## 10. New decisions introduced here

| ID | Decision | Default proposed |
|---|---|---|
| PD-23 | Payout mechanics: one vs multiple in-flight payouts per campaign and currency; minimum payout amount; who bears provider payout fees | One in flight; minimum and fee bearer set with provider pricing (PCR-013 — payout fees) |
| PD-24 | Approval policy for the pilot: whether AUTO approval exists at all, and the SINGLE/DUAL thresholds | No AUTO in the pilot; every payout at least SINGLE; DUAL above `<TBD: LR-030>` |

## New legal questions

| ID | Area | Question | Why it matters / what is blocked |
|---|---|---|---|
| LR-084 | Payout holds and delays | How long may FundZim hold or delay a beneficiary's payout during an investigation or dispute, what must be communicated (without tipping off, LR-008), and does a hold create obligations or liability towards beneficiaries? | Sets maximum hold durations, escalation timers and owner messaging; affects terms of service (LR-022) and hold design (§5). |
