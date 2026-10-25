# Stage 20 — Preflight: repository, CI, integration and governance

Observed 2026-10-25 before any change. Values are verified, not copied from the Stage 19 report.

## 1. Repository

| Check | Observed |
|---|---|
| Branch at start | `stage-19/financial-concurrency`, clean, in sync with `origin` |
| HEAD | `20159782691ce7561b91a91bf9d6a0dcd3b1da95` (documentation-only on top of code head `16429d2`) |
| `origin/main` | `a552de2b44daf83d662b4675055908f7d7a7a982` (Stage 0) |
| Stage branches contained in HEAD | **all 19** (`git merge-base --is-ancestor` for every `origin/stage-*`): the branches are a strict linear stack |
| Pull requests | none |
| `stage-20/*` on the remote | none before this stage; `stage-20/production-certification` created from `2015978` |
| Repository visibility | public (`GET /repos/Fatifizo/fundzim`) |
| **Branch protection on `main`** | **`protected: false`** (`GET /repos/Fatifizo/fundzim/branches/main`). docs/DEVELOPMENT.md §5 requires a protected `main` with PR, green CI and reviewers "once hosted" → finding F-S20-01 |

**Integration gap.** Every stage's code exists only on the stacked branch; `main` holds Stage 0. There is no approved
release baseline. A controlled resolution (owner decision, not performed here): open one PR
`stage-20/production-certification → main` after branch protection is configured, require the CI gate and reviewers
(≥ 2 for ledger/payments code per DEVELOPMENT.md §5), merge with a merge commit (no history rewrite), tag the merge as
the release candidate, and re-run CI on `main`. Nothing was merged.

## 2. CI (observed through the GitHub REST API)

| Run | Commit | Status | Jobs |
|---|---|---|---|
| 40034115480 | `2015978` (candidate head) | completed / success | 8/8 |
| 40031324294 | `16429d2` (code head) | completed / success | 8/8 |
| 39984705098 | `801babe` (Stage 18) | completed / success | 8/8 |

The candidate code is identical between `16429d2` and `2015978` (the later commit changes documentation only).

## 3. Governance

`docs/governance/stage-acceptance-register.md`: Stages 0–19 **NOT_RECORDED**. No owner decision exists in the
repository. The register is a proposed template; only the owner may record an acceptance.

## 4. Environment used for Stage 20 execution

The shared development database is contaminated by Stage 19 load data (KI-S19-17). Stage 20 therefore ran its
regression evidence on a **disposable compose project** (`docker compose -p fundzim-s20`), started from empty volumes
the way CI does (secrets init, storage init, migrations, ClamAV), after `docker compose down` of the development
project **without** `-v` (development volumes `fundzim_*` kept untouched). The disposable project's volumes are
`fundzim-s20_*` and were removed at the end (`down -v` on that project only).

## 5. Documents inspected

Stage 17/18/19 handovers and final reports; docs/stage-19/known-issues.md; docs/stage-18/known-issues.md,
production-readiness.md, disaster-recovery.md, deployment-security.md; docs/compliance/open-legal-questions.md;
docs/payments/provider-questions.md; docs/stage-10/known-issues.md; docs/adr/README.md (ADR-001 … ADR-051);
`.github/workflows/ci.yml`; `compose.yaml`; the code paths cited in each assessment.
