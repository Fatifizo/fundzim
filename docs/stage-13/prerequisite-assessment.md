# Stage 13 prerequisite assessment

Date: 2026-10-15. Branch `stage-13/risk`, created from `stage-12/fees` at `8c2855d` (CI run 38737272533: all 8 jobs
succeeded). Inputs read: `CLAUDE.md`, `README.md`, `docs/PRODUCT.md`, `docs/ARCHITECTURE.md`, `docs/SECURITY.md`,
`docs/ROADMAP.md`, [STAGE-12-TO-STAGE-13.md](../stage-handover/STAGE-12-TO-STAGE-13.md), the Stage 1 specifications
([aml-risk-framework.md](../compliance/aml-risk-framework.md), [transaction-monitoring.md](../compliance/transaction-monitoring.md),
[sanctions-screening.md](../compliance/sanctions-screening.md), [compliance-case-management.md](../compliance/compliance-case-management.md),
[operational-controls.md](../compliance/operational-controls.md)), the Stage 2 baseline (module and table ownership), the
code of `internal/risk` (Stage 5: signals, model v1, assessments, decisions, escalation), `internal/compliance` (cases,
resolutions, subject restrictions and their enforcement), `internal/campaigns` (review, restrictions, reports),
`internal/kyc` (verification, actor identities, `SANCTIONS_PEP_SCREEN` = N_A), `internal/payments` (intents, attempts,
state machine, provider evidence), `internal/payouts` (eligibility EC-01..EC-22, holds EC-12, screening EC-15, indicators
EC-16, approvals), `internal/fees`, `internal/auth` (sessions, security events, step-up), the outbox and workers, the
migrations, OpenAPI, the web admin and CI, the legal register and the acceptance register.

Classification: **SATISFIED**, **SATISFIED_WITH_CONDITIONS**, **BLOCKED**, **NOT_VERIFIED**.

## 1. Governance (verified against `docs/governance/stage-acceptance-register.md`)

| Item | Status | Evidence |
|---|---|---|
| Formal acceptance of Stages 1–12 | **NOT_VERIFIED** | every row `NOT_RECORDED` |
| Stage 9 | PARTIAL (report recommendation) | provider integration and certification BLOCKED (ADR-040) |
| Stages 10, 11, 12 | PASS WITH CONDITIONS (report recommendations) | TEST ledger, TEST payouts, TEST fees |
| Consequence | — | isolated engineering only; production financial execution stays BLOCKED; nothing is marked accepted |

## 2. Legal and compliance dependencies (register text abridged; the register is authoritative)

| Item | Question (abridged) | Status | Effect |
|---|---|---|---|
| LR-007 | CDD per level; thresholds that trigger EDD and donor identification | OPEN — LEGAL_REVIEW_REQUIRED | No EDD threshold is invented; rules carry configurable thresholds |
| LR-008 / LR-060 / LR-051 | Is FundZim a reporting institution (STR, records, compliance officer, tipping-off)? | OPEN — LEGAL_REVIEW_REQUIRED | No regulatory report is filed or drafted automatically; owners never see risk detail |
| LR-009 / LR-053 / LR-065 | Which sanctions lists, who is screened, when | OPEN — LEGAL_REVIEW_REQUIRED, PROVIDER_CONFIRMATION_REQUIRED | Screening foundation and TEST simulator only; production screening BLOCKED |
| LR-066 | "Freeze within 24 hours" without custody | OPEN — LEGAL_REVIEW_REQUIRED | A confirmed match escalates to compliance; no regulatory deadline is invented |
| LR-064 | PEP definition and senior-management approval | OPEN — COMPLIANCE_REVIEW_REQUIRED | No PEP logic beyond the screening status vocabulary |
| LR-054 | Beneficial-ownership verification | OPEN | Reused from Stage 5 KYB; no new BO logic |
| LR-055 | High-risk jurisdiction list | OPEN | No jurisdiction rule is seeded |
| LR-071 | Automated decision-making (CDPA s25): which risk actions are solely automated decisions with significant effects | OPEN — LEGAL_REVIEW_REQUIRED | Automated enforcement limited to temporary payout/destination holds with a case and human review (ADR-045 §5) |
| LR-012 / LR-074 / LR-034 | Retention, minimisation, data-subject rights | OPEN — LEGAL_REVIEW_REQUIRED | Risk records are never purged; no retention period is invented ([privacy.md](privacy.md)) |
| LR-011 | Cross-border processing and hosting | OPEN | A screening provider's processing location is a selection criterion |

## 3. Provider dependencies

| Item | Status |
|---|---|
| Sanctions/PEP screening provider (KI-S5-04, PD-29) | **BLOCKED** — none selected |
| Payment provider risk signals (PCR-005) | **BLOCKED** — no provider (ADR-040); no provider signal is invented |
| Device intelligence | Not used: no invasive fingerprinting (privacy basis open) |

