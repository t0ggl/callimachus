BIN_DIR ?= bin
BUILD_VERSION ?= dev
COMMIT_TAG ?= none
TARGETOS ?= $(GOOS)
TARGETARCH ?= $(GOARCH)
LDFLAGS := -w -s -X main.version=$(BUILD_VERSION) -X main.commit=$(COMMIT_TAG)

.PHONY: test sast test-backend test-frontend install-frontend build build-frontend build-backend

test: sast
	$(MAKE) test-backend test-frontend

sast:
	./scripts/sast.sh

test-backend:
	go vet ./...
	go test -v -race -timeout 5m ./...

test-frontend: install-frontend
	cd web && npm test

install-frontend:
	cd web && npm ci

build: build-frontend
	$(MAKE) build-backend

build-frontend: install-frontend
	cd web && npm run build

build-backend:
	CGO_ENABLED=0 GOOS=$(TARGETOS) GOARCH=$(TARGETARCH) go build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/callimachus ./cmd/callimachus
	CGO_ENABLED=0 GOOS=$(TARGETOS) GOARCH=$(TARGETARCH) go build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/callimachus-worker ./cmd/callimachus-worker
