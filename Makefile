SHELL := /usr/bin/env bash

BINARY  := corral-sync
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.Version=$(VERSION)

.PHONY: build test test-race vet lint format clean install

build:
	go build -ldflags="$(LDFLAGS)" -o $(BINARY) .

install: build
	install -m 0755 $(BINARY) $(HOME)/.local/bin/$(BINARY)

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

format:
	gofmt -w .

clean:
	rm -f $(BINARY) $(BINARY).exe
	rm -rf dist
