# syntax=docker/dockerfile:1

# Stage 1: Build Web UI
FROM --platform=$BUILDPLATFORM node:20-alpine AS web-builder
WORKDIR /app/web
COPY web/package*.json ./
RUN if [ -f package.json ]; then \
      npm ci; \
    else \
      mkdir -p dist && echo "<!DOCTYPE html><html><body><h1>Callimachus</h1></body></html>" > dist/index.html; \
    fi
COPY web/ ./
RUN if [ -f package.json ]; then npm run build; fi

# Stage 2: Build Go Server & Worker
FROM --platform=$BUILDPLATFORM golang:alpine AS go-builder
ARG TARGETOS
ARG TARGETARCH
ARG COMMIT_TAG
ARG BUILD_VERSION

WORKDIR /app
RUN apk add --no-cache git ca-certificates tzdata
ENV GOTOOLCHAIN=auto

COPY go.mod go.sum* ./
RUN if [ -f go.mod ]; then go mod download; fi

COPY . .
# Copy compiled web assets into embedded directory
COPY --from=web-builder /app/web/dist ./web/dist

RUN if [ -f go.mod ]; then \
      CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
      go build -trimpath -ldflags="-w -s -X main.version=${BUILD_VERSION} -X main.commit=${COMMIT_TAG}" \
      -o /bin/callimachus ./cmd/callimachus && \
      CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
      go build -trimpath -ldflags="-w -s -X main.version=${BUILD_VERSION} -X main.commit=${COMMIT_TAG}" \
      -o /bin/callimachus-worker ./cmd/callimachus-worker; \
    else \
      mkdir -p /bin && echo "#!/bin/sh" > /bin/callimachus && chmod +x /bin/callimachus; \
    fi

# Stage 3: Runtime
FROM debian:bookworm-slim AS runner
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
