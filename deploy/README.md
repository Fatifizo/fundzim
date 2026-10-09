# deploy — container builds and local environment configuration

Local development only so far ([ADR-012](../docs/adr/ADR-012-container-first.md)). The stack itself is defined
in the root [`compose.yaml`](../compose.yaml) so that `docker compose up -d --build` works from the repository
root.

| Path | Purpose |
|---|---|
| `docker/api.Dockerfile` | API image (build context: repo root). Go 1.27.2 build (`golang:1.27.2-alpine3.24`), distroless `static-debian12:nonroot` runtime pinned by digest, uid 65532, binaries `api` and `fundzimctl`, health check via `fundzimctl healthcheck` |
| `docker/web.Dockerfile` | Web image (build context: `apps/web`). Next.js standalone on `node:24.21.0-bookworm-slim`, runs as `node`, read-only app files, health check on `/healthz`; `API_BASE_URL` is read at runtime |
| `docker/postgres/init/10-roles.sh` | Runs once on an empty Postgres volume: creates `fundzim_migrator` (database owner, not superuser) and the runtime roles with passwords from `.env` and role-level timeouts, creates the `fundzim` database |
| `docker/garage/garage.toml` | Single-node Garage configuration (local only; S3 API on 3900, admin API 3903 not published) |
| `docker/garage/init.sh` | Idempotent init via the Garage admin API: node layout, three access keys, three buckets, each key granted on exactly one bucket (design-baseline I-19/I-26) |

Services, ports (all bound to `127.0.0.1`) and commands: [README.md](../README.md#quick-start-local-development).
Production topology, hosting location (subject to data-residency review, [docs/COMPLIANCE.md](../docs/COMPLIANCE.md)),
secret management, TLS and storage encryption are decided in Stages 18–20. No Kubernetes unless an ADR
demonstrates the need. Nothing here may point at a real payment provider or hold real data.
