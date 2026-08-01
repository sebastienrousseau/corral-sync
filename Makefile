SHELL := /usr/bin/env bash

BINARY  := corral-sync
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.Version=$(VERSION)

.PHONY: build test test-coverage test-race fuzz vulncheck vet lint format clean install

build:
	go build -ldflags="$(LDFLAGS)" -o $(BINARY) .

install: build
	install -m 0755 $(BINARY) $(HOME)/.local/bin/$(BINARY)

test:
	go test ./...

test-coverage:
	go test ./... -coverprofile=coverage.out
	@test "$$(go tool cover -func=coverage.out | awk '/^total:/ {gsub("%", "", $$3); print $$3}')" = "100.0"

test-race:
	go test -race ./...

fuzz:
	go test -fuzz=FuzzValidateCloneURL -fuzztime=30s ./internal/remote
	go test -fuzz=FuzzVisibilityFromPath -fuzztime=30s ./internal/crawler

vulncheck:
	govulncheck ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

format:
	gofmt -w .

clean:
	rm -f $(BINARY) $(BINARY).exe
	rm -f coverage.out
	rm -rf dist
