# syntax=docker/dockerfile:1.29.0@sha256:572cebbc2b15e6f84c077742952674889cb63cd5e9b80abdffcbe587b6f19183

# Stage 1: Build Web UI
FROM --platform=$BUILDPLATFORM node:26.13.1-slim@sha256:e6bedef217677b276c591795e169840edd9096dbf13f29df8ef8d1856b8820bc AS web-builder
RUN apt-get update && apt-get install -y --no-install-recommends make \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY Makefile ./
COPY web/ ./web/
RUN make build-frontend

# Stage 2: Build Go Server & Worker
FROM --platform=$BUILDPLATFORM golang:1.27.4@sha256:205c7636d74e0ffbe3d007162df7d74f859f5a8c8322dfb9f907977686c126cf AS go-builder
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
FROM debian:13.7-slim@sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2 AS runner
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
