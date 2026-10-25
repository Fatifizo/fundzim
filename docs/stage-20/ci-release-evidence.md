# Stage 20 — CI, release and supply-chain evidence

**Area result: FAIL for release integrity (no protected baseline, no release process, unsigned artifacts); PASS for the
CI gate itself.** Every result below names its commit. A passing run on another commit is never used as proof for the
candidate.

## 1. Candidate and runs (observed through the GitHub REST API, 2026-10-25)

| Commit | Role | Run | Result | Artifact |
|---|---|---|---|---|
| `2015978` | candidate head (Stage 19 documentation on top of the code head) | 40034115480 | success, 8/8 jobs | `supply-chain-evidence-20159782691ce7561b91a91bf9d6a0dcd3b1da95` (546 525 bytes, not expired) |
| `16429d2` | candidate code head (identical `internal/`, `migrations/`) | 40031324294 | success, 8/8 jobs | `supply-chain-evidence-16429d27…` |
| Stage 20 commit(s) | documentation, evidence and two script fixes (`scripts/backup`) | recorded in the final report after the push | — | — |

The eight jobs: Go (format, vet, unit tests with race detector, gosec, deploytest, govulncheck); Web (lint, types, unit
tests, build, `npm audit`); Web end-to-end (Playwright, mocked API); Real-stack E2E (compose + Playwright); Integration
(real PostgreSQL, Valkey, Garage, ClamAV; all non-opt-in integration tests; destructive migration round trip); Container
images (build, CycloneDX SBOM with syft, Trivy gate on fixable CRITICAL/HIGH, checksums, provenance record); OpenAPI and
SQL design drafts; Secret scanning (gitleaks over the full history).

Artifact contents (SBOMs, Trivy reports, provenance) are downloadable only with repository credentials; they were
**not** downloaded in Stage 20 (NOT_VERIFIED as to content). Independently, Stage 20 re-scanned the images locally
(security-certification.md §5) and ran the full integration suite on an isolated stack (financial-certification.md §1).

## 2. Supply-chain controls

| Control | Evidence | Result |
|---|---|---|
| Workflow permissions | `permissions: contents: read` at the top of `ci.yml`; no secrets; no deployment (`TestWorkflowLeastPrivilege`) | PASS |
| Action pinning | 17/17 `uses:` pinned to a 40-character commit SHA | PASS |
| Image pinning | third-party images digest-pinned (`internal/deploytest`) | PASS |
| Dependency scans | govulncheck, `npm audit` in CI: success on the candidate | PASS |
| Secret scanning | gitleaks job success on the candidate; `scripts/check-secrets.sh` locally | PASS |
| Container scans | Trivy gate passes (no fixable CRITICAL/HIGH); unfixed web-image CVEs need a risk decision (KI-S18-10) | PASS WITH CONDITIONS |
| SBOM | generated per run (artifact) | PASS (content NOT_VERIFIED in Stage 20) |
| Provenance | unsigned record; no signing or attestation (KI-S18-08, F-S20-06) | FAIL for production |
| Branch protection | **none on `main`** (F-S20-01) | FAIL |
| Release process | none: no release workflow, no tags, no registry publication (F-S20-02) | FAIL |
| Migration execution | `fundzimctl migrate up` with a goose session lock; CI Up/Down/Up round trip on a throwaway database | PASS |
| Rollback / recovery | application: redeploy previous digests (procedure only); database: forward-only, fix-forward migrations; restore drill (infrastructure-dr-readiness.md) | PASS WITH CONDITIONS (no exercised release rollback) |

No release was created, nothing was merged, and nothing was deployed.
