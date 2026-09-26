# Reusery.dev developer commands. `make check` runs the full verification.
# On Windows without make, use the equivalent scripts/check.ps1 instead.

GO ?= go
BIN_DIR := bin
APP_NAME := reusery
MAIN := ./cmd/reusery
SQLC_OUT := internal/store/postgres/sqlc

.PHONY: format generate generate-check openapi openapi-check mcp-contract mcp-contract-check integration test vet lint vuln build build-all run check clean

format:
	gofmt -s -l -w .

# Regenerate sqlc code from migrations and queries.
generate:
	sqlc generate

# Fail if committed generated code drifts from the schema/queries.
generate-check: generate
	git diff --exit-code -- $(SQLC_OUT)

# Regenerate the checked-in OpenAPI 3.1 contract from the registered routes.
openapi:
	$(GO) run ./cmd/openapi -write openapi/reusery-v1.json

# Fail if API types or routes changed without regenerating the contract.
openapi-check:
	$(GO) run ./cmd/openapi -check openapi/reusery-v1.json

# Regenerate the checked-in MCP tool contract from the registered server.
mcp-contract:
	$(GO) run ./cmd/mcpcontract -write mcp/reusery-tools-v1.json

# Fail if a tool name, schema or annotation changed without regenerating it.
mcp-contract-check:
	$(GO) run ./cmd/mcpcontract -check mcp/reusery-tools-v1.json

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

build-all:
	$(GO) build ./...

run:
	$(GO) run $(MAIN)

check: format generate-check openapi-check mcp-contract-check vet test integration lint vuln build build-all

clean:
	rm -rf $(BIN_DIR)
