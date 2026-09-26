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
shutdown. `net/http` (with `http.ServeMux`) covers all of it. No Gin, Echo,
Fiber, Chi or Gorilla Mux: a router framework would add dependency and CVE
surface for zero benefit.

Packet 8 keeps `http.ServeMux` as the router and adds **Huma v2.39.1** with
its `humago` adapter, which registers typed operations directly onto the
standard mux. Huma is added for the typed HTTP boundary, request validation,
OpenAPI 3.1, JSON Schema, RFC 9457 error support and generated documentation.
It is explicitly **not** added to own server lifecycle, configuration,
database access, logging architecture, resolver semantics or provider
orchestration: those stay ours.

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
`embed`, and the evaluation corpus reuses the same YAML parser. Packet 7 adds
**zero** dependencies. Packet 8 adds exactly one: **`github.com/danielgtaylor/huma/v2 v2.39.1`**
(plus its `humago` adapter and `queryparam` helper from the same module) for
the typed HTTP/OpenAPI boundary — no OpenAPI generator, no Swagger generator,
no `oapi-codegen`, no router, no middleware framework, no UUID library, no
validation library. It is a library dependency, so there is no install-tools
entry for it. Auth, Redis, queues, ORMs, search and telemetry still belong to
later packets. Each future dependency must justify itself against the
standard library first.

## Configuration

`internal/config` reads these environment variables:

```text
REUSERY_HTTP_ADDR=:8080             # default, optional
REUSERY_LOG_LEVEL=info              # default, optional
REUSERY_DATABASE_URL=postgres://... # REQUIRED (Packet 3 onwards)
REUSERY_GITHUB_TOKEN=               # optional, discovery rate limits only
REUSERY_OPENAI_API_KEY=             # optional, model-backed commands only
REUSERY_OPENAI_MODEL=gpt-6-luna     # optional model override
REUSERY_API_ENABLE_EXTERNAL_OPERATIONS=false  # HTTP-only switch (Packet 8)
```

No config framework: `os.Getenv` plus a small parser is sufficient.
`ParseLogLevel` falls back to `info` on unknown values so a typo can neither
crash startup nor silently disable logging. Copy `.env.example` to `.env`
for local overrides; `.env` is git-ignored and must never be committed.

`REUSERY_API_ENABLE_EXTERNAL_OPERATIONS` is parsed strictly by
`ParseStrictBool`: only `true` or `false`, case-insensitively. An unset value
means `false`; anything else (`yes-please`, `1`, `on`) is a configuration
error rather than a silent default, because the switch guards HTTP operations
that spend model tokens and provider quota. It gates only the HTTP routes
`/v1/normalize`, `/v1/discover` and `/v1/enrich`; the equivalent CLI commands
are unaffected, readiness never fails because it is false, and `resolve`,
`refine`, inspection, `/health` and `/ready` stay available.

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
never included in an error. Since Packet 8 the HTTP server also starts with no
key: the normaliser is only constructed inside a `/v1/normalize` request, and
a missing key there answers 503 `model_provider_unconfigured` instead of
failing startup.

## HTTP server

`internal/server` owns **only** the generic lifecycle — listen, timeouts,
cancellation propagation and graceful shutdown. It knows nothing about
Reusery's routes or domain. `internal/api` owns the HTTP contract and hands
`server.New` an `http.Handler`.

- Timeouts: read-header 5s, read 10s, **write 65s**, idle 60s, shutdown 10s.
  WriteTimeout is 65s because legitimate bounded API operations include two
  20-second model calls, three bounded discovery provider passes and a
  30-second enrichment run. Every per-operation budget inside the handlers is
  strictly tighter, so the application deadline — never the server deadline —
  terminates a slow request.
- The serve context is installed as `http.Server.BaseContext`, so cancelling
  it propagates to every in-flight request context and from there to
  PostgreSQL, OpenAI, GitHub, pkg.go.dev and deps.dev through
  `context.Context`.
- `GET /health` — liveness only, never touches PostgreSQL and never runs a
  readiness checker.
- `GET /ready` — runs registered `ReadyChecker`s. Only PostgreSQL is
  registered: model, GitHub, pkg.go.dev and deps.dev are degradable providers
  and are never readiness dependencies. A failing checker yields HTTP 503
  `{"status":"not ready"}` without leaking database diagnostics.
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
| `reusery enrich --request FILE [--root DIR] [--format text\|json]` | record attributable metadata observations (network) |
| `reusery choose --request FILE --policy FILE [--feedback FILE] [--root DIR] [--format text\|json]` | compare candidates under policy (offline) |
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
acceptance gate is `1`. For `enrich`, a missing/oversized/duplicate specimen
list or an unreadable request is `2`, and storage failure or a run in which
every applicable provider failed is `1`. For `choose`, request, policy and
feedback structural problems are `2`; storage, invalid stored data and
persistence failure are `1`; **`resolved` and `needs_verification` are both
`0`** — needs_verification is a product outcome, not a crash. JSON goes to
stdout only; logs, warnings and errors go to stderr, so `--format json` output
stays machine-readable.

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
- **Default model `gpt-6-luna`.** Intent normalisation is bounded structured
  work and Reusery has a first-class cost-saving objective, so the default is
  deliberately not the strongest available model. Packet 6 was released with
  `gpt-5.6-luna`; Packet 7 changes the default to `gpt-6-luna` because that is
  the model the two successful release-gate corpus runs actually used, the only
  model the evaluation account exposed, the current documented efficient
  cost-sensitive model, and cheaper per published token pricing.
  `REUSERY_OPENAI_MODEL` still overrides it for evaluation; the model string
  the provider actually returns is recorded in metadata. Prompt version, schema
  version, reasoning effort, `store: false` and the two-call maximum are
  unchanged.
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

