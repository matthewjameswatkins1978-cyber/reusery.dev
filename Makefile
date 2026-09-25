# Reusery.dev developer commands. `make check` runs the full verification.
# On Windows without make, use the equivalent scripts/check.ps1 instead.

GO ?= go
BIN_DIR := bin
APP_NAME := reusery
MAIN := ./cmd/reusery

.PHONY: format test vet lint vuln build run check clean

format:
	gofmt -l -w .

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

lint:
	golangci-lint run ./...

vuln:
	govulncheck ./...

build:
	$(GO) build -o $(BIN_DIR)/$(APP_NAME) $(MAIN)

run:
	$(GO) run $(MAIN)

check: format vet test lint vuln build

clean:
	rm -rf $(BIN_DIR)
