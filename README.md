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
engineering foundation, persistence floor, the first complete resolution
slice, the first public discovery layer, and natural-language intent
normalisation: a production-shaped Go HTTP server with structured logging and
health endpoints, a deterministic evidence evaluator and resolver kernel,
PostgreSQL-backed storage for the core domain model, a CLI that can seed a
catalogue, resolve a structured request and inspect the stored decision,
`reusery discover`, which queries real public provider APIs (pkg.go.dev,
GitHub) for plausible candidates, and `reusery normalize`, which turns an
ordinary engineering request into an inspectable provisional contract draft.
See [VISION.md](VISION.md), [MODEL.md](MODEL.md), and [RESOLVER.md](RESOLVER.md)
for the product design, and [docs/engineering.md](docs/engineering.md) for
foundation decisions.

## Requirements

- Go 1.27.1 (see [docs/engineering.md](docs/engineering.md) for toolchain notes)
- PostgreSQL (required for serve, seed, resolve, resolution and discover;
  **not** required for `normalize` or `normalize-eval`)
- `golangci-lint` v2.14.0, `govulncheck` v1.8.0, `sqlc` v1.31.1 and
  `goose` v3.28.0 for the full check
- Docker, only for the integration tests
- An OpenAI API key, only for `normalize` and `normalize-eval`

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
REUSERY_GITHUB_TOKEN=            # optional, for GitHub discovery rate limits
REUSERY_OPENAI_API_KEY=          # optional, only for normalize / normalize-eval
REUSERY_OPENAI_MODEL=gpt-5.6-luna # optional model override
```

`REUSERY_DATABASE_URL` is required by every database-backed command and never
logged. Startup fails clearly if it is missing, malformed or unreachable.
`REUSERY_GITHUB_TOKEN` is optional and only raises GitHub's rate limits for
discovery; discovery works without it and the token is never logged or
persisted. `REUSERY_OPENAI_API_KEY` is optional globally and required **only**
by `reusery normalize` and `reusery normalize-eval`; `reusery serve`,
`seed`, `discover`, `resolve` and `resolution` keep working with no model key
at all. The key is never logged, never persisted and never included in an
error.

### Database migrations

Migrations are applied explicitly, never automatically on HTTP startup:

```powershell
goose -dir internal/store/postgres/migrations postgres "$REUSERY_DATABASE_URL" up
```

## First resolution workflow

The end-to-end slice, from a clean database to an inspected decision:

```powershell
# 1. migrations
goose -dir internal/store/postgres/migrations postgres "$REUSERY_DATABASE_URL" up

# 2. seed the development catalogue
go run ./cmd/reusery seed --root . --manifest catalogue/dev/bounded-subprocess/manifest.yaml

# 3. resolve the example request (partial candidate first, complete second)
go run ./cmd/reusery resolve --request examples/resolve-bounded-subprocess.json --format text

# 4. inspect the stored resolution
go run ./cmd/reusery resolution --id 1 --format json

# 5. the BUILD LOCALLY example
go run ./cmd/reusery resolve --request examples/resolve-bounded-subprocess-build-local.json --format json
```

Expected outcomes: step 3 returns `depend` (the partial fixture is rejected,
the complete dependency fixture is selected) and step 5 returns
`build_locally`. Both resolutions are persisted and survive restarts.

> The development catalogue candidates are deterministic **fixtures**. They are
> NOT recommendations about real public software — they are behavioural
> evidence for tests. The live public discovery workflow below is a different
> thing: it finds *plausible* real-world candidates and deliberately supplies
> no behavioural evidence.

## Public discovery workflow

Discovery queries real public provider APIs for the same primitive, within
explicit time, request, response-size and result budgets:

```powershell
# 1. migrations + seed (same as above)

# 2. discover, human-readable
go run ./cmd/reusery discover --root . `
  --profile discovery/process/bounded-subprocess-v1.yaml --format text

# 3. discover, machine-readable
go run ./cmd/reusery discover --root . `
  --profile discovery/process/bounded-subprocess-v1.yaml --format json
