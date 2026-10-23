# deploy — container builds and local environment configuration

Local development only so far ([ADR-012](../docs/adr/ADR-012-container-first.md)). The stack itself is defined
in the root [`compose.yaml`](../compose.yaml) so that `docker compose up -d --build` works from the repository
root.

| Path | Purpose |
|---|---|
| `docker/api.Dockerfile` | API image (build context: repo root). Go 1.27.3 build (`golang:1.27.3-alpine3.24`, pinned by digest), distroless `static-debian12:nonroot` runtime pinned by digest, uid 65532, binaries `api` and `fundzimctl`, health check via `fundzimctl healthcheck` |
| `docker/web.Dockerfile` | Web image (build context: `apps/web`). Next.js standalone on `node:24.21.0-bookworm-slim` (pinned by digest; Stage 18: Debian security updates applied at build, npm/npx/corepack/yarn removed from the runtime), runs as `node`, read-only app files, health check on `/healthz`; `API_BASE_URL` is read at runtime |
| `docker/postgres/init/10-roles.sh` | Runs once on an empty Postgres volume: creates `fundzim_migrator` (database owner, not superuser) and the runtime roles with passwords from `.env` and role-level timeouts, creates the `fundzim` database |
| `docker/postgres/init/20-ledger-role.sh` | Idempotent: the ledger posting role `fundzim_ledger` (Stage 10) |
| `docker/postgres/init/30-backup-role.sh` | Idempotent (Stage 18): the read-only backup role `fundzim_backup` (`pg_read_all_data`, BYPASSRLS, `default_transaction_read_only`); for an existing volume run it with `docker compose exec` (see the script) |
| `docker/garage/garage.toml` | Single-node Garage configuration (local only; S3 API on 3900, admin API 3903 not published) |
| `docker/garage/init.sh` | Idempotent init via the Garage admin API: node layout, three access keys, three buckets, each key granted on exactly one bucket (design-baseline I-19/I-26) |

**Stage 18 hardening** ([docs/stage-18/container-security.md](../docs/stage-18/container-security.md)): every service in
`compose.yaml` drops all capabilities (third-party entrypoints get back only what they need to switch users), runs with
`no-new-privileges` and memory/PID limits; FundZim's containers have read-only roots with tmpfs scratch space; third-party
images are pinned by digest; the web container is only on the `edge` network (web ⇄ API) and cannot reach any data
service; the Valkey password is passed through a tmpfs config file, never on the command line. `internal/deploytest`
fails the build when any of this regresses. Backups, restore drills and DR exercises: `scripts/backup`, `scripts/dr`
([docs/stage-18/backup-recovery.md](../docs/stage-18/backup-recovery.md)).

Services, ports (all bound to `127.0.0.1`) and commands: [README.md](../README.md#quick-start-local-development).
Production topology, hosting location (subject to data-residency review, [docs/COMPLIANCE.md](../docs/COMPLIANCE.md)),
TLS termination and production secret management are infrastructure work for Stage 20 (proposed reference architecture:
[docs/stage-18/deployment-security.md](../docs/stage-18/deployment-security.md)). No Kubernetes unless an ADR
demonstrates the need. Nothing here may point at a real payment provider or hold real data.
