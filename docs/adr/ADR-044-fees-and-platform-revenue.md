# ADR-044: Fees and platform revenue — TEST fees only, versioned policies, pure calculation, derived fee journals

- **Status:** Proposed (Stage 12, 2026-10-15)
- **Deciders:** Technical lead (Stage 12); owner acceptance pending; accounting, tax and legal review pending
- **Implements:** [MONEY.md §5–§6](../MONEY.md) (basis points, single rounding point, largest-remainder allocation); Stage 2
  module `fees` ([design-baseline §2, §5.7](../stage-2/design-baseline.md)) and draft `design/sql/0007_fees_psp.sql`;
  Stage 10 posting rules `DONATION_CAPTURED` (platform-fee line), `PSP_FEE`, `PAYOUT_FEE`, `REFUND_RESERVED`,
  `REFUND_SETTLED`, `DISPUTE_OPENED`, `DISPUTE_FEE`
- **Amends:** the Stage 2 dependency graph (§3): `fees` may import `ledger` (it moves from layer 2 to layer 3)
- **Related:** ADR-013 (Model A), ADR-014 (chart of accounts), ADR-017 (maker-checker), ADR-040 (no provider selected),
  ADR-042 (TEST ledger, posting role), ADR-043 (TEST payouts); [stage-12/fee-architecture.md](../stage-12/fee-architecture.md)

## Context

Stage 12 builds fee policies, calculation, quotes, assessments, refund/chargeback/payout fee treatment, reconciliation and
revenue reporting while: no fee model is decided (PD-01, LR-028); the tax treatment of fees is open (LR-016, LR-045,
LR-059); revenue recognition needs an accounting review (KI-S10-02, LR-002); refund and chargeback fee treatment is a
provisional business decision (PD-20) with open legal items (LR-018, LR-020, LR-076, LR-080); no provider is selected, so
no provider fee, rounding rule, refund or chargeback fee, payout fee or settlement fee is known (ADR-040; PCR-005, PCR-007,
PCR-013, PCR-016); the ledger is a TEST ledger (ADR-042) and payouts are TEST payouts (ADR-043).

The Stage 2 design says `fees` "never posts" and imports only `platform` and `audit`. The Stage 12 brief requires fee
accounting through the ledger.

## Decision

1. **TEST fees only.** Every fee policy version, quote, calculation, assessment, adjustment and provider fee schedule
   carries `fee_environment = 'TEST'` (CHECKs `ck_*_test_only_stage12`). The Go constant `fees.ProductionFeesEnabled =
   false` gates any production path. Rates are synthetic configuration entered by staff for testing; no commercial rate,
   tax rate or provider rate is seeded or hard-coded. Production fee charging and revenue recognition are BLOCKED until
   PD-01, LR-016/LR-028/LR-045, an accounting review and owner acceptance exist.
2. **One package `internal/fees`** (schema `app`, owner `fees`). Fee calculation is a pure function (`Calculate`) with no
   I/O, separate from HTTP, persistence, the ledger and providers. `fees` imports `platform`, `audit` and `ledger` (graph
   amendment); it never imports `campaigns`, `payments`, `payouts` or `psp`: campaign and routing facts reach it through
   small interfaces wired in `internal/app`. `payments` and `payouts` import `fees` (already allowed by the graph).
3. **Policies and versions.** `fee_policies` holds identity and an immutable scope (category, transaction type, currency,
   and optionally payment method, provider, campaign category, campaign). `scope_key` is unique: two policies can never
   share a scope. `fee_policy_versions` holds the terms (model, rate in integer basis points, fixed, minimum, maximum,
   tiers, rounding mode, responsibility, refund treatment, effective window). Terms are editable only in `DRAFT`; a
   trigger refuses any change afterwards. Lifecycle (guarded by `app.guard_transition('fee_policy_version')`):
   `DRAFT → PENDING_APPROVAL → APPROVED → ACTIVE → SUSPENDED → ACTIVE | RETIRED`, `ACTIVE → RETIRED`,
   `PENDING_APPROVAL → REJECTED`, `DRAFT | APPROVED → RETIRED`. The maker (`fee.config.request`, FINANCE) cannot approve:
   the checker (`fee.config.approve`, BUSINESS_APPROVER) must be a different person including linked accounts
   (`app.actor_identities`, checked in Go and SQL) and needs a fresh step-up; activation and suspension need a fresh
   step-up; re-activating a suspended version needs a checker other than the suspender.
4. **Deterministic selection.** A version applies at time *t* if it is `ACTIVE` and `effective_from ≤ t < effective_to`.
   Within a policy the version with the greatest `effective_from` wins. Across policies the most specific scope wins
   (campaign 16 > campaign category 8 > provider 4 > payment method 2, summed); because `scope_key` is unique, two
   matching policies can never have equal specificity, and the code refuses `FEE_POLICY_AMBIGUOUS` as a defence.
