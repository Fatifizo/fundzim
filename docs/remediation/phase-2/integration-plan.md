# Remediation phase 2 — integration dependency map and controlled integration plan (phase C)

**Status: PROPOSED. Nothing was merged, rebased, force-pushed or tagged.** This plan supersedes
`docs/remediation/phase-1/integration-plan.md` (kept as history). Every step marked *owner* needs the owner's decision.

## 1. Dependency map (verified 2026-10-26 from `git fetch --all`; all remote and local heads identical)

The repository has one strictly linear history: **101 commits with no merge commits**. Every stage branch is an ancestor of
the next one, and every branch is an ancestor of the Phase 2 head.

| # | Branch | Head | Commits over the previous branch | Migrations added (timestamps) | Earlier migration files changed |
|---|---|---|---|---|---|
| 0 | `main` | `a552de2` | — | 0 | — |
| 1 | `stage-1/regulatory-architecture` | `aa9f9b3` | 1 | 0 | 0 |
| 2 | `stage-2/system-architecture` | `fe9cfa7` | 1 | 0 | 0 |
| 3 | `stage-3/core-platform` | `17c9d88` | 6 | 4 (`20261008120000`…`120300`) | 0 |
| 4 | `stage-4/identity-access` | `1adbb1e` | 6 | 9 (`20261008130000`…`140600`) | 0 |
| 5 | `stage-5/verification` | `d4b4734` | 6 | 7 (`20261009150000`…`150600`) | 0 |
| 6 | `stage-6/campaigns` | `ca898ae` | 10 | 6 (`20261009170000`…`171200`) | 0 |
| 7 | `stage-7/public-experience` | `e355968` | 8 | 3 (`20261010090000`…`090200`) | 0 |
| 8 | `stage-8/payments` | `e5490cf` | 13 | 2 (`20261012090000`…`090100`) | 0 |
| 9 | `stage-9/provider-integrations` | `e57cbfc` | 1 | 1 (`20261013090000`) | 0 |
| 10 | `stage-10/ledger` | `dbaf500` | 5 | 2 (`20261013120000`…`120100`) | 0 |
| 11 | `stage-11/payouts` | `491e0be` | 5 | 1 (`20261014090000`) | 0 |
| 12 | `stage-12/fees` | `8c2855d` | 1 | 1 (`20261015090000`) | 0 |
| 13 | `stage-13/risk` | `df0e43e` | 2 | 1 (`20261016090000`) | 0 |
| 14 | `stage-14/operations` | `6af7229` | 2 | 2 (`20261017090000`…`090100`) | 0 |
| 15 | `stage-15/notifications` | `558825d` | 5 | 3 (`20261018090000`…`20261019090100`) | 0 |
| 16 | `stage-16/discovery` | `c80c39c` | 3 | 2 (`20261020090000`…`090100`) | 0 |
| 17 | `stage-17/reconciliation` | `f5e9d00` | 5 | 2 (`20261022090000`…`090100`) | 0 |
| 18 | `stage-18/security-hardening` | `801babe` | 8 | 1 (`20261023090000`) | 0 |
| 19 | `stage-19/financial-concurrency` | `2015978` | 2 | 2 (`20261024090000`…`090100`) | 0 |
| 20 | `stage-20/production-certification` | `6a65d70` | 1 | 0 | 0 |
| R1 | `remediation/reconciliation-completeness` | `c8fb8c6` | 9 | 1 (`20261025090000`) | 0 |
| R2 | `remediation/release-governance` | this phase | see the final report | 0 | 0 |

Answers to the questions in the brief:

- **Authoritative branch per stage.** Each stage's changes are on its own branch and are carried unchanged by every later
  branch. The head of the newest branch contains everything, and no stage's work exists anywhere else.
- **Do later branches include earlier ones?** Yes, all of them (ancestry checked pairwise and against the head).
- **Duplicated or superseded changes.** No cherry-picks or duplicates exist, because the history is linear. Within a stage,
  later commits fix earlier ones, as in normal development. One stage branch is never "partly superseded" by another.
