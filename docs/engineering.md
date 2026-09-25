# Reusery.dev — Engineering Notes (Packet 1)

Foundation decisions for the bootstrap. Future agents: these choices are
intentional — do not "improve" them back into complexity without a packet
that asks for it.

## Go version

- Adopted: **Go 1.27.1** (current stable release per https://go.dev/dl/?mode=json
  at the time of writing, 2026-09-25).
- The machine's system install was Go 1.27.0 with no admin rights available
  for an MSI upgrade, so the project pins `go 1.27.1` in `go.mod` and relies
  on Go's official toolchain switching (`GOTOOLCHAIN=auto`, the default):
  the first `go` command inside the module automatically downloads and uses
  the go1.27.1 toolchain. CI installs 1.27.1 directly via `actions/setup-go`.
- Module path `github.com/matthewjameswatkins1978-cyber/reusery.dev` is taken
  from the repository's configured Git remote — no organisation was invented.

## Why standard `net/http`

The Packet 1 server needs routing for two endpoints, timeouts and graceful
shutdown. `net/http` (with `http.ServeMux`) covers all of it. No Gin, Echo
or Fiber: a framework would add dependency and CVE surface for zero current
benefit. Revisit only when a later packet demonstrates a concrete need
(e.g. OpenAPI-driven handlers).

## Why `log/slog`

Structured logging is required and `log/slog` is in the standard library:
no dependency, JSON/text handlers built in, level-aware. Nothing else is
needed until a telemetry packet arrives.

## Why no other frameworks/dependencies

`go.mod` has zero third-party requirements by design. PostgreSQL drivers,
migrations, auth, Redis, queues, ORMs, search and telemetry all belong to
later packets. Each future dependency must justify itself against the
standard library first.

## Configuration

`internal/config` reads two environment variables with development defaults:

```text
REUSERY_HTTP_ADDR=:8080
REUSERY_LOG_LEVEL=info
```

No config framework: `os.Getenv` plus a small parser is sufficient.
`ParseLogLevel` falls back to `info` on unknown values so a typo can neither
crash startup nor silently disable logging. Copy `.env.example` to `.env`
for local overrides; `.env` is git-ignored and must never be committed.

## HTTP server

`internal/server` owns construction and lifecycle; `cmd/reusery/main.go`
stays thin (wire config → logger → server → signals).

- Timeouts: read 10s, read-header 5s, write 10s, idle 60s, shutdown 10s.
- `GET /health` — liveness only, never touches external systems.
- `GET /ready` — runs registered `ReadyChecker`s; Packet 1 registers none,
  so it returns ok. Packet 2 adds PostgreSQL as a checker without changing
  the handler shape. A failing checker yields HTTP 503 `{"status":"not ready"}`.
- Shutdown: `signal.NotifyContext` on Ctrl+C/SIGTERM → `Server.Shutdown`
  with a 10s bound → startup and shutdown both logged via `slog`.

## Quality checks

```powershell
gofmt -l -w .            # formatting (write)
go vet ./...             # static analysis
go test ./...            # tests
golangci-lint run ./...  # lint (config: .golangci.yml)
govulncheck ./...        # vulnerability scan
go build ./cmd/reusery   # build
```

- `./scripts/check.ps1` runs all of the above on Windows; `make check`
  is the equivalent for make-based environments and is mirrored in CI.
- `./scripts/install-tools.ps1` installs the pinned tools into `GOPATH/bin`.
- Pinned tool versions:
  - golangci-lint **v2.14.0** (latest release at time of writing)
  - govulncheck **v1.8.0** (`golang.org/x/vuln` release)
- Tools install into `GOPATH/bin` (`C:\Users\Matmus\go\bin` here); that
  directory must be on `PATH` for `golangci-lint`/`govulncheck` to resolve.
- CI (`.github/workflows/ci.yml`) uses current action majors:
  `actions/checkout@v6`, `actions/setup-go@v6` (Go 1.27.1),
  `golangci/golangci-lint-action@v9` (golangci-lint v2.14.0), plus a
  `govulncheck` step.

## Tests

Standard `testing` only. Coverage is behavioural, not numeric:

- `internal/config` — defaults, env overrides, blank-value handling, level parsing.
- `internal/server` — `/health` and `/ready` status codes, JSON bodies,
  content-type headers, and the failing-checker 503 path that Packet 2
  will exercise for real.
