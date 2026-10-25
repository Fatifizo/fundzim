# Handover: remediation phase 1 → next remediation workstream

## Where things stand

- Branch `remediation/reconciliation-completeness` (from `6a65d70`), **not merged**; `main` is still `a552de2` and unprotected.
- KI-S19-09 engineering work is done (ADR-052): coverage sweeps for PAYOUTS, FEE_ASSESSMENTS, PAYMENT_JOURNALS and
  PAYMENT_FACTS, DB-guarded checkpoints, deferrals, ops-portal coverage age, tests. B-05 stays **open** until the owner sets the
  coverage requirement (D-06), the branch is reviewed and integrated, and CI is green on the integrated commit.
- Stage 0–20 acceptance: NOT_RECORDED. Launch: NOT authorised. Every financial gate is off.
- Current status of all launch workstreams: `docs/remediation/phase-1/remediation-register.md`.

## Open incidents and CI

- **F-R1-07:** migration `20261025090000` was applied to the shared development database during teardown (additive). The
  development stack is **stopped** until the owner decides between keeping it and restoring from a backup. Before starting the
  development stack, rebuild the images from the intended commit (`docker compose build`).
- **KI-R1-10:** branch CI failed the Web job on a pre-existing race between `requestAnimationFrame` error focus and simulated
  typing (reproduced locally). Both affected tests are fixed. 27 components share the focus pattern: if another test fails the
  same way, apply the same wait.

## Inputs the next workstream needs from the owner

1. D-01: apply branch protection (`docs/remediation/phase-1/integration-plan.md` §4) and approve the PR route.
2. D-06 / D-05: maximum acceptable reconciliation cycle duration per detector; pilot volumes. Measured costs:
   `docs/remediation/phase-1/performance.md`.
3. D-13: stage acceptance decisions (in order), including this phase.

## Recommended next workstream

**Release governance and integration (B-13, F-S20-01/02/06)** — it is the prerequisite for every other engineering closure:
once `main` is protected, merge the stack and this branch, tag a baseline with green CI, and add signing/attestation and a
release/rollback procedure (D-12). In parallel and engineering-only: KI-S19-16 (batched exception writes), the staff security
conditions (F-S18-03/04/05/12) if the owner chooses remediation over risk acceptance, and the DSR workflow once counsel answers.

## Rules to keep (also in CLAUDE.md)

- Coverage comes from the sweeps; windows guarantee nothing and read the newest records.
- Never run a detector in the `default` scope from a test or tool; never advance, rewrite or delete a checkpoint by hand.
- Never plant invariant-breaking ledger journals on the shared or disposable integration database — use a scratch
  `fundzim_perf*` database (F-R1-02).
- Load and certification runs use a disposable compose project; the development database stays contaminated (KI-S19-17)
  and untouched.
