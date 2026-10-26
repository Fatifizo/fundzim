# Remediation phase 2 — final report

**Result: PASS WITH CONDITIONS** for the engineering phase. **Production launch: BLOCKED.** This result does not authorise
deployment, integration, release or the activation of any financial operation.

## 1. Executive summary

Phase 2 verified the repository and CI baseline and mapped the integration dependencies. It produced a protection proposal
and a controlled integration route, made the release-candidate record repeatable and bound to source, and closed the
image-isolation cause of F-R1-07.
- The history is a single linear chain, so integration is one conflict-free pull request.
- An independent review of the Phase 1 reconciliation sweeps found the coverage engine sound, but two MEDIUM
  detection-quality defects (F-R2-01, F-R2-02) and one LOW liveness gap (F-R2-03). They are documented with targeted
  corrections and are **not fixed** in this phase.
- `main` is still unprotected. Applying protection is the owner's decision (D-01), and this host has no GitHub admin
  credential.
- No merge, deployment, release, signing or acceptance took place.

## 2. Actual repository state

- Start: `remediation/reconciliation-completeness` at `c8fb8c6`, clean, equal to origin.
- `main` = `a552de2`, untouched.
- History: 101 commits, linear, no merge commits; every stage branch is an ancestor of the next.
- Phase 2 branch: `remediation/release-governance` (§12).

Details: preflight.md.

## 3. CI results and commit SHAs

| Run | Commit | Result |
|---|---|---|
| 40213732609 | `07e5965` | success 8/8 (Phase 1; no longer the latest) |
| 40279994161 | `c8fb8c6` | success 8/8 (latest Phase 1 head) |
| **40290159925** | **`efa7b62`** (Phase 2 code) | **success 8/8** |
| docs commit(s) | see §12 | reported in the completion message after the push. Not claimed here |

## 4. Branch protection status

**NOT APPLIED.** The current state, read from the API, is no protection, no rulesets and no rules.
- The proposal covers: PR required, 1 independent review, 8 required checks pinned to GitHub Actions, strict up-to-date
  branches, conversation resolution, no force push, no deletion, no bypass actors, and merge commits only.
- It includes exact UI and API steps and a verification method.
- New decision D-01a: an independent reviewer must exist, or the review requirement cannot be met.

Details: branch-protection.md.

## 5. Integration dependency map

`main` → Stage 1 → … → Stage 20 → Phase 1 → Phase 2, strictly linear.
- The 50 migrations are in timestamp order along the history, and no stage changed another stage's migration.
- Two in-stage Up edits added only `ON CONFLICT DO NOTHING`, so they are equivalent for already-migrated databases. A rule now
  forbids editing applied migrations.
- Route: a single PR (or per-stage PRs in order) with merge commits, then CI on the merge commit, then the manifest, then the
  owner's tag.

Details: integration-plan.md.

## 6. Release governance changes

| Change | Where |
|---|---|
| Release-candidate manifest bound to commit + `ci.yml` run + required checks + artifact digest; built / verified / authorised kept distinct | `scripts/release/rc-manifest.py`, release-candidate-procedure.md |
| Keyless attestation design (no ad-hoc keys) | release-candidate-procedure.md §4 (D-12) |
| `main` CI runs never cancelled | ci.yml |
| Required checks cannot drift or be satisfied by a second workflow (one workflow; job names = required checks) | `TestWorkflowRequiredChecks` |

## 7. Development environment findings

- The dev database holds all 50 migrations (newest `20261025090000`) with nothing pending; API and worker are compatible and
  ready.
- Image provenance was `commit: unknown`; it is now reported when `GIT_COMMIT` is set.
- Images are tagged per compose project. This was proven by a disposable run: the dev image IDs were identical before and
  after.
- Residual: host ports are shared, so the dev stack is stopped (no `-v`) during disposable runs.

Details: dev-environment-isolation.md.

## 8. Reconciliation review results