- **Migration ordering.** The 50 migrations have strictly increasing timestamps along the history. Each stage's first
  migration is newer than every migration of all earlier stages, so goose never sees an out-of-order file. No stage changed
  another stage's migration file.
  - Three commits changed a migration **within its own stage**: `2494c4b` (Stage 11), `558825d` (Stage 15) and `3ea9738`
    (Stage 17). `558825d` touches only Down. The Up hunks of the other two only append `ON CONFLICT (id) DO NOTHING` to seed
    inserts. A database migrated with the earlier text therefore has the same rows and schema.
  - goose does not checksum applied files, so rule §4.4 forbids this pattern from now on for migrations that may have reached
    a shared or long-lived database.
- **API and web compatibility.** There is one OpenAPI contract (`api/openapi/fundzim-v1.yaml`) and one web app in the same
  tree, so they cannot diverge across branches. CI lints the contract and runs the mock and real-stack Playwright suites on
  every commit it tests.
- **Is `main` a safe base?** Yes. `main` = `a552de2` has no commits of its own after Stage 0, so integrating the head is
  conflict-free and equivalent to a fast-forward.
- **Is a staged integration branch needed?** No, because no branch needs combining. A separate integration branch would only
  add a commit that no CI run has proven.

## 2. Proposed integration route

**Base:** `main` (`a552de2`). **Candidate:** the head of `remediation/release-governance`. It contains Stage 1–20, Phase 1 and
Phase 2. The exact SHA is fixed only when the owner chooses it, after review, and it must have a green 8/8 CI run of its own.

1. *owner* — apply branch protection (branch-protection.md) and settle D-01a (the independent reviewer). Do this **before**
   any merge, so the integration itself goes through the controls.
2. Open one pull request, `remediation/release-governance` → `main`.
   - The PR CI runs on the merge ref (`refs/pull/N/merge`). Because `main` is an ancestor of the head, that tree is the
     candidate tree.
   - All 8 required checks must pass on that run.
3. Review it (§5). A reviewer may ask for the PR to be split so each stage can be reviewed on its own merits. The order is
   then fixed by the dependency map: one PR per stage branch, in order 1 → 20 → R1 → R2. Each must be green and merged before
   the next is opened. The resulting tree is identical either way.
4. Merge with a **merge commit** (never squash or rebase): every stage commit, and the CI run that names it, stays
   addressable.
5. Wait for the push CI run on the merge commit on `main`. `main` runs are never cancelled (ci-governance.md §3). The release
   baseline is that run, not the PR run.
6. *owner* — after D-13 for the stages being baselined: run `scripts/release/rc-manifest.py <merge-sha>` and archive the
   manifest. Then create an annotated tag `baseline-YYYY-MM-DD-rN` on the merge commit, signed if D-12 chooses it, and record
   tag, SHA, CI run and manifest digest in the acceptance register.
7. Stage branches are kept. Deleting them is the owner's choice after the baseline exists.

## 3. Candidate commits

| Candidate | SHA | CI evidence | Status |
|---|---|---|---|
| Stage 20 certified head | `6a65d70` | run 40179378365 (Stage 20 record) | superseded by R1/R2 (lacks the KI-S19-09 fix) |
| Phase 1 head | `c8fb8c6` | run **40279994161**, 8/8 success (`evidence/rc-manifest-c8fb8c6.json`) | eligible for review; lacks Phase 2 governance changes |
| Phase 2 head | see final report | see final report | proposed integration candidate |

## 4. Rules

1. **Conflict resolution.** None is expected. If `main` gains a commit first, the author rebases the stack onto the new `main`
   in a **new** branch: never force-push a shared stage branch, and never resolve a conflict in a migration file by editing an
   applied migration. CI must then pass again on the new head, and §4.3 must be re-checked.
