# deploy — deployment configuration

**Status: empty in Stage 0.**

- Stage 3 adds Docker Compose for local dependencies: PostgreSQL, Redis, MinIO (two buckets), Mailpit,
  ClamAV, plus container builds for the API and web apps ([ADR-012](../docs/adr/ADR-012-container-first.md)).
- Production topology, hosting location (subject to data-residency review — see
  [docs/COMPLIANCE.md](../docs/COMPLIANCE.md)) and secret management are decided in later stages
  (Stages 18–20). No Kubernetes unless an ADR demonstrates the need.
