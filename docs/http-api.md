# Reusery HTTP API v1

Reusery exposes its existing application services through a stable, versioned
HTTP/JSON API with an inspectable OpenAPI 3.1 contract. The CLI is one client
of those services; HTTP is another. Packet 9's MCP adapter will be a third.
None of them own product logic.

**The rule for this boundary:** HTTP adapts to Reusery. Reusery does not
become HTTP-shaped internally.

> **Not production internet ready.** Packet 8 establishes the service
> contract. It does *not* make Reusery safe to expose as an unrestricted
> production internet service. Edge protection, rate limiting, identity
> quotas, concurrency controls and abuse budgets arrive later (Packet 13
> owns identity, Packet 15 owns HTTP hardening). There is no authentication
> in v1, deliberately, because there is no identity model yet.

## Starting the server

```bash
export REUSERY_DATABASE_URL='postgres://reusery:reusery@localhost:5432/reusery?sslmode=disable'
reusery serve
```

The server starts without `REUSERY_OPENAI_API_KEY`: the model provider is
only constructed inside a `/v1/normalize` request.

| Route | Purpose |
| --- | --- |
| `GET /health` | Liveness. Never touches a dependency. |
| `GET /ready` | Readiness. PostgreSQL only; degradable providers are never checked. |
| `GET /openapi.json` | Canonical OpenAPI 3.1 contract (JSON). |
| `GET /openapi.yaml` | The same contract as YAML (documentation output only). |
| `GET /openapi-3.0.json`, `/openapi-3.0.yaml` | Downgraded 3.0 copies for older tooling. |
| `GET /docs` | Generated API reference (Stoplight Elements). |
| `GET /schemas/{schema}` | Individual JSON Schemas. |
| `/v1/...` | Product operations. |

## External operations are disabled by default

```bash
REUSERY_API_ENABLE_EXTERNAL_OPERATIONS=false   # the default
```

When `false`, these routes stay **documented** but answer:

```
503 Service Unavailable
{"status":503,"title":"Service Unavailable","detail":"HTTP model, discovery and enrichment operations are disabled; set REUSERY_API_ENABLE_EXTERNAL_OPERATIONS=true to enable them","code":"external_operations_disabled"}
```

- Gated: `POST /v1/normalize`, `POST /v1/discover`, `POST /v1/enrich`
- Always available: `resolve`, `refine`, inspection, `health`, `ready`
- The equivalent **CLI** commands are unaffected either way
- Readiness never fails because the switch is false
- The value is parsed strictly: only `true` or `false`, case-insensitively.
  Anything else (for example `yes-please`) is a configuration error.

This switch exists precisely because Packet 8 has no authenticated
production boundary, and it stops an unauthenticated paid-model endpoint
being enabled by accident.

## The four stages are explicit

`/v1/normalize`, `/v1/discover`, `/v1/enrich` and `/v1/resolve` are four
separate native operations. **There is no hidden chain.** Packet 6 intent
contracts are still provisional and are not automatically mapped to
discovery profiles, so the client explicitly chooses the stages and carries
structured data between them.

## Stable operation IDs

Operation IDs are part of the frozen contract. Renaming one is a breaking
change and belongs in `/v2`.

| Operation ID | Method | Path |
| --- | --- | --- |
| `healthCheck` | GET | `/health` |
| `readyCheck` | GET | `/ready` |
| `normalizeIntent` | POST | `/v1/normalize` |
| `discoverCandidates` | POST | `/v1/discover` |
| `enrichCandidates` | POST | `/v1/enrich` |
| `resolveCandidates` | POST | `/v1/resolve` |
| `refineResolution` | POST | `/v1/refine` |
| `getPrimitive` | GET | `/v1/primitives` |
| `getContract` | GET | `/v1/contracts` |
| `getSpecimen` | GET | `/v1/specimens` |
| `listEvidence` | GET | `/v1/evidence` |
| `getResolution` | GET | `/v1/resolutions/{resolution_id}` |

All product operations live under `/v1`. There is no `/latest`, no `/v1beta`
and no `Accept-Version` negotiation: a breaking future contract becomes
`/v2`. `info.version` is `v1`, deliberately uncoupled from any Reusery
product release number.

## Opaque Reusery IDs

Reusery domain IDs contain `/`, `@`, `:` and `%`:

```
process/bounded-subprocess/v1
public/github/code/owner/repo@9ca7c88…:proc_attr_linux.go
```

They are **never** placed in wildcard path segments. Primitive, contract,
specimen and evidence-subject IDs are query parameters, treated as opaque
strings. **Clients must not parse them for semantics.**

