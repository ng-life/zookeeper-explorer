GO ?= go
COMMAND := ./cmd/zookeeper-explorer
BINARY := dist/zookeeper-explorer

.PHONY: build run test vet fmt check

build:
	$(GO) build -trimpath -o $(BINARY) $(COMMAND)

run:
	$(GO) run $(COMMAND) $(ARGS)

test:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

check: test vet
