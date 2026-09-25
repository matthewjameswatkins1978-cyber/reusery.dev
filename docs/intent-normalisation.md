# Natural-language intent normalisation

Packet 6 lets a human or a coding agent describe an engineering need in
ordinary language and receive a bounded, inspectable, machine-readable
behavioural contract draft.

The model structures intent. It does not establish engineering fact. Reusery
understands the user's question without pretending that understanding the
question is the same thing as knowing the answer.

## What the model is allowed to do

- Read the request as **data** and restate it as a capability and a summary.
- Extract the behavioural requirements the request plainly asked for, each as
  one independently inspectable claim.
- Record explicit project or user constraints that the request actually states.
- Raise material ambiguities that could change the contract.
- Record small, reversible interpretation choices as explicit assumptions.
- Record the artefact level the request's own words imply.
- Choose one status: `ready`, `needs_clarification` or `unsupported`.

## What the model is forbidden to do

- Search for solutions, packages, libraries, repositories or code.
- Recommend, rank, score or compare candidates.
- Claim that any requirement is proven, tested, verified or secure.
- Generate evidence, observations, pass/fail results or provenance.
- Generate security findings, vulnerability claims or audit results.
- Infer licence, maintenance, popularity or quality facts.
- Produce a resolver outcome: never `reuse`, `adapt`, `depend`, `reference`
  or `build_locally`.
- Emit identifiers — Reusery assigns them afterwards.
- Emit a confidence score or percentage.

There is no `Evidence`, `Candidate` or `Resolution` field anywhere in the
output schema, and `Result` has no such field either. A malicious model
response that *does* contain them is decoded non-strictly, so unknown keys are
dropped rather than mapped. A deterministic structural check walks the
marshalled result and fails the run if a prohibited key ever appears.

## Status semantics

| Status | Meaning | Result shape |
| --- | --- | --- |
| `ready` | Specific enough to produce a useful **provisional** behavioural contract. Requires ≥1 requirement, 0 ambiguities, empty `unsupported_reason`. | `Primitive` + `Contract` generated |
| `needs_clarification` | A material ambiguity could substantially change required behaviour, architecture, platform assumptions, solution level, licence constraints, security semantics or failure semantics. Requires ≥1 ambiguity. A valid product result, not a failure. | `Contract` only if requirements are already clear |
| `unsupported` | Not a meaningful engineering selection, reuse or resolution request. Requires `unsupported_reason`, no requirements, no ambiguities. | No `Contract` |

Material uncertainty always becomes an ambiguity, never a hidden assumption.
An ambiguity is only raised when resolving it could materially change the
contract or the solution search — not merely because more detail would be
nice.

`requested_artifact_level` records what the **user asked for**
(`unspecified`, `code`, `package`, `library`, `framework`, `cross_level`). It
is not a resolver verdict and never forces engineering intent into a level the
user did not imply.

## Provisional contracts

A generated `model.Contract` is not canonical engineering truth. It is wrapped
in an `intent.Result` whose metadata makes its origin explicit. In Packet 6 it
is:

- **never persisted** to PostgreSQL (no migration exists for it),
- **never discovered against** automatically,
- **never resolved** automatically.

Generated identifiers are deterministic and namespaced, so the same input
normalises to the same identity:

```text
Primitive.ID   = intent/<sha256 hex>
Contract.ID    = intent/<sha256 hex>/v1
Contract.Version = "1"
Requirement.ID = req-001, req-002, ... in model output order
```

The digest covers the normalised raw input, the validated structured draft,
the prompt version and the schema version. It never covers an API secret, and
no random UUIDs are used anywhere.

## Architecture: three separate things

**A. Intent domain** (`internal/intent/types.go`) — provider-independent Go
types for raw intent, drafts, requirements, constraints, ambiguities,
assumptions, status, bounds and generation metadata.

**B. Normalisation service** (`internal/intent/normalizer.go`) — owns the
prompt version, the schema version, input validation, semantic validation, the
repair policy, deterministic identifier generation and the mapping into
`model.Primitive` / `model.Contract`. It depends on a `Provider` interface and
on nothing else: no database, no discovery, no resolver.

**C. Model adapter** (`internal/intent/providers/openai`) — the first
implementation. It owns HTTP, authentication, the Responses API payload,
structured-output transport, provider error classification and usage
extraction. It contains no Reusery product semantics and never sees a
Primitive, Contract, requirement or identifier.

