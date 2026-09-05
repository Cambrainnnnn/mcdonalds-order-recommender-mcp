.PHONY: run test vet fmt check

run:
	go run ./cmd/api

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

check: test vet
