# Reusery.dev

Reusery.dev — currently a bootstrap/engineering foundation (Packet 1).

## Requirements

- Go 1.27.1 (see [docs/engineering.md](docs/engineering.md) for toolchain notes)
- `golangci-lint` v2.14.0 and `govulncheck` for the full check (see Tooling below)

## Running locally

```powershell
go run ./cmd/reusery
```

Configuration via environment (see `.env.example`):

```text
REUSERY_HTTP_ADDR=:8080
REUSERY_LOG_LEVEL=info
```

## Testing

```powershell
go test ./...
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

This runs: `gofmt`, `go vet`, `go test`, `golangci-lint`, `govulncheck`, `go build`.

## Health check

```text
http://localhost:8080/health
http://localhost:8080/ready
```

Both return `{"status":"ok"}` with HTTP 200. `/ready` will gain a
PostgreSQL readiness check in Packet 2.

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

## Tooling

Install the pinned developer tools:

```powershell
./scripts/install-tools.ps1
```

Expected versions are recorded in [docs/engineering.md](docs/engineering.md).
