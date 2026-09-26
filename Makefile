VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PREFIX ?= $(HOME)/.local

.PHONY: build install test vet fmt check clean

build:
	go build -ldflags "$(LDFLAGS)" -o hatch .

install:
	go build -ldflags "$(LDFLAGS)" -o $(PREFIX)/bin/hatch .

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

check: fmt vet test

clean:
	rm -f hatch
	rm -rf dist
