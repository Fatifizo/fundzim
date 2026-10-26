# Remediation register — current status after Phase 4 (2026-10-26)

This register supersedes the Phase 3 register for the rows below. Every other row in `docs/remediation/phase-2/remediation-register.md` is
unchanged. Nothing is closed by documentation. No owner, legal, accounting, provider, security or operational approval exists
in the repository, and no formal acceptance is recorded.

## 1. Findings

| ID | Severity | Status after Phase 4 | Evidence | Still required |
|---|---|---|---|---|
| KI-R1-07 | LOW (test race; latent product-safety assertion) | **FIXED on the branch.** The test is deterministic: a barrier with proven interception, 20/20 repeats against the live worker, full suite green | test-evidence.md §1, §3 | Every subsequent CI run green on this test; review and integration |
| F-R4-01 (Phase 3's "checkpoint forward-move design limit") | MEDIUM (coverage claim not provable) | **FIXED on the branch** (preventive DB control and detective verifier) | checkpoint-integrity-design.md; test-evidence.md | Review and integration; the residual limit below |
| F-R4-01 residual | — | **OPEN (accepted limitation proposal).** A writer that forges evidence listing real keys it never examined is undetectable by any evidence-based control | design §2 | Role separation and review (B-07, B-13); tech lead and owner acknowledgement |
| Evidence retention | — | **OPEN.** The evidence tables grow (≈40 MB per detector per day at 100 000 records per 2.5 h cycle; arithmetic). No retention is set | performance.md §3 | LR-012 decision (counsel and owner) before production volumes |
| Verification cost at very large cycles | LOW | OPEN (documented). The status becomes INCOMPLETE, never falsely VERIFIED; incremental verification is the documented alternative | performance.md §3 | D-05 volumes decide whether it is needed |
| F-R2-01, F-R2-02, F-R2-03, F-R3-01 (Phase 3) | — | FIXED (pending review and integration); regressions re-run green | test-evidence.md §4 | Review and integration |

## 2. Launch blockers affected

| # | Blocker | Status | Still required |
|---|---|---|---|
| B-05 | Reconciliation coverage | **IN_PROGRESS.** Engineering (Phases 1, 3 and 4) is on the branch with tests in isolated environments | Independent review (D-01a), protected integration, green CI on the integrated commit, D-06 coverage freshness, D-10 alert destination and thresholds, D-05 volumes, LR-012 evidence retention |
| B-12 | Alerting and on-call | OPEN. In-app alerts only (now including RECONCILIATION_SWEEP_INTEGRITY, CRITICAL) | D-10 |
| B-13 | Release governance | OPEN. `main` unprotected, no CODEOWNERS, no baseline (verified 2026-10-26) | D-01, D-01a, D-12 |
| All others (B-01 … B-04, B-06 … B-11, B-14 … B-17) | — | unchanged | see the Phase 2 register |

## 3. Owner decisions (no decision below has been taken: there is no authoritative record in the repository)

| # | Decision | Verified state | New input from Phase 4 |
|---|---|---|---|
| D-01 | Protection of `main` and the integration policy | NOT DECIDED. API: `protected: false`, rulesets `[]`, rules `[]` | Integration stays **blocked** |
| D-01a | Second reviewer | NOT DECIDED. No CODEOWNERS file; collaborators not visible without a token | Needed for the reconciliation and migration review |
| D-05 | Financial and reconciliation volumes | NOT DECIDED | Determines evidence storage and verification budget |
| D-06 | Full-sweep completion time and freshness | NOT DECIDED | Also sets the stall, slow and deferral-age thresholds (engineering defaults 1 h / 24 h / 24 h) |
| D-10 | Sweep-health thresholds and an alert destination | NOT DECIDED | INTEGRITY_FAILURE alerts are CRITICAL in-app only |
| D-12 | Artifact signing | NOT DECIDED | — |
| D-13 | Formal stage acceptance | NOT DECIDED. Register: 21 rows NOT_RECORDED, unchanged since Stage 20 | — |
| LR-012 | Retention, now including the integrity evidence | NOT DECIDED | — |
| Development database | Applying `20261026120000` and `20261027090000` | **Not authorised; not applied** (verified: highest version `20261025090000`) | — |
