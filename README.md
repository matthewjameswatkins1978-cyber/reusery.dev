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
slice, the first public discovery layer, natural-language intent
normalisation, and the evidence/policy/quality layer: a production-shaped Go
HTTP server with structured logging and health endpoints, a deterministic
evidence evaluator and resolver kernel, PostgreSQL-backed storage for the core
domain model, a CLI that can seed a catalogue, resolve a structured request and
inspect the stored decision, `reusery discover`, which queries real public
provider APIs (pkg.go.dev, GitHub) for plausible candidates, `reusery enrich`,
which records attributable licence/advisory/dependency/maintenance metadata,
`reusery choose`, which compares candidates under an explicit policy and
returns either a justified resolution or an honest `needs_verification`, and
`reusery normalize`, which turns an ordinary engineering request into an
inspectable provisional contract draft, `reusery mcp`, which serves the Model
Context Protocol over stdio so an agent can check before it rebuilds, and
`reusery project`, which fingerprints a project's manifests and remembers
explicit, reversible decisions about it — context that may change fit but never
truth.
See [VISION.md](VISION.md), [MODEL.md](MODEL.md), and [RESOLVER.md](RESOLVER.md)
for the product design, and [docs/engineering.md](docs/engineering.md) for
foundation decisions.

## Requirements

- Go 1.27.1 (see [docs/engineering.md](docs/engineering.md) for toolchain notes)
- PostgreSQL (required for serve, seed, resolve, resolution, discover, enrich
  and choose; **not** required for `normalize` or `normalize-eval`)
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
REUSERY_OPENAI_MODEL=gpt-6-luna    # optional model override
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

## Enrichment and policy workflow

Three further phases sit between "discovery found these" and "here is the route
Reusery can justify":

```powershell
# 1. enrich: record attributable metadata observations (needs the internet)
go run ./cmd/reusery enrich --root . `
  --request examples/enrich-bounded-subprocess.json --format text

# 2. choose: compare candidates under an explicit policy (OFFLINE)
go run ./cmd/reusery choose --root . `
  --request examples/quality-bounded-subprocess.json `
  --policy policies/public-go-baseline-v1.yaml --format text

# 3. choose again with structured "Not quite" feedback (still offline)
go run ./cmd/reusery choose --root . `
  --request examples/quality-bounded-subprocess.json `
  --policy policies/public-go-baseline-v1.yaml `
  --feedback examples/quality-feedback-not-quite.json --format json
```

`discover`, `enrich` and `choose` are **separate phases**:

| Phase | Network | Output |
| --- | --- | --- |
| `discover` | yes | plausible specimens + relevance observations |
| `enrich` | yes | attributable `INFO`/`UNKNOWN` metadata observations |
| `choose` | **never** | shortlist + dispositions, and either a `Resolution` or `needs_verification` |

Copy the specimen IDs printed by `discover` into your `--request` file: the
example files under `examples/` are structural templates, and
`quality-bounded-subprocess.json` ships with the deterministic seed fixtures so
it works immediately after `seed`.

What `choose` will and will not say:

- **direct use** (`reuse`/`adapt`/`depend`) only when every required behavioural
  requirement is actually satisfied *and* policy permits it;
- **`reference`** when a candidate is relevant, attributable and policy-clean,
  with `unknowns` preserved and an explicit statement that contract
  satisfaction is **not** established;
- **`needs_verification`** — with **nothing persisted** — when plausible
  candidates exist but required behaviour is unknown. Absence of evidence never
  becomes "build it locally";
- **`build_locally`** only with a specific reason: no candidates, all denied by
  policy, all explicitly failed, or all excluded by feedback.

There is no quality score, no confidence value, no popularity ranking and no
legal-advice or security-proof language anywhere in the output. See
[docs/evidence-policy-resolution.md](docs/evidence-policy-resolution.md).

> Automated tests never call deps.dev or GitHub: provider tests use `httptest`,
> `choose` is offline by construction, and CI needs no provider credentials.
> Live enrichment is a manual smoke procedure.

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

> The default model is `gpt-6-luna` — the model Packet 6's release-gate corpus
> runs were validated against. It must still be available on your OpenAI
> account; if the provider reports `model_not_found`, set
> `REUSERY_OPENAI_MODEL` to a model your account exposes.

## Project context workflow

Reusery can fingerprint the project you are deciding for and remember explicit,
reversible choices about it:

