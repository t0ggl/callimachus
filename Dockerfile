# syntax=docker/dockerfile:1

# Stage 1: Build Web UI
FROM --platform=$BUILDPLATFORM node:26-slim AS web-builder
RUN apt-get update && apt-get install -y --no-install-recommends make \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY Makefile ./
COPY web/ ./web/
RUN make build-frontend

# Stage 2: Build Go Server & Worker
FROM --platform=$BUILDPLATFORM golang:1.26 AS go-builder
WORKDIR /app

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
COPY --from=web-builder /app/web/dist ./web/dist

ARG TARGETOS
ARG TARGETARCH
ARG COMMIT_TAG
ARG BUILD_VERSION
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build,id=go-build-${TARGETOS}-${TARGETARCH} \
    make build-backend BIN_DIR=/bin

# Stage 3: Runtime
FROM debian:12-slim AS runner
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata ffmpeg curl calibre \
    && rm -rf /var/lib/apt/lists/*
ENV QT_QPA_PLATFORM=offscreen

WORKDIR /app
COPY --from=go-builder /bin/callimachus /usr/local/bin/callimachus
COPY --from=go-builder /bin/callimachus-worker /usr/local/bin/callimachus-worker

VOLUME ["/config", "/books"]
EXPOSE 8080

# Container runs as root by default to preserve host-mounted /config and /books permissions.
# nosemgrep: dockerfile.security.missing-user-entrypoint.missing-user-entrypoint
ENTRYPOINT ["/usr/local/bin/callimachus"]
# nosemgrep: dockerfile.security.missing-user.missing-user
CMD ["--config", "/config"]
