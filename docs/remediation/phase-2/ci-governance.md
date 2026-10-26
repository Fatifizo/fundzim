# Remediation phase 2 — CI governance (phase F)

One workflow, `.github/workflows/ci.yml`:
- top-level permissions `contents: read`;
- every action pinned by commit SHA;
- every downloaded tool checksum-verified;
- no secrets, no deployment environment, no `pull_request_target`.

`internal/deploytest` enforces all of this.

## 1. Triggers

| Event | Branches | Effect |
|---|---|---|
| `push` | `main`, `stage-**`, `remediation/**` | `remediation/**` was added in Phase 1 (`08411f6`). Phase 1 and Phase 2 branches get all 8 jobs on every push (runs 40279994161 and the Phase 2 runs in test-evidence.md) |
| `pull_request` | any base | GitHub runs the workflow on `refs/pull/N/merge`, the merge of the PR head into the current base |

## 2. Do required checks apply to the commit that is merged?

- **On the PR.** The run tests the merge ref. With "require branches to be up to date" (strict, branch-protection.md §2), the
  base cannot move after the run without the PR becoming out of date, which forces a rebase or update and a new run. The tested
  tree is therefore the merged tree.
- **After the merge.** The merge commit is a new commit, so its own push run on `main` is the baseline evidence. Two pieces now
  guarantee that run:
  - `main` runs are no longer cancelled by a later push (§3);
  - `rc-manifest.py` refuses a candidate whose own SHA has no completed, all-green run.

## 3. Change: never cancel a `main` run

Before: `concurrency: {group: ci-${{ github.ref }}, cancel-in-progress: true}`. Two quick pushes to `main` cancelled the first
run, so that commit had no CI result. That is a silent gap in baseline evidence: Phase 1 run 40213342713 on a working branch
was cancelled exactly this way.

Now: `cancel-in-progress: ${{ github.ref != 'refs/heads/main' }}`. Working branches still save runner time.

## 4. Change: required checks cannot drift or be impersonated

Branch protection matches required checks **by name**. That creates two risks:
- a renamed job silently stops being required, because the old name is never reported and the gate either blocks for ever or
  is edited to pass;
- another workflow, or another integration, posts a check with the same name.

Controls:
- `TestWorkflowRequiredChecks` (new, `internal/deploytest`):
  - exactly one workflow file may exist;
  - every job has a name;
  - the set of job names equals the required-check list in branch-protection.md §3.
- The protection proposal pins each check's source to the GitHub Actions app, so a commit status or check run from any other
  app does not satisfy it.
- **Residual risk:** a PR can modify `ci.yml` itself, and its run uses the PR's version. Only review catches a PR that weakens
  a job while keeping its name. That is why `.github/` belongs on the CODEOWNERS list once a reviewer is named (D-01a).
  `TestWorkflowLeastPrivilege` still blocks secrets, write scopes and unpinned actions in any version.

## 5. Coverage of the CI jobs (verified against ci.yml at `efa7b62`)

| Area | Job and step | Status |
|---|---|---|
| Go format, vet, static analysis | `go`: gofmt, `go vet` (also for the integration and perf tags), gosec v2.29.0 | present |
| Go unit and race tests | `go`: `go test -race ./apps/api/... ./internal/... ./migrations/...`; money fuzz smoke; payments verbose run | present |
| Go vulnerabilities | `go`: govulncheck v1.8.0 | present |
| Web lint, types, unit tests, build, audit | `web` | present (production audit blocking; dev audit reported, KI-03) |
| Mock end-to-end | `e2e` (Playwright) | present |
| Integration on real services | `integration`: PostgreSQL, Valkey, Garage, OpenBao, Mailpit and ClamAV via compose; the API and worker run on the runner; `-tags integration` including `FUNDZIM_IT_PERF`; destructive migration round trip on the throwaway DB | present |
| Real-stack end-to-end | `e2e-real`: full compose stack, Playwright | present |
| OpenAPI and SQL design validation | `contracts`: Redocly lint; PGlite validation of the design drafts | present |
| Secret scanning | `secrets`: `check-secrets.sh` plus gitleaks v8.30.1 over the full history | present |
| Container build and scan | `images`: both images, non-root check, Trivy (fails on fixable CRITICAL/HIGH) | present |
| SBOM | `images`: syft CycloneDX for the api and web images and the source tree | present |
| Artifact provenance | `images`: an **unsigned** `provenance.json`, binary checksums and image IDs | partial: no attestation (D-12) |
| Release-candidate check | `scripts/release/rc-manifest.py` (run by a person, not by CI) | new (release-candidate-procedure.md) |
| Ledger volume tiers, load and crash tests | not in CI by design (scratch DB / disposable stack only; Stage 19) | documented exclusion |

No workflow was duplicated, no job was made advisory and no assertion was weakened. The web focus-timing race (KI-R1-10) did
not recur in Phase 2: the local web suite passed 578/578, and the CI results are in test-evidence.md.

## 6. Owner decisions

- D-01: required checks and protection.
- D-01a: a reviewer for `.github/`.
- D-12: add the `provenance` attestation job.
