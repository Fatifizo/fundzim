# Remediation phase 4 — deterministic financial tests and checkpoint integrity

Non-production remediation; **not a product stage**. Branch `remediation/checkpoint-integrity` from `1e167b1`. Nothing merged,
nothing deployed, every financial gate stays off, launch **not** authorised. Acceptance of Stage 0–20 and Phases 1–4:
NOT_RECORDED. Historical reports are unchanged.

| Document | Content |
|---|---|
| [findings-and-corrections.md](findings-and-corrections.md) | KI-R1-07 root cause and deterministic correction; F-R4-01 checkpoint forward moves |
| [checkpoint-integrity-design.md](checkpoint-integrity-design.md) | write-path map, threats, preventive (DB) and detective (verifier) layers, recovery cases, retention, limits |
| [performance.md](performance.md) | 20 000 / 100 000-journal measurements, locks, scaling limits |
| [test-evidence.md](test-evidence.md) | every run, every failure and its cause, financial regressions |
| [remediation-register.md](remediation-register.md) | findings, blockers, owner decisions (verified state) |
| [integration-readiness.md](integration-readiness.md) | diff and migration review; integration stays blocked |
| [phase-4-final-report.md](phase-4-final-report.md) | final report |
| [../../adr/ADR-052-reconciliation-coverage-sweeps.md](../../adr/ADR-052-reconciliation-coverage-sweeps.md) | Amendment 2 (ADR still Proposed) |
| [../../stage-handover/REMEDIATION-PHASE-4-TO-PHASE-5.md](../../stage-handover/REMEDIATION-PHASE-4-TO-PHASE-5.md) | handover |

Commits: `30eb045` (code, migration `20261027090000`, tests), `e4f8c24` and `f7d1cf7` (tests), then the documentation commit.
Evidence: [evidence/](evidence/) with `SHA256SUMS`.