## Evidence, policy and resolution quality (Packet 7)

Domain in `internal/policy` (facts, profiles, evaluation, feedback),
`internal/enrichment` (providers, evidence identity, budgets) and
`internal/resolver` (`quality.go`, `quality_service.go`). Full design notes
live in [evidence-policy-resolution.md](evidence-policy-resolution.md); the
decisions worth recording here are:

- **Enrichment providers.**
  - `deps.dev` — code-owned base `https://api.deps.dev`, stable **v3** API
    (never v3alpha, never HTML):
    `GET /v3/systems/GO/packages/{module}/versions/{version}` and
    `GET /v3/systems/GO/packages/{module}/versions/{version}:requirements`.
    Module path and version are percent-escaped as single path segments; module
    identity is recovered from Packet 5's `package_module` claim and the exact
    version from `Specimen.Source.Revision`. If either is missing, an
    `identity_unresolved` issue is reported and nothing is guessed.
  - `github-metadata` — `GET /repos/{owner}/{repo}` on
    `https://api.github.com`, reusing Packet 5's shared GitHub client (same
    optional `REUSERY_GITHUB_TOKEN`, `X-GitHub-Api-Version: 2026-03-10`, rate
    limits, fixed host, safe redirects). Only `archived`, `pushed_at`,
    `default_branch` and `license.spdx_id` are decoded — no stars, forks or
    watchers, and popularity never reaches ordering.
- **Enrichment budgets are fixed, not configurable.** 24 specimens per run, 2
  providers per specimen, 3 HTTP requests per provider/specimen, 10 s per
  provider call, 30 s for the whole run, 2 MiB per response. No pagination, no
  retry loops. One provider failing never erases another's evidence; only an
  all-providers-failed run is an execution failure.
- **Enrichment trust rules.** INFO/UNKNOWN only, `applies_to` always empty, no
  PASS/FAIL ever — structurally validated before persistence. Evidence IDs are
  `enrichment/<provider>/<sha256>` over the same length-prefixed canonical
  form Packet 5 uses.
- **Fact artifact format.** Policy never parses `Evidence.Claim`. Packet 7
  enrichment evidence carries a canonical JSON value in `Evidence.Artifact`:
  `{"schema_version":1,"value":<string|int|bool>}`. Historical artifacts are
  untouched, and an unreadable fact artifact makes the fact **unknown**, never
  guessed.
- **Policy profiles are strict YAML** with `schema_version`, exact-string
  licence lists, `allow`/`review`/`deny` actions (omitted action ⇒ `review`, so
  an incomplete profile can never silently allow), optional numeric thresholds
  (`dependencies.max_direct`, `maintenance.max_days_since_push`,
  `maintenance.max_days_since_release`) where **absent means no threshold**, and
  `selection.max_options` bounded to 1..5. Path escape and every structural
  rule are rejected at load time. Nothing in `internal/policy` imports
  PostgreSQL.
- **No legal advice, no security proof.** Licence decisions are phrased
  "allowed by policy `<id>`" / "denied by policy `<id>`". A zero advisory count
  is phrased as an absence of *reported* identifiers at the observation time
  and never as secure/safe/vulnerability-free. Neither becomes EvidencePass.
- **Migration `00002_resolution_policy.sql`** adds `resolutions.policy_id
  text NOT NULL DEFAULT ''`, with a reversible `DROP COLUMN` down migration.
  Pre-Packet-7 resolutions load with an empty `PolicyID`; nothing is backfilled.
  Candidate assessments, shortlists and trade-offs are deliberately **not**
  persisted.
- **`choose` is offline by construction.** It opens PostgreSQL and an authored
  policy file and nothing else. No provider credential is consulted.

### Manual live smoke procedure

```powershell
# 1. migrate, then seed the canonical first primitive
goose -dir internal/store/postgres/migrations postgres "$env:REUSERY_DATABASE_URL" up
go run ./cmd/reusery seed --root . --manifest catalogue/dev/bounded-subprocess/manifest.yaml

# 2. discover real candidates
go run ./cmd/reusery discover --root . --profile discovery/process/bounded-subprocess-v1.yaml --format json

# 3. copy the returned specimen IDs into examples/enrich-bounded-subprocess.json
go run ./cmd/reusery enrich --root . --request examples/enrich-bounded-subprocess.json --format text

# 4. choose under the baseline policy (offline), then re-choose with feedback
go run ./cmd/reusery choose --root . --request examples/quality-bounded-subprocess.json `
  --policy policies/public-go-baseline-v1.yaml --format text
