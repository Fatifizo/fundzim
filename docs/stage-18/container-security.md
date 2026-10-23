# Stage 18 — Container security

## 1. Images

| Image | Base | Runtime user | Notes |
|---|---|---|---|
| `fundzim/api` (api, worker, fundzimctl) | build `golang:1.27.3-alpine3.24@sha256:b8c9b8…`, runtime `gcr.io/distroless/static-debian12:nonroot@sha256:afa5c8…` | 65532:65532 | static binaries (CGO off, `-trimpath`), no shell; HEALTHCHECK via `fundzimctl healthcheck`; Trivy: 0 CRITICAL/HIGH |
| `fundzim/web` | `node:24.21.0-bookworm-slim@sha256:d6aa75…` | node (1000) | **Stage 18:** `apt-get upgrade` at build; npm, npx, corepack and yarn removed from the runtime stage; app files root-owned, read-only for the runtime user |
| Third-party | postgres, valkey, garage, curl, openbao, mailpit, clamav | — | **Stage 18:** all pinned by digest in `compose.yaml` |

No credentials are embedded: no secret build arguments (test), `.env` is never copied. Image updates are deliberate:
refresh a digest, rebuild, let the CI Trivy gate and SBOM record the change (supply-chain-security.md §4).

## 2. Runtime hardening (`compose.yaml`, Stage 18)

| Setting | FundZim containers (api, api-2, worker, migrate, web) | Third-party |
|---|---|---|
| `cap_drop: [ALL]` | yes | yes; `cap_add` only what an entrypoint needs to switch users: postgres and clamav (CHOWN, DAC_OVERRIDE, FOWNER, SETGID, SETUID), openbao (SETGID, SETUID); Valkey runs directly as uid 999 with **no** capability |
| `security_opt: no-new-privileges` | yes | yes |
| `read_only` root filesystem | yes (`/tmp` tmpfs; web also `.next/cache` tmpfs) | Valkey, Garage, curl, mailpit, secrets-init; not postgres/clamav/openbao (writable state) |
| `pids_limit` / `mem_limit` | 512 / 256 MB–768 MB | 512 / per service |
| Health checks | API (Dockerfile), worker, web (Dockerfile) | postgres, valkey, garage, openbao, clamav |
| Secrets on the command line | none (the Valkey password moved from argv to a tmpfs config file: `/proc/1/cmdline` shows `valkey-server *:6379` only) | — |

Exceptions (documented): PostgreSQL, ClamAV and OpenBao keep a few capabilities for their entrypoints and writable
state; ClamAV needs Internet egress for signatures. Verified after the change: all containers healthy; `docker inspect`
shows read-only roots, `CapDrop [ALL]` and `no-new-privileges` on api, worker and web; the full real-stack E2E suite and
integration tests run against the hardened stack (security-testing.md).

## 3. Enforcement

`internal/deploytest` (runs with the Go unit tests in CI) fails when a service loses `no-new-privileges`, `cap_drop
[ALL]`, limits or loopback-only ports, adds a capability outside its allow-list, mounts the Docker socket, runs
privileged or in the host network, when a FundZim container is not read-only, a third-party image is not
digest-pinned, a Dockerfile `FROM` is not pinned, a runtime stage runs as root, or a build argument looks like a secret.
Mutation checks: removing `no-new-privileges` from the anchor produced 13 failures; unpinning PostgreSQL produced the
expected failure.

## 4. Scanning

Trivy v0.75.0 (checksum-verified) scans both images in CI: full report kept as an artifact; **fixable** CRITICAL/HIGH
fail the build. Local results (2026-10-23): API image 5 UNKNOWN, 0 CRITICAL/HIGH; web image before the change 6
CRITICAL / 69 HIGH (5/12 fixable, incl. two Next.js CRITICAL advisories), after: 1 CRITICAL / 56 HIGH, **0 fixable**
(Debian bookworm with no published fix: zlib CVE-2023-45853, util-linux CVE-2026-53613/53615/76642/78408/78409/78410,
ncurses CVE-2025-69720, systemd CVE-2026-16742, gzip CVE-2026-41992, acl CVE-2026-54369, perl CVE-2026-9538) —
KI-S18-10.
