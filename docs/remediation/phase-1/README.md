# Remediation phase 1 — repository integration, governance and reconciliation completeness

Non-production remediation; **not a product stage**. Branch `remediation/reconciliation-completeness` from `6a65d70`
(Stage 20); nothing merged into `main`; nothing deployed; every financial gate stays off. Launch is **not** authorised.
Stage 0–20 acceptance is still NOT_RECORDED. The Stage 20 report and registers are unchanged.

| Document | Content (brief phase) |
|---|---|
| [preflight.md](preflight.md) | repository, CI, branch protection, governance and environment baseline (A) |
| [integration-plan.md](integration-plan.md) | proposed integration route and branch-protection settings — owner applies (B) |
| [reconciliation-coverage.md](reconciliation-coverage.md) | every reconciliation path, root causes, coverage model, invariants C1–C8, checkpoint/recovery, concurrency (C, D) |
| [../../adr/ADR-052-reconciliation-coverage-sweeps.md](../../adr/ADR-052-reconciliation-coverage-sweeps.md) | decision record (Proposed) |
| [test-evidence.md](test-evidence.md) | the 25 cases → tests; every run and its result, including failures (E, F, H) |
| [performance.md](performance.md) | measured cost of the sweeps; no capacity target invented (G) |
| [known-issues.md](known-issues.md) | findings F-R1-01 … F-R1-05, known issues KI-R1-01 … KI-R1-09 |
| [remediation-register.md](remediation-register.md) | current status of every launch workstream (J) |
| [phase-1-final-report.md](phase-1-final-report.md) | final report and result |
| [../../stage-handover/REMEDIATION-PHASE-1-TO-PHASE-2.md](../../stage-handover/REMEDIATION-PHASE-1-TO-PHASE-2.md) | handover |

## Evidence

| Commit | Content |
|---|---|
| `97b8a70` | sweep engine, migration `20261025090000`, payouts/fees/ledger sweeps, OpenAPI, tests, ADR-052 code |
| `8baee4e` | discovery typo test fix (KI-R1-08) |
| `a0a687d` | keyset reads order by the uuid key (F-R1-06); ledger volume test deadlines scale with the run (KI-R1-09) — final code candidate |
| docs commit (this README) | phase documents, preserved evidence, CLAUDE.md block |

The logs are in [evidence/](evidence/), with hashes in `evidence/SHA256SUMS`. Each log names its commit in its first line, or in
test-evidence.md §2 where it does not. Database runs used only the disposable compose project `fundzim-r1` (fresh volumes) and
the scratch databases `fundzim_perf_r1` / `fundzim_perf_r1b` inside it. The development project `fundzim` was stopped
without `-v`; its data was not touched.
