.PHONY: run run-mcp run-mcp-http catalog-export test vet fmt check

catalog-export:
	go run ./cmd/catalog -output data/catalog.json

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