2. **No integration commit without CI.** A merge result that no CI run has seen is never tagged or deployed.
3. **Migration ordering.** A new migration's timestamp must be newer than every migration on `main`. goose's out-of-order mode
   stays off.
4. **Applied migrations are immutable.** Once a migration may have run on any shared or long-lived database (the development
   database counts), change it only through a new migration. The release manifest records the `migrations` tree hash, so a
   changed file shows up as a different tree for the same set of versions.
5. **Stop conditions** (§8) override every step.

## 5. Review and checks required

- The 8 required checks on the PR merge ref, then the same 8 on the merge commit's push run.
- Independent review (D-01a). For financial paths (`internal/{ledger,payments,payouts,fees,reconciliation}`, `migrations/`,
  `.github/`), the reviewer confirms in particular:
  - no financial gate constant or gate CHECK changed (`TestSecurityFinancialGatesInDatabase`,
    `internal/archtest/financial_gates_test.go`);
  - no posted-ledger or append-only guard is weakened;
  - the open reconciliation findings F-R2-01…03 (reconciliation-review.md) are either fixed in the candidate or accepted as
    known conditions.

## 6. Financial regression requirements

Each of these must pass on the candidate in CI (Integration job) or on a disposable compose project:
- Stage 19 regressions: `TestFinancial*`, including KI-S18-25, KI-S18-22, F-S19-01 and F-S19-12;
- payout and reservation tests (`TestPayout*`);
- ledger tests (`TestLedger*`);
- fee tests (`TestFee*`);
- `TestReconciliation*`, including the coverage, historical, interruption and checkpoint tests;
- the destructive migration round trip, on a throwaway database only.

The ledger volume tiers run only on a scratch `fundzim_perf*` database.

## 7. Rollback and recovery

- **Before production:** integration is a ref move. Recover by opening a revert PR. Never force-push `main`.
- **Database:** migrations are forward-only in every shared environment, and `fundzimctl` refuses Down outside development. A
  defective migration is fixed by a new migration.
  - Back up before `migrate up` on any long-lived database (Stage 18 backup, restore drill proven).
  - Migration `20261025090000` is purely additive. Rolling it back on the development database is out of scope without an
    approved recovery plan. The owner has decided to keep it (F-R1-07).
- **Application:** redeploy the previous release's image digests (release-candidate-procedure.md). Images are immutable by
  digest; tags are not trusted.

## 8. Release artifact provenance

See release-candidate-procedure.md: a manifest binds commit → CI run → required checks → artifact digest. Signing is not
configured (D-12), so provenance stays **UNSIGNED_NOT_VERIFIABLE** until attestation is adopted.

## 9. Owner decisions required

| # | Decision |
|---|---|
| D-01 | Apply protection; approve this route (single PR, or one PR per stage in order) |
| D-01a | Name the independent reviewer(s) |
| D-12 | Attestation and signing; tag signing; release and rollback procedure |
| D-13 | Formal stage acceptance in order (only accepted stages are baselined as accepted) |
| F-R2 | Fix the reconciliation findings F-R2-01…03 before integration (recommended), or accept them as recorded conditions |

## 10. Acceptance evidence for the integration

- the PR URL and number;
- the PR CI run (8/8) on the merge ref;
- reviewer approval;
- the merge commit SHA;
- the push CI run (8/8) on `main`;
- `rc-manifest.py` output, `ELIGIBLE_FOR_REVIEW`, archived with its SHA-256;
- the annotated tag;
- the acceptance-register entry by the owner.

## 11. Explicit stop conditions

Stop and report, without merging, when:
- `main` has moved and the rebase is not trivially clean;
- any required check fails or is missing on the merge ref or on `main`;
- a migration timestamp is out of order, or an applied migration file differs;
- a financial regression fails;
- a reviewer finds a defect that could cause a duplicate or incorrect financial effect;
- protection cannot be verified with the §6 queries of branch-protection.md;
- anyone proposes merging without the owner's D-01.
