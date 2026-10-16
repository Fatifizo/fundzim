# ADR-045: Risk engine — configurable rules on the Stage 5 signal store, risk cases, a central hold registry, provider-neutral screening

- **Status:** Proposed (Stage 13, 2026-10-15)
- **Deciders:** Technical lead (Stage 13); owner acceptance pending; compliance and legal review pending
- **Implements:** Stage 1 [aml-risk-framework.md](../compliance/aml-risk-framework.md), [transaction-monitoring.md](../compliance/transaction-monitoring.md),
  [sanctions-screening.md](../compliance/sanctions-screening.md); Stage 2 baseline: `risk` owns holds, limits, signals, assessments, monitoring rules and alerts
- **Amends:** the Stage 2 table ownership for screening (baseline: `compliance` owns screening requests/results); see §7
- **Related:** ADR-034 (Stage 5 risk foundation), ADR-037 (compliance restrictions), ADR-043 (payouts), ADR-044 (fees);
  [stage-13/risk-architecture.md](../stage-13/risk-architecture.md)

## Context

Stage 5 built `internal/risk`: append-only signals from domain events, a fixed rule-weighted model v1 whose weights are
`risk.limits` rows, assessments, routing decisions (NO_ACTION / MANUAL_REVIEW / ESCALATE) and an escalation event that the
compliance module turns into a RISK_REVIEW compliance case. Stage 6 built compliance restrictions (RESTRICTED / SUSPENDED /
OFFBOARDED) projected from approved compliance resolutions and enforced by campaigns, payments and payouts. Stage 11 has no
hold registry (KI-S11-04) and no screening (EC-15 NOT_APPLICABLE for TEST payouts). No screening provider is selected
(KI-S5-04, PD-29, LR-009/LR-060/LR-065/LR-066).

## Decision

1. **Extend `internal/risk`; one signal store.** `risk.risk_signals` remains the single store of risk facts. It gains
   category, severity, confidence, kind (OBSERVED from a source event or DERIVED by the engine), environment, correlation id
   and evidence reference (new columns with defaults; rows stay append-only). False-positive classification is a separate
   append-only review, never a deletion. `risk` stays a leaf module (platform, audit).
2. **Two scorers, one purpose each.** Model v1 (Stage 5) keeps scoring onboarding signals on 0–1000 and routing to
   compliance, unchanged. The Stage 13 engine (`risk-engine-v2`) evaluates **configurable rule versions** over the same
   signals, scores 0–100 and classifies LOW / MEDIUM / HIGH / CRITICAL (engineering defaults 25 / 50 / 75, overridable by
   approved `risk.limits` rows). Folding model v1 into reviewed v2 rules is a follow-up (KI).
3. **Rules are data, not code.** A rule version is a JSON condition tree over whitelisted condition types (signal count,
   velocity, signal fact comparison, distinct count, ordered sequence, risk state) with bounded windows (≤ 90 days), depth
   (≤ 3) and size (≤ 12 conditions); no regular expressions, SQL or executable code. Evaluation is a pure function of the
   rules, the subject's signals in the window and risk state. Score = Σ contributions of fired rules, capped at 100.
4. **Rule lifecycle and maker-checker** as for fee policies: DRAFT → PENDING_APPROVAL (definition frozen and hashed) →
   APPROVED → ACTIVE ⇄ SUSPENDED → RETIRED (+ REJECTED). The checker is never the maker or a linked account (Go and
   database); approval, activation, suspension and reactivation need step-up. One ACTIVE version per rule. Evaluations
   reference the exact versions used.
5. **Recommendation ≠ enforcement ≠ human outcome.** An evaluation stores the recommended action (ALLOW,
   ALLOW_WITH_MONITORING, REVIEW_REQUIRED, TEMPORARY_HOLD, ESCALATE, BLOCK). Automated enforcement is limited to: opening a
   risk case, recording a monitoring event and creating a **temporary** WITHDRAWAL_HOLD or PAYOUT_DESTINATION_HOLD
   (≤ 7 days, recorded as automated). BLOCK and ESCALATE are never enforced automatically: they open a critical case for a
   human. The engine never bans, confiscates, declares fraud, files a report or terminates a campaign.
6. **Risk cases** (`risk.risk_cases`) are operational investigations (OPEN → TRIAGE → UNDER_INVESTIGATION ⇄
   AWAITING_INFORMATION → ESCALATED / DECISION_PENDING → RESOLVED → CLOSED, REOPENED), with assignments, immutable evidence
   (content hash) and append-only history. Decisions are maker-checker (an investigator proposes, a different person
   approves). Escalation to compliance emits `risk.case_escalated`; the compliance module opens a RISK_REVIEW compliance
   case, which remains the only path to legal restrictions (RESTRICT / SUSPEND / OFFBOARD).
