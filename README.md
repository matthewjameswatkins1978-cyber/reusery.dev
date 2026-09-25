# Reusery.dev

**Find what already works. Prove it. Reuse it.**

Reusery is an AI-first engineering reuse and verification system.

Before a human or coding agent writes another version of a solved engineering problem, Reusery should discover what already exists, understand its behavioural contract, inspect evidence and provenance, apply project constraints, and resolve toward one of:

- REUSE
- ADAPT
- DEPEND
- REFERENCE
- BUILD LOCALLY

The registry is memory. The resolver is the product.

## Status

Early design and implementation. The repository currently holds the
engineering foundation and persistence floor: a production-shaped Go HTTP
server with structured logging and health endpoints, a deterministic evidence
evaluator, and PostgreSQL-backed storage for the core domain model. See
[VISION.md](VISION.md), [MODEL.md](MODEL.md), and [RESOLVER.md](RESOLVER.md)
for the product design, and [docs/engineering.md](docs/engineering.md) for
foundation decisions.

## Requirements

- Go 1.27.1 (see [docs/engineering.md](docs/engineering.md) for toolchain notes)
- PostgreSQL (required at runtime; see below)
- `golangci-lint` v2.14.0, `govulncheck` v1.8.0, `sqlc` v1.31.1 and
  `goose` v3.28.0 for the full check
- Docker, only for the integration tests

## Running locally

Start PostgreSQL, then:

```powershell
go run ./cmd/reusery
```

Configuration via environment (see `.env.example`):

```text
REUSERY_HTTP_ADDR=:8080
REUSERY_LOG_LEVEL=info
REUSERY_DATABASE_URL=postgres://reusery:reusery@localhost:5432/reusery?sslmode=disable
```

`REUSERY_DATABASE_URL` is required and never logged. Startup fails clearly if
it is missing, malformed or unreachable.

### Database migrations

Migrations are applied explicitly, never automatically on HTTP startup:

```powershell
goose -dir internal/store/postgres/migrations postgres "$REUSERY_DATABASE_URL" up
```

## Testing

```powershell
go test ./...
```

Integration tests need Docker and use a real ephemeral PostgreSQL via
Testcontainers:

```powershell
go test -tags=integration ./internal/store/postgres/...
```

## Full project check

On Windows (PowerShell 7+):

```powershell
./scripts/check.ps1
```

With `make` (incl. CI on Linux):

```powershell
make check
```

This runs: `gofmt`, `sqlc generate` + drift check, `go vet`, `go test`,
integration tests, `golangci-lint`, `govulncheck`, `go build`.

## Health check

```text
http://localhost:8080/health
http://localhost:8080/ready
```

Both return `{"status":"ok"}` with HTTP 200. `/health` never touches the
database; `/ready` reflects PostgreSQL connectivity and returns
`{"status":"not ready"}` (HTTP 503) when it fails.

## Architecture

```text
cmd        executable entry points (thin main only)
internal   private application implementation
docs       project/engineering documentation
scripts    developer automation
```

- `internal/config` — environment-based configuration.
- `internal/server` — HTTP server construction, routes and lifecycle.
- `internal/version` — build metadata (linker-flag injectable).
- `internal/model` — core domain model (Primitive, Contract, Specimen, Evidence, Resolution).
- `internal/resolver` — deterministic evidence evaluation per contract requirement
  (see [docs/evidence-evaluation.md](docs/evidence-evaluation.md)).
- `internal/store/postgres` — PostgreSQL persistence (pgx pool, Goose
  migrations, hand-written sqlc mapping layer).

Source-shaped project assets live outside Go:

- `primitives/` — primitive definitions.
- `contracts/` — behavioural contract definitions.

## Tooling

Install the pinned developer tools:

```powershell
./scripts/install-tools.ps1
```

Expected versions are recorded in [docs/engineering.md](docs/engineering.md).