The only path parameter is `resolution_id`, which is the numeric PostgreSQL
storage identity and therefore safe as a path segment.

## Request IDs

Send an optional `X-Request-ID` (alphanumerics plus `- _ . :`, at most 128
characters). Every response — success or error — returns one. A missing,
oversized or unsafe value is **replaced** by a generated identifier rather
than rejected: correlation never fails a request, and no security decision
is ever derived from this ID.

## Response headers

| Header | Value |
| --- | --- |
| `Reusery-API-Version` | `v1`, on every response including errors |
| `X-Request-ID` | correlation id |
| `Cache-Control` | `no-store` |
| `X-Content-Type-Options` | `nosniff` |

There is no CORS policy in Packet 8. Packet 11's web client is same-origin
by default; Packet 13/15 define production cross-origin and auth policy if
one is ever needed.

## Request body limits

Oversized bodies are rejected with **HTTP 413** before any application work
runs.

| Operation | Limit |
| --- | --- |
| `POST /v1/normalize` | 16 KiB |
| `POST /v1/discover` | 64 KiB |
| `POST /v1/enrich` | 32 KiB |
| `POST /v1/resolve` | 256 KiB |
| `POST /v1/refine` | 256 KiB |

JSON only. No CBOR, no XML, no YAML request bodies, no multipart.

Strict requests: unknown query parameters are rejected, and request object
schemas reject unknown fields — a typo such as `"speciman_id"` is never
silently ignored. Schema validation runs first; the existing deterministic
domain validation runs after mapping. Schema validation does not replace
product validation.

**Arrays must be sent as `[]`, not `null`.** v1 serialises every collection
as `[]` and reserves `null` for optional single objects (`resolution`,
`selected`, `next_cursor`, `primitive`, `contract`).

## Operation budgets

Each handler wraps its service call in an explicit deadline. These sit
outside the already-bounded internal provider and service calls, so the
tighter inner deadline always wins. There are no automatic retries.

| Operation | Budget |
| --- | --- |
| health, ready | 3 s |
| inspection GETs | 5 s |
| resolve, refine | 10 s |
| enrich | 35 s |
| normalize | 45 s |
| discover | 50 s |

The global server `WriteTimeout` is **65 s**, deliberately larger than the
largest operation budget so the server never kills a legitimate bounded
operation first. `ReadHeaderTimeout` 5 s, `ReadTimeout` 10 s,
`IdleTimeout` 60 s remain unchanged. Server shutdown cancels the serve
context, which is the `BaseContext` of every request, so cancellation
propagates to PostgreSQL, OpenAI, GitHub, pkg.go.dev and deps.dev through
`context.Context`.

## Error format

Errors use RFC 9457 `application/problem+json` with one stable
machine-readable extension, `code`:

```json
{
  "status": 422,
  "title": "Unprocessable Entity",
  "detail": "candidate request is invalid",
  "code": "invalid_request",
  "errors": [{"message": "expected array", "location": "body.candidates"}]
}
```

Go error types, stack traces, database URLs, API keys, GitHub tokens,
provider authorization headers and raw provider response bodies are never
returned.

### Stable codes

`bad_request` · `validation_failed` · `invalid_request` · `not_found` ·
`conflict` · `external_operations_disabled` · `model_provider_unconfigured` ·
`upstream_authentication` · `upstream_rate_limited` · `upstream_timeout` ·
`upstream_unavailable` · `all_providers_failed` · `internal_error`

### Status mapping

| Situation | Status | Code |
| --- | --- | --- |
| Malformed JSON | 400 | `bad_request` |
| Request schema/field validation, unknown query parameter | 422 | `validation_failed` |
| Domain validation (policy, profile, feedback, cursor) | 422 | `invalid_request` |
| Missing primitive, contract, specimen or resolution | 404 | `not_found` |
| Evidence identity conflict | 409 | `conflict` |
| Request body too large | 413 | `invalid_request` |
| External HTTP operation while the switch is off | 503 | `external_operations_disabled` |
| No OpenAI key for `/v1/normalize` | 503 | `model_provider_unconfigured` |
| Upstream authentication/config failure, rate limit | 503 | `upstream_*` |
| Every configured provider failed | 502 | `all_providers_failed` |
| Upstream timeout or operation budget exceeded | 504 | `upstream_timeout` |
| Unknown internal/storage failure | 500 | `internal_error` |

**502 is used when an upstream service failed while Reusery itself is
operating**; 503 is reserved for "this server will not do that right now".

### Partial provider failure