5. **Categories.** `PLATFORM_FEE` (FundZim's own fee; models `ZERO_FEE`, `PERCENTAGE`, `FIXED`,
   `PERCENTAGE_PLUS_FIXED`, `TIERED`, `CAPPED`, `MINIMUM_FEE`). Provider-cost categories `PAYMENT_PROCESSING_FEE`,
   `PAYOUT_PROCESSING_FEE`, `REFUND_FEE`, `CHARGEBACK_FEE` use model `PROVIDER_PASS_THROUGH`: the policy decides only
   who bears the provider's fee; the amount comes from provider fee evidence. `CURRENCY_CONVERSION_FEE` (FundZim never
   converts currencies) and `OTHER_APPROVED_FEE` (no posting rule) cannot be created: `FEE_CATEGORY_NOT_ENABLED`.
6. **Calculation.** Integer minor units with exact big-integer intermediates; rates in basis points (0–10 000); one
   rounding per component (`HALF_UP`, `HALF_EVEN`, `FLOOR`, `CEILING`), then minimum, then maximum; tiers select the band
   containing the whole amount; every fee is computed on the donation (or payout/refund) amount, never grossed up.
   Splits (SHARED) use largest-remainder allocation. Each calculation stores its canonical inputs, policy and schedule
   versions, components, rounding, result, `algorithm_version` and SHA-256 hashes of inputs and result; recomputing
   from the stored inputs must reproduce the result (checked by reconciliation).
7. **Breakdown identity.** For a donation *D* with platform fee *P* and provider fee *V* (each split by responsibility
   into donor / organiser / platform parts): `donor_charge = D + P_donor + V_donor`,
   `campaign_allocation = D − P_organiser − V_organiser`, `platform_net = P − V_platform`, and
   `donor_charge = campaign_allocation + platform_net + V`. An unknown provider fee makes the provider-dependent totals
   `UNKNOWN`, never zero. `PLATFORM_ABSORBED` is not allowed for `PLATFORM_FEE` (use `ZERO_FEE`).
8. **Fee bases are distinct values:** `QUOTED` (FundZim policy), `ESTIMATED` (unverified or synthetic provider
   schedule), `PROVIDER_REPORTED`, `PROVIDER_CONFIRMED`, `RECONCILED`, `UNKNOWN`. Provider fee schedules may be
   `SYNTHETIC` (sandbox providers only, TEST) or `UNVERIFIED`; `VERIFIED` is refused by CHECK in Stage 12 (no provider
   evidence exists; ADR-040). Nothing is revenue because it was quoted or estimated.
9. **Quotes** are immutable, owned by the requesting user, expire (`FEES_QUOTE_TTL`, default 15 minutes) and are
   invalidated when a policy version in their currency changes lifecycle state. A quote is bound once, to one donation
   intent, only if its campaign, amount, currency, method and policy versions still match: otherwise
   `FEE_QUOTE_STALE`. Every donation intent gets a quote (the caller's, or one computed at creation).
10. **Assessments** are created idempotently (unique on source type, source id and category) from the bound quote when a
    payment succeeds (outbox consumer `fees.payments`), and from payout completion. Test payments are never posted
    (ADR-042): their assessments are `accounting_status = NOT_POSTED_TEST_PAYMENT`. Fee journals exist only for
    TEST_ONLY synthetic fixtures and TEST payouts, posted through `ledger.Service` with `source_system = 'fees'`,
    `SIMULATED` evidence (CHECK `ck_ledger_transactions_fees_simulated_in_test`), deterministic keys and the policy
    version in `ledger_transactions.fee_schedule_version_id`. No new account class or posting rule is added.
11. **Revenue recognition** (TEST only): the Stage 1/10 design credits `revenue:platform_fees` in the capture journal;
    Stage 12 keeps that trigger for synthetic fixtures and labels it `ACCOUNTING_REVIEW_REQUIRED`. Recognised TEST revenue
    is what the ledger holds, never quotes or assessments.
12. **Adjustments** are new immutable rows (`REFUND`, `CHARGEBACK`, `PROVIDER_CORRECTION`, `POLICY_CORRECTION`,
    `ACCOUNTING_ADJUSTMENT`); the net adjustment never takes an assessment below zero (checked under a row lock and by a
    trigger). Refund and chargeback adjustments follow the version's refund treatment (`PROPORTIONAL`,
    `PROPORTIONAL_EXCEPT_FIXED`, `NON_REFUNDABLE`) with the version's rounding; provider fees on refunds are `UNKNOWN`
    (never assumed refundable). Manual reasons are maker-checker (`fee.adjust.request` / `fee.adjust.approve`) and never
    post: a ledger correction is a Stage 10 adjustment.
13. **Payout fees:** the payout request stores the fee estimate; the reservation stays the requested amount (an
    organiser-borne fee is carved out of it, a platform-absorbed fee is a `PAYOUT_FEE` expense journal), so a fee is never
    deducted twice; a known organiser-borne fee that is not below the amount is refused.

## Consequences

- Fees can be designed, approved, calculated, disclosed, assessed, adjusted, reconciled and reported end to end in TEST,
  without inventing commercial, tax or provider terms.
- Real-payment fee assessments are records only until production posting exists (ADR-042 rules unchanged).
- Donor-covered amounts are recorded on the intent; passing the higher charge to a provider needs provider confirmation
  that fee pass-through is permitted (KI-S12).
