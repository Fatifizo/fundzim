# Remediation phase 3 — integration readiness (section 14)

**Not merged. Not ready for a protected pull request yet.** Branch protection does not exist (verified 2026-10-26:
`protected: false`), so no PR here can be called protected.

| Check | Result |
|---|---|
| Final diff reviewed | yes, by the author (self-review): `git diff be9cfb6..f85192d -- internal migrations tests api`. Changes are confined to fee and payout reconciliation, the sweep engine, sweep health, ops metrics, the alert scan, and one OpenAPI property. No financial gate, posting rule, ledger table, payout state machine, reservation, maker-checker or provider-polling code changed (archtest financial-gate contract passes) |
| Migration reviewed | `20261026120000_reconciliation_sweep_attempts.sql`: additive (nullable / defaulted columns, CHECKs). The guard is replaced by an identical function plus an attempt-only branch. Down restores the previous guard and drops the columns. Round trip passed on a throwaway database. **Never applied to the shared development database** (its images predate it) |
| Regression coverage | unit, engine (with mutations) and integration tests per finding; full suite 247/0/7 and the volume test on `fundzim-r5` at `f85192d` |
| CI on the final code commit `f85192d` | run 40305574851: 7/8. Integration failed on the pre-existing race KI-R1-07, unrelated to this change. `rc-manifest.py` REJECTS the commit accordingly |
| CI on the final branch head | see the phase 3 final report (documentation commit; reported after the push) |
| Independent review | **not performed**: no second reviewer exists (D-01a). The phase 2 review that found these defects was done by a separate review agent of the same tool, not a person |

**Integration plan update** (phase-2 integration-plan.md §3). The proposed candidate becomes the head of
`remediation/reconciliation-corrections`, which contains Stage 1–20 and Phases 1–3 (linear, conflict-free onto `main`). It may
be opened as a PR only after:
1. D-01 and D-01a: protection applied and verified, and a reviewer named;
2. a CI run with all 8 checks green on the PR merge ref (if KI-R1-07 recurs, its re-run is the reviewer's call, recorded with
   the run ids; it is never declared green without a successful run);
3. independent review of the reconciliation changes and the migration.

**Stop conditions in force:** do not merge without D-01, do not tag without a green `main` run, and never apply
`20261026120000` to the shared development database without the owner's authorisation.
