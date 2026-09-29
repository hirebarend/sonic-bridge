# sonic-bridge build entry points. `make help` lists them.

SHELL := /bin/bash
.DEFAULT_GOAL := help

RELAY_HTTP_ADDR ?= 127.0.0.1:8080
RELAY_TCP_ADDR  ?= 127.0.0.1:9000
BIN_DIR         := bin

.PHONY: help
help:
	@grep -hE '^[a-z][a-zA-Z0-9_-]*:.*## ' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*## "} {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: test
test: ## Run the Go tests with the race detector
	go test -race ./...

.PHONY: check
check: ## Verify formatting, vet, tests, the cgo-free build and the player syntax
	@test -z "$$(gofmt -l cmd internal)" || { echo "gofmt needed:"; gofmt -l cmd internal; exit 1; }
	go vet ./...
	go test -race ./...
	CGO_ENABLED=0 go build ./...
	node --check internal/relay/web/codec.js
	node --check internal/relay/web/playback-processor.js

.PHONY: wire
wire: ## Prove the Go, C++ and JavaScript wire implementations agree
	./scripts/check-wire-format.sh

.PHONY: build
build: ## Build the relay and console binaries
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/server ./cmd/server
	go build -o $(BIN_DIR)/console ./cmd/console

.PHONY: server
server: ## Run the relay
	go run ./cmd/server --http-addr $(RELAY_HTTP_ADDR) --tcp-source-addr $(RELAY_TCP_ADDR)

.PHONY: console
console: ## Run the console source against a local relay
	go run ./cmd/console --server $(RELAY_TCP_ADDR)

.PHONY: tone
tone: ## Run the console source with its built-in test tone instead of a microphone
	go run ./cmd/console --server $(RELAY_TCP_ADDR) --input tone

.PHONY: devices
devices: ## List the capture devices this host exposes
	go run ./cmd/console --list-devices

.PHONY: image
image: ## Build the deployable container image
	docker build -t sonic-bridge:local .

.PHONY: clean
clean: ## Remove build output
	rm -rf $(BIN_DIR)
