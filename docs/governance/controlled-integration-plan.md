# Controlled integration plan (governance gate)

**Integration status: BLOCKED.** `main` is unprotected (D-01), there is no independent reviewer (D-01a), the integration target
is not approved, and none of the work is accepted (D-13). This plan prepares the integration; it executes nothing.

It updates `docs/remediation/phase-2/integration-plan.md` with the verified state of 2026-10-27, refreshed after remediation
Phase 6. **The live status of every gate is kept in `integration-checklist.md`**; the review scope is in
`review-scope-phases-1-6.md`.

## 1. Facts from the git graph (verified)

- One linear history: `main` (`a552de2`) → Stages 1–20 (91 commits) → remediation Phases 1–6 (24 commits): 115 commits to
  the candidate `dfaa53b`, 0 merges.
- Every stage and remediation branch is an ancestor of the current head; no branch has commits that the head lacks.
- No branch duplicates or supersedes another. Each later branch **contains** the earlier ones; it does not re-implement them.
- **Containment is not review.** That a branch contains another's commits says nothing about whether those commits were
  reviewed or accepted. None was independently reviewed, and none is accepted.
- **Migrations:**
  - 53 files with unique, strictly increasing timestamps (`20261008120000` … `20261028090000`); every stage's first
    migration is newer than all earlier ones.
  - Three earlier migrations were edited inside their own stage (`2494c4b`, `558825d`, `3ea9738`), only idempotency or Down;
    see phase-2 integration-plan.md §1.
  - The remediation migrations `20261025090000`, `20261026120000`, `20261027090000` and `20261028090000` were each written
    in a single commit. `20261028090000`'s Down was corrected in the next commit (`b9496c3`) while the migration had been
    applied only to disposable databases (Phase 6, F-R6-01). No migration applied to a retained database was edited.
- **Environment state:**
  - the shared development database is at `20261025090000`;
  - it **must not** receive `20261026120000`, `20261027090000` or `20261028090000` without the code that uses them, all
    three together (packet D4/D5; `docs/remediation/phase-6/migration-compatibility.md` §3), after a backup, and only with
    the owner's authorisation.

## 2. Gates, all required before any merge

| # | Requirement | Current state |
|---|---|---|
| 1 | Approved integration target (`main`) and candidate head SHA | not approved |
| 2 | Active branch protection, verified | NOT ACTIVE |
| 3 | An independent reviewer | none |
| 4 | Reviewed commit range: `a552de2..<candidate>`, with financial paths reviewed per `independent-review-process.md` §2 | not reviewed; scope decision D-15 open (`review-scope-phases-1-6.md` §1) |
| 5 | The 8 required checks green on the PR merge ref and on the head | head `dfaa53b`: 8/8 (run 40476468316); merge ref not run (no PR) |
| 6 | Reviewed migration sequence (all 53; focus on the 4 remediation ones) | packet D4/D5: NOT REVIEWED |
| 7 | Regression tests passing on the candidate | full integration 255/0/8 on `b9496c3` (disposable `fundzim-r10`); CI on `dfaa53b` |
| 8 | Reconciliation completeness at relevant volumes | ledger 20 000 and 100 000 journals (Phase 4); payouts and fees small-volume only |
| 9 | Financial invariants and concurrency tests | `TestFinancial*` and the families, green on `f7d1cf7` |
| 10 | Backup and restore validation for the integration | Stage 18 drills on development; **to repeat** before migrating any long-lived database |
| 11 | Release-candidate manifest and commit/CI traceability | `rc-manifest.py` `ELIGIBLE_FOR_REVIEW` for `dfaa53b` (run 40476468316); unsigned, not authorised |
| 12 | Explicit owner authorisation of the merge | none |
| 13 | A separate authorisation before any deployment or live financial operation | none (launch BLOCKED; all financial gates off) |

## 3. Proposed sequence (once gates 1–4 and 12 are met)

1. The owner fixes the candidate (the head of `remediation/integrity-status`, or a later reviewed head) and records it.
2. Open **one** pull request, candidate → `main`.
   - The history is linear and `main` has no commits of its own, so the merge is conflict-free and equivalent to a
     fast-forward.
   - A reviewer may require **per-stage PRs instead** (stage-1 … stage-20, then each remediation phase, in order): smaller
     review units, with the same final tree. Choosing between them is part of the owner's authorisation.
3. CI on the merge ref: 8/8.
4. The independent review, as records per `independent-review-process.md`; findings fixed in new commits, which dismiss stale
   approvals and re-run CI.
5. The owner authorises; the merge is done with a **merge commit**.
6. Push CI on the merge commit on `main` (never cancelled): 8/8.
7. `scripts/release/rc-manifest.py <merge-sha>` from a clean checkout: `ELIGIBLE_FOR_REVIEW`. Archive the manifest and the CI
   artifact.
8. The owner records acceptance decisions (D-13) and, if wanted, an annotated baseline tag. Signing per D-12.
9. **Separately:** the decision to migrate the shared development database (packet D4 prerequisites: backup, code and
   migrations together, never Down).

## 4. Migration conflicts

New work must branch from the integrated `main`. Before merging:
- every new migration's timestamp must be newer than `main`'s newest;
- goose out-of-order mode stays off;
- an applied migration is never edited;
- a conflict on a migration file is resolved by a new migration, never by rewriting the old one.

## 5. Rollback and containment

- **Before deployment:** a merge is a ref move. Undo it with a revert PR under the same controls; never force-push `main`.
- **Databases:** forward-only. A defective migration is corrected by a new migration. Down is development only and destroys
  the evidence tables (packet D4).
- **Development environment:** preserved by the per-project image tags and by stopping it without `-v` during disposable runs.
  A merge changes nothing there until someone rebuilds and migrates it deliberately.

## 6. What this plan does not authorise

- Merging, tagging, releasing, deploying, or migrating the shared development database.
- Enabling any payment, payout or provider instruction.
- Production.
