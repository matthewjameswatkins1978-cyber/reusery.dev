# Reusery.dev — Engineering Notes

Foundation decisions for the bootstrap and persistence floor. Future agents:
these choices are intentional — do not "improve" them back into complexity
without a packet that asks for it.

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

Packet 1 had zero third-party requirements. Packet 3 adds the persistence
stack only: `pgx`, `sqlc`, Goose and Testcontainers (test-only). Packet 4 adds
`gopkg.in/yaml.v3` for strict catalogue loading — the maintained YAML parser,
not a hand-rolled one, and deliberately kept out of `internal/model`. Packet 5
adds **zero** dependencies: provider calls use `net/http`, `encoding/json` and
the same YAML parser. No GitHub SDK — the three REST calls Packet 5 needs are
trivially hand-written, and an SDK would add dependency and CVE surface for no
benefit. Packet 6 also adds **zero** dependencies: the OpenAI Responses API
call is `net/http` + `encoding/json`, the prompt and schema are embedded with
`embed`, and the evaluation corpus reuses the same YAML parser. No AI
orchestration framework, no OpenAI SDK. Auth, Redis, queues, ORMs, search and
telemetry still belong to later packets. Each future dependency must justify
itself against the standard library first.

## Configuration

`internal/config` reads these environment variables:

```text
REUSERY_HTTP_ADDR=:8080             # default, optional
REUSERY_LOG_LEVEL=info              # default, optional
REUSERY_DATABASE_URL=postgres://... # REQUIRED (Packet 3 onwards)
REUSERY_GITHUB_TOKEN=               # optional, discovery rate limits only
REUSERY_OPENAI_API_KEY=             # optional, model-backed commands only
REUSERY_OPENAI_MODEL=gpt-5.6-luna   # optional model override
```

No config framework: `os.Getenv` plus a small parser is sufficient.
`ParseLogLevel` falls back to `info` on unknown values so a typo can neither
crash startup nor silently disable logging. Copy `.env.example` to `.env`
for local overrides; `.env` is git-ignored and must never be committed.

`Load()` returns `(Config, error)`. Since Packet 3 the database URL is
mandatory and validated (postgres/postgresql URL or libpq keyword form), so
startup fails clearly instead of falling back to an invented credential. The
URL may contain a password, so it is never logged and never included in a
config error message.

`REUSERY_GITHUB_TOKEN` is optional and may legitimately be empty. It only
raises GitHub's rate limits for `reusery discover`; GitHub's public
unauthenticated endpoints work without it. It is never required to start
`reusery serve`, never part of readiness, never logged, and never included in a
configuration error — the token is only ever placed in an `Authorization`
header, never in a URL.

Model configuration is deliberately **not** part of `Load()`. `LoadModel()`
returns a `ModelConfig{OpenAIAPIKey, OpenAIModel}` and never consults the
database URL, so `reusery normalize` and `reusery normalize-eval` work with no
PostgreSQL configured at all: natural-language structuring and the database are
independent concerns. The key is optional globally and required only by
commands that actually invoke OpenAI; it is never logged, never persisted and
never included in an error.

## HTTP server

`internal/server` owns construction and lifecycle; `cmd/reusery/main.go`
stays thin (wire config → logger → server → signals).

- Timeouts: read 10s, read-header 5s, write 10s, idle 60s, shutdown 10s.
- `GET /health` — liveness only, never touches PostgreSQL.
- `GET /ready` — runs registered `ReadyChecker`s. Packet 3 registers the
  PostgreSQL readiness checker here; a failing checker yields HTTP 503
  `{"status":"not ready"}` without leaking database diagnostics. Before
  Packet 3 there were no checkers, so it was trivially ok.
- Shutdown: `signal.NotifyContext` on Ctrl+C/SIGTERM → `Server.Shutdown`
  with a 10s bound → startup and shutdown both logged via `slog`. The pool is
  closed during the same shutdown.

## PostgreSQL persistence (Packet 3)

Layout: `internal/store/postgres/{pool,migrate,store,mappings}.go`, authored
SQL in `queries/`, reversible Goose migrations in `migrations/`, generated
code in `sqlc/` (checked into Git), config in `sqlc.yaml`.

### Why pgx

