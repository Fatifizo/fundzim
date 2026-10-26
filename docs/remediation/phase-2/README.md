# Remediation phase 2 — release governance and controlled repository integration

Non-production remediation; **not a product stage**. Branch `remediation/release-governance` from `c8fb8c6`. Nothing merged,
nothing deployed, no repository setting changed, every financial gate stays off, launch **not** authorised. Stage 0–20 and
Phase 1/2 acceptance: NOT_RECORDED.

| Document | Content (brief phase) |
|---|---|
| [preflight.md](preflight.md) | repository, branches, CI runs and SHAs, protection, dev stack at start (A) |
| [branch-protection.md](branch-protection.md) | current rules (none), proposed minimum effective ruleset, exact UI/API steps, verification, decisions — NOT APPLIED (B) |
| [integration-plan.md](integration-plan.md) | dependency map (linear history, migration ordering) and controlled integration plan (C) |
| [reconciliation-review.md](reconciliation-review.md) | independent review of the Phase 1 sweeps; findings F-R2-01 … F-R2-03 (D) |
| [release-candidate-procedure.md](release-candidate-procedure.md) | built / verified / authorised; RC manifest; signing and attestation design; rollback (E) |
| [ci-governance.md](ci-governance.md) | triggers, merge-commit checks, required-check drift guard, `main` never cancelled (F) |
| [dev-environment-isolation.md](dev-environment-isolation.md) | dev state, per-project image tags, residual risks (G) |
| [remediation-register.md](remediation-register.md) | current blockers and owner decisions (H) |
| [test-evidence.md](test-evidence.md) | every check run and its result (I) |
| [phase-2-final-report.md](phase-2-final-report.md) | final report and recommendation |
| [../../stage-handover/REMEDIATION-PHASE-2-TO-PHASE-3.md](../../stage-handover/REMEDIATION-PHASE-2-TO-PHASE-3.md) | handover |

Evidence: [evidence/](evidence/) with `SHA256SUMS`.

Code and config changes: `efa7b62` (compose per-project image tags, deploytest guards, `main` CI never cancelled,
`scripts/release/rc-manifest.py`). Documentation: the Phase 2 docs commit.
