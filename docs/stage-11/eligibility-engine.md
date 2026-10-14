# Stage 11 — Eligibility engine

`internal/payouts/eligibility.go` (`Evaluate`, pure) over `Facts` gathered by `gather.go` from the owning modules'
public interfaces (campaigns, kyc, beneficiaries, payouts destinations, ledger, psp, risk limits, payments refund exposure
through an interface). Every evaluation is stored in `app.payout_eligibility_decisions` (inputs, all 22 results, outcome,
tier, engine version, SHA-256 record hash); the database refuses a result set that is not exactly EC-01..EC-22 or whose
outcome does not follow from the results (fail closed). Refused requests are stored too (no payout row).

Phases: **REQUEST** (before anything is reserved), **PRE_SUBMIT** (immediately before submission; the database refuses
`SUBMITTED` without an ELIGIBLE PRE_SUBMIT decision in the same transaction), **RECHECK** (staff, while under review).

## 1. Checks as implemented

| Check | Rule | Result on failure |
|---|---|---|
| EC-01 | Campaign ACTIVE or COMPLETED | FAIL |
| EC-02 | Requester is the owner or an ORG_ADMIN (others get 404 before evaluation) and active | FAIL |
| EC-03 | Individual owner PAYOUT_VERIFIED + ACTIVE; organisation ORG_PAYOUT_VERIFIED + ACTIVE and representative IDENTITY/PAYOUT_VERIFIED | FAIL |
| EC-04 | No linked beneficiary, or SELF (the verified owner), or a VERIFIED beneficiary | FAIL |
| EC-05 | Destination VERIFIED, ownership CONFIRMED, compliance APPROVED, not expired, owned by the campaign owner, paying the owner (SELF/none) or the campaign's own beneficiary (no cross-beneficiary use) | FAIL |
| EC-06 | Destination cooling-off over | DEFER |
| EC-07 | (submit/recheck) destination details version unchanged since the request | REVIEW |
| EC-08 | `LOCAL_SIMULATOR` execution, TEST payout, TEST ledger, currency with verified minor units (ZWG refused), destination currency = payout currency, a payout route | FAIL |
| EC-09 | REQUEST: amount ≤ campaign_payable − open refund exposure, payable account ACTIVE · PRE_SUBMIT: reservation ACTIVE and payout_pending ≥ amount (REVIEW if refund exposure now exceeds what is left) | FAIL |
| EC-10 | Provider min/max (FAIL); `payout.min_amount` (FAIL); `payout.max_per_transaction` (REVIEW); unknown/unconfigured (REVIEW) | FAIL / REVIEW |
| EC-11 | No other non-terminal payout for (campaign, currency) — the partial unique index is the backstop | FAIL |
| EC-12 | No hold: campaign SUSPENDED/FROZEN, payable ledger account FROZEN/CLOSED, destination SUSPENDED | FAIL |
| EC-13 | No compliance restriction on campaign, owner, organisation or beneficiary (detail never names the reason) | FAIL |
| EC-14 | Dispute/chargeback exposure — no dispute source exists | NOT_APPLICABLE |
| EC-15 | Screening — no provider (KI-S5-04): TEST payouts NOT_APPLICABLE, any other payout FAIL | FAIL |
| EC-16 | Risk indicators (recent destination change, repeated failed payouts in 30 d, multiple destinations in 30 d); an unconfigured rule is itself an indicator | REVIEW |
| EC-17 | Velocity (`payout.velocity.count_per_7d`, `payout.velocity.amount_per_7d`); unconfigured | REVIEW |
| EC-18 | Provider not in a declared outage or open circuit | DEFER |
| EC-19 | Latest ledger integrity run passed (no run ⇒ fail) | FAIL |
| EC-20 | Settlement freshness — settlement reconciliation NOT_PERFORMED: TEST payouts NOT_APPLICABLE, any other payout FAIL | FAIL |
| EC-21 | `payout.dual_approval_min`, `payout.min_amount`, `payout.max_per_transaction` configured | REVIEW |
| EC-22 | (submit) required approvals of the current round present, unexpired, no REJECT | FAIL |

Outcome: any FAIL ⇒ REJECTED; else any REVIEW ⇒ REVIEW; else any DEFER ⇒ DEFERRED; else ELIGIBLE.
Tier (never AUTO): DUAL if `payout.dual_approval_min` is unconfigured or reached, on any risk indicator, or for the first
payout of an organisation campaign; else SINGLE.

## 2. Acknowledged reviews (ADR-043 §5)

At PRE_SUBMIT, a REVIEW result whose `check:detail` appeared in the latest non-PRE_SUBMIT decision (the one the approvers
reviewed) is recorded as PASS with detail `ACKNOWLEDGED_BY_APPROVAL:<detail>`. A new REVIEW finding returns the payout to
review with a new approval round. DEFER is never acknowledged away.

## 3. At submission

| Outcome | Effect |
|---|---|
| ELIGIBLE | APPROVED → SUBMITTED; in-transit journal; then the provider call |
| DEFERRED | stays APPROVED (retried); PROVIDER_UNAVAILABLE exception on EC-18 |
| REVIEW | → PENDING_REVIEW, new round |
| REJECTED on EC-08 or EC-19 | stays APPROVED; PROVIDER_UNAVAILABLE exception (nothing the requester did) |
| REJECTED on EC-22 | → PENDING_REVIEW; APPROVAL_EXPIRED exception |
| REJECTED on EC-01, EC-12, EC-13 | → PENDING_REVIEW; COMPLIANCE_HOLD exception (funds stay reserved for a human decision) |
| REJECTED on EC-05, EC-07 | → PENDING_REVIEW; DESTINATION_MISMATCH exception |
| REJECTED on EC-09 | → PENDING_REVIEW; RECONCILIATION_MISMATCH (SEV1) |
| REJECTED otherwise | → REJECTED; reservation released |

## 4. Configurable limits (`risk.limits`, maker-checker; none configured)

`payout.min_amount`, `payout.max_per_transaction`, `payout.dual_approval_min`, `payout.velocity.count_per_7d`,
`payout.velocity.amount_per_7d` (MONEY/COUNT); `payout.approval_validity`, `payout.request_validity`,
`payout.risk.destination_change_window` (DURATION); `payout.risk.failed_payouts_30d`, `payout.risk.destinations_30d`
(COUNT). The most restrictive of all configured limit types applies. No value is invented (LR-030).
