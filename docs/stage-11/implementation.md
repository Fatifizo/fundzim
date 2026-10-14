# Stage 11 — Implementation

Withdrawals, payout reservations, beneficiary and destination checks, approval workflows, disbursement orchestration and
payout security. **Non-production engineering**: every payout is a TEST payout of synthetic TEST_ONLY funds executed by
the sandbox simulator; no real payout can execute and production available funds are BLOCKED.

Start here: [prerequisite-assessment.md](prerequisite-assessment.md) · [payout-architecture.md](payout-architecture.md) ·
[withdrawal-lifecycle.md](withdrawal-lifecycle.md) · [eligibility-engine.md](eligibility-engine.md) ·
[available-funds.md](available-funds.md) · [reservations.md](reservations.md) · [approval-workflow.md](approval-workflow.md)
· [provider-abstraction.md](provider-abstraction.md) · [payout-simulator.md](payout-simulator.md) ·
[ledger-integration.md](ledger-integration.md) · [reconciliation.md](reconciliation.md) · [security-review.md](security-review.md)
· [testing.md](testing.md) · [known-issues.md](known-issues.md) · handover
[STAGE-11-TO-STAGE-12.md](../stage-handover/STAGE-11-TO-STAGE-12.md) · decision [ADR-043](../adr/ADR-043-withdrawals-and-payouts.md).

## 1. What was built

| Area | Artefacts |
|---|---|
| Schema | `migrations/20261014090000_payouts.sql`: payout requests, reservations, approvals, eligibility decisions, attempts, events, reconciliation references, exceptions (+ history), simulator payouts; ledger `source_system = payouts` (SIMULATED in TEST); sandbox payout capability (ECOCASH/USD v2); kill switch `payouts.live_execution.enabled`; permissions `payout.view`, `payout.operate`, `payout.sandbox.control`; runtime grants for the payout CHECK helpers |
| Backend | `internal/payouts` (see [payout-architecture.md](payout-architecture.md)); `internal/psp/payouts.go`, `internal/psp/sandbox/payouts.go`; `internal/ledger/payouts.go`; helpers `risk.ValueLimits`, `campaigns.CurrentBeneficiary`, `payments.OpenRefundExposure`; inbox routes PAYOUT events to payouts |
| Configuration | `PAYOUTS_EXECUTION_ENVIRONMENT` (DISABLED default; LOCAL_SIMULATOR; others refused); rate-limit policy `payouts.request.user` |
| Destinations | step-up on create/change/retire; `payout_readiness` (ownership verified ≠ payout eligible ≠ provider confirmed); approval invalidation on change, retirement, suspension and expiry |
| API | owner: `GET /campaigns/{campaign_id}/payout-eligibility`, `POST|GET /campaigns/{campaign_id}/payout-requests`, `GET /payout-requests`, `GET /payout-requests/{id}`, `GET /payout-requests/{id}/timeline`, `POST /payout-requests/{id}/cancel`; staff: `/admin/payout-requests` (list, detail, events, approve, reject, cancel, eligibility-checks, status-checks), `/admin/payouts/` (overview, reservations, exceptions + investigate/resolve, reconciliation + run, providers, sandbox controls) — 24 paths, OpenAPI tags "Payouts (Stage 11)" and "Payout operations (Stage 11)" |
| Staff UI | `/admin/finance/withdrawals`, `/withdrawals/[id]`, `/payouts`, `/payouts/[id]`, `/payouts/approvals`, `/payouts/reservations`, `/payouts/exceptions`, `/payouts/reconciliation` |
| Organiser UI | `/dashboard/campaigns/[id]/withdrawals` (TEST_ONLY availability, blockers, request form with step-up and a stable Idempotency-Key), `/dashboard/withdrawals`, `/dashboard/withdrawals/[id]` (timeline, cancel); destination readiness and step-up in the payout-account screen |
| Jobs | `payouts.submit` (2 s), `payouts.poll` (5 s), `payouts.recover` (30 s), `payouts.reconcile` (15 min) |

## 2. Audit and outbox

Audit: `payout.requested`, `payout.request.refused`, `payout.eligibility.evaluated`, `payout.reservation.created`,
`payout.approval.granted`, `payout.status_changed` (every transition, with source and transition code), `payout.submitted`,
`payout.reconciliation.run`, `payout.exception.investigating|resolved`. Outbox: `payouts.requested`,
`payouts.status_changed`, `payouts.exception_opened` (ids, codes and amounts as strings; no personal data). Payout events
(`app.payout_events`) carry the brief's event names (WITHDRAWAL_REQUESTED, ELIGIBILITY_CHECKED, RESERVATION_CREATED,
APPROVAL_REQUESTED/GRANTED/REJECTED, DESTINATION_CHANGED, PAYOUT_SUBMITTED_SANDBOX, PROVIDER_RESPONSE_RECEIVED,
PAYOUT_STATUS_CHANGED, PAYOUT_UNKNOWN, RECONCILIATION_REQUIRED, RESERVATION_RELEASED/CONSUMED/BLOCKED/EXPIRED, …) with
correlation ids and source references.

## 3. Fraud indicators (minimal; Stage 13 replaces them)

Recent destination change, repeated failed/rejected/reversed payouts in 30 days, multiple destinations in 30 days, and
velocity (count and amount per 7 days), each against a configurable `risk.limits` value; an unconfigured rule is itself an
indicator (REVIEW + DUAL). Automated fraud detection is **not** complete.

## 4. Test funds isolation

Synthetic funds exist only in the TEST ledger, created by the TEST_ONLY `fund_campaign` fixture (staff API, ADMIN,
`LEDGER_SYNTHETIC_FIXTURES_ENABLED`, refused in production). The simulator receives only masked synthetic destinations and
uses test secrets. No cross-environment transfer is possible: payouts, their journals and the ledger are all TEST, enforced
by CHECKs.

## 5. Deviations from the brief

| Brief | Implementation | Why |
|---|---|---|
| Table `withdrawal_requests`; states REQUESTED…RECONCILIATION_REQUIRED | `payout_requests`; ADR-020 states (mapping in [withdrawal-lifecycle.md](withdrawal-lifecycle.md)) | Stage 1/2 names are normative ("use existing state conventions") |
| Endpoints `/withdrawals`, `/admin/payouts/{id}` | Stage 2 design paths `/campaigns/{campaign_id}/payout-requests`, `/payout-requests/…`, `/admin/payout-requests/…` plus `/admin/payouts/…` | "Adapt to existing API conventions" |
| Suggested sub-packages | one `payouts` package (destinations already lived there) | "Do not create unnecessary packages" |
| Reservation in one transaction with the request | slot first, then ledger reservation (ADR-043 §3) | the ledger posts on its own role (ADR-042) |
| Holds (compliance holds, provider holds, reserves) | campaign/account/destination states and compliance restrictions; provider outages defer | no hold registry exists (KI-S11-04) |
| Payout risk indicators | minimal configurable rules | the risk engine is Stage 13 |
| Cache outage and outbox failure injection | not exercised for payouts | covered by the platform tests of Stages 3–8; the payout paths add no cache dependency |
