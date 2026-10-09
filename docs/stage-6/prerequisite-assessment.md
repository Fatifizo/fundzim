# Stage 6 prerequisite assessment

Date: 2026-10-09. Branch `stage-6/campaigns`, created from `stage-5/verification` at `d4b4734`. Inputs: the
Stage 5 completion report, `docs/stage-handover/STAGE-5-TO-STAGE-6.md`, and `docs/stage-5/{implementation, known-issues,
security-review, testing}.md`. The code was inspected before any change.

Classification: **SATISFIED**, **SATISFIED_WITH_CONDITIONS**, **BLOCKED**, **NOT_VERIFIED**. No acceptance
record was fabricated.

## 1. Process prerequisites

| Prerequisite | Status | Evidence / condition |
|---|---|---|
| Stage 1–5 acceptance recorded by the owner | **NOT_VERIFIED** | No acceptance record exists in the repository or the conversation; Stage 6 was started at the owner's explicit request (the Stage 6 master prompt). Carried as KI. |
| Working tree clean at start | SATISFIED | `git status` was clean at `d4b4734` |
| Remote CI | **SATISFIED_WITH_CONDITIONS** (was BLOCKED) | Root cause found: the workflow filter `branches: [main, "stage-*"]` never matched `stage-5/verification` (in GitHub's filter syntax `*` does not match `/`), and `main` has no workflow file, so no run was ever triggered. Fixed to `stage-**`. The first real run (#37920418624) passed: integration (with ClamAV), Playwright, container builds, secret scanning and OpenAPI. It failed web typecheck (Next 16 route types need `next typegen` before `tsc` on a clean checkout), the `-race` unit run (a real data race in a notifications test helper, now fixed) and govulncheck (setup-go used `go 1.27.1`; `go.mod` now says `go 1.27.2`). Final CI status: see the completion report. |

## 2. Stage 5 functional prerequisites

| Prerequisite | Status | Notes |
|---|---|---|
| Authentication, sessions, CSRF, step-up, RBAC (Stage 4) | SATISFIED | Reused unchanged; permission policies enforce step-up for permissions that require it |
| Organisation memberships and roles | SATISFIED | `organisations.MemberRole`, `IsAdmin`, `OrganisationsOf` |
| KYC levels (`kyc.Service.Level`) | SATISFIED | Gate on the kyc schema, never the `app.users` mirror |
| BASIC_VERIFIED (KI-S5-17) | **SATISFIED_WITH_CONDITIONS** after remediation | Never granted in Stage 5; Stage 6 adds age attestation and grants BASIC from email + phone + attestation + active account (ADR-037). Condition: the device/IP-risk input of kyc-architecture.md is dropped until a risk signal exists (KI) |
| KYB levels and representative authority | SATISFIED_WITH_CONDITIONS | `ORG_KYB_VERIFIED` and `kyc.representative_authorities` exist; Stage 6 adds `kyc.RepresentativeHas`. `ORG_REGISTERED_VERIFIED`/`ORG_PAYOUT_VERIFIED` are still never granted (KI-S5-16) |
| Beneficiary verification | SATISFIED | APPROVED required for non-SELF beneficiaries; a SELF beneficiary of an identity-verified owner needs no separate approval |
| PVO fundraising-authority records | **BLOCKED** (as Stage 1 designed) | Not built; campaigns record a declared `fundraising_basis` instead, and individuals raising for someone else stay blocked by `campaign.individual_for_others.enabled = false` (LR-046 – LR-048, PD-27). LEGAL_REVIEW_REQUIRED |
| Payout destinations | SATISFIED (not used) | Campaigns hold no financial state; `eligible_for_payout` stays false |
| Compliance outcomes enforced (KI-S5-21) | **SATISFIED_WITH_CONDITIONS** after remediation | Projection `compliance.subject_restrictions` plus enforcement at every gated campaign action and automatic suspension (ADR-037 §3, ADR-036 §6) |
| Staff ↔ personal account link (KI-S5-02/03) | SATISFIED_WITH_CONDITIONS after remediation | A link ceremony and `app.actor_identities()` are used by the KYC and campaign triggers. Condition: a staff member who never links is not detected (conflict declarations remain the procedural control) |
| Object storage public-media class | SATISFIED_WITH_CONDITIONS | `PUBLIC_MEDIA` bucket and purposes exist since Stage 3/5; the worker now needs the public credential for media processing |
| Malware scanning | SATISFIED_WITH_CONDITIONS | Real ClamAV in compose and CI; production scanner deployment is not done (KI-S5-07) |
| Audit, evidence, outbox, worker | SATISFIED | App-pool `audit.Record`, `outbox.Write`; worker consumers |
| Screening provider | **BLOCKED (accepted)** | `SCREENING_PROVIDER_NOT_SELECTED` is preserved in campaign policy v1; reviews proceed without screening, which is recorded |
| ZiG (ZWG) minor units | **BLOCKED (accepted)** | `minor_units_verified = false` (LR-043): ZWG goals are refused with `CURRENCY_NOT_AVAILABLE` until verified |
| OpenAPI contract | SATISFIED_WITH_CONDITIONS | Stage 5 routes are documented; `info.version` is still `1.0.0-draft.stage4` |

## 3. Conclusion

Stage 5 is ready for integration **with conditions**:

- The three remediation items (A–C of the brief) are implemented in Stage 6 before campaign creation is enabled.
- Remote CI now runs.
- The legal and provider blockers are preserved as LEGAL_REVIEW_REQUIRED / PROVIDER_CONFIRMATION_REQUIRED /
  SCREENING_PROVIDER_NOT_SELECTED, and enforced as refusals rather than simulated approvals.
