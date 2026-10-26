# Handover: remediation phase 2 → next remediation workstream

## Where things stand

- Branch `remediation/release-governance` (from `c8fb8c6`), **not merged**. `main` is still `a552de2` and **unprotected**: the
  protection proposal is ready, but no setting was changed.
- History is linear: every stage branch is an ancestor of the next, and the Phase 2 head contains Stage 1–20, Phase 1 and
  Phase 2. Integration is one conflict-free PR (or per-stage PRs in order). See
  `docs/remediation/phase-2/integration-plan.md`.
- CI evidence: Phase 1 head `c8fb8c6` run 40279994161 (8/8). For the Phase 2 runs, see
  `docs/remediation/phase-2/test-evidence.md`.
- Stage 0–20, Phase 1 and Phase 2 acceptance: NOT_RECORDED. Launch: NOT authorised. Every financial gate is off.
- Development stack: running from image `sha256:adfb403d…` (tag `fundzim/api:fundzim`). Its database holds migration
  `20261025090000` (owner decided: keep). From now on, images are tagged per compose project.

## Open engineering findings (reconciliation-review.md)

- **F-R2-01 (MEDIUM):** the fee MISSING_FEE dedupe key omits the category, so a second missing category in one source group
  is suppressed. Fix: add the category to the key, plus a test.
- **F-R2-02 (MEDIUM):** a payout whose provider check could not run (adapter error, timeout) counts as examined. Fix: defer
  it, and report it as provider-unverified.
- **F-R2-03 (LOW):** sweep stalls (deferral overflow, a failing record, a journal error that also skips PAYMENT_FACTS) have
  no alert.

## Inputs the next workstream needs from the owner

1. **D-01** apply protection (`branch-protection.md` §4), then verify it (§6). **D-01a** name an independent reviewer:
   without one, a required review is unsatisfiable.
2. **F-R2:** fix F-R2-01/02 before integration (recommended), or accept them as conditions.
3. **D-12:** adopt keyless attestation (`release-candidate-procedure.md` §4); tag signing; registry.
4. **D-13:** stage acceptance in order. **D-06 / D-05:** reconciliation coverage target and volumes.

## Recommended next workstream

**Reconciliation detection fixes and integration (B-05, B-13):**
1. Targeted fixes for F-R2-01 and F-R2-02, and the PAYMENT_FACTS independence part of F-R2-03, with tests, on a new branch
   from this head.
2. Once D-01 / D-01a exist: open the integration PR, get 8/8 on the merge ref and on `main`, run `rc-manifest.py`, and the
   owner tags the baseline.
3. If D-12 is approved: add the `provenance` attestation job and verify one attestation with `gh attestation verify`.

## Rules to keep (also in CLAUDE.md)

- Job names are required checks; there is one workflow only; `main` CI is never cancelled.
- Never describe a candidate beyond the state it has reached. Never sign with an ad-hoc key.
- Applied migrations are immutable. Disposable stacks use their own project name (and so their own image tag), and still stop
  the development project first, because host ports are shared.
