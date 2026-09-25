# Reusery.dev developer commands. `make check` runs the full verification.
# On Windows without make, use the equivalent scripts/check.ps1 instead.

GO ?= go
BIN_DIR := bin
APP_NAME := reusery
MAIN := ./cmd/reusery
SQLC_OUT := internal/store/postgres/sqlc

.PHONY: format generate generate-check integration test vet lint vuln build run check clean

format:
	gofmt -s -l -w .

# Regenerate sqlc code from migrations and queries.
generate:
	sqlc generate

# Fail if committed generated code drifts from the schema/queries.
generate-check: generate
	git diff --exit-code -- $(SQLC_OUT)

test:
	$(GO) test ./...

# Requires Docker; uses a real ephemeral PostgreSQL via Testcontainers.
integration:
	$(GO) test -tags=integration ./...

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

check: format generate-check vet test integration lint vuln build

clean:
	rm -rf $(BIN_DIR)