Discovery and enrichment partial failure is a valid product result. If at
least one provider succeeded the response is **HTTP 200** with the provider
reports, issues and successful output intact. Only an all-provider failure
becomes a 502 — and even then the safe provider issue messages are preserved
in the error's `errors` list.

## Operation reference

### `normalizeIntent` — `POST /v1/normalize`

`{"input": "I need..."}` → a Packet 6 `IntentResult`.

`ready`, `needs_clarification` and `unsupported` are all valid product
results and all return **200**. Only a model-provider or configuration
failure is an HTTP error. This operation does not persist intent, does not
discover, does not enrich and does not resolve.

### `discoverCandidates` — `POST /v1/discover`

Body is the structured Packet 5 discovery profile supplied directly as JSON.
The API never accepts a filesystem path, `--root` or a profile filename —
HTTP is not a remote filesystem interface, and that also removes an entire
class of path-handling attack surface.

```json
{
  "schema_version": 1,
  "primitive_id": "process/bounded-subprocess",
  "contract_id": "process/bounded-subprocess/v1",
  "providers": [{"id": "pkg.go.dev", "queries": [{"text": "subprocess runner cancellation", "limit": 5}]}]
}
```

Packet 5 semantics are preserved: provider data is INFO/UNKNOWN only,
partial provider failure remains inspectable, and **discovery is not
verification**.

### `enrichCandidates` — `POST /v1/enrich`

`{"specimen_ids": ["..."]}` → Packet 7 trust rules apply unchanged. It does
not resolve, does not select, and can never create behavioural PASS or FAIL.

### `resolveCandidates` — `POST /v1/resolve`

The stable HTTP resolver operation. It uses Packet 7's `QualityService` —
not an API-specific resolver, and never the Packet 4 first-acceptable kernel.

The **policy is supplied as structured JSON** (`schema_version`, `id`,
`reuse`, `license`, `security`, `dependencies`, `maintenance`, `source`,
`selection`); clients never submit a filesystem policy path. Initial resolve
requests carry no feedback: a `feedback` field on `/v1/resolve` is rejected.

Successful statuses (both **HTTP 200**):

- `resolved` → `resolution_id` and `resolution` present, exactly one
  Resolution persisted
- `needs_verification` → `resolution_id` **null**, `resolution` **null**,
  nothing persisted. It is not a failure and not BUILD LOCALLY.

### `refineResolution` — `POST /v1/refine`

`"Not quite"` → structured reason → deterministic re-resolution. The request
carries `primitive_id`, `contract_id`, the candidate set, the **original
base policy** and the **complete accumulated feedback list** (at least one
item).

**Refinement is stateless.** The server derives the effective policy from
scratch on every call. There is no hidden HTTP session. Clients must resend
their own original base policy, not a previously derived effective policy —
feeding an effective policy back as the base would apply feedback twice.

Supported reasons: `not_quite`, `too_many_dependencies`,
`licence_not_allowed`, `avoid_dependency`, `avoid_reference`,
`archived_project`. Anything else is 422.

This endpoint calls the same Packet 7 quality service as `/v1/resolve` and
implements no feedback logic of its own.

### Inspection

`GET /v1/primitives?id=…`, `GET /v1/contracts?id=…`,
`GET /v1/specimens?id=…`, `GET /v1/resolutions/{resolution_id}`.

`getResolution` returns the remembered decision exactly as recorded; it does
**not** re-evaluate current evidence.

### Evidence pagination

```
GET /v1/evidence?subject_id=<opaque-id>&limit=50&cursor=<opaque>
```

- Ordered by `observed_at` ascending then evidence id ascending
- Default limit 50, maximum 100 (limit 101 → 422)
- `next_cursor` is `null` on the final page; an empty final page is
  `{"evidence": [], "next_cursor": null}`
- The cursor is opaque: a base64url versioned payload. It never exposes a
  SQL offset and never uses numeric OFFSET pagination
- A malformed, oversized (over 2048 bytes) or wrong-version cursor →
  422 `invalid_request`
- The API never emits an unbounded evidence array

## Decision response shape

```json
{
  "status": "resolved",
  "policy_id": "public-go-baseline/v1",
  "resolution_id": 12,
  "resolution": { "...": "..." },
  "selected": { "...": "..." },
  "shortlist": [],
  "assessments": [],
  "effective_policy": { "...": "..." },
  "applied_feedback": []
}
```

### What the transport deliberately does not do

