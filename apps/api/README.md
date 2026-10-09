# apps/api — Go entrypoints

Wiring only; business logic lives in `internal/`. One Go module at the repository root
(`github.com/Fatifizo/fundzim`, Go 1.27.2).

| Command | Purpose |
|---|---|
| `cmd/api` | The HTTP API. Loads configuration from the environment (exits listing invalid variable names), serves the public listener (`HTTP_HOST:HTTP_PORT`, default `127.0.0.1:8080`: `/healthz`, `/readyz`, `/api/v1/health`, `/api/v1/ready`, `/api/v1/version`) and the internal listener (`INTERNAL_HTTP_HOST:INTERNAL_HTTP_PORT`, default `127.0.0.1:9090`: `/metrics`, `/internal/readiness`), and shuts down gracefully on SIGINT/SIGTERM |
| `cmd/fundzimctl` | Operator CLI: `config check`, `migrate up|status|version|down` (down only in development/test), `version`, `healthcheck [url]` (used as the container health check) |

```bash
set -a; . ./.env; set +a
go run ./apps/api/cmd/api
go run ./apps/api/cmd/fundzimctl config check
CGO_ENABLED=0 go build -trimpath -o bin/ ./apps/api/cmd/...      # or: make build
```

Container image: [`deploy/docker/api.Dockerfile`](../../deploy/docker/api.Dockerfile) (distroless, non-root,
both binaries). There is **no worker binary yet**; the worker (job queue, outbox dispatcher) is carried to
Stage 4 ([handover](../../docs/stage-handover/STAGE-3-TO-STAGE-4.md)). Details:
[docs/stage-3/implementation.md](../../docs/stage-3/implementation.md),
[configuration](../../docs/development/configuration.md).