7. **Central hold registry in `risk`** (`risk.risk_holds`): CAMPAIGN_HOLD, DONATION_HOLD, WITHDRAWAL_HOLD,
   PAYOUT_DESTINATION_HOLD, ACCOUNT_REVIEW_HOLD, BENEFICIARY_HOLD. Holds are operational holds; compliance restrictions
   remain the legal restriction source. Both are enforced; neither lifts the other. Payout-protective holds (withdrawal,
   destination, beneficiary) take effect at once; disruptive holds (campaign, donation, account review) are REQUESTED by
   one person and activated by another (`activated_by`, Go and database). Release is maker-checker with step-up, never by
   the requester or anyone linked to the requester or to the held user (Go and database). A hold is enforced while ACTIVE,
   UNDER_REVIEW or RELEASE_PENDING. Enforcement points: campaign publication, donation intent and payment attempt creation,
   payout eligibility (EC-12 at request and pre-submission; payout eligibility engine `payout-eligibility-v2`) and payout
   approval. An approved case outcome applies to the case's holds: NO_FURTHER_ACTION / FALSE_POSITIVE release them (the
   proposer is the release maker, the approver its checker); RISK_CONFIRMED_HOLD_MAINTAINED / ESCALATED_TO_COMPLIANCE
   replace a temporary automated hold with a manual hold without expiry.
8. **Screening foundation in the `risk` schema** (deviation from the baseline's compliance ownership): a provider-neutral
   `ScreeningProvider` interface and a deterministic TEST_ONLY simulator over synthetic references; no identity data is
   stored; provider result, analyst review and final compliance decision are separate (a CONFIRMED_MATCH escalates to a
   compliance case). Production screening is BLOCKED; a pending, failed or timed-out screening never clears a subject.
   Moving screening under `compliance` with identity data from `kyc` comes with a provider (KI).
9. **Permissions** `risk.operations.view` (new: `risk.view` already exists since Stage 5 for KYC reviewers' signal views
   and keeps that meaning), `risk.case.work`, `risk.case.approve`, `risk.rule.request`, `risk.rule.approve`,
   `risk.hold.create`, `risk.hold.release`, `risk.screening.request`, `risk.screening.review` (COMPLIANCE; FINANCE gets
   `risk.operations.view`; two people are always required where maker-checker applies) and `risk.sandbox.control` (ADMIN,
   development only). Every mutation permission requires step-up.
10. **Payouts read risk.** Payout eligibility v2: EC-12 fails on any enforced hold; EC-15 (TEST payouts) routes an open
    potential match and a requested-but-unanswered (PENDING/ERROR) screening to REVIEW and fails on an analyst-confirmed
    match; EC-16 adds a HIGH/CRITICAL classification as a review indicator (never an outright refusal).
11. **Contract.** The Stage 2 *design* paths for holds, hold-release requests, monitoring alerts and the screening vendor
    callback (`/admin/risk/holds…`, `/admin/risk/hold-release-requests…`, `/admin/risk/alerts…`,
    `/webhooks/screening/{vendor}` with a `compliance.screening_callback_inbox`) are replaced by the implemented Stage 13
    API (`/admin/risk/…`, `/webhooks/screening/{provider}`); their orphaned design schemas are removed.

## Consequences

- Rules can be added and tuned without deployments, under maker-checker; every evaluation is explainable and reproducible.
- No rule is active until two people approve it: an installation with no approved rules evaluates nothing (Stage 5 model v1
  still runs).
- Holds give Stage 11's EC-12 a real registry (KI-S11-04 remediated); risk decisions never bypass existing financial controls.
- Evidence content hashes are SHA-256 over a canonical JSON form (sorted keys at every depth, numbers verbatim), so they
  verify against what PostgreSQL returns from `jsonb`; reconciliation recomputes them.
- New outbox events: `risk.evaluation_recorded`, `risk.case_opened`, `risk.case_escalated`, `risk.case_resolved`,
  `risk.hold_activated`, `risk.hold_released`, `risk.hold_expired`, `risk.rule_version_changed`, `risk.screening_updated`;
  new source events `identity.signed_in` (keyed per-user user-agent hash; no IP, no fingerprint),
  `fees.policy_version_changed`, `fees.adjustment_requested`; richer payloads (ids only) on `campaigns.created`,
  `payments.donation_intent_created`, `payments.payment_status_changed` (FAILED), `payouts.requested`, `payouts.status_changed`.
- Production screening stays BLOCKED (`risk.ScreeningProductionEnabled = false`, `ck_risk_screening_requests_test_only_stage13`,
  provider CHECK `sandbox`); the simulator is refused in production (`RISK_SCREENING_SANDBOX_ENABLED`).
