# Public discovery

Packet 5 gives Reusery its first look outward. Discovery finds **plausible
candidates** for a primitive by querying bounded public provider APIs, then
normalises what it finds into ordinary `model.Specimen` values with
attributable `INFO` / `UNKNOWN` observations in the existing PostgreSQL
registry.

Discovery is **not** verification. Nothing in this document should be read as
a claim that any discovered candidate satisfies a contract — see
[resolver-kernel.md](resolver-kernel.md) for what actually decides that.

## Provider architecture

The domain lives in `internal/discovery`. One provider interface, no
micro-framework:

```go
type Provider interface {
	ID() string
	Discover(context.Context, ProviderRequest) (ProviderResult, error)
}
```

`ProviderRequest` carries only domain information — the primitive, the
contract, the profile's queries, the budget and one run timestamp. No
PostgreSQL types, no CLI types and no HTTP request objects reach a provider.

The discovery `Service` depends only on `Provider`. Concrete implementations
live in `internal/discovery/providers/…` and share one small bounded HTTP
helper, `internal/discovery/httpx`.

| Layer | Package | Role |
| --- | --- | --- |
| Domain | `internal/discovery` | profile, budget, candidates, evidence rules, service, persistence |
| Transport | `internal/discovery/httpx` | bounded GET: fixed base URL, size ceiling, content-type, status, safe errors |
| Providers | `…/providers/pkggodev`, `…/providers/github` | map provider payloads into candidates |
| Adapter | `internal/cli` | the `discover` command |

### Provider IDs

| ID | Endpoint | Produces |
| --- | --- | --- |
| `pkg.go.dev` | `GET https://pkg.go.dev/v1/search`, `GET /v1/package/{path}` | Go **packages**, mode `dependency` |
| `github-repositories` | `GET https://api.github.com/search/repositories` | **repositories**, mode `reference` |
| `github-code` | `GET https://api.github.com/search/code` | **source files**, mode `reference` |

Base URLs are code-owned constants. There is deliberately no configuration for
a provider base URL: that would be an SSRF-shaped feature waiting to happen.
Tests inject `httptest` URLs through provider constructors; production never
does.

## Discovery profiles

A profile is a strict YAML plan, loaded from beneath the repository root:

```yaml
schema_version: 1
primitive_id: process/bounded-subprocess
contract_id: process/bounded-subprocess/v1
providers:
  - id: pkg.go.dev
    queries:
      - text: subprocess cancellation
        limit: 4
```

`discovery/process/bounded-subprocess-v1.yaml` is the shipped profile.
Loading rejects unknown fields, unsupported schema versions, absolute paths and
path escape, plus structural problems: unknown or duplicate provider IDs, zero
or too many queries, blank or duplicated query text, over-long query text, and
limits outside `1…6`. Relationships are checked against the registry: the
profile's primitive and contract must match each other and the stored values.

Query wording is authored to describe the **problem** — bounded subprocess
execution — not to name particular projects. Improving query wording is
allowed; scoring the results is not.

## Bounded work model

One fixed budget, not a configuration surface:

| Limit | Value |
| --- | --- |
| providers per profile | 3 |
| queries per provider | 3 |
| results accepted per query | 6 |
| HTTP requests per provider | 20 |
| unique candidates per run | 24 |
| provider wall-clock timeout | 15s |
| response body | 2 MiB |

Providers stop when a budget is exhausted and say so: an inspectable
`budget_exhausted` issue, `incomplete: true`, and whatever was already
successfully fetched. There is no pagination crawl, no follow-every-page
behaviour and no attempt to walk GitHub's 1,000-result search ceiling.

Within one run, repeated query text and repeated candidates are deduplicated.
There is no persistent cache: a bounded one-shot CLI run does not justify
another subsystem yet.

## Evidence rules

Provider-generated evidence may be `info` or `unknown`. It may **never** be
`pass` or `fail`, and it always leaves `AppliesTo` empty.

`ObservationSpec` has no `AppliesTo` field at all, so the normal construction
path cannot aim evidence at a requirement. A defensive validation pass runs
before anything is persisted and rejects a candidate whose evidence carries a
behavioural result, a non-empty `AppliesTo`, a missing or non-deterministic ID,
a missing source, or a missing observation time.

This is the trust boundary in one sentence:

> Search relevance is not behavioural verification.

A GitHub code hit containing `exec.CommandContext` does not establish
`supports-cancellation`. A hit containing `StdoutPipe` does not establish
`drains-stdout-stderr-concurrently`. A hit containing `SysProcAttr.Setpgid`
does not establish process-tree termination. Recording the match is `info`;
nothing more.

`discovery != verification` is asserted by an integration test that feeds live
shape provider evidence from PostgreSQL into the Packet 2 evaluator and
requires every required requirement to stay `unknown`.

### Evidence kinds

| Provider | Kinds |
| --- | --- |
| `pkg.go.dev` | `discovery_match`, `package_synopsis`, `package_module`, `package_version`, `package_standard_library`, `package_redistributable`, `source_license` |
| `github-repositories` | `discovery_match`, `repository_description`, `repository_language`, `repository_archived`, `repository_last_push`, `repository_default_branch`, `source_license`, `source_revision` |
| `github-code` | `discovery_match`, `source_revision`, `source_license` |