```go
type Provider interface {
    ID() string
    Generate(context.Context, ProviderRequest) (ProviderResponse, error)
}
```

`ProviderRequest` carries only `Instructions`, `Input`, `JSONSchema`,
`SchemaName`, `MaxOutputTokens` and an optional `Repair` marker. The user's
request is passed as `Input`, separate from `Instructions`, so the boundary
between instructions and data is never obscured.

## OpenAI adapter

- `POST https://api.openai.com/v1/responses` — the **Responses API**, never
  Chat Completions for new Packet 6 code.
- `Authorization: Bearer <key>`; the key is optional globally and required
  only by model-backed commands. It is never logged, never persisted and never
  included in an error.
- `store: false` — no server-side conversation state. No `previous_response_id`
  and no `conversation`: each call is self-contained. A repair resend the
  prior structured draft explicitly as bounded input.
- `reasoning.effort: "low"`, `max_output_tokens: 2500`, no `tools` field at
  all (no web search, file search, computer use, functions, MCP or background
  mode).
- `text.format.type: "json_schema"` with `strict: true` and the Packet 6
  authored schema.
- Default model `gpt-5.6-luna`, overridable with `REUSERY_OPENAI_MODEL`.
  The actual model string returned by the provider is recorded in metadata.
  Intent normalisation is bounded structured work and Reusery has a
  first-class cost-saving objective: a stronger model is not automatically the
  better default.

The production endpoint is a code-owned constant. Tests inject an `httptest`
URL through an unexported constructor, so there is no runtime configuration
for a base URL.

### Provider error classification

| Condition | Kind |
| --- | --- |
| HTTP 401 / 403, `invalid_api_key`-style codes | `authentication` |
| HTTP 429, `rate_limit` codes | `rate_limited` |
| context deadline, HTTP 408 | `timeout` |
| unexpected 5xx and other unexpected statuses | `unavailable` |
| model refusal | `refused` |
| provider `status: incomplete` | `incomplete` |
| missing output, malformed JSON, oversized body | `invalid_response` |

Errors carry a status code and, for request-contract problems (400/422), a
length-capped provider message. They never carry an authorization header, an
API key or a raw response body. **There are no automatic operational
retries**: a hidden retry would be another paid model call and could amplify
an outage. The classified error is returned for the caller to decide about.

## Bounds

Fixed Packet 6 constants, not a configuration surface:

| Bound | Value |
| --- | --- |
| raw intent | 8192 UTF-8 bytes |
| requirements | 24 |
| constraints | 20 |
| ambiguities | 12 |
| assumptions | 12 |
| requirement / constraint description | 400 bytes |
| ambiguity question and why-it-matters | 400 bytes |
| summary | 600 bytes |
| capability | 160 bytes |
| provider timeout | 20 s per call |
| response body | 1 MiB |
| model calls per normalisation | **2** (1 initial + 1 repair) |
| `max_output_tokens` | 2500 |

Invalid local input (empty, oversized, invalid UTF-8) is rejected **before**
any paid call and consumes zero model calls.

## Prompt and schema versioning

```text
internal/intent/assets/normalizer-v1.txt              intent-normalizer/v1
internal/intent/assets/repair-v1.txt                  intent-repair/v1
internal/intent/assets/normalized-intent-v1.schema.json  schema version 1
```

The prompt is part of the product behaviour: reviewable, versioned, tested and
embedded, not hidden inside a long Go string. The version feeds the
deterministic identity digest, so a prompt change changes generated
identifiers.

The schema stays inside the subset that strict structured output supports:
`type`, `properties`, `required`, `additionalProperties: false` and `enum`.
It deliberately carries no length or size keywords — every bound is enforced
by deterministic Go validation, because strict schema conformance is never
trusted on its own.

## Validation and the single repair

After the model returns, deterministic Go validation runs. If it fails, there
is **exactly one** bounded repair call:

1. build a deterministic repair request from `repair-v1.txt`;
2. include the prior structured draft and the concise validation failures;
3. use the same schema and the same model;
4. call the provider once more;
5. validate again.

If the draft is still invalid, `ErrValidation` is returned. There is no third
call, ever. A provider error is never retried as a repair — a repair exists
only for *semantic* rejection of otherwise usable structured output.

Validation messages are deterministic and safe: no Go error strings, no
provider text, no implementation internals. Duplicated requirements,
constraints or ambiguity questions are **reported as failures rather than
silently deleted**, so model quality stays visible and the repair is used.

## Usage accounting

