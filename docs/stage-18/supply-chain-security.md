# Stage 18 — Dependency and supply-chain security

## 1. Dependency auditing

| Ecosystem | Tool | Gate | Result (2026-10-23) |
|---|---|---|---|
| Go modules + standard library | govulncheck v1.8.0 (reachability) | any reachable vulnerability fails CI | 0 reachable (Go 1.27.3) |
| npm (production) | `npm audit --omit=dev --audit-level=high` | fails CI | 0 (Next 16.4.2) |
| npm (all) | `npm audit --audit-level=high` | report (dev-only lint chain, Stage 3 KI-03) | report |
| Container images (OS + language packages) | Trivy v0.75.0 | fixable CRITICAL/HIGH fail CI; everything reported | api 0; web 0 fixable (unfixed listed in container-security.md §4) |
| Go source | gosec v2.29.0 | any unreviewed finding fails CI | 0 (9 reviewed) |
| CI actions | SHA pins | `TestWorkflowLeastPrivilege` | all pinned |
| Build tools | Go via `go.mod` toolchain, Node 24.21.0 pinned, tool downloads checksum-verified (gitleaks, syft, trivy) | test checks every `curl -o` has `sha256sum -c` | pass |

Severity handling: CRITICAL/HIGH with a fix → fix before merge (CI blocks); without a fix → recorded (KI-S18-10) and
re-evaluated on every build; reachability decides Go severity (govulncheck). Exceptions require an owner decision and an
expiry; none was granted.

## 2. SBOM

syft v1.54.1, CycloneDX 1.7 JSON, generated in CI for the API image, the web image and the source tree (Go modules and
the npm lockfile), uploaded as `supply-chain-evidence-<sha>` (30 days). Local generation: API 1 097 components, web
3 511, source 118.

## 3. Provenance and integrity

The images job builds the binaries with `-trimpath` and the commit, writes `binaries.sha256` and `provenance.json`
(repository, commit, ref, run id and attempt, Go version, image ids, binary checksums), uploaded with the SBOMs and scan
reports. It is an **unsigned** record: signing (cosign / GitHub attestations, which need `id-token: write` and a signing
identity) is not configured (KI-S18-08). Immutable release identifiers = commit SHA + image id; there is no release
pipeline and nothing is pushed to a registry.

## 4. Controlled updates

Base images are pinned by digest; to update: pull the new tag, record the digest in `compose.yaml` /
Dockerfile, run the CI (Trivy gate + SBOM diff). Go modules: `go get` + `go mod tidy` + govulncheck. npm: exact versions
(`--save-exact`) + audit. Actions: update the SHA with the tag comment. No automatic dependency bot is configured.

## 5. CI/CD security

| Control | State |
|---|---|
| Workflow token | `permissions: contents: read` at the top level; no job escalates (tested) |
| Untrusted code with secrets | no `pull_request_target`, no `workflow_run`, no repository secrets referenced (tested) |
| Deployments | none; no `environment:` (tested) |
| Fork pull requests | run with the read-only token and no secrets (GitHub default for `pull_request`) |
| Artifact retention | 30 days for supply-chain evidence |
| Branch protection, required reviews, protected environments | repository settings, not visible without an authenticated admin API call: **NOT_VERIFIED** (KI-S18-09); no pull request exists for any stage |
| Concurrency | `cancel-in-progress` per ref |
