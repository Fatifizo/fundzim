# syntax=docker/dockerfile:1
#
# FundZim web (Next.js standalone) image.
#
# Build context: apps/web   (self-contained: has its own package-lock.json)
#   docker build -f deploy/docker/web.Dockerfile -t fundzim-web:dev apps/web
#
# Runtime environment (read at REQUEST/START time, never baked into the image):
#   API_BASE_URL   REQUIRED. Origin of the Go API, e.g. http://api:8080. The server refuses to start
#                  without it (NODE_ENV=production). Server-only; never sent to browsers.
#   WEB_TRUSTED_PROXY_CIDRS  optional, default empty. Comma-separated CIDRs of reverse proxies in FRONT of
#                  this container whose X-Forwarded-For may be used to find the client address forwarded to
#                  the API (right-most untrusted entry). Empty = the TCP peer is the client and any
#                  client-supplied X-Forwarded-For is ignored. Invalid values stop the server at start.
#   PORT           default 3000
#   HOSTNAME       default 0.0.0.0
# No build args are required. Do not pass secrets as build args: anything NEXT_PUBLIC_* is compiled into
# the public JS bundle.
#
# Pinned by digest (Stage 18, reproducible builds; scripts/ci/check-compose.py refuses an unpinned FROM). The tag is
# re-pushed for OS security updates: refresh the digest deliberately (docs/stage-18/supply-chain-security.md §4).
ARG NODE_IMAGE=node:24.21.0-bookworm-slim@sha256:d6aa754f16b3197301076f047b5def2f02ea1dbbc2ca920407d46d7ec7f87b20

# ---- deps: install exactly what the lockfile says ---------------------------------------------------
FROM ${NODE_IMAGE} AS deps
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund

# ---- build: produce .next/standalone ---------------------------------------------------------------
FROM ${NODE_IMAGE} AS build
WORKDIR /app
ENV NEXT_TELEMETRY_DISABLED=1
COPY --from=deps /app/node_modules ./node_modules
COPY . .
# `public/` may be absent (empty directories are not tracked by git).
RUN mkdir -p public && npm run build

# ---- runtime: minimal, non-root --------------------------------------------------------------------
FROM ${NODE_IMAGE} AS runtime
# Stage 18 (docs/stage-18/container-security.md): apply the Debian security updates published since the base image was
# built, and remove the package managers the server never uses (npm, npx, corepack and their bundled dependencies were
# most of the image's known vulnerabilities). The SBOM built in CI records the exact package versions of each image.
RUN apt-get update \
 && apt-get upgrade -y --no-install-recommends \
 && rm -rf /var/lib/apt/lists/* \
 && rm -rf /usr/local/lib/node_modules/npm /usr/local/lib/node_modules/corepack \
           /usr/local/bin/npm /usr/local/bin/npx /usr/local/bin/corepack /opt/yarn-* /usr/local/bin/yarn /usr/local/bin/yarnpkg
WORKDIR /app
ENV NODE_ENV=production \
    NEXT_TELEMETRY_DISABLED=1 \
    HOSTNAME=0.0.0.0 \
    PORT=3000

# Application files stay root-owned (read-only for the runtime user). Only the Next.js cache directory
# is writable. (Checked outside Docker: the standalone server serves every Stage 3 route from a
# read-only tree.)
COPY --from=build /app/.next/standalone ./
COPY --from=build /app/.next/static ./.next/static
COPY --from=build /app/public ./public
RUN mkdir -p .next/cache && chown node:node .next/cache

# `node` (uid 1000) is the unprivileged user shipped with the official image.
USER node
EXPOSE 3000

# Liveness only: /healthz does not call the API. Uses node (no curl/wget in slim images).
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD ["node", "-e", "fetch('http://127.0.0.1:' + (process.env.PORT || 3000) + '/healthz').then(r => process.exit(r.ok ? 0 : 1)).catch(() => process.exit(1))"]

CMD ["node", "server.js"]
