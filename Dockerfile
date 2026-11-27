# syntax=docker/dockerfile:1@sha256:572cebbc2b15e6f84c077742952674889cb63cd5e9b80abdffcbe587b6f19183

# Stage 1: Build Web UI
FROM --platform=$BUILDPLATFORM node:26.13.1-slim@sha256:abe7e5a82b61a8ef67b315b85497c9e65173387ccf2f4a029248184b8e020be5 AS frontend-builder
RUN apt-get update && apt-get install -y --no-install-recommends make \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app

COPY Makefile ./
COPY web/package.json web/package-lock.json ./web/
RUN make install-frontend

COPY web ./web
RUN make build-frontend

# Stage 2: Build Go Server & Worker
FROM --platform=$BUILDPLATFORM golang:1.27.4@sha256:5f1b98fa02fe396ebce2a0227a0e08309e11792915b9dd2e1f71053d7e7e6666 AS backend-builder
WORKDIR /app

COPY Makefile go.mod go.sum ./
RUN make install-backend

COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg
COPY web/embed.go ./web/embed.go
COPY --from=frontend-builder /app/web/dist ./web/dist

ARG TARGETOS
ARG TARGETARCH
ARG COMMIT_TAG=none
ARG VERSION=dev
ARG BUILD_VERSION=${VERSION}

RUN make build-backend

# Stage 3: Runtime
FROM debian:13.8-slim@sha256:8aa5a7bf5a1955bddbe7d74fcdd0b91ab30e74362a8b73af672b0481b35b44a0 AS runner
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata ffmpeg curl calibre \
    && rm -rf /var/lib/apt/lists/* \
    && install -d -o 1000 -g 1000 /config /books
ENV QT_QPA_PLATFORM=offscreen \
    XDG_CONFIG_HOME=/tmp/callimachus-config \
    XDG_CACHE_HOME=/tmp/callimachus-cache

WORKDIR /app
COPY --from=backend-builder /app/bin/callimachus /usr/local/bin/callimachus
COPY --from=backend-builder /app/bin/callimachus-worker /usr/local/bin/callimachus-worker

VOLUME ["/config", "/books"]
EXPOSE 8080

USER 1000:1000
ENTRYPOINT ["/usr/local/bin/callimachus"]
CMD ["--config", "/config"]