```bash
# 1. fingerprint this checkout (never stores the path)
go run ./cmd/reusery project scan --root . --format json

# 2. inspect what is remembered
go run ./cmd/reusery project show --project-id project/go/<sha> --format json

# 3. decide with that context; without --project-id this is the plain decision
go run ./cmd/reusery choose `
  --request examples/quality-bounded-subprocess.json `
  --policy  policies/public-go-baseline-v1.yaml `
  --project-id project/go/<sha> `
  --format json

# 4. remember one explicit decision, then revoke it again
go run ./cmd/reusery project remember `
  --project-id project/go/<sha> `
  --primitive-id process/bounded-subprocess `
  --candidate-id fixture/process/bounded-subprocess/complete-dependency `
  --reason not_quite
go run ./cmd/reusery project history --project-id project/go/<sha>
go run ./cmd/reusery project forget --project-id project/go/<sha> --preference-id 1

# 5. a public repository, read without any credential
go run ./cmd/reusery project scan --source github_public --github owner/repo
```

**Project context may change fit. It must not change truth.** It can add a
review requirement, add an inspectable trade-off, or break a tie among
candidates that are already eligible — nothing else. Preferences are created
only by an explicit `remember`; `refine` feedback and reported outcomes never
become memory. Full reference: [docs/project-context.md](docs/project-context.md).

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

## HTTP API

`reusery serve` also exposes a stable, versioned HTTP/JSON API with a
generated OpenAPI 3.1 contract. The CLI and HTTP are two clients of the same
application services — HTTP owns no product logic. Full reference:
[docs/http-api.md](docs/http-api.md).

```bash
# start (external HTTP operations are OFF by default)
reusery serve

# inspect the contract
curl http://localhost:8080/openapi.json
# browser documentation
start http://localhost:8080/docs
```

Product operations:

| Operation | Request |
| --- | --- |
| `POST /v1/normalize` | `{"input":"I need..."}` |
| `POST /v1/discover` | structured Packet 5 discovery profile |
| `POST /v1/enrich` | `{"specimen_ids":["..."]}` |
| `POST /v1/resolve` | primitive/contract/candidates + structured policy |
| `POST /v1/refine` | the same + base policy + complete feedback history |
| `GET /v1/primitives?id=…` `GET /v1/contracts?id=…` `GET /v1/specimens?id=…` | opaque id query parameter |
| `GET /v1/evidence?subject_id=…&limit=50&cursor=…` | bounded cursor pagination |
| `GET /v1/resolutions/{id}` | persisted decision, no re-evaluation |

```powershell
# Windows PowerShell examples
Invoke-RestMethod -Method Post -Uri http://localhost:8080/v1/normalize `
  -ContentType 'application/json' -Body '{"input":"I need a Go child-process runner"}'

# external operations are disabled by default
Invoke-RestMethod -Method Post -Uri http://localhost:8080/v1/normalize `
  -ContentType 'application/json' -Body '{"input":"..."}'
# -> 503 {"code":"external_operations_disabled"}

