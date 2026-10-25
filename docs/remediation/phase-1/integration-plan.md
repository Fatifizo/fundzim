# Remediation phase 1 — integration plan (proposal; nothing merged)

**Status: PROPOSED.** No branch was merged, rebased or force-pushed; no repository setting was changed. Every step marked
*owner* needs the repository owner's decision (D-01, D-12, D-13 in the Stage 20 owner decision pack).

## 1. Facts this plan starts from

- `main` = `a552de2` (Stage 0). All work since lives on a linear stack of stage branches: `stage-1` → … → `stage-20`
  (`6a65d70`) → `remediation/reconciliation-completeness` (this phase). Every later stage depends on every earlier one (code,
  migrations, docs); no stage can be integrated alone out of order.
- Migrations are goose files with strictly increasing timestamps (`20261008120000` … `20261025090000`), embedded in the binary
  and applied by `fundzimctl migrate up` as `fundzim_migrator` under a goose session lock; production is forward-only and
  `fundzimctl` refuses Down outside development (ADR-028). CI already runs a Down-to-0 / Up round trip on a throwaway database.
- CI (`.github/workflows/ci.yml`) has 8 jobs, `contents: read`, SHA-pinned actions, no secrets, no deployment.

## 2. Intended integration baseline

The integration baseline is the head of the remediation branch once its CI run is green and the owner accepts it — or, if the
owner prefers to integrate the certified candidate first, `6a65d70` (Stage 20, CI 40179378365 green) followed by this branch.
Both routes contain identical Stage 0–20 commits; the choice only decides whether `main` ever points at a commit without the
KI-S19-09 fix.

Recommended order (each step a pull request into `main`, merged with a merge commit — no squash, so every stage commit and its
CI evidence stays addressable):

1. *owner* — configure protection on `main` (§4) **before** the first merge.
2. PR `stage-20/production-certification` → `main` (fast-forward-equivalent: `main` is an ancestor; no conflicts are possible
   because `main` has no commits of its own since `a552de2`). Required: the 8 CI checks green on the PR head (`6a65d70` already
   has run 40179378365; GitHub re-runs CI for the PR merge ref).
3. PR `remediation/reconciliation-completeness` → `main` after step 2 (again conflict-free: linear history).
4. *owner* — tag the merge commit as the first release baseline (e.g. `baseline-2026-10-r1`) only after CI on `main` is green;
   record the tag, commit and CI run in the acceptance register.

Stacked stage branches remain as history; they are not deleted (deletion is the owner's choice after integration).

## 3. Conflicts, migrations and verification

- **Conflicts:** none expected (linear stack onto an untouched `main`). If `main` ever receives another commit first, the
  stack is rebased by the author onto the new `main` in a new branch (never force-pushing a shared stage branch), CI re-run, and
  migration timestamps checked for ordering (a migration older than the newest applied one would be refused by goose in
  out-of-order mode — the repository does not enable it).
- **Migrations:** after merge, verify on a throwaway database: `migrate up` from empty; `TestMigrationsAppliedAndRepeatable`
  (Down-to-0 / Up, CI step "Destructive migration round trip"); `fundzimctl migrate status` shows `20261025090000` applied. On any
  shared or long-lived environment: `migrate up` only (forward-only), after a verified backup (Stage 18 backup + restore drill).
  This phase's migration creates three new tables and one trigger function, touches no existing table or row, and needs no
  backfill (the first run opens cycle 1).
- **Concurrent index builds:** this phase adds none (KI-S19-15 procedure still missing for live data).

## 4. Proposed branch protection for `main` (owner applies; Claude Code cannot)

| Setting | Proposed value | Source |
|---|---|---|
| Require a pull request | yes; ≥ 1 approving review; ≥ 2 for `internal/ledger`, `internal/payments`, `internal/payouts`, `internal/fees`, `internal/reconciliation`, `migrations/` (CODEOWNERS) | DEVELOPMENT.md §5, F-S20-01 |
| Dismiss stale approvals on new commits | yes | — |
| Required status checks (strict: branch up to date) | the 8 CI jobs by name: `Go (format, vet, unit tests, vulnerabilities)`, `Web (lint, types, unit tests, build, audit)`, `Web end-to-end (Playwright)`, `Real-stack E2E (compose + Playwright)`, `Integration (real PostgreSQL, Valkey, Garage, ClamAV via docker compose)`, `OpenAPI and SQL design drafts`, `Secret scanning`, `Container images (build, SBOM, vulnerability scan, provenance)` | ci.yml |
| Force pushes / deletions | blocked | F-S20-01 |
| Include administrators | yes | — |
| Linear history | not required (merge commits preserve stage commits) | §2 |
| Signed commits | owner decision (D-12) | F-S20-06 |

Verification: `GET /repos/Fatifizo/fundzim/branches/main` → `protected: true` and the rule set visible (Stage 20 closure
evidence for F-S20-01).

## 5. Release artifacts and acceptance register

- Artifacts (images, SBOMs, Trivy reports, provenance) are produced by the CI run of the merge commit; the release record names
  that commit SHA and run id. Signing/attestation is not implemented (F-S20-06, D-12).
- The acceptance register is updated **only by the owner** (decision, date, decider, evidence, conditions). This phase adds no
  row and changes no acceptance state; the phase report is the input for the owner.

## 6. Decisions required from the repository owner

| # | Decision | Evidence the owner needs | Blocks |
|---|---|---|---|
| D-01 | apply §4 protection; approve the PR route of §2 | this plan; API output after applying | integration |
| D-13 | formal acceptance of Stages 0–20 (and of this phase) in order | stage reports, this phase's report, CI runs | release baseline |
| D-12 | signing / release / rollback procedure | ci-release-evidence.md (Stage 20) | production artifacts |
| D-06 | reconciliation coverage requirement: maximum acceptable cycle duration per detector | performance.md of this phase | B-05 closure |