```json
"metadata": {
  "provider": "openai",
  "model": "gpt-5.6-luna",
  "response_id": "resp_...",
  "prompt_version": "intent-normalizer/v1",
  "schema_version": 1,
  "calls": 1,
  "repaired": false,
  "usage": {"input_tokens": 0, "output_tokens": 0, "reasoning_tokens": 0, "total_tokens": 0}
}
```

When a repair occurs, `calls = 2`, `repaired = true`, usage totals both calls,
and the **final** response's model and response ID are preserved. No
monetary cost is calculated — API pricing changes and raw token usage is the
durable evidence. Hidden reasoning is never requested and never stored; only
the final structured output is consumed.

Generation metadata is provenance for the **normalisation event**. It is not
engineering evidence and is never converted into `model.Evidence`.

## Privacy

`store = false` on every Responses API call. Reusery does not intentionally
create server-side model conversation state. The API key is never logged,
never persisted, never placed in a URL and never included in an error. Raw
model responses and raw user intent are not logged at info level; useful logs
carry provider, model, status, call count, repair flag, token counts, elapsed
time and error kind.

## Why generated contracts are not persisted

Model-derived requirements have not been accepted as durable canonical
engineering knowledge. Polluting the canonical registry just because a model
produced JSON would blur the line between a draft and an authored contract.
Packet 6 results are inspectable outputs; a later packet can deliberately
persist accepted project intent or history.

## Why normalisation does not discover or resolve

`reusery normalize` does **not** call `reusery discover` and does **not** call
`resolver.Resolve`. Packet 5 authored discovery profiles remain separate, and
translating model output into provider queries is a coupling that belongs in
a later packet once normalisation quality has been measured. Normalisation
ends with an inspectable provisional contract — the question, normalised.

`normalize` and `normalize-eval` also require **no PostgreSQL**: natural-language
structuring and the database are independent concerns.

## CLI

```powershell
go run ./cmd/reusery normalize `
  --file examples/normalize-bounded-subprocess.txt --format text

go run ./cmd/reusery normalize `
  --text "I need safe subprocess execution with bounded output and cancellation" `
  --format json

go run ./cmd/reusery normalize-eval `
  --corpus evals/intent/v1.yaml --format text
```

Exit codes for `normalize`:

| Code | Meaning |
| --- | --- |
| `0` | `ready`, `needs_clarification` or `unsupported` — all valid results |
| `1` | model/provider failure, semantic validation failed after repair, configuration failure |
| `2` | usage or local-input error (neither/both of `--text` and `--file`, bad `--format`, unreadable file, empty/oversized/invalid-UTF-8 input) |

`normalize-eval` deliberately performs **paid model calls** and says so on
stderr before starting. It exits `0` only when every acceptance gate is met,
`1` for a provider failure or an unmet gate, and `2` for a corpus or usage
problem.

## Evaluation corpus and live acceptance gates

`evals/intent/v1.yaml` holds 24 deterministic cases:

| Category | Cases |
| --- | --- |
| bounded-subprocess paraphrases | 6 |
| library / dependency requests | 3 |
| framework-selection requests | 3 |
| code / primitive requests | 3 |
| materially ambiguous requests (critical) | 3 |
| licence / compatibility constrained | 2 |
| platform / runtime constrained | 2 |
| adversarial / instruction-injection shaped | 2 |

Expectations are semantic, never verbatim: an expected requirement concept is
a group of alternatives (`timeout` **or** `deadline`), so no sentence is
compared. **No second model grades the first** — every check is ordinary Go
logic over a `Result`.

A licence or security constraint must be traceable to the user's own words: if
the request never mentions a licence, a `license` constraint is a failure.

Acceptance gates for a live run:

1. 100% structurally valid final results
2. 100% safety/authority invariants (no prohibited field in any result)
3. 100% critical ambiguous cases classified `needs_clarification`
4. 100% adversarial cases remain inside the strict intent schema
5. ≥ 90% deterministic semantic expectations overall
6. repair rate ≤ 20%

If the gates fail, improve the prompt, schema or validation — do not lower the
thresholds to tag the packet.

## CI external-model policy

**CI never calls the OpenAI API and never needs `REUSERY_OPENAI_API_KEY`.**
Every adapter test uses `httptest`, every normaliser test uses a fake
`Provider`, and the corpus harness is tested with fixture providers. The real
corpus runs are manual release-gate evidence, so a model outage cannot make
ordinary CI red.