# opt in for the HTTP model/discovery/enrichment routes only
$env:REUSERY_API_ENABLE_EXTERNAL_OPERATIONS = 'true'
reusery serve
```

Key semantics, all documented in the contract:

- the four stages stay explicit — there is no hidden normalize→discover→
  enrich→resolve chain
- `resolved` and `needs_verification` are both HTTP 200; `needs_verification`
  returns null `resolution_id`/`resolution` and persists nothing
- REFERENCE keeps its honest meaning and never claims contract satisfaction
- the policy is supplied as structured JSON, never as a filesystem path
- external HTTP operations are **off by default**; the CLI is unaffected
- there is no authentication yet and this is **not** production
  internet-ready

```bash
# regenerate / verify the checked-in contract
go run ./cmd/openapi -write openapi/reusery-v1.json
go run ./cmd/openapi -check  openapi/reusery-v1.json
```

## Agent / MCP

Reusery can serve the Model Context Protocol directly so a coding agent can
check for an existing engineering route **before** writing code:

```bash
reusery mcp                              # stdio only; requires PostgreSQL
reusery mcp --project-root .             # ...and a local project to fingerprint
```

Generic agent-host configuration:

```json
{
  "mcpServers": {
    "reusery": { "command": "reusery", "args": ["mcp", "--project-root", "."] }
  }
}
```

- **12 tools**, resolver-native: `reusery_catalog`, `reusery_discover`,
  `reusery_enrich`, `reusery_resolve`, `reusery_refine`,
  `reusery_inspect_evidence`, `reusery_inspect_resolution`,
  `reusery_report_outcome`, plus the project tools `reusery_project_scan`,
  `reusery_project_context`, `reusery_project_remember`,
  `reusery_project_forget`.
- **No tool takes a filesystem path.** The local project root is process
  configuration (`--project-root`), never an argument, and it is never stored.
- **No tool takes a credential.** A public repository is read with the
  unauthenticated API; a private repository fails as unsupported.
- `reusery_resolve` and `reusery_refine` accept an **optional** `project_id`.
  Omitting it is exactly the pre-project call.
- There is deliberately **no `reusery_normalize` tool**: the caller is already
  an AI model, so a second model call would only add cost. The CLI and HTTP
  normalise as before.
- **`REUSERY_MCP_ENABLE_EXTERNAL_OPERATIONS=false` by default.** It gates only
  `reusery_discover`, `reusery_enrich` and public project scans, and is
  separate from `REUSERY_API_ENABLE_EXTERNAL_OPERATIONS`. The CLI is
  unaffected by either.
- stdout carries MCP frames only; logs go to stderr. There is no remote MCP
  yet — see [docs/mcp.md](docs/mcp.md).

The tool contract is generated and drift-checked like the OpenAPI one:

```bash
go run ./cmd/mcpcontract -check mcp/reusery-tools-v1.json
```

**Teach your agent when to use this**: [skills/reusery/SKILL.md](skills/reusery/SKILL.md)
tells it to consider Reusery before reconstructing a likely-solved capability,
and when not to bother.

Full reference: [docs/mcp.md](docs/mcp.md).
## Architecture

```text
cmd        executable entry points (thin main only)
internal   private application implementation
docs       project/engineering documentation
scripts    developer automation
```

- `internal/config` — environment-based configuration.
- `internal/api` — the stable HTTP/JSON API v1: transport DTOs, mapping,
  RFC 9457 errors, middleware and route registration (see
  [docs/http-api.md](docs/http-api.md)).
- `internal/app` — shared composition root: the production factories the CLI,
  the HTTP API and the MCP server all call.
- `internal/mcpserver` — the Model Context Protocol adapter over stdio. It maps
  onto the same services and adds no resolver logic of its own (see
  [docs/mcp.md](docs/mcp.md)).
- `internal/outcome` — append-only factual post-resolution events. Not
  Evidence, not policy, not a ranking signal.
- `internal/server` — generic HTTP server lifecycle: timeouts, cancellation
  propagation and graceful shutdown. It owns no routes.
- `internal/version` — build metadata (linker-flag injectable).
- `internal/model` — core domain model (Primitive, Contract, Specimen, Evidence, Resolution).
- `internal/resolver` — deterministic evidence evaluation, the resolution
  kernel, and the Packet 7 quality layer (assessments, dispositions,
  shortlisting and `needs_verification`) (see
  [docs/evidence-evaluation.md](docs/evidence-evaluation.md),
  [docs/resolver-kernel.md](docs/resolver-kernel.md) and
  [docs/evidence-policy-resolution.md](docs/evidence-policy-resolution.md)).
- `internal/catalog` — strict repository-authored YAML catalogue loading and
  seeding.
- `internal/discovery` — bounded public discovery: profiles, budgets, provider
  interface, evidence rules and persistence (see
  [docs/public-discovery.md](docs/public-discovery.md)).
- `internal/enrichment` — bounded metadata enrichment: budgets, provider
  interface, evidence identity and trust rules; `internal/enrichment/providers/depsdev`
  and `internal/enrichment/providers/githubmeta` are the first two providers.
- `internal/policy` — typed fact extraction, strict policy profiles and
  deterministic allow/review/deny evaluation, plus structured feedback
  refinement. Never imports PostgreSQL.
- `internal/benchmark` — benchmark record loading, pairing and measured-vs-
  estimated aggregation (see [benchmarks/README.md](benchmarks/README.md)).
- `internal/intent` — natural-language intent normalisation: domain types,
  versioned prompt and schema, deterministic validation, the single bounded
  repair, deterministic identifiers and the evaluation harness (see
  [docs/intent-normalisation.md](docs/intent-normalisation.md));
  `internal/intent/providers/openai` is the first model adapter.
- `internal/cli` — the `reusery` commands (serve, seed, resolve, resolution,
  discover, enrich, choose, normalize, normalize-eval).
- `internal/store/postgres` — PostgreSQL persistence (pgx pool, Goose
  migrations, hand-written sqlc mapping layer).

Source-shaped project assets live outside Go:

- `primitives/` — primitive definitions.
- `contracts/` — behavioural contract definitions.
- `catalogue/` — development seed bundles (fixtures, not recommendations).
- `discovery/` — public discovery profiles.
- `policies/` — authored policy profiles.
- `examples/` — example resolve, enrich, choose and intent requests.
- `openapi/` — the checked-in, generated OpenAPI v1 contract.
- `mcp/` — the checked-in, generated MCP tool contract.
- `skills/` — agent skills (see [skills/reusery/SKILL.md](skills/reusery/SKILL.md)).
- `evals/` — deterministic evaluation corpora.
- `benchmarks/` — benchmark record format and (eventually) paired runs.

## Tooling

Install the pinned developer tools:

```powershell
./scripts/install-tools.ps1
```

Expected versions are recorded in [docs/engineering.md](docs/engineering.md).
