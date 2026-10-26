# Handover: remediation phase 3 → next remediation workstream

## Where things stand

- Branch `remediation/reconciliation-corrections` (from `be9cfb6`), **not merged**. `main` = `a552de2`, unprotected.
- F-R2-01, F-R2-02 and F-R2-03 are fixed on the branch, with unit, engine and integration tests run in isolated disposable
  projects. A latent Stage 18 alert-scan race (F-R3-01) was found in CI and fixed. See `docs/remediation/phase-3/`.
- New migration `20261026120000` (additive) exists on the branch only. **The shared development database does not have it**
  (owner authorisation required before applying it there). The development stack runs pre-phase-3 images (`fundzim/api:fundzim`
  = `sha256:adfb403d…`). Do not `docker compose up --build` the development project from this branch without that
  authorisation: its migrate step would apply the migration.
- CI on the final code commit `f85192d`: 7/8. The failure is the pre-existing race KI-R1-07. For the final head, see the phase 3
  final report.
- Acceptance: NOT_RECORDED for everything. Launch: NOT authorised.

## Open items

1. **KI-R1-07:** `TestPayoutApprovalSeparationOfDutiesAndInvalidation` races the worker, so CI turns red at random. A
   deterministic fix needs a barrier that stops the worker submitting that one payout during the test. That is a test
   redesign; the assertion must stay unchanged.
2. **Checkpoint forward-move design limit** (coverage-guarantees.md): accept it, or add a detective control (examined
   versus in-scope keys).
3. **A record whose examination always errors** still stops its sweep. It is visible as FAILING with the error. Automatic
   deferral of erroring records would need its own design.
4. **Sweep-health thresholds** are engineering defaults (1 h stall / 24 h slow / 24 h deferral age / 3 failures). Production
   values come from D-06 and D-10.

## Inputs needed from the owner

D-01 and D-01a (protection, reviewer); D-06, D-10 and D-05; authorisation before migrating the development database; D-12 and
D-13.

## Recommended next workstream

1. **Make KI-R1-07 deterministic** (test only), so CI can be green reliably.
2. Then, once D-01 and D-01a exist: open the integration PR from this branch's head and get 8/8 on the merge ref.
3. Independent review of the Phase 1 and Phase 3 reconciliation changes and both migrations.
4. Merge with a merge commit; CI on `main`; `rc-manifest.py`; the owner tags the baseline.
