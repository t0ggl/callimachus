BIN_DIR ?= bin
VERSION ?= dev
BUILD_VERSION ?= $(VERSION)
COMMIT_TAG ?= none
DOCKER_BUILD_NETWORK ?= default
export GOOS ?= $(TARGETOS)
export GOARCH ?= $(TARGETARCH)
LDFLAGS := -w -s -X main.version=$(BUILD_VERSION) -X main.commit=$(COMMIT_TAG)

.PHONY: test sast test-backend test-frontend test-container install-frontend install-backend build build-frontend build-backend

test: sast
	$(MAKE) install-backend test-backend install-frontend test-frontend

sast:
	./scripts/sast.sh

test-backend:
	go vet ./...
	go test -v -race -timeout 5m ./...

test-frontend:
	npm --prefix web test

test-container:
	docker build --network $(DOCKER_BUILD_NETWORK) -t callimachus-readonly-test .
	bash scripts/test-container.sh callimachus-readonly-test

install-frontend:
	npm --prefix web ci

install-backend:
	go mod download

build: install-frontend install-backend
	$(MAKE) build-frontend
	$(MAKE) build-backend

build-frontend:
	npm --prefix web run build

build-backend:
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/callimachus ./cmd/callimachus
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/callimachus-worker ./cmd/callimachus-worker
