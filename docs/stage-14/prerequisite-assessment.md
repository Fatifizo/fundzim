# Stage 14 prerequisite assessment

Date: 2026-10-16. Branch `stage-14/operations`, created from `stage-13/risk` at `df0e43e` (CI run 38864342678: all 8 jobs
succeeded). Inputs read: `CLAUDE.md`, `README.md`, `docs/PRODUCT.md`, `docs/ARCHITECTURE.md`, `docs/SECURITY.md`,
`docs/ROADMAP.md`, [STAGE-13-TO-STAGE-14.md](../stage-handover/STAGE-13-TO-STAGE-14.md),
[operational-controls.md](../compliance/operational-controls.md), the role/permission catalogue in the database, the admin
routes of every module (`internal/*/http.go`, `internal/app/*.go`), the web admin (`apps/web/src/app/admin/*`,
`admin-shell.tsx`), the audit schema and grants, health and worker infrastructure, OpenAPI, CI and the acceptance register.

Classification: **SATISFIED**, **SATISFIED_WITH_CONDITIONS**, **BLOCKED**, **NOT_VERIFIED**.

## 1. Governance (verified against `docs/governance/stage-acceptance-register.md`)

| Item | Status | Evidence |
|---|---|---|
| Formal acceptance of Stages 0–13 | **NOT_VERIFIED** | every row `NOT_RECORDED`; nothing is marked accepted in Stage 14 |
| Stage 9 | PARTIAL (report recommendation) | provider selection deferred (ADR-040) |
| Stages 10–13 | PASS WITH CONDITIONS (report recommendations) | TEST ledger, payouts, fees, risk data |
| Production financial operations, production screening | BLOCKED | code constants and CHECKs; the portal adds no override |

## 2. Technical prerequisites

| Prerequisite | Status | Evidence |
|---|---|---|
| Staff authentication, MFA, step-up, sessions | SATISFIED | Stage 4 (`internal/auth`) |
| RBAC catalogue (10 roles, permissions with `requires_step_up`) | SATISFIED | `app.roles`, `app.permissions`; no implicit KYC/ledger powers |
| Role grants/revocations (dual control) | SATISFIED | `role_assignment_requests` (GRANT/REVOKE), `role.assign.request` / `.approve` |
| Staff invitation, staff suspension | SATISFIED | `POST /admin/staff`, `/admin/staff/{id}/suspend` |
| Staff directory, session revocation, access review | **missing** (OpenAPI DEFERRED) | built in Stage 14 (auth + operations) |
| Effective permissions for the UI | **missing** (`/me` returns roles only) | Stage 14 adds `permissions` to the staff self view |
| Campaign moderation, reports | SATISFIED | Stage 6/7 queues and decisions |
| Verification review (KYC/KYB/beneficiary/destination), justified document access | SATISFIED | Stage 5 queue, tickets, access log |
| Risk cases, holds, rules, screening | SATISFIED_WITH_CONDITIONS | Stage 13; rule fault isolation missing (KI-S13-11) — fixed first |
| Payment operations (overview, intents, attempts, UNKNOWN queue, reconciliation, refunds, providers, health) | SATISFIED | Stage 8/9 admin API; providers derived health only |
| Ledger inspection, adjustments (maker-checker), integrity, reconciliation exceptions | SATISFIED | Stage 10 |
| Payout review, approvals, exceptions, reconciliation | SATISFIED | Stage 11 |
| Fee policies, adjustments, reconciliation, revenue (TEST) | SATISFIED | Stage 12 |
| Compliance cases and restrictions | SATISFIED | Stage 6 (separate pool) |
| Audit log (hash chain) readable by the app role | SATISFIED_WITH_CONDITIONS | `fundzim_app` has SELECT on `audit.*`; no search/export API yet (OpenAPI design only); `audit.verify_chain` is granted to the worker only |
| Health checks, worker, outbox metrics | SATISFIED_WITH_CONDITIONS | `/readyz` checks DB/cache/storage; outbox backlog and River jobs are queryable; there is no worker heartbeat table |
| In-app notifications | not built | Stage 14 adds task indicators only (delivery is Stage 15) |
| Support cases, incidents, central exceptions | not built | Stage 14 |

## 3. Stage 13 remediation review (KI-S13-01 … KI-S13-22, definitions in [../stage-13/known-issues.md](../stage-13/known-issues.md))

| ID | Definition (abridged) | Classification | Rationale |
|---|---|---|---|
| KI-S13-01 | Acceptance of Stages 1–13 not recorded | EXTERNAL_BLOCKER | owner |
| KI-S13-02 | No screening provider; lists/scope/timing/PEP/freeze open | EXTERNAL_BLOCKER | legal + provider |
| KI-S13-03 | Reporting-institution / STR duties unknown | EXTERNAL_BLOCKER | legal |
| KI-S13-04 | Automated decisions, retention, data-subject rights | EXTERNAL_BLOCKER | legal |
| KI-S13-05 | Screening in the `risk` schema | DEFER_WITH_RATIONALE | moves with a provider |
| KI-S13-06 | No rule seeded or approved | DEFER_WITH_RATIONALE | rules are operational configuration under maker-checker; seeding invented thresholds would be worse. The command center now shows "no active rules" as a NOT_CONFIGURED monitoring status |
| KI-S13-07 | No accuracy measurement | DEFER_WITH_RATIONALE | needs labelled evidence; never fabricated |
| KI-S13-08 | Model v1/v2 coexist | DEFER_WITH_RATIONALE | folding v1 into reviewed v2 rules needs compliance review of the v1 weights |
| KI-S13-09 | No provider signals; narrow session signals | EXTERNAL_BLOCKER | provider / privacy basis |
| KI-S13-10 | No fairness review / rule review reminders | DEFER_WITH_RATIONALE | governance process; the access-review foundation is the template |
| KI-S13-11 | An unrunnable ACTIVE rule blocks signal recording | **FIX_IN_STAGE_14** | security/monitoring gap — fault isolation (§6 of the brief) |
| KI-S13-12 | Case SLA and screening timeout are code defaults | DEFER_WITH_RATIONALE | Stage 14 adds configurable internal SLA targets for operations reporting; the risk module (a leaf) keeps its defaults, documented as ENGINEERING_DEFAULT |
| KI-S13-13 | Bounded reconciliation windows | DEFER_WITH_RATIONALE | TEST volumes; paging is Stage 17 |
| KI-S13-14 | One open hold per type and subject | DEFER_WITH_RATIONALE | accepted design |
| KI-S13-15 | EC-15 TEST screening scope | DEFER_WITH_RATIONALE | by design until a provider exists |
| KI-S13-16 | Simulator signing scheme is FundZim's own | EXTERNAL_BLOCKER | provider |
| KI-S13-17 | Dev-database residue | DEFER_WITH_RATIONALE | test environment |
| KI-S13-18 | Compose worker image predates Stage 13 until rebuilt | **FIX_IN_STAGE_14** | containers rebuilt before the real-stack suites |
| KI-S13-19 | Fingerprint / self-donation / refund-abuse signals not built | DEFER_WITH_RATIONALE | payments must emit fingerprint ids; disputes are later |
| KI-S13-20 | `TestPublicExperienceEndpoints` intermittent | **FIX_IN_STAGE_14** | investigate the cross-test interaction |
| KI-S13-21 | Risk UI shows actions the viewer may not hold | **FIX_IN_STAGE_14** | permission-aware actions (§15) |
| KI-S13-22 | Payout perf cleanup vs the worker | DEFER_WITH_RATIONALE | already fixed in the harness |
