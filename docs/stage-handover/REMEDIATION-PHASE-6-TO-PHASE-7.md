# Handover: remediation phase 6 (integrity status) → next workstream

## State

- **Branch.** `remediation/integrity-status` (from `dfa91b6`), pushed, **not merged**. The final code commit is `b9496c3`
  (CI run 40472554403, success 8/8).
- **The governance gate is BLOCKED.**
  - `main` is `a552de2`, unprotected: no rulesets, no rules, no PRs, no CODEOWNERS (verified 2026-10-27).
  - No independent reviewer exists.
- **F-R5-01 and F-R5-02 are fixed on the branch** (integrity status, metric, alert, portal; migration `20261028090000`;
  ADR-052 Amendment 3). They are **not reviewed**.
- **F-R5-03** is analysed and tested (`docs/remediation/phase-6/migration-compatibility.md`). The rollout control is still
  procedural.
- **Development database.** It is at `20261025090000` and runs `07e5965` images. `20261026120000`, `20261027090000` and
  `20261028090000` were **not applied**. They must be deployed together with the code (migration first, services
  stopped), after a backup, and only with authorisation.
- **Status.** Integration: **BLOCKED**. Launch: **BLOCKED**. Acceptance: NOT_RECORDED everywhere.

## Inputs needed

| Input | From | Needed for |
|---|---|---|
| D-01a: a named independent reviewer | owner | review, integration |
| D-01: protection applied and verified | administrator | integration |
| D-06: integrity freshness target (replaces the 1 h engineering default) | owner | launch at volume |
| D-10: alert routing | owner, security | launch |
| D-05, D-12, LR-012, D-13 | owner, counsel | launch, acceptance |

## Recommended next workstream

1. Independent review of Phases 1–6 with the governance packet and `docs/remediation/phase-6/`.
2. Fix review findings as new commits. Never rebase or squash shared branches.
3. Integrate under protection (`docs/governance/controlled-integration-plan.md`). Deploy the three remediation migrations
   with their code (`migration-compatibility.md` §3).

Without D-01 and D-01a, no engineering work changes the integration status.

## Rules added in this phase

- A verification failure is recorded, never left as a stale success.
- `integrity_verified_at` is the last success.
- Results are ordered by the check's start.
- The integrity metric is never HEALTHY.
- The staleness threshold is an engineering default until D-06.
- Tests that need several schema levels use a scratch `fundzim_perf_compat_*` database (`FUNDZIM_IT_MIGRATION_COMPAT=1`),
  never the shared one.
