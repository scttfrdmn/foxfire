.PHONY: test lint fmt build cover

build:
	go build ./...
	go build -o bin/foxfire ./cmd/foxfire

test:
	go test -race ./...

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

fmt:
	gofmt -w .

lint:
	go vet ./...
	golangci-lint run