`github.com/jackc/pgx/v5` is the standard low-level PostgreSQL driver: a real
connection pool (`pgxpool`), full PostgreSQL type support, no ORM on top.
pgx defaults are used for pool tuning; no numbers were invented without
evidence.

### Why sqlc rather than an ORM

sqlc generates ordinary Go from authored SQL and keeps the SQL reviewable
next to the schema. It gives compile-time-checked queries without hiding SQL
behind an ORM abstraction or a reflection layer. Generated code lives in its
own subpackage and is never exposed as the domain model: a hand-written layer
(`mappings.go`) converts between sqlc row structs and `internal/model`. The
resolver still only ever sees `internal/model`.

### Why Goose

Goose is a tiny migration runner with reversible `-- +goose Down` sections and
a plain Go library API, so tests can migrate from zero. Migration files stay
reviewable SQL. SQL is **not** executed automatically on every HTTP start:
`Migrate(ctx, pool)` is a separate, explicit operation. The documented
operator command is:

```powershell
goose -dir internal/store/postgres/migrations postgres "$REUSERY_DATABASE_URL" up
```

The eventual production deployment packet decides exactly where migrations
execute.

### Schema rules

- Textual domain IDs are stored as `text`, not UUIDs.
- Requirement order is an explicit `position` column
  (`UNIQUE(contract_id, position)`), with `PRIMARY KEY(contract_id,
  requirement_id)` so duplicate IDs inside one contract are impossible.
- Rejection order is an explicit `position` column.
- Ordered slices that have no row identity (tags, reasons, unknowns,
  evidence IDs, reuse modes) are PostgreSQL arrays, not extra tables.
- `Evidence.result` is CHECK-constrained to exactly
  `pass|fail|unknown|info`; `unknown` is a real stored value.
- `Resolution` gets a storage-only `BIGINT ... AS IDENTITY` key; that key is
  never added to `internal/model`. `Resolution.SpecimenID` is nullable so
  `BUILD LOCALLY` persists without a specimen.
- `Evidence.SubjectID` has **no** foreign key: it is deliberately more
  general than `SpecimenID` so evidence can later attach to other subjects.

### Tests: real PostgreSQL only

Integration tests are guarded by `//go:build integration` and use
Testcontainers with a pinned `postgres:18.6-alpine`. No SQLite, no mocks, no
in-memory fake — the point is to test PostgreSQL. Run them with:

```powershell
go test -tags=integration ./...
```

Docker must be running. CI runs them on GitHub-hosted Linux runners, which
have Docker available.

## Resolver kernel and CLI (Packet 4)

Three layers, kept deliberately separate:

- **Pure kernel** (`internal/resolver/kernel.go`) — consumes domain values
  only. No PostgreSQL, HTTP, filesystem, environment or CLI parsing. Candidates
  are considered in supplied order; the first one satisfying every required
  requirement is selected; otherwise BUILD LOCALLY. No scores, no ranking. See
  [resolver-kernel.md](resolver-kernel.md).
- **Application service** (`internal/resolver/service.go`) — loads through a
  `resolver.Repository` interface defined outside PostgreSQL, takes the
  timestamp from an injected `Clock` (deterministic tests), runs the kernel and
  persists exactly one Resolution. Load or validation failures persist nothing.
- **Adapters** — `internal/store/postgres`, `internal/cli`, `internal/catalog`.

### Catalogue loading

`internal/catalog` decodes repository-authored YAML into loader-local DTOs
with `KnownFields(true)` (typos fail) and maps them into `internal/model`, so
the domain model never learns what YAML is. Paths in a manifest must resolve
beneath the repository root. Relationships are validated after decoding.

Seeding upserts Primitive/Contract/Specimen but keeps Evidence append-only: an
observation is inserted if new, skipped if identical, and rejected with
`catalog.ErrEvidenceConflict` if the same ID carries different content. The
server never seeds automatically; seeding requires an explicit CLI command.

### Development fixtures

`catalogue/dev/bounded-subprocess/` holds deterministic **development
fixtures** (`fixture/.../partial-adapt` and `fixture/.../complete-dependency`).
They are not claims about real public software and not recommendations — every
evidence record says so in `Methodology` and points back at the repository
fixture file in its `SourceRef`. They stay: they are the only behavioural
evidence Reusery has, whereas live discovery output is deliberately not
behavioural evidence at all.

