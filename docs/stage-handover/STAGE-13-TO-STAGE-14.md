# Stage 13 → Stage 14 Handover: Administration & Operations Portal

> Stage 13 is implemented on branch `stage-13/risk` and **awaits acceptance** (Stages 1–12 too; Stage 9 PARTIAL; Stages
> 10–12 PASS WITH CONDITIONS). Do not start Stage 14 until the owner accepts and asks for it. This document prepares
> Stage 14; it implements nothing. Production financial operations and production screening remain BLOCKED.

Stage 13 docs: [implementation](../stage-13/implementation.md) · [architecture](../stage-13/risk-architecture.md) ·
[case management](../stage-13/case-management.md) · [holds](../stage-13/compliance-holds.md) ·
[security review](../stage-13/security-review.md) · [known issues](../stage-13/known-issues.md). ADR-045.
Roadmap: [ROADMAP.md](../ROADMAP.md) "Stage 14 — Administration & Operations Portal". Stage 1 inputs:
[operational-controls.md](../compliance/operational-controls.md), [audit-evidence-model.md](../compliance/audit-evidence-model.md).

## Preparation topics (brief §72)

| Topic | What exists after Stage 13 | What Stage 14 must decide or build |
|---|---|---|
| Staff roles and permissions | Roles SUPER_ADMIN, ADMIN, SUPPORT, KYC_REVIEWER, REVIEWER, COMPLIANCE, FINANCE, BUSINESS_APPROVER; permissions with `is_sensitive` / `requires_step_up`; dual-controlled role grants (`role_assignment_requests`); linked staff↔personal accounts (`app.actor_identities`) | A permission-matrix screen and E2E matrix tests; separation of the staff portal (hostname, session cookie scope); break-glass procedure |
| Campaign moderation | Review queue and decisions (Stage 6), reports (Stage 7), campaign holds (Stage 13) | One moderation workspace showing review, reports, risk cases and holds together |
| Verification review | KYC/KYB/beneficiary/destination review UI (Stage 5), justified identity-number reveal | Queue SLAs, workload balancing, justified-access audit views |
| Risk investigation | `/admin/risk/*` (Stage 13): overview, signals, evaluations, rules, cases (workspace), holds, screening, monitoring | Cross-module subject view (user/org/campaign timeline with masked PII), saved queues, case templates, bulk-safe actions (no bulk export without approval) |
| Compliance holds | Hold registry with maker-checker activation/release; compliance cases and restrictions (Stage 6) | A single "restrictions and holds" panel per subject showing both sources (never merged) |
| Payment operations | Payment admin views, dead-letter re-queue, refund requests (non-executable) | Operations runbooks in the UI; provider incident banners (no provider yet) |
| Ledger operations | Ledger views, maker-checker adjustments, integrity runs (Stage 10) | Period close UI (Stage 17 dependency), adjustment queues |
| Withdrawal operations | Payout review/approval UI, exceptions, reconciliation (Stage 11), risk holds in EC-12 | Approval workload views; payout risk context (classification, holds, screening) on the approval screen |
| Fee management | Fee policy/version admin, provider evidence, adjustments, revenue (Stage 12) | Fee change calendars and diff views |
| Financial reconciliation | Payment, ledger, payout, fee and risk reconciliation runs | A reconciliation console across modules (Stage 17 owns the logic) |
| Audit reporting | Audit hash chain, per-module histories (`risk_events`, `fee_audit_events`, payout events), case access logs | An audit log viewer with filters, chain verification, export under approval |
| Operational dashboards | Per-module overviews (fees, payouts, risk) | One operations home with health, queues, overdue items (monitoring events) |
| Administrative approvals | Maker-checker everywhere (rules, holds, cases, fees, payouts, ledger adjustments, role grants) | A unified "awaiting my approval" inbox that respects linked-identity separation |
| Support workflows | Support role, account recovery beyond password reset not built | Support-mediated recovery, masked user search, ticket linkage |
| Security monitoring | Security events (`app.security_events`), rate limits, monitoring events | Security dashboard (failed logins, step-up failures, callback rejections), alert routing |
| Incident management | Kill switches (payments/payouts), dead-letter queues | Incident records, freeze procedures, communications (Stage 15) |
| Staff accountability | Audit of every staff action, step-up times recorded on decisions, access logs on risk cases and KYC documents | Staff activity reports, periodic access reviews, dormant-account handling |
| Production access controls | TEST-only environments, production gates by constants and CHECKs | Production access model (just-in-time access, approvals), separate production roles; depends on Stage 18/19 |

## Rules Stage 14 must keep

- The portal is presentation over the modules' services: no new direct table access, no bypass of maker-checker,
  step-up or linked-identity checks; no role gains implicit KYC or ledger powers.
- Risk scores, holds and cases stay staff-only; owners see coarse categories (LR-008).
- Production financial operations and production screening remain BLOCKED until their ADRs, legal sign-off and acceptance.

## Open blockers carried

Screening provider and legal scope (LR-009, LR-053, LR-064, LR-065, LR-066), reporting-institution status (LR-008, LR-051,
LR-060), automated decisions and retention (LR-071, LR-012, LR-034, LR-074), payment provider selection (ADR-040),
settlement evidence (PCR-007), the Stage 12 legal/tax items, accounting review (KI-S10-02), and the acceptance of Stages 1–13.
