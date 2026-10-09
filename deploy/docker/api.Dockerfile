# FundZim API image: one image, three binaries (api, worker, fundzimctl). The worker runs from the same
# image with entrypoint /usr/local/bin/worker. Non-root, distroless, no shell.
# Build context: repository root.  docker build -f deploy/docker/api.Dockerfile .
FROM golang:1.27.2-alpine3.24 AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-trimpath
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY apps/api ./apps/api
COPY internal ./internal
COPY migrations ./migrations
ARG VERSION=0.0.0-dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown
RUN LDFLAGS="-s -w \
      -X github.com/Fatifizo/fundzim/internal/platform/version.Version=${VERSION} \
      -X github.com/Fatifizo/fundzim/internal/platform/version.Commit=${COMMIT} \
      -X github.com/Fatifizo/fundzim/internal/platform/version.BuildTime=${BUILD_TIME}" && \
    go build -ldflags "$LDFLAGS" -o /out/api ./apps/api/cmd/api && \
    go build -ldflags "$LDFLAGS" -o /out/worker ./apps/api/cmd/worker && \
    go build -ldflags "$LDFLAGS" -o /out/fundzimctl ./apps/api/cmd/fundzimctl

# distroless/static-debian12:nonroot pinned by digest (uid 65532, no shell, no package manager)
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/api /out/worker /out/fundzimctl /usr/local/bin/
USER 65532:65532
EXPOSE 8080 9090 9091
HEALTHCHECK --interval=10s --timeout=4s --start-period=10s --retries=3 \
  CMD ["/usr/local/bin/fundzimctl", "healthcheck", "http://127.0.0.1:8080/healthz"]
ENTRYPOINT ["/usr/local/bin/api"]
