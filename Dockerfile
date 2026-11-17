# syntax=docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e

# Stage 1: Build Web UI
FROM --platform=$BUILDPLATFORM node:20-alpine@sha256:fb4cd12c85ee03686f6af5362a0b0d56d50c58a04632e6c0fb8363f609372293 AS web-builder
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
FROM --platform=$BUILDPLATFORM golang:alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS go-builder
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
FROM alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc AS runner
RUN apk add --no-cache ca-certificates tzdata ffmpeg curl

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