### CLI

`internal/cli` owns command behaviour so `cmd/reusery/main.go` stays thin
(signals + `cli.New().Run`). Standard library `flag`, no CLI framework.

| Command | Purpose |
| --- | --- |
| `reusery` / `reusery serve` | start the HTTP server (no-argument behaviour preserved) |
| `reusery seed --root . --manifest FILE` | load a catalogue seed bundle |
| `reusery resolve --request FILE [--format text\|json]` | run a resolve request |
| `reusery resolution --id N [--format text\|json]` | inspect a stored resolution |
| `reusery discover --profile FILE [--root DIR] [--format text\|json]` | run bounded public discovery |
| `reusery normalize (--text STR \| --file FILE) [--format text\|json]` | structure intent into a provisional contract (no PostgreSQL) |
| `reusery normalize-eval --corpus FILE [--format text\|json]` | run the intent evaluation corpus (**paid** model calls, no PostgreSQL) |
| `reusery help` | usage |

Exit codes: `0` success, `1` execution failure, `2` usage error — and for
`discover`, a **profile problem** is a usage error while provider or
persistence failure is an execution failure. For `normalize`, `2` is a usage
**or local-input** problem (missing/both input flags, bad `--format`,
unreadable file, empty/oversized/invalid-UTF-8 input) while a provider
failure, a configuration failure or a validation failure after the single
repair is `1`; `needs_clarification` and `unsupported` are valid `0` results,
not crashes. For `normalize-eval`, a corpus problem is `2` and an unmet
acceptance gate is `1`. JSON goes to stdout only; logs, warnings and errors go
to stderr, so `--format json` output stays machine-readable.

## Public discovery (Packet 5)

Domain in `internal/discovery`, providers in `internal/discovery/providers/`,
one bounded HTTP helper in `internal/discovery/httpx`. Full design notes live
in [public-discovery.md](public-discovery.md); the decisions worth recording
here are:

- **Three providers, one interface.** `pkg.go.dev`,
  `github-repositories` and `github-code` implement the same
  `Provider` interface. Repository and code discovery share one GitHub client
  but keep separate provider IDs because they produce different candidate
  shapes. No provider micro-framework.
- **Zero new dependencies.** `net/http` + `encoding/json` + the existing
  `gopkg.in/yaml.v3`. No GitHub SDK: three REST calls do not need one.
- **GitHub REST API version pin.** Every GitHub request sends
  `X-GitHub-Api-Version: 2026-03-10` and `Accept: application/vnd.github+json`
  so a server-side API change surfaces as a classified failure instead of a
  silently different response shape.
- **pkg.go.dev JSON API.** `/v1/search` and `/v1/package/{path}` only — HTML is
  never scraped. Live testing showed `/v1beta` now issues a permanent redirect
  to `/v1`, so the canonical paths are used directly and same-host redirects
  are tolerated as a safety net.
- **Provider budgets are fixed, not configurable.** 3 providers, 3 queries per
  provider, 6 results per query, 20 HTTP requests per provider, 24 candidates
  per run, 15s per provider, 2 MiB per response. Providers stop when a budget
  is spent and report `budget_exhausted` with `incomplete: true`.
- **Base URLs are code-owned constants.** There is no configuration for a
  provider base URL; that would be an SSRF-shaped feature. Tests inject
  `httptest` URLs through provider constructors. Redirects are followed only
  inside the fixed base host.
- **External network policy.** The public internet is reachable only from
  manual, human-run smoke tests. **CI never calls live GitHub or pkg.go.dev** —
  provider tests use `httptest`, and the PostgreSQL integration test wires
  `httptest` providers to real PostgreSQL. A provider outage therefore cannot
  make CI red.
- **Provider-generated evidence trust rule.** Provider evidence may be `info`
  or `unknown`, always leaves `AppliesTo` empty, and is checked by a defensive
  validation pass before anything is persisted. It can never be `pass`/`fail`
  for a behavioural contract requirement. Search relevance is not behavioural
  verification, and provider ordering is not Reusery ranking.

### Manual live smoke procedure

