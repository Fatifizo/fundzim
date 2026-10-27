# Repository baseline — governance gate (remediation phase 5)

**Verified 2026-10-27 06:2x UTC.** Sources: local git, after `git fetch --all --prune`; the public GitHub REST API without
authentication; read-only SQL on the development database. Claude Code has no GitHub token and `gh` is not installed.
**"Not verified"** below means unavailable without a token. It does not mean "not configured".

## 1. Git

| Item | Verified value |
|---|---|
| Repository root and remote | `/home/administrator/projects/fundzim`; `origin` = `git@github-fatifizo:Fatifizo/fundzim.git` (GitHub `Fatifizo/fundzim`, public) |
| Branch at start | `remediation/checkpoint-integrity` at **`854e824`**, equal to `origin`, working tree clean, nothing untracked |
| Phase 4 commits | `30eb045` → `e4f8c24` → `f7d1cf7` (code) → `854e824` (documentation): present, in this order |
| Phase 3 and 4 handovers | `docs/stage-handover/REMEDIATION-PHASE-3-TO-PHASE-4.md`, `…-PHASE-4-TO-PHASE-5.md`: present |
| Release-candidate tooling | `scripts/release/rc-manifest.py`: present (Phase 2), behaviour re-verified (§4) |
| `main` | `a552de2` (Stage 0), equal on origin |
| Branch for this phase | `remediation/governance-gate` (new) from `854e824` |
| History | **linear**: 111 commits from `main` to this branch, **0 merge commits**. Every remote branch is an ancestor of the branch head (stage-1 … stage-20 and remediation phases 1–5) |

| Chain link | Commits over the previous link |
|---|---|
| `main` → `stage-20/production-certification` (`6a65d70`) | 91 (Stages 1–20; per-stage map in phase-2 integration-plan.md §1) |
| → `remediation/reconciliation-completeness` (`c8fb8c6`) | 9 |
| → `remediation/release-governance` (`be9cfb6`) | 2 |
| → `remediation/reconciliation-corrections` (`1e167b1`) | 4 |
| → `remediation/checkpoint-integrity` (`854e824`) | 4 |
| → `remediation/governance-gate` (this phase) | see the final report |

## 2. GitHub governance (public API)

| Query | Result | Meaning |
|---|---|---|
| `GET /repos/Fatifizo/fundzim/branches/main` | `"protected": false`, protection `enabled: false`, no required checks | **branch protection is not configured** (an unauthenticated read reports protection on public repos) |
| `GET /repos/Fatifizo/fundzim/rulesets` | `[]` | no repository rulesets |
| `GET /repos/Fatifizo/fundzim/rules/branches/main` | `[]` | no rule applies to `main` |
| `GET /repos/Fatifizo/fundzim/pulls?state=all` | `[]` | no pull request has ever been opened |
| Collaborators, teams, admin bypass settings, plan features | `Requires authentication` | **not verified** |
| CODEOWNERS | absent from the repository (`.github/` holds only `workflows/`) | no code-owner routing |

## 3. CI

| Item | Verified value |
|---|---|
| Workflow | one, `.github/workflows/ci.yml` (`ci`, id 379524151, active); triggers: push to `main`, `stage-**`, `remediation/**`; `pull_request` |
| Jobs (= required-check names) | the 8 in `main-branch-protection.md` §3, extracted from `ci.yml` (4-space `name:` keys); `TestWorkflowRequiredChecks` enforces the list |
| Phase 4 runs | 40353861155 on `f7d1cf7`: **cancelled** (web e2e exceeded 20 min; 7/8 success). 40358498063 on `854e824`: **success 8/8** |
| Browser-test timing | ci-reliability.md |

## 4. Release tooling (behaviour verified by running it)

`scripts/release/rc-manifest.py`, run from clean worktrees:

| Commit | Result |
|---|---|
| `854e824` | **ELIGIBLE_FOR_REVIEW**: run 40358498063, 8/8 checks, artifact digest recorded |
| `f7d1cf7` | **REJECTED**: "required check 'Web end-to-end (Playwright)' concluded cancelled" |

**What it records.** The source commit and tree, and a refusal of a dirty tree or an unpushed commit. Inputs: lockfile blob
ids, digest-pinned base and third-party images, the migrations tree hash, count and newest file, the workflow file's SHA-256.
The CI run (id, attempt, event, branch, conclusion) with each required check's conclusion, and the CI artifact's id, digest
and expiry. Three separate states: `built` (BUILT_BY_CI), `provenance` (always UNSIGNED_NOT_VERIFIABLE) and `authorisation`
(always NOT_AUTHORISED).

**What it does not do.** It does not build, sign, verify a signature, download or re-hash the artifact (that needs
authentication), or authorise anything. Signing is unresolved (D-12).

## 5. Databases and environments

| Item | Verified value |
|---|---|
| Development project `fundzim` | running; images `fundzim/api:fundzim` = `sha256:adfb403d…`, `fundzim/web:fundzim` = `sha256:9c4e1ccd…` (pre-Phase-3 code) |
| Development database (read-only query) | 50 migrations applied, highest **`20261025090000`**; **no** `*_reconciliation_sweep_batches` table, **no** `failed_attempts` column, so neither `20261026120000` nor `20261027090000` is applied |
| Disposable projects | none running (`fundzim-r2` … `r8` removed with `-v` in earlier phases) |
| Migrations in the repository | 52 files, timestamps unique and strictly increasing; the three remediation migrations each exist in exactly one commit (never edited); three earlier in-stage edits are documented in phase-2 integration-plan.md §1 |

## 6. Documents

All referenced Phase 4 documents are present and consistent:
- phase-4-final-report, integration-readiness, checkpoint-integrity-design, remediation-register;
- the handover;
- `docs/stage-20/production-launch-blockers.md` and `owner-decision-pack.md` (unchanged since Stage 20);
- `docs/governance/stage-acceptance-register.md` (21 rows NOT_RECORDED, unchanged since Stage 20);
- the phase-2 branch-protection, release-candidate and CI-governance documents.

**Stale statement found.** The Phase 4 final report says the documentation-head CI run is "reported in the completion
message". It is now known: 40358498063, success 8/8 (recorded here and in the register).

## 7. Discrepancies against the reports

None in substance. The Phase 4 identifiers, CI runs, protection state and database state are as reported.
