# Stage 12 — Implementation

Platform fees, provider fees, revenue accounting, fee disclosure, refund and chargeback adjustments, financial reporting
and fee administration. **Non-production engineering**: every fee is a TEST fee; rates are synthetic configuration entered
by staff for testing; no commercial, tax or provider rate exists; production fee charging and revenue recognition are
BLOCKED (`fees.ProductionFeesEnabled = false`, `ck_*_test_only_stage12`).

Start here: [prerequisite-assessment.md](prerequisite-assessment.md) · [fee-architecture.md](fee-architecture.md) ·
[fee-policy-model.md](fee-policy-model.md) · [fee-calculation.md](fee-calculation.md) · [rounding.md](rounding.md) ·
[provider-fees.md](provider-fees.md) · [fee-disclosure.md](fee-disclosure.md) · [revenue-accounting.md](revenue-accounting.md) ·
[refund-adjustments.md](refund-adjustments.md) · [chargeback-fees.md](chargeback-fees.md) · [payout-fees.md](payout-fees.md) ·
[reconciliation.md](reconciliation.md) · [security-review.md](security-review.md) · [testing.md](testing.md) ·
[known-issues.md](known-issues.md) · handover [STAGE-12-TO-STAGE-13.md](../stage-handover/STAGE-12-TO-STAGE-13.md) · decision
[ADR-044](../adr/ADR-044-fees-and-platform-revenue.md).

## 1. What was built

| Area | Artefacts |
|---|---|
| Schema | `migrations/20261015090000_fees.sql`: `fee_policies` (immutable unique scope), `fee_policy_versions` (terms frozen at submission, maker-checker, lifecycle), `fee_provider_schedules` (SYNTHETIC/UNVERIFIED evidence), `fee_calculations` + `fee_components` (immutable, reproducible), `fee_quotes`, `fee_assessments`, `fee_adjustments`, `fee_accounting_references`, `fee_provider_reports`, `fee_reconciliation_runs` + `fee_reconciliation_references`, `fee_audit_events`; `donation_intents.fee_quote_id` / `fee_donor_charge_minor`; `payout_requests.fee_*`; ledger `source_system = 'fees'` (SIMULATED in TEST); permissions `fee.view`, `fee.adjust.request`, `fee.adjust.approve`, `fee.fixtures` (`fee.config.request` / `fee.config.approve` exist since Stage 4) |
| Backend | `internal/fees` (one package): `calc.go` (pure engine), `policies.go`, `schedules.go`, `quotes.go`, `assess.go`, `adjustments.go`, `fixtures.go`, `reconcile.go`, `reports.go`, `http.go`, `jobs.go`; `internal/ledger/fees.go` (read API) and `Posting.FeePolicyVersionID`; `internal/app/fees.go` (adapters for campaigns, routing and linked identities) |
| Integrations | `payments.CreateIntent` binds a quote to every intent (`fee_quote_id`, optional on the request); `payouts.RequestPayout` stores the payout fee estimate; `payouts.syncLedger` assesses the payout fee on completion; outbox consumer `fees.payments` assesses succeeded payments |
| Configuration | `FEES_QUOTE_TTL` (1m–60m, default 15m); rate-limit policy `fees.quote.user` (60/hour) |
| API | 34 paths (OpenAPI tags "Fees (Stage 12)" and "Fee administration (Stage 12)"); the Stage 2 design paths `/admin/fee-schedules` and `/admin/fee-schedule-change-requests` are superseded and removed |
| Staff UI | `/admin/finance/fees` (overview), `/fees/policies`, `/fees/policies/[id]`, `/fees/provider-schedules`, `/fees/quotes`, `/fees/assessments[/id]`, `/fees/adjustments`, `/fees/reconciliation`, `/admin/finance/revenue` |
| Organiser / donor UI | `/dashboard/campaigns/[id]/fees`; payout fee estimate on withdrawal pages; signed-in donation fee preview (public checkout stays disabled) |
| Jobs | `fees.maintenance` (1 min: expire quotes, retry pending payout fee journals), `fees.reconcile` (15 min) |

## 2. Audit and history

Platform audit (`audit.Record`) for every staff action: `fee.policy.created`, `fee.policy_version.drafted|submitted|approved|
rejected|activated|suspended|reactivated|retired`, `fee.provider_schedule.recorded|activated|retired`,
`fee.adjustment.requested|approved|rejected`, `fee.reconciliation.resolved`, `fee.fixture.run`. The append-only fee history
`app.fee_audit_events` records every aggregate change, including quotes and assessments (quotes do not write the platform
audit log: they are on a user request path and the audit hash chain serialises writers — Stage 9 lesson).

## 3. Test-funds isolation

Fees are TEST rows (CHECK). Test payments are assessed but never posted (`ck_fee_assessments_test_payments_not_posted`).
Fee journals exist only for TEST_ONLY synthetic fixtures and TEST payouts, in the TEST ledger, with `SIMULATED` evidence
(`ck_ledger_transactions_fees_simulated_in_test`). Synthetic figures are labelled `TEST_ONLY` / "TEST — synthetic
configuration, not FundZim's commercial rates" everywhere they are shown.

## 4. Deviations from the brief

| Brief | Implementation | Why |
|---|---|---|
| Suggested sub-packages (`fees/domain`, `fees/policies`, …) | one `internal/fees` package with a pure `calc.go` | "Do not create unnecessary packages"; the engine is still separated from HTTP, persistence, providers and the UI |
| Tables `fee_policy_versions` approvals via separate table | approval columns on the version (one checker per version) | one maker-checker step per version; history in `fee_audit_events` |
| `POST /admin/fees/policies/{id}/approve`, `/activate` | per version: `/admin/fees/policy-versions/{version_id}/approve|activate|…` | approvals bind to an immutable version, not to the policy |
| Models CAPPED and MINIMUM_FEE | percentage with a mandatory maximum / minimum; min/max are also optional on other models | the brief asks for explicit min/max behaviour |
| PLATFORM_ABSORBED platform fee | refused (use ZERO_FEE) | FundZim cannot pay its own fee; PLATFORM_ABSORBED applies to provider costs |
| `CURRENCY_CONVERSION_FEE`, `OTHER_APPROVED_FEE` | in the vocabulary, cannot be created | no currency conversion ever; no posting rule |
| Donor-covered charge sent to the provider | recorded on the intent only | the payment amount is bound to the intent amount (Stage 8); pass-through needs provider confirmation |
| Fee assessment of real (test) payments with ledger postings | assessments only; postings demonstrated on synthetic fixtures | ADR-042: test payments are never posted |
| Revenue recognition engine | the Stage 1/10 capture-time credit to `revenue:platform_fees`, in TEST, labelled ACCOUNTING_REVIEW_REQUIRED | no accounting approval exists |
| Cache outage, outbox failure injection | covered by the platform's Stage 4/8 tests; fee consumers are idempotent (duplicate delivery tested) | quotes use the existing limiter fallback; no new cache dependency |