`Methodology` names the provider path (`pkg.go.dev v1 API discovery`,
`GitHub REST repository search`, `GitHub REST code search`). `Artifact`
records what caused the observation without secrets: `query=<text>` for a
match, `endpoint=/v1/package/<path>` for metadata.

### Licence uncertainty

A licence is recorded on `SourceRef.License` only when the provider returns one
clear, unambiguous value. Missing, null, empty, `NOASSERTION`, `NONE` and
compound expressions all leave `SourceRef.License` empty and emit an explicit
`source_license` **unknown** observation. Reusery never invents an SPDX
expression, and never treats a provider's licence field as proof of
compatibility.

pkg.go.dev's JSON API currently returns no licence field at all, so every
pkg.go.dev candidate carries `source_license: unknown`.

### Candidate identity

| Provider | Specimen ID |
| --- | --- |
| `pkg.go.dev` | `public/pkg.go.dev/<escaped package path>@<version>` |
| `github-repositories` | `public/github/repository/<owner>/<repo>` |
| `github-code` | `public/github/code/<owner>/<repo>@<blob sha>:<escaped path>` |

The searched version is authoritative for a pkg.go.dev package: the package
endpoint's notion of "latest" never silently rewrites the identity the search
actually matched. A repository search supplies no immutable revision, so its
`SourceRef.Revision` stays empty and that absence is recorded as an explicit
`source_revision` unknown rather than pretended away.

Evidence IDs are deterministic: `discovery/<provider>/<sha256-hex>` over a
length-prefixed canonical form of the provider, subject, kind, claim, result,
source, applies-to, methodology, artifact and UTC observation time. Random
identifiers are never used. Because the run timestamp participates, a later
real run adds a new observation instead of rewriting history.

## Partial provider failure

Operational failure is never candidate evidence. Issue kinds are
`rate_limited`, `authentication`, `forbidden`, `timeout`, `unavailable`,
`invalid_query`, `invalid_response`, `budget_exhausted` and
`incomplete_results`, each with only safe fields — provider, query, HTTP
status, `Retry-After` and a human message. Tokens, authorization headers and
response bodies never appear.

- One provider failing does not discard another provider's results.
- Providers that fail partially report issues with `incomplete: true` and still
  return what they found.
- If **every** provider fails, `discover` exits `1` while the provider reports
  stay in the output for inspection.
- If providers succeed but find nothing, that is a successful empty discovery
  and exits `0`.

A provider returning candidates plus an error is treated as a failure and its
candidates are not used: partial output from a failed run is not a trustworthy
observation set.

## Ordering: retrieval mechanics, not quality

Provider results arrive in relevance order because that is how search works.
Reusery preserves nothing about that order in its output: the combined result
is ordered by **profile provider order, then stable specimen ID**. That order
is traceability, not quality. There are no scores, weights, star thresholds,
maintenance metrics, licence scores, popularity rankings or provider-vote
aggregations anywhere in this packet.

## Persistence and resumability

Discovery writes to the existing registry tables — specimens and evidence.
There is no migration, no `discovery_runs` table, no `provider_results` table
and no cache table.

Persistence is deliberately **not** one giant transaction. Specimen writes are
upserts and evidence writes are identity-checked appends, so a run that fails
part way through returns its error without claiming success and a later rerun
continues safely. Applying an identical run twice adds nothing; a run at a new
timestamp adds new observations, which is evidence history rather than
corruption.

## Discovery does not resolve

`reusery discover` never calls `resolver.Resolve`, never persists a
`Resolution`, never picks a winner and never claims `BUILD LOCALLY`. Discovered
candidates become stored specimens that a later, explicit resolve request may
consider — under whatever evidence and ordering policy a future packet defines.

The Packet 4 development fixtures remain useful and are not deleted: they are
deterministic behavioural evidence, whereas discovery output is not behavioural
evidence at all.

## GitHub token

`REUSERY_GITHUB_TOKEN` is optional:

- absent → public unauthenticated calls, GitHub's public rate limits apply;
- present → `Authorization: Bearer <token>` is sent on GitHub requests.

It is never required to start `reusery serve`, never part of readiness, never
logged, never included in a configuration error and never persisted. Only the
header carries it — never a URL.

## Live versus deterministic tests

Automated tests **never** call the public internet. Provider tests use
`httptest` fixtures for mapping, budgets, rate limits, malformed responses,
oversized bodies and timeouts; the PostgreSQL integration test uses `httptest`
providers over real PostgreSQL. A provider outage therefore cannot make CI red.

Live calls are a manual smoke procedure run before freezing a packet:

```powershell
go run ./cmd/reusery discover --root . `
  --profile discovery/process/bounded-subprocess-v1.yaml --format text
```

Exercised against real pkg.go.dev, GitHub repository search and GitHub code
search for Packet 5, with and without a token, with results inspected for
plausibility rather than just for HTTP 200.
