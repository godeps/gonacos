.PHONY: build test fmt vet run contract-generate contract-verify

build:
	GOWORK=off go build ./cmd/gonacos ./cmd/gonacos-contract

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
