# Stage 3 Prerequisite Assessment

**Date:** 2026-10-08. **Branch:** `stage-3/core-platform`, created from `stage-2/system-architecture` (`fe9cfa7`).
Classification: **SATISFIED** · **SATISFIED_WITH_CONDITIONS** · **BLOCKED** · **NOT_VERIFIED**.

## 1. Stage readiness

| # | Prerequisite | Finding | Classification |
|---|---|---|---|
| P1 | Stage 2 deliverables complete | Present: baseline, architecture, database, API and security docs; 147-table SQL drafts with 666 passing cases; OpenAPI lint-clean; ADR-021 – 031; Stage 2 → 3 handover | SATISFIED |
| P1a | Stage 1 and Stage 2 **acceptance recorded** | Neither has a signed acceptance record. The project owner issued the Stage 3 prompt, which is an instruction to proceed, not a recorded acceptance. | **NOT_VERIFIED** (carried into the Stage 3 report) |
| P2 | Docker access for the dev user | The user was not in the `docker` group. The owner chose to run `sudo usermod -aG docker administrator`; Docker commands then run through `sg docker`. | See the Stage 3 report for the outcome |
| P3 | Go toolchain | Not installed at the start. With the owner's approval, **Go 1.27.1** was installed to `~/.local/go` from the official tarball (SHA-256 verified). | SATISFIED |
| P4 | `make` | Not installed (needs sudo). Every Make target has a documented equivalent command. | SATISFIED_WITH_CONDITIONS |
| P5 | GitHub Actions enabled and branch protection | Cannot be verified from this machine. Workflows are written and lint-checked locally; whether they run is verified on push. | NOT_VERIFIED |
| P6 | Go module path | `github.com/Fatifizo/fundzim`, matching the `origin` remote (`git@github-fatifizo:Fatifizo/fundzim.git`) | SATISFIED |
| P7 | Git identity | Configured (`Fatifizo`) | SATISFIED |
| P8 | PostgreSQL major version | The Stage 2 handover left 17 vs 18 open (LEC-1). **Decision: PostgreSQL 17** (the briefed version, mature, supported to 2029). The SQL drafts were validated on 18 (PGlite); the migrations are re-validated on 17 in Stage 3. | SATISFIED |

## 2. Brief vs Stage 2 design: reconciled differences

The Stage 3 brief is newer than the Stage 2 handover. Where they differ, the brief governs **unless** following
it would break an accepted ADR or a security control. Each difference:

| Topic | Stage 3 brief | Stage 2 design | Stage 3 decision |
|---|---|---|---|
| Go module location | `apps/api/go.mod`, `apps/api/internal/...` | One module at the repo root, `internal/<module>` (ARCHITECTURE §4, baseline §1) | **Stage 2 kept.** A layout change needs an ADR and brings no benefit. `apps/api/cmd/*` hold the entrypoints. |
| Health endpoints | `GET /api/v1/health`, `/api/v1/ready`, `/api/v1/version` | `/healthz`, `/readyz` outside `/api/v1` | **Both.** `/healthz` and `/readyz` serve infrastructure probes. `/api/v1/health`, `/api/v1/ready` and `/api/v1/version` are the public API equivalents, added to the OpenAPI contract. |
| Config names | `HTTP_HOST`/`HTTP_PORT`, `S3_ENDPOINT`/`S3_BUCKET`/`S3_ACCESS_KEY`… | `API_LISTEN_ADDR`, `STORAGE_*` with three credential sets (I-19) | `HTTP_HOST` + `HTTP_PORT` adopted. **Storage keeps three credentials (I-19, a security control)** under `STORAGE_*`; the brief's single-credential `S3_*` set would give one key access to both private prefixes. |
| Storage classes | PUBLIC_CAMPAIGN_MEDIA, PRIVATE_IDENTITY_DOCUMENTS, PRIVATE_COMPLIANCE_DOCUMENTS | Buckets `public-media`, `private-kyc` (prefixes `kyc/`, `compliance/`, `reports/`) | The brief's three logical classes are mapped onto the I-19 buckets, prefixes and credentials (`internal/platform/storage`) |
| Compose file | `docker-compose.yml` at the root, `docker compose up -d --build` | `deploy/local/compose.yaml` | **Root `compose.yaml`** so that `docker compose up -d --build` works as briefed. Container images live under `deploy/docker/`. |
| Redis | Required service | Optional (`REDIS_REQUIRED=false`) | Redis runs in compose by default. The API still degrades safely without it (ARCHITECTURE §8.2). |
| Outbox, River job queue, idempotency store | Not listed in the brief's objectives | Listed in the Stage 2 → 3 handover §12 | The `queue` schema and the outbox/inbox tables are created. **No worker binary exists yet.** The worker, River job processing, the outbox dispatcher and the idempotency middleware move to the start of Stage 4, because the brief limits Stage 3 to the listed objectives (recorded in known-issues.md). |
| Frontend scope | Homepage, design system, routes, API client | Scaffold, API client, money types | The brief's scope is built. Pages for future features show clearly labelled "coming soon" states. |

## 3. Unresolved items that Stage 3 does not touch

All P0 legal items (LR register §5), provider selection (all PENDING), retention periods (LR-012) and every
financial workflow. Stage 3 builds no part of authentication, KYC, campaigns, payments, ledger, payouts,
refunds or compliance.
