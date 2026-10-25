# Remediation phase 1 — repository and governance preflight

Verified on 2026-10-25 before any change. Commands were run in this session; results are quoted, not summarised from the
Stage 20 report.

## 1. Repository state

| Item | Verified value |
|---|---|
| Working tree at start | clean (`git status --short --branch` → `## stage-20/production-certification...origin/stage-20/production-certification`) |
| Branch at start | `stage-20/production-certification`, in sync with `origin` after `git fetch` |
| HEAD at start | `6a65d70206ec6d622b868cce725f3b320765e67d` — "Stage 20: production certification assessment — recommendation BLOCKED, launch not authorised" |
| Stage 20 candidate commit | `2015978` (Stage 19 final report and remote CI evidence) = `stage-19/financial-concurrency` head |
| `main` (local and `origin/main`) | `a552de2` — "chore: Stage 0 product definition and engineering foundation" (no stage work integrated) |
| Remote | `origin` = `git@github-fatifizo:Fatifizo/fundzim.git` (repository public, default branch `main`) |
| Stage branches | `stage-1` … `stage-20`, each local head equal to its `origin` head |
| Ancestry | `main`, `stage-1/regulatory-architecture` (`aa9f9b3`) and every `stage-2` … `stage-19` head are ancestors of `6a65d70` (`git merge-base --is-ancestor`): the stages form one linear stack on top of an untouched `main` |
| Existing remediation branch | none (`refs/heads/remediation/reconciliation-completeness` absent; `git ls-remote --heads origin 'remediation/*'` empty) |
| Branch created | `remediation/reconciliation-completeness` from `6a65d70` (no merge, no rebase, nothing discarded) |

Stage heads (local = origin): stage-17 `f5e9d00`, stage-18 `801babe`, stage-19 `2015978`, stage-20 `6a65d70`. The Stage 17,
18, 19 and 20 implementations described in their reports are present in this history (their code, migrations
`20261022090000_reconciliation.sql` … `20261024090100_provider_health_indexes.sql`, and docs). Nothing was reconstructed from a
report.

## 2. CI evidence verified

`GET https://api.github.com/repos/Fatifizo/fundzim/actions/runs?branch=stage-20/production-certification` (unauthenticated):
run **40179378365**, head `6a65d70`, workflow `ci`, `completed` / `success`, created 2026-10-25T09:07:55Z. Its jobs
(`/actions/runs/40179378365/jobs`): Web end-to-end (Playwright), Real-stack E2E, OpenAPI and SQL design drafts, Secret scanning,
Go (format, vet, unit tests, vulnerabilities), Integration, Container images (build, SBOM, vulnerability scan, provenance),
Web (lint, types, unit tests, build, audit) — **8/8 success**. This is evidence for `6a65d70` only, not for any commit of this
phase.

## 3. Branch protection

`GET /repos/Fatifizo/fundzim/branches/main` → `"protected": false`. `GET …/branches/main/protection` → HTTP 401 without
credentials. This session has no GitHub credentials (`gh` not installed; no token) and **did not and could not change
repository settings**. F-S20-01 stands.

## 4. Governance state

- `docs/governance/stage-acceptance-register.md`: Stages 0–20 **NOT_RECORDED**; unchanged by this phase.
- Stage 20 findings register and blocker register read; unchanged (historical record). Current status of the items this phase
  touches is recorded separately in [remediation-register.md](remediation-register.md).

## 5. Environment

- The development compose project `fundzim` was running (contaminated by Stage 19 load data, KI-S19-17). It was stopped with
  `docker compose down` **without** `-v`; its volumes `fundzim_postgres-data`, `fundzim_storage-*`, `fundzim_clamav-db` were not
  touched and no development record was read for testing, changed or deleted.
- All database tests of this phase ran on a **disposable compose project** `fundzim-r1` started from empty volumes
  (`fundzim-r1_*`) the way CI does (secrets init, storage init, migrations, ClamAV), with API and worker containers built from the
  working tree. Isolation check before testing: `goose_db_version` head `20261025090000`, `app.payout_requests` 0 rows,
  `app.payment_intents` 0 rows.
- The disposable project was recreated once from empty volumes (`docker compose -p fundzim-r1 down -v`, that project only)
  after a test of this phase left a permanent L6 violation in its append-only ledger (see final report §9, F-R1-02).
- Stack operations and integration runs were serialised with `flock /tmp/claude-1000/fundzim-stack.lock`.