```

Expected: provider reports for `pkg.go.dev`, `github-repositories` and
`github-code`, plus plausible package/repository/source candidates stored in
PostgreSQL with attributable `INFO`/`UNKNOWN` observations.

Two things to keep straight:

- **`seed`** loads deterministic development fixtures so tests are stable.
- **`discover`** talks to the public internet and stores plausible candidates.

Discovery never resolves: it does not pick a winner, does not persist a
`Resolution` and cannot turn provider metadata into a satisfied contract
requirement. See [docs/public-discovery.md](docs/public-discovery.md).

> Automated tests never call the public internet — provider tests use local
> `httptest` fixtures, so a provider outage cannot make CI red. Live calls are
> a manual smoke procedure.

## Natural-language intent workflow

Describe an engineering need in ordinary language and get back an inspectable
provisional contract draft. No YAML or JSON is required from the user.

```powershell
# 1. from a file, human-readable (no database needed)
go run ./cmd/reusery normalize `
  --file examples/normalize-bounded-subprocess.txt `
  --format text

# 2. from the command line, machine-readable (no database needed)
go run ./cmd/reusery normalize `
  --text "I need safe subprocess execution with bounded output and cancellation" `
  --format json

# 3. the deterministic evaluation corpus (PAID model calls, no database)
go run ./cmd/reusery normalize-eval `
  --corpus evals/intent/v1.yaml `
  --format text
```

What comes back is a status — `ready`, `needs_clarification` or
`unsupported` — plus a `model.Primitive`, a `model.Contract` with ordered
requirement IDs, explicit constraints, assumptions and material ambiguities,
and reproducibility metadata (provider, model, prompt version, schema version,
response ID, token usage, repair count).

`normalize` **normalises the question; it does not answer it**. It creates no
evidence, recommends no candidate, produces no resolver outcome, and the
generated contract is never persisted, never discovered against and never
resolved automatically in this packet. A materially ambiguous request comes
back as `needs_clarification` with the ambiguity spelled out rather than
silently guessed.

> `normalize`, `discover` and `resolve` are still **separate explicit phases**.
> There is no one-command product yet, and there is deliberately no automatic
> translation of model output into discovery queries or resolutions.

> `normalize` and `normalize-eval` need no PostgreSQL. CI never calls OpenAI
> and never needs `REUSERY_OPENAI_API_KEY`: adapter tests use `httptest` and
> normaliser tests use a fake provider. See
> [docs/intent-normalisation.md](docs/intent-normalisation.md).

> The default model is `gpt-5.6-luna`; it must be available on your OpenAI
> account. If the provider reports `model_not_found`, set
> `REUSERY_OPENAI_MODEL` to a model your account exposes.

## Testing

```powershell
go test ./...
```

Integration tests need Docker and use a real ephemeral PostgreSQL via
Testcontainers:

```powershell
go test -tags=integration ./...
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
- `internal/resolver` — deterministic evidence evaluation and the resolution
  kernel (see [docs/evidence-evaluation.md](docs/evidence-evaluation.md) and
  [docs/resolver-kernel.md](docs/resolver-kernel.md)).
- `internal/catalog` — strict repository-authored YAML catalogue loading and
  seeding.
- `internal/discovery` — bounded public discovery: profiles, budgets, provider
  interface, evidence rules and persistence (see
  [docs/public-discovery.md](docs/public-discovery.md)).
- `internal/intent` — natural-language intent normalisation: domain types,
  versioned prompt and schema, deterministic validation, the single bounded
  repair, deterministic identifiers and the evaluation harness (see
  [docs/intent-normalisation.md](docs/intent-normalisation.md));
  `internal/intent/providers/openai` is the first model adapter.
- `internal/cli` — the `reusery` commands (serve, seed, resolve, resolution,
  discover, normalize, normalize-eval).
- `internal/store/postgres` — PostgreSQL persistence (pgx pool, Goose
  migrations, hand-written sqlc mapping layer).

Source-shaped project assets live outside Go:

- `primitives/` — primitive definitions.
- `contracts/` — behavioural contract definitions.
- `catalogue/` — development seed bundles (fixtures, not recommendations).
- `discovery/` — public discovery profiles.
- `examples/` — example resolve requests and an example intent request.
- `evals/` — deterministic evaluation corpora.

## Tooling

Install the pinned developer tools:

```powershell
./scripts/install-tools.ps1
```

Expected versions are recorded in [docs/engineering.md](docs/engineering.md).
