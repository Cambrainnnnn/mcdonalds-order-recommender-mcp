.PHONY: run run-mcp test vet fmt check

run:
	go run ./cmd/api

run-mcp:
	go run ./cmd/mcp

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

check: test vet
