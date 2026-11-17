.PHONY: test test-backend test-frontend sast

test: test-backend test-frontend sast

test-backend:
	go vet ./...
	go test -race ./...

test-frontend:
	cd web && npm test

sast:
	./scripts/sast.sh
