# Stage 13 — Implementation

Trust, fraud and risk engine: normalised risk signals, an explainable configurable rule engine, risk cases with immutable
evidence, a central hold registry enforced at publication, donations and payouts, a provider-neutral screening
foundation with a TEST simulator, monitoring events, reconciliation and risk operations reporting. **Non-production
engineering**: no screening provider is selected (production screening BLOCKED, `risk.ScreeningProductionEnabled = false`);
every screening is TEST with synthetic references; payments, payouts and fees stay TEST (Stages 8–12 controls unchanged).
**A risk score is a prioritisation aid, never proof of fraud**, and no engine output is worded as a fraud finding (database
CHECKs).

Start here: [prerequisite-assessment.md](prerequisite-assessment.md) · [risk-architecture.md](risk-architecture.md) ·
[risk-signals.md](risk-signals.md) · [risk-scoring.md](risk-scoring.md) · [risk-rules.md](risk-rules.md) ·
[risk-decisions.md](risk-decisions.md) · [campaign-risk.md](campaign-risk.md) · [payment-risk.md](payment-risk.md) ·
[payout-risk.md](payout-risk.md) · [screening.md](screening.md) · [case-management.md](case-management.md) ·
[compliance-holds.md](compliance-holds.md) · [privacy.md](privacy.md) · [security-review.md](security-review.md) ·
[testing.md](testing.md) · [known-issues.md](known-issues.md) · handover
[STAGE-13-TO-STAGE-14.md](../stage-handover/STAGE-13-TO-STAGE-14.md) · decision
[ADR-045](../adr/ADR-045-risk-engine-cases-holds-screening.md).

## 1. What was built

| Area | Artefacts |
|---|---|
| Schema | `migrations/20261016090000_risk_engine.sql`: `risk.risk_signals` gains category, severity, confidence, kind (OBSERVED/DERIVED), environment (TEST), correlation id, evidence reference; new `risk_events` (append-only domain history), `risk_rules` + `risk_rule_versions` (immutable after submission, maker-checker, machine `risk_rule_version`), `risk_evaluations` (immutable, one per trigger signal), `risk_subject_profiles` (latest evaluation projection), `risk_evaluation_actions`, `risk_signal_reviews` (false-positive labels), `risk_cases` (machine `risk_case`), `risk_case_assignments`, `risk_case_evidence` (immutable, SHA-256), `risk_case_access_log`, `risk_holds` (machine `risk_hold`), `risk_screening_requests` (machine `risk_screening`, TEST/sandbox only), `risk_screening_results`, `sandbox_screenings`, `risk_monitoring_events`; compliance links may reference `RISK_CASE`; permissions `risk.operations.view`, `risk.case.work`, `risk.case.approve`, `risk.rule.request`, `risk.rule.approve`, `risk.hold.create`, `risk.hold.release`, `risk.screening.request`, `risk.screening.review`, `risk.sandbox.control` |
| Backend | `internal/risk`: `engine.go` (pure rule engine), `catalog.go` (signal catalogue and Stage 13 event mapping), `evaluate.go` (pipeline and automated actions), `rules.go`, `cases.go`, `holds.go` (registry and enforcement queries), `screening.go` + `screening_sandbox.go`, `reconcile.go`, `reports.go`, `http.go`, `jobs.go`, `common.go`; `internal/app/risk.go` (wiring) |
| Integrations | payments: hold checks in `CreateIntent` and `CreateAttempt`, donor id in `donation_intent_created` and FAILED `payment_status_changed`; campaigns: hold check at publication, `title_hash` on `campaigns.created`; payouts: eligibility `payout-eligibility-v2` (EC-12 holds, EC-15 TEST screening status, EC-16 classification), hold check at approval, `requested_by` / `destination_id` on payout events; auth: `identity.signed_in` (keyed per-user user-agent hash); fees: `fees.policy_version_changed`, `fees.adjustment_requested` (KI-S12-17); compliance: consumer of `risk.case_escalated` opens a RISK_REVIEW case linked to the risk case |
| Configuration | `RISK_SCREENING_SANDBOX_ENABLED` (refused in production); rate-limit policy `risk.screening_webhook.ip` (300/min) |
| API | 49 paths (OpenAPI tag "Risk operations (Stage 13)" and `POST /webhooks/screening/{provider}`); the Stage 2 design paths for holds, hold-release requests, alerts and the screening vendor callback are replaced |
| Staff UI | `/admin/risk` (overview), `/signals`, `/evaluations`, `/rules`, `/rules/[id]`, `/cases`, `/cases/[id]` (investigation workspace), `/holds`, `/screening`, `/monitoring` |
| Jobs | `risk.maintenance` (1 min: expire temporary holds, deliver simulated screening callbacks when the simulator runs, poll/timeout screenings), `risk.reconcile` (15 min: overdue and unassigned-critical cases, reconciliation) |

## 2. Audit and history

Platform audit (`audit.Record`) for every staff action and automated action: `risk.rule.created`,
`risk.rule_version.drafted|submitted|approved|rejected|activated|suspended|reactivated|retired`,
`risk.evaluation.actioned`, `risk.case.opened|assigned|transitioned|evidence_added|escalated|decision_proposed|resolved|
decision_returned|reopened`, `risk.signal.reviewed`, `risk.hold.active|requested|activated|revoked|release_requested|
released|release_rejected|expired`, `risk.screening.requested|reviewed`, `risk.reconciliation.run`. The append-only
`risk.risk_events` keeps every aggregate's history; case and evidence views are recorded in `risk.risk_case_access_log`.

## 3. What is automated and what is not

Automated (ADR-045 §5): signals from trusted events; evaluations; monitoring events; opening or linking a case; a
temporary (≤ 7 days) WITHDRAWAL_HOLD or PAYOUT_DESTINATION_HOLD when an approved rule asks for one. **Never automated:**
bans, confiscation, fraud declarations, regulatory reports, campaign termination, legal restrictions (compliance only),
clearing a screening, releasing a hold.

## 4. Preserved controls

Stage 5 verification and model v1 run unchanged; compliance restrictions remain the only legal restriction source and
are enforced alongside holds; payments still never confirm from a browser and test payments never reach the ledger;
payouts keep every Stage 11 check (the eligibility engine adds risk facts, never removes a check); fees keep their
controls (they only emit two new events). No ledger table is touched by `risk`.
