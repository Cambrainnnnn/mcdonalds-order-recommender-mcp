.PHONY: run run-mcp run-mcp-http test vet fmt check

run:
	go run ./cmd/api

run-mcp:
	go run ./cmd/mcp

run-mcp-http:
	go run ./cmd/mcp-http

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

check: test vet
