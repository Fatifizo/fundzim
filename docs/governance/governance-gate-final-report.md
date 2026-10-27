# Governance and integration-readiness gate — final report (remediation phase 5)

**GOVERNANCE DOCUMENTATION: COMPLETE.** **CONTROLLED INTEGRATION: BLOCKED.** **PRODUCTION LAUNCH: BLOCKED.**

## 1. Scope and identifiers

| Item | Value |
|---|---|
| Phase | Remediation phase 5: repository governance, independent-review preparation, controlled-integration preparation |
| Starting commit | `854e824` (`remediation/checkpoint-integrity`, clean, equal to origin) |
| Branch | `remediation/governance-gate` (new) |
| Commits | `78b9908` (CI: Playwright install split, failure artifacts); the documentation commit (see §13) |
| Push | the branch is pushed to origin. `main` was not touched |
| Working tree at the end | clean after the documentation commit |

## 2. Files created or changed

- `docs/governance/`:
  - repository-baseline.md
  - main-branch-protection.md
  - independent-review-process.md (including the review-record template)
  - reconciliation-integrity-review-packet.md
  - owner-decision-register.md
  - controlled-integration-plan.md
  - ci-reliability.md
  - remediation-register.md
  - this report
- `docs/stage-handover/REMEDIATION-PHASE-5-TO-PHASE-6.md`
- `CLAUDE.md` (current-stage block)
- `.github/workflows/ci.yml` (Work Package E)

## 3. GitHub governance, as verified

Via the unauthenticated public API, 2026-10-27:
- `main` `protected: false`; rulesets `[]`; branch rules `[]`;
- no pull request ever opened;
- no CODEOWNERS.

Collaborators, admin bypass and plan features are **not verified** (no token; `gh` not installed). No setting was changed.

## 4. Independent review

**NOT REVIEWED / BLOCKED.**
- No independent reviewer is named (D-01a).
- The process, checklist, record template and a complete review packet exist.
- Claude Code's notes in the packet (§6) are the author's own and are labelled as such.

## 5. Owner decisions

All **OPEN**: D-01, D-01a, D-05, D-06, D-10, D-12, D-13, LR-012. None was resolved; there is no authoritative record. The
register gives, for each, what closes it and who decides.

## 6. CI and tests

| Check | Commit | Result |
|---|---|---|
| CI run 40461811832 | `78b9908` | **success 8/8**; web e2e 512 s (install 13 s + 10 s) |
| CI run 40358498063 | `854e824` (start) | success 8/8 |
| CI run 40353861155 | `f7d1cf7` | cancelled (timeout during install) |
| `go test ./internal/deploytest/` | `78b9908` | pass |
| Workflow YAML parse; Playwright 1.63.0 command dry runs | `78b9908` | ok |
| `rc-manifest.py` | `854e824` / `f7d1cf7` | ELIGIBLE_FOR_REVIEW (run 40358498063) / REJECTED (cancelled check), as specified |

No application code changed in this phase, so the Phase 4 test evidence on `f7d1cf7` stands for the code: full integration
253/0/7, the 20× KI-R1-07 repeat, the volume harness. No integration suite was re-run locally (no code change); CI's
Integration job on `78b9908` passed.

## 7. Migrations and database safety

- The shared development database is at `20261025090000` (read-only check). The evidence tables and attempt columns are
  absent.
- `20261026120000` and `20261027090000` were **not applied**.
- Nothing was reset, reseeded or migrated.
- No disposable stack was needed in this phase.

## 8. New findings

| Id | Severity | Finding |
|---|---|---|
| F-R5-01 | LOW–MEDIUM | verifier failure leaves a stale integrity status |
| F-R5-02 | LOW | INCOMPLETE and UNVERIFIABLE not surfaced by Health |
| F-R5-03 | MEDIUM | deployment ordering hazard of `20261027090000` with old code |
| F-R5-04 | LOW | Phase 4 storage estimate overstated; corrected |
| F-R5-05 | LOW | CI install slowdown; mitigated for diagnosis |

See `remediation-register.md`.

## 9. Remaining risks and limitations

- The **residual trust-boundary limitation** of checkpoint evidence (packet D3) is unchanged.
- Nothing has been independently reviewed.
- Evidence storage grows without a retention decision.
- The CI install slowdown is external and may recur.
- Payout and fee sweeps have not been measured at load.

## 10. Gates

| Area | Status | Evidence |
|---|---|---|
| Repository baseline | **PASS** | repository-baseline.md (git, API, database checks) |
| Branch protection | **BLOCKED** | API: `protected: false`, no rulesets; specification only |
| Independent reviewer | **BLOCKED** | none named; independent-review-process.md |
| Phase 3/4 integrity review | **NOT REVIEWED** | reconciliation-integrity-review-packet.md |
| Migration readiness | **PASS WITH CONDITIONS** for the documented review inputs; **BLOCKED** for application to any shared database (independent review, D4 prerequisites, owner authorisation) | packet D4 |
| CI reliability | **PASS WITH CONDITIONS** | ci-reliability.md; 8/8 on `78b9908`; watch the next runs |
| Owner decisions | **OPEN** | owner-decision-register.md |
| Controlled integration | **BLOCKED** | gates B and C not met; target not approved |
| Production launch | **BLOCKED** | Stage 20 blockers B-01 … B-17 open; no authorisation |

## 11. Confirmations

- No merge, pull request, tag, release or deployment.
- No change to `main`, repository settings or access.
- No migration was applied to the shared database.
- No real-money operation was enabled.
- No test, assertion or safeguard was weakened; no timeout was extended.
- No acceptance was recorded or claimed.

## 12. Next actions

**Owner or administrator (external):**
1. D-01a: name an independent reviewer (not the account `Fatifizo`) and give them access.
2. D-01: apply `main-branch-protection.md` §5 and verify it with §7.
3. Have the reviewer complete `reconciliation-integrity-review-packet.md` with the record template.
4. Decide D-05, D-06, D-10 and D-12, and commission LR-012 with counsel.

**Repository work (once the reviewer exists):**
1. Fix F-R5-01 and F-R5-02 under review.
2. Open the integration PR per `controlled-integration-plan.md` §3.
3. Watch the browser-job timings.

## 13. Documentation commit and CI

Reported in the completion message after the push. Not claimed here.