| Area | Result |
|---|---|
| Keyset coverage, record-then-advance compare-and-swap, interruption safety, cycle re-evaluation, concurrency | confirmed |
| Checkpoint guard | blocks rewinds, rewrites and deletes; forward jumps by owning roles are a documented design limit. My Phase 1 completion message overstated this; corrected |
| F-R2-01 (MEDIUM) | fee MISSING_FEE dedupe suppresses a second missing category |
| F-R2-02 (MEDIUM) | a payout whose provider check failed counts as covered |
| F-R2-03 (LOW) | sweep stalls have no alert |

None of the findings causes a skipped record or an incorrect financial effect.

Details: reconciliation-review.md.

## 9. Tests executed and results

| Test | Commit | Result |
|---|---|---|
| Go `-race` | `efa7b62` | 40/40 packages ok |
| Web | `efa7b62` | 578/578; lint and typecheck clean |
| Integration on disposable `fundzim-r2` | `efa7b62` | 243 pass / 0 fail / 7 opt-in skips |
| Destructive migration round trip | `efa7b62` | pass |
| Financial regressions (KI-S18-25, KI-S18-22, F-S19-01, F-S19-12) and reconciliation coverage | `efa7b62` | all pass |
| deploytest guards | `efa7b62` | pass, mutation-checked |
| Release-candidate manifest | `c8fb8c6`, `efa7b62`; `a0a687d` | `ELIGIBLE_FOR_REVIEW`; `REJECTED` for `a0a687d` (no CI run) and for a dirty tree |
| CI | `efa7b62` | 8/8 |

No integrated candidate exists, so no integrated-system result is claimed.

Details: test-evidence.md.

## 10. Outstanding owner decisions

| # | Decision |
|---|---|
| D-01 | apply branch protection |
| D-01a (new) | name an independent reviewer |
| F-R2 (new) | fix F-R2-01/02 before integration, or accept them |
| D-12 | signing / attestation and release procedure |
| D-13 | stage acceptance (0–20, Phase 1, Phase 2) |
| D-05 / D-06 | reconciliation volumes and coverage target |
| D-02 … D-11 | unchanged |
| D-14 | launch |

Recorded: F-R1-07 (keep the dev migration).

## 11. Updated launch blockers

All 17 remain open; none was closed by documentation.
- B-13 is IN_PROGRESS (proposal and tooling exist; protection is not applied).
- B-05 is back to IN_PROGRESS (F-R2-01/02).

Details: remediation-register.md.

## 12. Commits and branch details

Branch `remediation/release-governance`, pushed; **not merged**.

| Commit | Content |
|---|---|
| `efa7b62` | compose per-project image tags; deploytest image-tag and required-check guards; `main` CI never cancelled; RC manifest script |
| docs commit | `docs/remediation/phase-2/*`, evidence + SHA256SUMS, handover, CLAUDE.md |

## 13. Known limitations

- No GitHub token: rulesets, collaborators and job logs cannot be read, and nothing can be configured.
- Artifacts and logs need authentication to download. Their contents (SBOM, Trivy) were not inspected in this phase, only
  their existence, digests and the job conclusions.
- Provenance is unsigned.
- Image IDs are not reproducible across hosts (no timestamp normalisation).
- The opt-in load, crash and volume tests were not rerun (no financial code changed).
- The review agent and I read the same code; the review is independent of the authoring, not of the repository.

## 14. Engineering recommendation

**PASS WITH CONDITIONS.** The codebase is ready for controlled review and integration once these four conditions hold:
1. the owner applies and verifies protection (D-01, D-01a);
2. F-R2-01 and F-R2-02 are fixed (recommended) or explicitly accepted;
3. the integration PR shows 8/8 on the merge ref and on the `main` merge commit;
4. D-12 decides attestation before any artifact is called verified.

It is not production-ready, and production launch remains BLOCKED (B-01 … B-17).

## 15. Recommended next remediation phase

**Reconciliation detection fixes and protected integration:**
- fix F-R2-01, F-R2-02 and the PAYMENT_FACTS independence part of F-R2-03, with tests;
- then, under protection and with a named reviewer, integrate through one PR and tag the first baseline;
- if D-12 approves, add the attestation job and verify one attestation.

See `docs/stage-handover/REMEDIATION-PHASE-2-TO-PHASE-3.md`.
