# Remediation phase 2 — release-candidate procedure, provenance and signing plan (phase E)

**No release was published, nothing was signed, nothing was pushed to a registry.** This procedure produces a
*release candidate record*, which is input for the owner. It is not a release.

## 1. Three distinct states

| State | Meaning | How it is established today |
|---|---|---|
| **Built** | CI built the images and binaries from exactly this commit | the `Container images (build, SBOM, vulnerability scan, provenance)` job succeeded in a `ci.yml` run whose `head_sha` is the commit |
| **Verified** | a third party can check cryptographically that an artifact came from this commit and workflow | **not possible today.** The CI provenance record is an unsigned JSON statement inside a workflow artifact (F-S20-06). Status: `UNSIGNED_NOT_VERIFIABLE` |
| **Authorised** | the owner approved this exact artifact set for a named environment | **never recorded by engineering.** Owner decisions D-12, D-13 and D-14. Status: `NOT_AUTHORISED` |

A candidate is never described with a state it has not reached. "CI green" means *built and tested*, not *verified* or
*authorised*.

## 2. What identifies a candidate

| Element | Source of truth | Where recorded |
|---|---|---|
| Source commit and tree | git | manifest `commit`, `tree` |
| Build workflow and run | `.github/workflows/ci.yml` (SHA-256 of that file at the commit), run id and attempt | manifest `inputs.workflow_sha256`, `ci.workflow_run_id`, `ci.run_attempt` |
| Build inputs | `go.mod`, `go.sum`, `apps/web/package-lock.json` (git blob ids); base images pinned by digest in both Dockerfiles; third-party compose images pinned by digest; migrations tree hash, count and newest version | manifest `inputs` |
| Test results | the 8 required checks of that run, each `success` | manifest `ci.checks` |
| Container image identity | image IDs (config digests) and binary SHA-256 checksums in `provenance.json` and `binaries.sha256` inside the artifact. Images are **not pushed**, so no registry digest exists yet | workflow artifact `supply-chain-evidence-<sha>` (kept 30 days); manifest `ci.artifacts[].digest` is GitHub's digest of that artifact |
| SBOMs | CycloneDX JSON for the api image, the web image and the source tree (syft 1.54.1, checksum-pinned) | same artifact |
| Vulnerability scan | Trivy 0.75.0 JSON and table per image; the build fails on fixable CRITICAL/HIGH; unfixed findings are reported (B-10 / D-08) | same artifact |
| Signing status | none (§4) | manifest `status.provenance` |
| Approval status | none | manifest `status.authorisation` |

## 3. Procedure (repeatable; read-only except step 5)

1. Choose the commit (normally the merge commit on protected `main`; integration-plan.md §2).
2. Wait for its push run of `ci.yml` to complete. `main` runs are never cancelled.
3. In a **clean checkout of that commit**, run:
   ```bash
   python3 scripts/release/rc-manifest.py <sha> > rc-<sha>.json   # exit 0 = ELIGIBLE_FOR_REVIEW, 1 = REJECTED (reasons listed)
   sha256sum rc-<sha>.json
   ```
   The script rejects:
   - a dirty tree, or a commit that is not on origin;
   - no completed `ci.yml` run for that exact SHA;
   - a run of another commit or another workflow file;
   - a required check that is missing, appears twice, or did not succeed.

   It never builds, signs or authorises anything.
4. Download the `supply-chain-evidence-<sha>` artifact (this needs a GitHub login, even on a public repository). Check that its
   SHA-256 equals `ci.artifacts[].digest`, then archive it **beyond the 30-day retention** alongside the manifest.
5. *owner* — record the decision (D-12, D-13 and later D-14) in the acceptance register, naming the manifest SHA-256. Only then
   is an annotated tag created on the commit.
6. **Rebuild check (reproducibility evidence, recommended):** build `deploy/docker/api.Dockerfile` locally from the same commit
   with `--build-arg COMMIT=<sha> --build-arg VERSION=ci`, then compare the `api`, `worker` and `fundzimctl` binary checksums
   with `binaries.sha256`.
   - The CI binaries come from `go build -trimpath` with only `version.Commit` set, so the local build must use the same
     flags. A mismatch is reported, never explained away.
   - Image IDs are not expected to match across hosts yet, because there is no `SOURCE_DATE_EPOCH` or layer timestamp
     normalisation. That is a known gap, not a failure.

Exercised in Phase 2 on `c8fb8c6`: steps 1–3 produced `ELIGIBLE_FOR_REVIEW` with run 40279994161
(`evidence/rc-manifest-c8fb8c6.json`). The negative check on `a0a687d`, which has no CI run, was `REJECTED`. Steps 4–6 were not
performed: the artifact download needs authentication, and the owner records the decision.

## 4. Signing and attestation design (requires D-12; not implemented)

No key may be created ad hoc. The proposal uses **keyless** signing, so no long-lived private key exists anywhere:

| Option | What it gives | Requirements | Recommendation |
|---|---|---|---|
| **GitHub artifact attestations** (`actions/attest-build-provenance`, Sigstore-backed SLSA provenance, verifiable with `gh attestation verify`) | signed provenance binding artifact digests to repository, workflow, commit and run | a separate `provenance` job with `id-token: write` and `attestations: write`. `TestWorkflowLeastPrivilege` already allows exactly that job name and those scopes. The action is pinned by SHA. Public repositories need no paid plan | **recommended**, the smallest change |
| cosign keyless on registry images | signatures on pushed image digests | a container registry (none chosen) and `packages: write` | after a registry decision |
| cosign or GPG with stored keys | — | key custody, rotation and a secret in CI, which conflicts with "CI uses no secrets" (Stage 18) | not recommended |

Owner decisions for D-12:
- adopt attestations, yes or no;
- which artifacts to attest (binaries and image tarballs now, registry images later);
- whether tags must be signed and by whom;
- a registry choice;
- the release and rollback procedure, including who may deploy which digest.

Until then, every candidate's provenance is `UNSIGNED_NOT_VERIFIABLE`.

## 5. Rollback

- Redeploy the previous candidate's artifacts by digest. They come from its manifest and archived artifact; tags are never
  trusted.
- The database is forward-only (integration-plan.md §7).
- Rehearse once infrastructure exists (B-11).
