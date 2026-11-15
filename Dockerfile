# syntax=docker/dockerfile:1

# Stage 1: Build Web UI
FROM --platform=$BUILDPLATFORM node:20-alpine AS web-builder
WORKDIR /app/web
COPY web/package*.json web/pnpm-lock.yaml* ./
RUN if [ -f package.json ]; then \
      corepack enable && pnpm install --frozen-lockfile; \
    else \
      mkdir -p dist && echo "<!DOCTYPE html><html><body><h1>Callimachus</h1></body></html>" > dist/index.html; \
    fi
COPY web/ ./
RUN if [ -f package.json ]; then pnpm run build; fi

# Stage 2: Build Go Server & Worker
FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS go-builder
ARG TARGETOS
ARG TARGETARCH
ARG COMMIT_TAG
ARG BUILD_VERSION

WORKDIR /app
RUN apk add --no-cache git ca-certificates tzdata

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
FROM alpine:3.20 AS runner
RUN apk add --no-cache ca-certificates tzdata ffmpeg curl

WORKDIR /app
COPY --from=go-builder /bin/callimachus /usr/local/bin/callimachus
COPY --from=go-builder /bin/callimachus-worker /usr/local/bin/callimachus-worker

VOLUME ["/config", "/books"]
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/callimachus"]
CMD ["--config", "/config"]