Run before freezing any packet that touches discovery:

```powershell
# migrate + seed first (see the workflows in README.md)
go run ./cmd/reusery discover --root . `
  --profile discovery/process/bounded-subprocess-v1.yaml --format text
```

Inspect the returned candidates for plausibility — an HTTP 200 is not the
point — then repeat with `--format json`. Run once without
`REUSERY_GITHUB_TOKEN` to confirm unauthenticated GitHub code search is
classified as `authentication` while the other providers still succeed, and
once with a token to confirm all three providers run. Never put a token in
shell history, logs, screenshots or reports.

## Natural-language intent normalisation (Packet 6)

Domain in `internal/intent`, the first model adapter in
`internal/intent/providers/openai`, versioned assets in
`internal/intent/assets`, evaluation corpus in `evals/intent`. Full design
notes live in [intent-normalisation.md](intent-normalisation.md); the
decisions worth recording here are:

- **Three things stay separate.** A provider-independent intent domain, a
  normalisation service that owns prompt/schema/validation/repair/identifiers,
  and a model adapter that speaks one provider's wire protocol. The service
  depends on a `Provider` interface, never on OpenAI directly, and the adapter
  contains no Reusery product semantics.
- **Responses API, not Chat Completions.** `POST
  https://api.openai.com/v1/responses` with `store: false`,
  `reasoning.effort: "low"`, `max_output_tokens: 2500`, no `tools` field at
  all, and `text.format.type = "json_schema"` with `strict: true`. Each call
  is self-contained: no `previous_response_id`, no `conversation`.
- **Default model `gpt-5.6-luna`.** Intent normalisation is bounded structured
  work and Reusery has a first-class cost-saving objective, so the default is
  deliberately not the strongest available model. `REUSERY_OPENAI_MODEL`
  overrides it for evaluation; the model string the provider actually returns
  is recorded in metadata.
- **Absolute maximum of two paid calls per normalisation** — one initial and
  one semantic repair, with a 20 s timeout per call. There are no automatic
  operational retries: a hidden retry is another paid call and can amplify an
  outage. Local input errors are rejected before any paid call.
- **Versioned prompt and schema.** `intent-normalizer/v1`,
  `intent-repair/v1` and schema version 1 live as embedded, reviewable files,
  and both versions feed the deterministic identity digest.
- **Deterministic identifiers, never model-supplied ones.**
  `intent/<sha256>` for the primitive, `intent/<sha256>/v1` for the contract,
  `req-001…` in model output order. The digest covers the normalised input,
  the validated draft, the prompt version and the schema version.
- **No score, no confidence.** The verdict is `ready` / `needs_clarification`
  / `unsupported`. Semantic validation is deterministic Go code — strict JSON
  Schema is never trusted on its own.
- **No persistence, no discovery, no resolution.** No migration exists for
  model-derived drafts; `normalize` never calls `discover` or `resolve`, and
  the generated contract is provisional.
- **External model policy.** CI never requires `REUSERY_OPENAI_API_KEY` and
  never calls OpenAI: adapter tests use `httptest`, normaliser tests use a
  fake provider, and the corpus harness uses fixtures. Live corpus runs are
  manual release-gate evidence.
- **Secret handling.** The API key is optional globally and required only by
  model-backed commands. It is never logged, never persisted, never placed in
  a URL and never included in an error; provider errors carry a status code
  and at most a length-capped provider message.

### Manual live corpus procedure

Run before freezing any packet that touches intent normalisation:

```powershell
# five manual smoke cases first (see intent-normalisation.md)
go run ./cmd/reusery normalize --file examples/normalize-bounded-subprocess.txt --format text

# then the corpus, twice
go run ./cmd/reusery normalize-eval --corpus evals/intent/v1.yaml --format text
```

Record provider, model, case counts, pass/fail, repair count and rate, and
input/output/reasoning/total tokens. Never record the API key.

## Quality checks

```powershell
gofmt -s -l -w .            # formatting (write)
sqlc generate            # regenerate query code
git diff --exit-code -- internal/store/postgres/sqlc   # drift check
go vet ./...             # static analysis
go test ./...            # unit tests
go test -tags=integration ./... # Docker required
golangci-lint run ./...  # lint (config: .golangci.yml)
govulncheck ./...        # vulnerability scan
go build ./cmd/reusery   # build
```