- **REFERENCE keeps its meaning.** A reference resolution may contain
  required behavioural unknowns and preserves the reasons *"selected as
  reference-only engineering knowledge"* and *"behavioural contract
  satisfaction is not established"*. The transport never simplifies
  REFERENCE into `recommended`, `verified` or `approved`.
- **`needs_verification` is never an HTTP error and never becomes BUILD
  LOCALLY.**
- **BUILD LOCALLY is a valid resolution outcome**, not an error.
- **Provider metadata can never create behavioural PASS.**
- Handlers validate transport shape, map DTOs, call an application service,
  map the result and translate safe errors. They do not evaluate evidence,
  rank candidates, apply licence rules, apply feedback, invent unknowns,
  decide REFERENCE or decide BUILD LOCALLY. If deleting the HTTP package
  would change resolver behaviour, the architecture would be wrong.

## Retry safety and idempotency

There are **no automatic retries**, HTTP or provider. A POST retry could
spend model tokens again, record a new observation time, or insert another
Resolution — clients must know which calls are retry-safe.

| Operation | Retry safe? | Why |
| --- | --- | --- |
| all `GET`s (`/health`, `/ready`, inspection, evidence, OpenAPI) | yes | read-only |
| `POST /v1/normalize` | **no** | costs model tokens |
| `POST /v1/discover` | **no** | records a new observation time |
| `POST /v1/enrich` | **no** | records a new observation time |
| `POST /v1/resolve` | **no** | may insert a new Resolution |
| `POST /v1/refine` | **no** | may insert a new Resolution |

**Durable idempotency is deliberately deferred.** Packet 8 does not
implement fake idempotency with an in-memory response cache, a
process-local key map or a pretend `Idempotency-Key` header, and it does not
persist raw normalisation requests or transient assessments just to
reproduce a HTTP response. Doing so would reverse deliberate Packet 6 and
Packet 7 persistence decisions and introduce a data-retention policy before
private-project design exists. This is a conscious roadmap refinement, not
an omission.

## No filesystem or provider-base-URL inputs

No request field named `root`, `path`, `manifest`, `profile_file`,
`policy_file` or `corpus_file` exists, and no request may configure an
OpenAI, GitHub, pkg.go.dev or deps.dev base URL. Production hosts are
code-owned; `httptest` injection is test-only.

## OpenAPI contract

`openapi/reusery-v1.json` is a shipped artifact, generated from the
registered Huma API — never hand-authored. Route registration is separated
from live service construction, so generation needs no PostgreSQL, no OpenAI
key, no GitHub token and no network.

```bash
go run ./cmd/openapi -write openapi/reusery-v1.json   # regenerate
go run ./cmd/openapi -check  openapi/reusery-v1.json   # drift gate (CI)
make openapi          # regenerate
make openapi-check    # drift gate
scripts/check.ps1     # includes the drift gate
```

CI fails if Go API types or routes change without the contract being
deliberately regenerated, which makes API changes visible in code review.

### Compatibility commitment

After Packet 8 freezes, API v1 is a compatibility commitment. Future changes
may add optional response fields, new operations and new optional request
fields where safe. They must not rename existing fields, change enum
meanings, change operation IDs, remove response fields, change an existing
field's type or reinterpret status semantics. A deliberate breaking change
belongs in `/v2`. There is no automated semantic breaking-change analyser in
Packet 8: the checked-in golden diff is the gate.

### Schema naming

Every API type is a named DTO in `internal/api` with a mapping layer to and
from internal domain types. Schema component names are the DTO names and
never expose package implementation names such as `postgres.GetResolutionRow`
or `sqlc.Evidence`.

## Architecture

```
internal/api     HTTP routes, transport DTOs, mapping, errors, middleware
internal/app     shared composition root (normalizer/discovery/enrichment/
                 quality factories + read interfaces)
internal/server  generic HTTP lifecycle: listen, timeouts, cancellation,
                 graceful shutdown
```

`internal/api` depends on interfaces and application services. It does **not**
import `internal/store/postgres`, and provider HTTP implementations never
leak into handlers.

```go
handler := api.NewHandler(api.Dependencies{...})
srv := server.New(cfg.HTTPAddr, logger, handler)
srv.Start(ctx)
```

## Logging

One bounded structured line per request:

```
request_id, operation_id, method, status, duration_ms
```

Never logged: request bodies, raw user intent, API tokens, database URL,
provider response bodies or full policy JSON. The contract operation ID is
preferred over raw URL/query strings.

## Network policy in CI

CI never calls OpenAI, GitHub, pkg.go.dev or deps.dev. Every automated API
test injects fakes or `httptest` upstreams, so a provider outage cannot make
CI red.