## 4. Technical prerequisites

| Prerequisite | Status | Evidence |
|---|---|---|
| Risk signal store, limits registry, model v1, escalation | SATISFIED | Stage 5 `internal/risk` |
| Compliance cases, resolutions, subject restrictions and enforcement | SATISFIED | Stage 6 `internal/compliance` |
| Campaign moderation, reports, publication controls | SATISFIED | Stages 6–7 |
| Identity verification, linked identities, KYB | SATISFIED | Stage 5 |
| Beneficiary verification | SATISFIED | Stage 5 |
| Payment state machine, attempts, provider evidence | SATISFIED_WITH_CONDITIONS | test payments only; outbox payloads lack the donor id (added in Stage 13) |
| Payout eligibility, reservations, approvals | SATISFIED_WITH_CONDITIONS | EC-12 has no hold registry (KI-S11-04) — built in Stage 13 |
| Fee assessments, chargeback foundations | SATISFIED_WITH_CONDITIONS | no outbox events for fee changes (added in Stage 13) |
| Authentication sessions, step-up, RBAC, maker-checker | SATISFIED | Stage 4 |
| Session/login telemetry | SATISFIED_WITH_CONDITIONS | `app.security_events` is high-volume telemetry kept off the outbox by design; Stage 13 adds a sign-in outbox event with a keyed hash of the user agent only |
| Audit, outbox, workers, migrations, OpenAPI, web admin, CI | SATISFIED | Stage 12 baseline green |
| Screening | **BLOCKED** (production) | no provider; TEST simulator only |

## 5. Stage 12 remediation review (KI-S12-01 … KI-S12-21, definitions in [../stage-12/known-issues.md](../stage-12/known-issues.md))

| ID | Definition (abridged) | Classification | Rationale |
|---|---|---|---|
| KI-S12-01 | Acceptance of Stages 1–12 not recorded | EXTERNAL_BLOCKER | owner |
| KI-S12-02 | No fee model decided (PD-01, LR-028) | EXTERNAL_BLOCKER | business decision |
| KI-S12-03 | Tax treatment unknown | EXTERNAL_BLOCKER | tax review |
| KI-S12-04 | Revenue recognition TEST-only, accounting review required | EXTERNAL_BLOCKER | accounting review |
| KI-S12-05 | No provider fee is known | EXTERNAL_BLOCKER | provider selection |
| KI-S12-06 | Donor-covered charges not sent to the provider | EXTERNAL_BLOCKER | provider confirmation |
| KI-S12-07 | Fees not grossed up | EXTERNAL_BLOCKER | PCR-016 |
| KI-S12-08 | Test payments assessed, never posted | DEFER_WITH_RATIONALE | by design until production posting exists (ADR-042) |
| KI-S12-09 | Refunds/chargebacks only on fixtures | EXTERNAL_BLOCKER | provider |
| KI-S12-10 | One chargeback-fee assessment per synthetic donation | DEFER_WITH_RATIONALE | fixture-only; no real dispute source exists; no risk-control bypass |
| KI-S12-11 | Payout fee estimate not re-quoted | DEFER_WITH_RATIONALE | estimate is immutable and disclosed; no risk-control bypass (payout eligibility re-checks risk and holds at pre-submission) |
| KI-S12-12 | Reconciliation scans ≤ 5 000 assessments | DEFER_WITH_RATIONALE | TEST volumes; paging before production (Stage 17) |
| KI-S12-13 | Manual adjustment's ledger reference not enforced | DEFER_WITH_RATIONALE | adjustments never post; Stage 13 adds a FEE_MANIPULATION signal on every manual adjustment request so it is monitored |
| KI-S12-14 | One checker for fee changes | DEFER_WITH_RATIONALE | Stage 13 monitors fee policy changes as FEE_MANIPULATION signals; dual approval thresholds need operational-controls decisions |
| KI-S12-15 | Scopes permanent | DEFER_WITH_RATIONALE | accepted design |
| KI-S12-16 | Organiser view granularity | DEFER_WITH_RATIONALE | disclosure detail, not a control |
| KI-S12-17 | No automated fee-manipulation detection | **REMEDIATE_IN_STAGE_13** | fee policy lifecycle changes and manual adjustment requests emit outbox events → FEE_MANIPULATION_RISK signals available to rules |
| KI-S12-18 | Quote rate limit is a starting value | DEFER_WITH_RATIONALE | operational value |
| KI-S12-19 | Dev-database residue | DEFER_WITH_RATIONALE | test environment only |
| KI-S12-20 | Quote latency / N+1 policy list | DEFER_WITH_RATIONALE | performance; no control impact |
| KI-S12-21 | UI tier editor, draft editing, payout estimate endpoint | DEFER_WITH_RATIONALE | UI completeness |

Also remediated because it invalidates Stage 13 integration: **KI-S11-04** (no hold registry) — the central hold registry
is built and wired into EC-12.
