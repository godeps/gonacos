.PHONY: build test fmt vet run contract-generate contract-verify web build-web

# Build the React SPA and embed it into the gonacos binary.
web:
	bash pkg/web/build-web.sh

# Build gonacos binaries.
build:
	GOWORK=off go build ./cmd/gonacos ./cmd/gonacos-contract

# Full build: web assets first, then go binaries.
build-web: web build

test:
	GOWORK=off go test ./...

fmt:
	gofmt -s -w .

vet:
	GOWORK=off go vet ./...

run:
	GOWORK=off go run ./cmd/gonacos

contract-generate:
	GOWORK=off go run ./cmd/gonacos-contract -write

contract-verify:
	GOWORK=off go run ./cmd/gonacos-contract -verify