go run ./cmd/reusery choose --root . --request examples/quality-bounded-subprocess.json `
  --policy policies/public-go-baseline-v1.yaml `
  --feedback examples/quality-feedback-not-quite.json --format text
```

Live calls are manual. **CI never calls deps.dev or GitHub** — enrichment
provider tests use `httptest`, `choose` performs no network call, and no
provider credential is required to build or test.

## Benchmark records (Packet 7)

`benchmarks/` documents the record schema; `internal/benchmark` implements
loading, pairing and aggregation. The rule that matters: **MEASURED and
MODELLED fields are structurally separate** (`Measured` vs `Estimated`) and are
aggregated by different functions into different types, so an estimate can
never inflate a measurement. Every measured field is optional — an unobserved
metric stays unknown and aggregates as a `missing` count, never as zero. A
missing baseline or missing Reusery run reports `comparable: false` with a
reason instead of manufacturing a delta, and `savings_percent` is computed only
when the baseline value exists and is non-zero. Packet 7 establishes recording
and aggregation only; **Packet 16 owns release-level validation claims.**

## Stable HTTP API and OpenAPI contract (Packet 8)

Full reference: [docs/http-api.md](http-api.md).

**Layout.**

- `internal/api` — route registration, v1 transport DTOs, the mapping layer,
  RFC 9457 errors, middleware (request id, version/cache/nosniff headers,
  bounded request logging) and the per-operation budgets. It depends on
  interfaces and application services only; it never imports
  `internal/store/postgres`.
- `internal/app` — the shared composition root. `NewNormalizer`,
  `NewDiscoverer`, `NewEnricher` and `NewQualityResolver` are called by both
  the CLI and the HTTP API, so Packet 9's MCP adapter can call them too. It
  is a composition root, not an application framework: no request types, no
  routing, no config loading, no lifecycle, no business logic.
- `internal/server` — generic lifecycle only: listen, timeouts,
  cancellation, graceful shutdown. Routes moved out in Packet 8.

**Stack.** Huma v2.39.1 with the `humago` adapter over the standard
`http.ServeMux`. Operation IDs are frozen contract values
(`normalizeIntent`, `discoverCandidates`, `enrichCandidates`,
`resolveCandidates`, `refineResolution`, `getPrimitive`, `getContract`,
`getSpecimen`, `listEvidence`, `getResolution`, `healthCheck`, `readyCheck`).

**Transport DTO rule.** `internal/api/types.go` declares every v1 type and
`mapping.go` converts to and from internal domain types, so an innocent
internal struct edit in a later packet cannot silently break API v1. Domain
*vocabulary* (`ReuseMode`, `Outcome`, `EvidenceResult`, `DecisionStatus`,
`Disposition`, `Policy.Action`, `FeedbackReason`) is reused one-for-one
rather than duplicated.

**OpenAPI generation and drift gate.**

```bash
go run ./cmd/openapi -write openapi/reusery-v1.json   # regenerate
go run ./cmd/openapi -check  openapi/reusery-v1.json   # CI gate
make openapi          # regenerate
make openapi-check    # CI gate
```

`cmd/openapi` builds `api.NewHandler(api.Dependencies{})`, which performs no
I/O: no PostgreSQL, no OpenAI key, no GitHub token, no network. Route
registration is therefore separated from live service construction. The
checked-in `openapi/reusery-v1.json` is the shipped 3.1 contract and is
compared byte-for-byte against generator output by `scripts/check.ps1`,
`make check` and the CI `verify` job, plus a `TestCheckedInContractMatchesGenerator`
unit test. After Packet 8 freezes, v1 is a compatibility commitment: new
optional fields and new operations are fine; renames, enum changes,
operation-id changes, removed fields and type changes are not.

**Error mapping.** `application/problem+json` with a stable `code`
extension. 400 malformed JSON · 422 schema/`validation_failed` · 422
domain/`invalid_request` · 404 `not_found` · 409 `conflict` · 413 oversized
body · 502 `all_providers_failed` (upstream failed, Reusery is fine) · 503
`external_operations_disabled` / `model_provider_unconfigured` /
`upstream_authentication` / `upstream_rate_limited` / `upstream_unavailable`
· 504 `upstream_timeout` · 500 `internal_error`. Partial provider failure
stays HTTP 200 with the provider issues intact. Errors never contain a
database URL, API key, GitHub token, provider authorization header, raw
provider body or stack trace.

**Request limits and budgets.** Body ceilings 16/64/32/256/256 KiB
(normalize/discover/enrich/resolve/refine) rejected with 413 before
application work. Operation budgets: health and ready 3s, inspection 5s,
resolve and refine 10s, enrich 35s, normalize 45s, discover 50s. Global
`WriteTimeout` 65s, always larger than the longest budget. No automatic
retries.

**Cancellation.** `http.Server.BaseContext` returns the serve context, so
shutdown reaches in-flight requests and propagates to PostgreSQL, OpenAI,
GitHub, pkg.go.dev and deps.dev.

**External-operations switch.** `REUSERY_API_ENABLE_EXTERNAL_OPERATIONS`,
strictly `true`/`false`, default `false`. See Configuration above.

**No filesystem or provider-base-URL inputs.** No request field named `root`,
`path`, `manifest`, `profile_file`, `policy_file` or `corpus_file`, and no
request can configure an OpenAI/GitHub/pkg.go.dev/deps.dev base URL.

**Retry and idempotency decision.** GET operations are retry-safe. The five
POST operations are not: normalize costs model tokens, discover and enrich
record a new observation time, resolve and refine may insert a Resolution.
No in-memory response cache, no process-local key map, no pretend
`Idempotency-Key`, and no persistence of raw intent or transient
assessments — that would reverse deliberate Packet 6/7 persistence decisions
and invent a data-retention policy before private-project design exists.
Documented as a deliberate roadmap refinement in `docs/http-api.md`.

**No authentication, no CORS.** Packet 8 has no API keys, accounts, sessions
or OAuth (Packet 13 owns identity) and no `Access-Control-Allow-Origin`.
The external-operations switch exists precisely because there is no
authenticated boundary yet.

**API integration test layout.**

- `internal/api/*_test.go` — offline unit tests with fakes: health/readiness,
  middleware, normalize, discover, enrich, resolve, refine, inspection,
  evidence pagination and the OpenAPI document. No network, no database.
- `internal/api/integration_test.go` (`-tags=integration`) — real PostgreSQL
  via Testcontainers with `httptest` discovery/enrichment upstreams and a
  fake model: the inspection → resolve → refine flow, the full
  normalize → discover → enrich → inspect → resolve → refine → inspect
  transport flow, and the external-operations-disabled smoke.

**CI network policy.** CI never calls OpenAI, GitHub, pkg.go.dev or deps.dev;
every API test injects fakes or `httptest` upstreams, so a provider outage
cannot make CI red.

**Manual live smoke procedure**

```powershell
# 1. migrate, then seed the canonical first primitive (see README workflows)
$env:REUSERY_API_ENABLE_EXTERNAL_OPERATIONS = 'true'
reusery serve

# 2. operational + contract routes
curl http://localhost:8080/health
curl http://localhost:8080/ready
curl http://localhost:8080/openapi.json
start http://localhost:8080/docs

# 3. the four stages over HTTP, then inspection
curl -X POST http://localhost:8080/v1/normalize -H 'Content-Type: application/json' -d '{"input":"..."}'
curl -X POST http://localhost:8080/v1/discover  -H 'Content-Type: application/json' -d '{...profile...}'
curl -X POST http://localhost:8080/v1/enrich    -H 'Content-Type: application/json' -d '{...specimen_ids...}'
curl -X POST http://localhost:8080/v1/resolve   -H 'Content-Type: application/json' -d '{...candidates + policy...}'
curl -X POST http://localhost:8080/v1/refine    -H 'Content-Type: application/json' -d '{...+ feedback...}'
curl 'http://localhost:8080/v1/contracts?id=process%2Fbounded-subprocess%2Fv1'
curl 'http://localhost:8080/v1/evidence?subject_id=...&limit=50'
curl http://localhost:8080/v1/resolutions/1

# 4. repeat with the switch off (or unset): /health, /ready, inspection and
#    resolve/refine still work; normalize, discover and enrich return 503
#    code external_operations_disabled, and no paid/provider call is made.
```

## MCP agent interface and production CLI (Packet 9)

Full reference: [docs/mcp.md](mcp.md). The agent-facing skill lives at
[`skills/reusery/SKILL.md`](../skills/reusery/SKILL.md).

**Dependency.** Exactly one new direct dependency:
`github.com/modelcontextprotocol/go-sdk v1.8.0`, package `mcp` — the official
SDK. Reusery does not hand-roll JSON-RPC, stdio framing, protocol negotiation,
tool schema transport or cancellation. Transitive additions: `google/jsonschema-go`,
`segmentio/encoding` + `segmentio/asm`, `yosida95/uritemplate/v3`,
`golang.org/x/oauth2`, `golang.org/x/time`. No second MCP SDK, no agent
framework, no LangChain, no custom JSON-RPC library.

**Protocol.** The SDK negotiates the current revision `2026-07-28` plus the
older revisions it advertises. Reusery does not parse protocol versions and is
not pinned to one; a test forces `2025-11-25` through the SDK's own
`ClientSessionOptions.ProtocolVersion` and requires the conversation to work.
Packet 9 is a TOOLS server and does not use roots, sampling or protocol
logging (deprecated in the 2026-07-28 revision), nor prompts or resources.

**Transport: stdio only.** `reusery mcp` spawns nothing of its own; the agent
host runs it as a child. stdout carries MCP frames and nothing else — no
banner, no log, no diagnostics. Operational logging goes to stderr, enforced by
a source-level test over the package's production files. Remote MCP is
deliberately deferred: an unauthenticated, internet-facing, expensive MCP
surface before Packet 13 (identity) and Packet 15 (abuse controls) would be the
wrong thing to add, and Packet 8 already owns the remote HTTP surface. No
`/mcp`, no `/mcp/sse`, no streamable handler, no MCP listener exists in
production code.

**Composition.** `internal/mcpserver` depends on `internal/app` interfaces
only. It does not import `internal/store/postgres`, `internal/api` or
`internal/intent`, and it never constructs a model provider — a calling agent
is already the reasoning surface, so a second model call would be pure
customer cost. `internal/app` gained `Catalog`, `OutcomeRecorder`,
`ListCapabilities` and `NewOutcomeRecorder`, shared by the CLI, HTTP and MCP
surfaces so provider and resolver construction is never duplicated.

**Built-in baseline policy.** `policy.PublicGoBaseline()` returns the exact
semantics of `policies/public-go-baseline-v1.yaml` so an installed binary can
resolve outside a repository checkout without reading a working-directory
file. `TestPublicGoBaselineMatchesAuthoredYAML` deep-equals the two; the YAML
stays the authored human-readable profile and neither may drift.

**Migration 00003_resolution_feedback.** Adds `resolution_feedback`
(`id`, `resolution_id` FK `ON DELETE CASCADE`, `kind` with a CHECK over the
five-value vocabulary, `note` with `char_length(note) <= 1000`, `recorded_at`)
plus `resolution_feedback_order_idx` on `(resolution_id, recorded_at, id)`.
Down drops the table. The migration lifecycle integration test covers
zero → 00001 → 00002 → 00003 → 00004 → down → up. No `agent_sessions`,
`mcp_sessions`, `tool_calls`, `conversation_history` or `prompt_history`: MCP
sessions are transport concerns.

**Outcome feedback semantics.** Package `internal/outcome` is a domain type,
not `model.Evidence`, because it does not mean what Evidence means. Note limit
is **1000 characters (runes)**, documented because the database constraint is
`char_length`, which counts characters. `Service.Report` confirms the
Resolution exists first, then appends. Reporting never alters the Resolution,
never creates Evidence, never changes a policy and never re-resolves. Notes are
never logged. Packet 10 owns remembered project preferences.

**MCP contract drift.**

```bash
go run ./cmd/mcpcontract -write mcp/reusery-tools-v1.json
go run ./cmd/mcpcontract -check  mcp/reusery-tools-v1.json
make mcp-contract
make mcp-contract-check
```

`internal/mcpserver.Contract` builds the real server, connects an official-SDK
client over in-memory transports, reads `tools/list` and renders a stable
snapshot (contract version, server name, supported protocol revisions, and each
tool's name, description, annotations, input and output schema — sorted by
name). No PostgreSQL, no model key, no provider token, no network. Wired into
`scripts/check.ps1`, `make check` and CI alongside the OpenAPI gate.

**CLI additions (incremental, not a rewrite).** `reusery mcp`, `version`,
`catalog`, `evidence`, `outcome`, `outcomes`. Every existing command still
works.

**CLI stdin rules.** A document argument of `-` reads standard input:
`resolve --request -`, `enrich --request -`, `choose --request/-/--policy-/
--feedback-`, `discover --profile -`, `normalize --file -`,
`normalize-eval --corpus -`. Where a command accepts multiple document inputs,
**at most one may be `-`**; `reusery choose --request - --policy -` returns
exit code 2 rather than silently reading one stream twice. Ordinary file inputs
keep every path-security check: choosing `-` changes where bytes come from,
never where a path may reach. Supporting this added reader-shaped helpers
(`policy.Decode`, `policy.DecodeFeedback`, `discovery.DecodeProfile`,
`intent.LoadCorpusReader`); the path-based loaders now delegate to them with
identical behaviour.

**Exit codes (unchanged, now documented).** `0` successful product result,
including `needs_verification`; `1` execution/configuration/provider/storage
failure; `2` usage or structurally invalid input. `needs_verification` is never
an exit-code failure.

**Integration-test architecture.** `internal/mcpserver/integration_test.go`
runs the real Packet 2 evaluator, Packet 3 store and Packet 7 quality service
behind an MCP client over in-memory transports, with httptest pkg.go.dev,
GitHub and deps.dev upstreams. `internal/mcpserver/stdio_test.go` compiles the
actual `reusery` binary and drives it through the official SDK's
`mcp.CommandTransport` against Testcontainers PostgreSQL with external
operations disabled — the stdout-purity proof, since a stray line would break
the handshake.

**CI network policy.** CI may use Testcontainers PostgreSQL, in-memory MCP
transports, local child processes and `httptest` upstreams. It must never call
OpenAI, GitHub, pkg.go.dev or deps.dev.
## Project context and remembered decisions (Packet 10)

Full reference: [project-context.md](project-context.md).

**Standing rule.** Project context may change fit; it must not change truth.
It can add a review requirement, add an inspectable trade-off, or break a tie
among candidates that are already implementation-eligible. It can never turn
blocked into eligible, unknown behaviour into satisfied, or policy review into
allow, and it never produces Evidence.

**Dependency.** Exactly one new direct dependency: `golang.org/x/mod v0.41.0`,
package `modfile` — Go's own manifest parser, so `go.mod` and `go.work` are read
by the implementation that defines them rather than by a hand-rolled parser.
Reusery uses `modfile.Parse`, `modfile.ParseWork` and `modfile.IsDirectoryPath`.
Note that `module.IsLocalImport` and `modfile.IsLocalImport` do **not** exist in
v0.41.0. No dependency graph resolution, no semantic-version library, no
`golang.org/x/mod/semver` comparison: version fit is exact string comparison.

**Domain package.** `internal/project` owns the whole domain and does not import
`internal/store/postgres`:

| File | Responsibility |
| --- | --- |
| `types.go` | types, closed vocabularies, bounds, sentinel errors |
| `canonical.go` | canonicalisation, `FingerprintHash`, `ProjectID`, `ContextHash` |
| `fingerprint.go` | shared fingerprint construction from parsed manifests |
| `local.go` | bounded local reading (`go.work` + `go.mod` only) |
| `github.go` | unauthenticated public GitHub reading, immutable SHA first |
| `preferences.go` | reason → preference derivation, policy overlay, dependency fit |
| `context.go` | context snapshot, effective policy ID, active preferences |
| `decide.go` | project-aware decisions through the existing quality service |
| `service.go` | the `Repository` interface and the storage-facing service |

**Identity and hashes.** `ProjectID` = `project/go/` + SHA-256 over
`"reusery-project-go-v1\n"` and the sorted unique module paths, so two checkouts
of the same module are one project and no path is ever stored. The fingerprint
hash covers modules, requirements and replacements only — never storage IDs,
`observed_at`, the root, warnings, source kind or locator. The project context
hash additionally covers the active preference effects a decision saw.

**Local privacy.** Never persists the local root; a `replace ... => ../foo`
records only `local_replacement: true`; never reads `*.go`, `vendor/` or `.git`;
never runs `go`, `git` or a shell. Bounds: 32 workspace modules, 512 KiB per
manifest, 33 manifest files, 8 MiB total, 5 seconds.

**Public GitHub.** `github.NewClient("")` only — no token is ever attached, so a
private repository fails as unsupported rather than leaking that it exists.
Resolves the ref to an immutable commit SHA first, then only
`/repos/{owner}/{repo}`, `/commits/{ref}` and `/contents/{path}`. Never clones,
never walks the tree. Bounds: 40 requests, 15 seconds, 1 MiB response, 512 KiB
decoded. Source kinds are a closed pair: `local` and `github_public`.

**Preferences.** Six kinds in a closed vocabulary: `exclude_candidate`,
`max_direct_dependencies`, `deny_licence`, `avoid_dependency`,
`avoid_reference`, `deny_archived`. The caller supplies a structured
`policy.FeedbackReason` and never a value; `DerivePreference` reads facts
Reusery already stored and fails with `ErrPreferenceUnsupported` when they
cannot support the requested memory. `remember` is idempotent, `forget` sets
`forgotten_at` without deleting the row, and `source_reason` records provenance.
`internal/project` and `internal/outcome` are asserted to share no code: refine
feedback and outcome events can never become memory.

**Policy overlay.** `ApplyPreferences` deep-clones the stored base policy; the
effective policy ID becomes `<base-id>+project:<full-context-hash>`.
`exclude_candidate` is expressed as ordinary structured feedback filtered to
the candidates present, so the exclusion stays visible in rejection reasons and
`ErrUnknownFeedbackCandidate` cannot fire.

**Dependency fit.** Five statuses: `not_applicable`, `new_dependency`,
`existing_exact`, `existing_version_change`, `existing_replaced`. Exact version
comparison only. Module matching is a longest module-path prefix on path
segment boundaries, so `github.com/foo` never matches `github.com/foobar/x`.
`existing_exact` is a late lexicographic tie-break after disposition rank and
authored preferred reuse mode — never a score. Version-change and replaced fits
add a trade-off via the exported `resolver.ProjectTradeoffMessage` (one wording,
shared by the assessment and the per-candidate `project_effects` entry) and
lower an `implementation_eligible` disposition to `needs_review`.

**Migration 00004_project_context.sql.** Adds `projects` (with a CHECK that a
`local` row has an empty `source_locator`), `project_fingerprints` (unique per
`(project_id, sha)`, newest-first index), `project_preferences` (kind and
`source_reason` CHECKs, `forgotten_at`, partial active index) and
`project_contexts`, plus `resolutions.project_id` and
`resolutions.project_context_hash` as `ON DELETE SET NULL` foreign keys. Down
drops the columns first, then the tables. The lifecycle test observes the schema
at each version, because the typed resolution loader only works once 00004 is
applied — older rows are checked with a raw query in between.

**Resolver integration (no second resolver).** `resolver.QualityInput` gained
optional `ProjectID`, `ProjectContextHash` and `Context map[string]CandidateContext`;
`QualityRequest` gained exported `ProjectID`/`ProjectContextHash` plus an
**unexported** context field with an exported setter, so no request document can
forge it. `Decide` applies candidate context after `Assess` and before the
feedback-exclusion override. With no project context the Packet 7 path is
byte-identical, which the packet tests assert directly.

**Surfaces.** CLI: `reusery project scan|show|remember|forget|history`,
`choose --project-id`, `mcp --project-root`. The local root is process
configuration and is validated as a directory *before* configuration loads.
MCP: four additive tools, `project_id` optional on `reusery_resolve` and
`reusery_refine`, `mcp/reusery-tools-v1.json` regenerated to 12 tools.
**HTTP and OpenAPI are unchanged**: `go run ./cmd/openapi -check` must still
pass byte-for-byte, there are no project endpoints, and no filesystem path
field exists anywhere in the contract.

**Integration tests.** `internal/mcpserver/project_pg_test.go` proves the whole
rule on real PostgreSQL: same primitive, contract, candidates and base policy —
only the project context differs — and the decision changes, is explained, and
returns to its previous answer after a forget. `internal/cli/project_integration_test.go`
walks scan → show → choose → remember → history → forget → show against real
PostgreSQL, parsing every JSON document from stdout. `internal/store/postgres/project_integration_test.go`
proves local and public scans share one fingerprint row and that no table ever
contains the local root. `internal/mcpserver/stdio_test.go` compiles the real
binary with `--project-root` and scans it over stdio.

## Quality checks

```powershell
gofmt -s -l -w .            # formatting (write)
sqlc generate            # regenerate query code
git diff --exit-code -- internal/store/postgres/sqlc   # drift check
go run ./cmd/openapi -check openapi/reusery-v1.json    # OpenAPI drift check
go run ./cmd/mcpcontract -check mcp/reusery-tools-v1.json  # MCP tool drift check
go vet ./...             # static analysis
go vet -tags=integration ./...
go test ./...            # unit tests
go test -tags=integration ./... # Docker required
golangci-lint run ./...  # lint (config: .golangci.yml)
govulncheck ./...        # vulnerability scan
go build ./cmd/reusery   # build
go build ./cmd/openapi   # contract generator builds
go build ./cmd/mcpcontract # MCP contract generator builds
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
  `govulncheck`, the sqlc drift check, the OpenAPI contract drift check and the
  MCP tool contract drift check.

## Tests

Standard `testing` only. Coverage is behavioural, not numeric:

- `internal/server` — handler wiring, the corrected timeout set (including
  the 65s write timeout) and `BaseContext` cancellation propagation, plus the
  graceful-shutdown lifecycle test. `/health` and `/ready` behaviour is
  asserted in `internal/api` now that routes live there.
- `internal/mcpserver` — the exact 12-tool surface (the 8 Packet 9 tools plus
  the 4 project tools) and no normalize/health/openapi
  tool; tool annotations; server identity and bounded instructions; catalog list
  and detail; discovery and enrichment gates with zero provider calls while
  disabled; resolve producing DEPEND, REFERENCE, BUILD LOCALLY and
  needs_verification through the real Packet 7 QualityService; refine applying
  all six feedback reasons as genuine re-resolution; evidence continuation and
  cursor validation; remembered-resolution inspection; append-only outcome
  reporting that never mutates a Resolution or creates Evidence; stable error
  codes with no secret leakage; payload byte budgets; the checked-in tool
  contract (drift, annotations, optional `project_id`, no filesystem/credential
  inputs, no internal schema names); official-SDK protocol negotiation
  including a forced legacy revision; cancellation reaching the tool handler;
  source-level boundary tests (stdio only, no HTTP transport, no model
  provider, no stdout writes); project scanning from a configured root, the
  project-aware decision explained by its own reasons, remember/forget
  round-trips, and the PostgreSQL proof that project context changes a decision
  for factual reasons while outcome events never become preferences.
- `internal/outcome` — closed kind vocabulary, note boundary in characters,
  unknown Resolution rejection, chronological append-only listing, and proof
  that reporting changes neither Resolution, Evidence nor the decision.
- `internal/api` — health/readiness (Packet 1 semantics preserved, including
  "health never runs a readiness checker"), middleware (request id accept /
  generate / replace, `Reusery-API-Version`, `Cache-Control: no-store`,
  `X-Content-Type-Options`, log fields with no bodies or secrets, no CORS),
  normalize (all three valid statuses → 200, invalid input → 422, disabled →
  503, unconfigured model → 503, upstream auth/rate-limit/timeout mappings,
  413, unknown field → 422, arrays never null), discover (success, zero
  candidates, partial failure → 200 with issues, all-failed → 502 with safe
  issues, invalid profile → 422, disabled → 503, 413, filesystem fields
  rejected), enrich (success, partial → 200, all-failed → 502, empty/absent/
  duplicate/oversized → 422, evidence conflict → 409, disabled → 503),
  resolve/refine through the real Packet 7 `QualityService` over an
  in-memory repository (eligible → resolved, reference → REFERENCE with
  honest wording, all blocked → BUILD LOCALLY, metadata-only →
  needs_verification with nothing persisted, feedback refinements for all six
  reasons, determinism, negative knowledge), inspection of slash-containing
  opaque IDs with 404s, evidence pagination (default 50, limit 1/100/101,
  ordering, cursor round-trip, malformed cursor → 422, no duplicate or skip,
  empty final page) and the OpenAPI document (3.1, operation IDs, paths,
  tags, enums, nullable decision fields, body limits, problem schema, no
  internal type names, golden drift). Integration variants run against real
  PostgreSQL with `httptest` upstreams.
- `internal/resolver` — deterministic evaluation semantics (Packet 2), the
  resolution kernel and the application service (Packet 4), and the Packet 7
  quality layer: dispositions, policy-driven assessment, lexicographic
  ordering, tie-break disclosure, bounded shortlisting, feedback-driven
  re-resolution and `needs_verification` persisting nothing. Packet 10 added
  the additive project-context hooks: `DependencyFit` and its five statuses,
  `CandidateContext`, the `project_dependency` assessment dimension, the
  shared `ProjectTradeoffMessage`, and the late exact-version tie-break — with
  a test proving an empty project context leaves the Packet 7 path unchanged.
- `internal/policy` — fact extraction (including conflicting and malformed
  artifacts), strict policy loading, every allow/review/deny rule, and each
  supported feedback reason with its refinement and error paths.
- `internal/enrichment` — evidence identity stability, the INFO/UNKNOWN-only
  trust rules, budgets, partial failure and idempotent append-only persistence.
- `internal/enrichment/providers/*` — `httptest` fixture tests for deps.dev
  request mapping, module-path escaping, exact version preservation, zero/one/
  many licences, advisory counts (including zero), dependency counts,
  deprecation, malformed JSON, oversized bodies, 404/429/5xx/timeout, request
  budget, and for GitHub repository/code mapping, licence variants
  (`null`/`NOASSERTION`/`NONE`), auth, rate limits, timeouts, token-free
  errors, and proof that no popularity field or behavioural evidence is
  produced.
- `internal/benchmark` — strict record loading, pairing rules (missing side,
  variant mismatch, success mismatch), delta and savings-percentage rules,
  unknown preservation, and measured/estimated separation.
- `internal/catalog` — strict YAML loading, path-escape rejection, relationship
  validation and idempotent seeding.
- `internal/cli` — command parsing, output formats, exit codes, stdout/stderr
  separation, `discover` with injected discovery behaviour (no network),
  `enrich` with an injected enricher, `choose` with an injected repository and
  an authored policy (no network), and `normalize` / `normalize-eval` with an
  injected normaliser, including the proof that none of these leak network or
  database access into a command that must not have it. Packet 10 added the
  `project` sub-command surface: sub-command and flag validation, mutually
  exclusive scan sources, required-flag and bound checks, unsupported-reason
  rejection, usage text, and `reusery mcp --project-root` failing before any
  configuration is read.
- `internal/config` — defaults, env overrides, blank-value handling, the strict
  REUSERY_API_ENABLE_EXTERNAL_OPERATIONS parse (true/false only, nonsense
  rejected, default false, never leaking the database URL), missing and
  malformed database URL rejection, level parsing, the optional GitHub
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
- `internal/store/postgres` (`-tags=integration`) — migration lifecycle across
  zero → 00001 → 00002 → 00003 → 00004 → down → up (including a pre-00002 row
  loading with an empty `policy_id`, a pre-00004 row loading with no project
  context, and the 00004 rollback leaving 00001–00003 intact), round-trips for
  every domain object, requirement and rejection ordering, `PolicyID`
  round-trips, transaction rollback, readiness, `persist → reload →
  resolver.Evaluate`, plus the proof that a local scan and a public scan of the
  same manifests share one fingerprint row and that no project table ever
  contains the local root.
- `internal/cli` (`-tags=integration`) — the full vertical slice against real
  PostgreSQL: migrate → seed → resolve → persist → inspect, for both `depend`
  and `build_locally`; plus public discovery against real PostgreSQL with
  `httptest` providers, including the assertion that persisted provider
  evidence leaves every required requirement `unknown`; plus the Packet 10
  project slice: scan → show → choose → choose with `--project-id` →
  remember → history → forget → show, parsing every JSON document from stdout
  and asserting a failing command leaves stdout empty.
- `internal/project` — canonicalisation and hash determinism (including hash
  sensitivity and the fact that the root never enters the project ID), the
  local scanner's bounds and its refusal to read anything outside `go.work` /
  `go.mod` (including a source-level test that the package cannot reference
  `os/exec`, `filepath.Walk` or `os.ReadDir`), the unauthenticated public
  scanner against `httptest` (immutable SHA, private rejection, unsafe
  subdirectories, response and workspace bounds, no tree endpoint, no
  `Authorization` header, and local ↔ public fingerprint agreement), preference
  derivation for all six reasons with its failure paths, the policy overlay's
  non-mutation of the stored base, and the proof that `Decide` persists nothing
  a caller did not ask for.
