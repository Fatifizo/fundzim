# ADR-012: Container-first development

- **Status:** Accepted
- **Date:** 2026-10-08
- **Stage:** 0 (decision). Local compose arrives in Stage 3. Production topology is decided in Stages 18–20.

## Context

Developers and CI need identical, reproducible dependencies: PostgreSQL, Redis, S3-compatible storage, a mail
catcher and a malware scanner. The production hosting provider is undecided. The master prompt prohibits
Kubernetes, microservices and Kafka without a demonstrated need.

## Decision

- **Docker and Docker Compose for local development.** From Stage 3, a compose file under `deploy/` runs:
  - PostgreSQL;
  - Redis;
  - MinIO;
  - Mailpit;
  - ClamAV.
  Application processes can run natively for fast iteration or in containers.
- Production artefacts are **OCI container images**:
  - Go API: one image, run in API mode and in worker mode.
  - Next.js web: one image.
  - Images are multi-stage, **non-root** and distroless or minimal, pinned by digest, and scanned (e.g. Trivy).
- Configuration comes only from environment variables (12-factor), with `.env.example` as the documented
  template. Production secrets are injected from a secret manager, never baked into images.
- **No Kubernetes, microservices or Kafka** without a demonstrated requirement recorded in an ADR. The
  production topology (e.g. managed containers plus managed Postgres) is decided later.
- Local dev credentials in compose are clearly local-only, documented as such, and never reused anywhere else.

## Consequences

### Positive
- Reproducible environments across dev and CI, portability across hosting providers, and an immutable
  deployment artefact.

### Negative / costs
- Developers need Docker. It is present on the current dev machine; Go and make must still be installed.
- Image hygiene (base image updates, scanning) is ongoing work.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Native installs of all dependencies | Drift between machines, which makes "works on my machine" bugs hard to reproduce. |
| Kubernetes from day one | Operational overhead with no demonstrated need. Prohibited for now. |
| PaaS buildpacks only | Less control over the image contents and supply chain. Can still be used as a host for our images. |

## Security implications
Non-root, minimal images reduce attack surface. Image and dependency scanning run in CI. Secrets stay out of
images and layers, and `.dockerignore` excludes `.env*` and credentials.

## Financial implications
None directly. Reproducible environments do make failure-injection and financial concurrency tests (Stage 19)
reliable in CI.

## Related
ADR-001, ADR-002, ADR-003, ADR-008, [DEVELOPMENT.md](../DEVELOPMENT.md), [SECURITY.md](../SECURITY.md).