- `./scripts/check.ps1` runs all of the above on Windows; `make check`
  is the equivalent for make-based environments and is mirrored in CI.
- `./scripts/install-tools.ps1` installs the pinned tools into `GOPATH/bin`.
- Pinned tool versions:
  - golangci-lint **v2.14.0**
  - govulncheck **v1.8.0** (`golang.org/x/vuln` release)
  - sqlc **v1.31.1**
  - goose **v3.28.0**
  - Testcontainers image **postgres:18.6-alpine** (a Go test dependency, not
    an installed binary)
- Tools install into `GOPATH/bin` (`C:\Users\Matmus\go\bin` here); that
  directory must be on `PATH` for them to resolve.
- CI (`.github/workflows/ci.yml`) uses current action majors:
  `actions/checkout@v6`, `actions/setup-go@v6` (Go 1.27.1),
  `golangci/golangci-lint-action@v9` (golangci-lint v2.14.0), plus
  `govulncheck` and the sqlc drift check.

## Tests

Standard `testing` only. Coverage is behavioural, not numeric:

- `internal/server` — `/health` and `/ready` status codes, JSON bodies,
  content-type headers, and the failing-checker 503 path.
- `internal/resolver` — deterministic evaluation semantics (Packet 2), the
  resolution kernel and the application service (Packet 4).
- `internal/catalog` — strict YAML loading, path-escape rejection, relationship
  validation and idempotent seeding.
- `internal/cli` — command parsing, output formats, exit codes, stdout/stderr
  separation, `discover` with injected discovery behaviour (no network), and
  `normalize` / `normalize-eval` with an injected normaliser, including the
  proof that neither command loads PostgreSQL configuration nor opens a store.
- `internal/config` — defaults, env overrides, blank-value handling, missing
  and malformed database URL rejection, level parsing, the optional GitHub
  token never leaking into errors, and the separate `LoadModel` path that
  succeeds with no database URL.
- `internal/discovery` — profile loading and structural validation, evidence
  ID stability, defensive candidate validation, merge/ordering/budget
  semantics, append-only persistence, and partial provider failure.
- `internal/discovery/httpx` — bounded GET behaviour: cancellation, size
  ceiling, content-type handling, safe status errors, base-host-only redirect
  policy, credential-free URLs.
- `internal/discovery/providers/*` — `httptest` fixture tests for response
  mapping, stable specimen IDs, exact version preservation, licence unknowns,
  deduplication, request/result budgets, rate-limit and auth classification,
  and proof that no provider can emit behavioural evidence.
- `internal/intent` — local input rejection before any paid call, status
  mapping, deterministic identifiers and requirement ordering, the single
  bounded repair and the two-call absolute maximum, metadata and usage
  aggregation, per-call timeout, strict-schema assertions (closed objects,
  enums, no prohibited field, supported keyword subset), prompt/version
  stability, every validation bound and enum, evaluation-corpus loading and
  gate computation, and the safety invariants: no prohibited field can appear
  in a Result, an injection-shaped response cannot introduce authority, and a
  generated contract stays `unknown` for the unchanged Packet 2 evaluator.
- `internal/intent/providers/openai` — `httptest` tests for the endpoint and
  request shape (bearer token, model, `store: false`, low reasoning effort,
  strict `json_schema`, no tools, bounded `max_output_tokens`), response
  parsing, model/response-ID/usage/reasoning-token extraction, every
  classification (401/403/429/5xx/408, refusal, incomplete, missing text,
  malformed JSON, oversized body), no hidden retries, and that the API key
  never appears in an error.
- `internal/store/postgres` (`-tags=integration`) — migration up/down/up,
  round-trips for every domain object, requirement and rejection ordering,
  transaction rollback, readiness, and `persist → reload → resolver.Evaluate`.
- `internal/cli` (`-tags=integration`) — the full vertical slice against real
  PostgreSQL: migrate → seed → resolve → persist → inspect, for both `depend`
  and `build_locally`; plus public discovery against real PostgreSQL with
  `httptest` providers, including the assertion that persisted provider
  evidence leaves every required requirement `unknown`.
