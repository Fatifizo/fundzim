# Remediation phase 2 — repository and CI baseline (phase A)

Read 2026-10-26, starting at 06:3x UTC, before any Phase 2 change.

## 1. Repository

| Item | Observed |
|---|---|
| Branch / HEAD at start | `remediation/reconciliation-completeness` at `c8fb8c6`, tracking origin, ahead 0 / behind 0, working tree clean |
| `07e5965` | Phase 1 web test fix (KI-R1-10); an ancestor of `c8fb8c6` |
| `c8fb8c6` | Phase 1 docs-only commit (F-R1-07 closed by the owner's decision). It is the latest commit on the branch: `git fetch --all --prune` found nothing newer on any remote branch |
| `main` | `a552de2` locally and on origin (Stage 0); no commits since |
| Other branches | `stage-1` … `stage-20` and the Phase 1 branch; all local heads equal their remotes. Dependency map: integration-plan.md §1 |
| History | 101 commits, linear (no merge commits) |
| Phase 2 branch | `remediation/release-governance`, created from `c8fb8c6` (it did not exist before; nothing was overwritten) |

## 2. CI evidence (GitHub REST API, unauthenticated)

| Run | Commit | Event | Result | Jobs | Notes |
|---|---|---|---|---|---|
| 40211164419 | `08411f6` | push | failure | 7/8 (Web) | KI-R1-10, fixed in `07e5965` |
| 40213342713 | `9d16090` | push | cancelled | — | superseded by the next push (concurrency) |
| 40213732609 | `07e5965` | push | success | 8/8 | was the latest 8/8 Phase 1 result at the end of Phase 1 |
| **40279994161** | **`c8fb8c6`** | push | **success** | **8/8** | 2026-10-26 06:28–06:53 UTC; artifact `supply-chain-evidence-c8fb8c6…` (digest `sha256:34fa5178…`, expires 2026-11-25) |

**Answer to the brief.** 40213732609 is no longer the latest 8/8 Phase 1 result. Run 40279994161 on `c8fb8c6` is now the
latest, also 8/8. No job of either run failed. The jobs, in GitHub's order:
- Web end-to-end (Playwright)
- Container images (build, SBOM, vulnerability scan, provenance)
- Go (format, vet, unit tests, vulnerabilities)
- Real-stack E2E (compose + Playwright)
- Integration (real PostgreSQL, Valkey, Garage, ClamAV via docker compose)
- Web (lint, types, unit tests, build, audit)
- OpenAPI and SQL design drafts
- Secret scanning

Limitation: job logs need authentication. The evidence is the per-job conclusion and the artifact metadata, not the log text.

## 3. Protection and repository rules

`main` is not protected: there are no rulesets and no branch rules. Details and evidence are in branch-protection.md §1 and
`evidence/github-protection-2026-10-26.txt`. Repository administration settings (collaborators, merge methods) need
authentication and were not visible.

## 4. Development database and stack

Development project `fundzim`, read-only checks: 50 migrations applied, the newest is `20261025090000`, the API is ready, and
the worker is sweeping. The image provenance is only partly known (`commit: unknown`). Details are in
dev-environment-isolation.md §1.

## 5. Unexpected state

None. The tree was clean and remote and local heads matched. The only change since Phase 1 is the owner-approved `c8fb8c6`.
