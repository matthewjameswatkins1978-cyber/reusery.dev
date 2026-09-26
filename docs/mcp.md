# Reusery MCP interface

Reusery exposes its existing application services over the Model Context
Protocol so a coding agent can reach for it **before** writing code. The MCP
server is another client of the same resolver: it adds no ranking, no policy
rule and no second evidence model. Delete this package and no resolver
behaviour changes.

The agent-facing companion to this document is
[`skills/reusery/SKILL.md`](../skills/reusery/SKILL.md), which teaches **when**
to call these tools.

## Why MCP exists

Packets 1–8 proved the engine and gave it an HTTP contract. The moment that
matters is the moment an AI coding agent is about to reinvent a solved
engineering problem. MCP puts the catalogue, the decision and the evidence
where agents already look for tools.

The rule: **HTTP adapts to Reusery, MCP adapts to Reusery. Reusery does not
become transport-shaped internally.**

## Transport: stdio only

```bash
reusery mcp
```

The agent host spawns Reusery as a child process and speaks MCP over
stdin/stdout. `internal/server` and `internal/api` are not involved.

- **stdout carries MCP frames and nothing else.** No startup banner, no log, no
  progress text, no diagnostics. One stray line would corrupt the transport.
  Operational logging goes to **stderr**. A source-level test enforces this.
- **PostgreSQL is required.** `reusery mcp` is a resolver server: startup loads
  configuration, connects to PostgreSQL, constructs the existing application
  services, then runs stdio. A missing, malformed or unreachable database fails
  startup clearly on stderr with a non-zero exit **before** any MCP frame is
  written.
- **No OpenAI key and no GitHub token are needed to start.** The server never
  constructs a model provider at all (see below). The GitHub token stays
  optional for enabled public operations.

There is **no remote MCP** in Packet 9: no `/mcp`, no `/mcp/sse`, no
streamable HTTP handler, no MCP listener. Packet 8 already provides the remote
HTTP surface, and an unauthenticated internet-facing expensive MCP surface
would be the wrong thing to add before Packet 13 (identity) and Packet 15
(abuse controls).

## SDK and protocol

- Official SDK: **`github.com/modelcontextprotocol/go-sdk` v1.8.0**, package
  `mcp`.
- Reusery does not hand-roll JSON-RPC, stdio framing, protocol negotiation or
  tool schema transport.
- The SDK negotiates the current revision **2026-07-28** and the compatible
  older revisions it advertises (`2025-11-25`, `2025-06-18`, `2025-03-26`,
  `2024-11-05`). Reusery does not parse protocol versions itself and is not
  pinned to one revision; a test forces an older supported revision through the
  SDK's own `ClientSessionOptions.ProtocolVersion` and requires the
  conversation to work.
- Packet 9 is a **TOOLS** server. It does not use roots, sampling, protocol
  logging, prompts, resources, tasks or elicitation, all of which are deprecated
  or out of scope for the 2026-07-28 revision.

## Generic agent-host configuration

```json
{
  "mcpServers": {
    "reusery": {
      "command": "reusery",
      "args": ["mcp"],
      "env": {
        "REUSERY_DATABASE_URL": "postgres://…",
        "REUSERY_MCP_ENABLE_EXTERNAL_OPERATIONS": "false"
      }
    }
  }
}
```

Only `REUSERY_DATABASE_URL` is required. Configuration examples in this
repository never contain real credentials.

## The eight tools

| Tool | Purpose | Annotation highlights |
| --- | --- | --- |
| `reusery_catalog` | List known capabilities, or inspect one primitive with its contract and ordered requirements. | `readOnlyHint: true`, `openWorldHint: false` |
| `reusery_discover` | Run the bounded Packet 5 discovery profile. | `readOnlyHint: false`, `destructiveHint: false`, `openWorldHint: true` |
| `reusery_enrich` | Record attributable metadata for specimens. | `readOnlyHint: false`, `destructiveHint: false`, `openWorldHint: true` |
| `reusery_resolve` | Compare candidates under a structured policy. | `readOnlyHint: false`, `destructiveHint: false`, `openWorldHint: false` |
| `reusery_refine` | Structured "not quite" → deterministic re-resolution. | same as resolve |
| `reusery_inspect_evidence` | Bounded, ordered evidence page. | `readOnlyHint: true`, `openWorldHint: false` |
| `reusery_inspect_resolution` | A remembered Resolution plus its outcome events. | `readOnlyHint: true`, `openWorldHint: false` |
| `reusery_report_outcome` | Append a factual post-resolution event. | `readOnlyHint: false`, `destructiveHint: false`, `openWorldHint: false` |

Annotations are **hints, not security boundaries**.

The surface is resolver-native, not an HTTP mirror: there is no
`reusery_health`, `reusery_ready` or `reusery_openapi`.

### There is no `reusery_normalize` tool

The caller of this server is already an AI agent. Routing its structured
reasoning through

```
agent → Reusery → another model → intent normalisation
```

would add cost and latency for no value: the agent can supply structured
resolver inputs directly. Packet 6 normalisation remains available to humans,
the CLI (`reusery normalize`) and HTTP clients (`POST /v1/normalize`). This is
a deliberate customer-cost optimisation, and a source-level test proves the MCP
package never constructs a model provider.

## External operations are off by default

```bash
REUSERY_MCP_ENABLE_EXTERNAL_OPERATIONS=false   # the default
```

This switch gates **only** `reusery_discover` and `reusery_enrich` — the two
tools that spend GitHub, pkg.go.dev or deps.dev quota. While it is false:

- the server still starts;
- `catalog`, `resolve`, `refine`, evidence inspection, resolution inspection
  and outcome reporting all work;
- `discover` and `enrich` return a tool error:
  `external_operations_disabled: discovery and enrichment are disabled for MCP;
  set REUSERY_MCP_ENABLE_EXTERNAL_OPERATIONS=true to enable them`.

**It is separate from `REUSERY_API_ENABLE_EXTERNAL_OPERATIONS`.** The HTTP and
MCP surfaces each need explicit enablement; neither silently turns the other
on. The equivalent CLI commands (`reusery discover`, `reusery enrich`) are
unaffected by either switch.

## Requesting a decision

`reusery_resolve` takes `primitive_id`, `contract_id`, a candidate list and an
optional structured `policy`.

- **Omit `policy`** and the built-in `public-go-baseline/v1` is used. It is
  code-owned so an installed binary works outside a repository checkout; a
  parity test deep-equals it against `policies/public-go-baseline-v1.yaml`, the
  authored human-readable profile. Neither may drift.
- **Supply `policy`** when you know the constraints. Every field the schema
  marks required must be present (send `[]` for empty lists), because an
  incomplete profile must never silently allow something.

`reusery_refine` is stateless: resend the **original base policy**, the same
bounded candidate set and the **complete accumulated feedback history** (at
least one item). The server derives the effective policy from scratch each
time. Feeding a returned `effective_policy` back in as the base would apply
feedback twice. Supported reasons:

`not_quite` · `too_many_dependencies` · `licence_not_allowed` ·
`avoid_dependency` · `avoid_reference` · `archived_project`

## Decision semantics over MCP

`status` is `resolved` or `needs_verification`. **Both are successful tool
results.**

- `resolved` → non-null `resolution_id`, non-null `outcome`, exactly one
  Resolution persisted.
- `needs_verification` → `resolution_id: null`, `outcome: null`, nothing
  persisted, and the missing required requirements listed in `unknowns`. It is
  **not** an MCP failure and **not** BUILD LOCALLY.
- `reference` → `outcome: reference`, reasons containing *"selected as
  reference-only engineering knowledge"* and *"behavioural contract
  satisfaction is not established"*, and every unresolved requirement listed.
  It is never translated into `recommended`, `verified` or `approved`.
- `build_locally` → a valid resolution outcome, not an error.

The compact decision is intentionally **not** Packet 7's whole assessment
graph: `status`, `policy_id`, `resolution_id`, `outcome`, `selected`,
`shortlist` (with `unknown_requirements`), `unknowns`, `reasons`, `rejected`,
and for refine `applied_feedback` plus `effective_policy`. An agent that needs
the observations calls `reusery_inspect_evidence`.

`reusery_enrich` is compact for the same reason: `observed_at`, a total count
and per-specimen `supported` / `providers` / `evidence_count` / safe issues.
Evidence bodies live behind `reusery_inspect_evidence`.

## Evidence continuation

`reusery_inspect_evidence` takes `subject_id`, an optional `limit`
(default **20**, maximum **50**) and an optional `after` object:

```json
{"observed_at": "2026-09-26T12:00:00Z", "evidence_id": "discovery/…"}
```

This is deliberately **not** the HTTP cursor encoding: MCP is its own transport,
so it uses native continuation fields instead of an opaque token. Ordering is
`observed_at` ascending then evidence id ascending; `next_after` is null on the
final page. An incomplete `after` object is `invalid_request`.

The `artifact` field is omitted: it carries machine-internal fact JSON that the
human-readable `claim` already states. Claims are never truncated.

## Outcome reporting

`reusery_report_outcome` appends a fact about a stored Resolution:

`adopted` · `rejected` · `integration_succeeded` · `integration_failed` ·
`abandoned`

Multiple chronological events are allowed — `adopted` then
`integration_succeeded` is normal. There is no single mutable status.

Reporting an outcome:

- **does not** alter the Resolution;
- **does not** create Evidence;
- **does not** change a policy;
- **does not** automatically re-resolve anything.

It is an append-only log. Packet 10 may later decide how remembered project
preferences learn from it; Packet 9 only records facts. Notes are capped at
1000 characters and are never logged.

## Errors

Tool failures use MCP tool-error semantics (`isError: true`) so the model can
read them, with a short safe message whose first token before `:` is the code:

| Code | When |
| --- | --- |
| `invalid_request` | Domain or transport validation failed |
| `not_found` | Unknown primitive, contract, specimen or resolution |
| `conflict` | Evidence identity conflict |
| `external_operations_disabled` | Gated tool while the switch is off |
| `upstream_rate_limited` | Upstream quota exhausted |
| `upstream_timeout` | Operation budget exceeded |
| `upstream_unavailable` | A configured provider is unavailable |
| `all_providers_failed` | Every configured provider failed operationally |
| `internal_error` | Anything unrecognised, collapsed on purpose |

MCP never returns a database URL, an API key, a GitHub token, a provider
`Authorization` header, a raw provider response body or a Go stack trace.
This package defines its own vocabulary: it does not import `internal/api`.

## Payload economy

Tool results are small enough for an agent to actually read. Measured on the
stable fixtures, with hard regression ceilings (these are **byte budgets**,
not token measurements):

| Tool result | Measured | Budget |
| --- | ---: | ---: |
| `reusery_resolve` | 1,884 B | 8 KiB |
| `reusery_refine` | 1,012 B | 4 KiB |
| `reusery_enrich` | 333 B | 2 KiB |
| `reusery_inspect_evidence` (50-record page) | 4,873 B | 16 KiB |

Each tool also returns a **short text summary** alongside the structured
content, for example:

```
DEPEND selected: fixture/eligible; 0 unresolved required requirements.
REFERENCE selected: fixture/reference; 11 behavioural requirement(s) remain unknown and contract satisfaction is not established.
No direct-use candidate is proven yet; 11 required requirement(s) remain unknown.
```

The structured JSON is never echoed into the text block — that would double
the token cost of every call.

## Tool contract and drift gate

The tool schemas are a public machine contract:

```bash
go run ./cmd/mcpcontract -write mcp/reusery-tools-v1.json   # regenerate
go run ./cmd/mcpcontract -check  mcp/reusery-tools-v1.json   # CI gate
make mcp-contract
make mcp-contract-check
```

The snapshot is generated through the **real registered server** using the
official SDK's in-memory transports and `tools/list` — no PostgreSQL, no model
key, no provider token, no network. It records the contract version, server
name, supported protocol revisions, and each tool's name, description,
annotations, input schema and output schema, sorted by name. Session ids, build
metadata and timestamps are excluded so the gate only fires on real changes.

After Packet 9 freezes: tool names are stable, required input fields are not
silently renamed, enum meanings do not change and output meaning is not
reinterpreted. Additive optional fields are fine; a breaking change becomes a
new contract revision rather than a silent change underneath an agent.

The same command is wired into `scripts/check.ps1`, `make check` and CI.

## Security and privacy

Not logged: MCP tool arguments, outcome note bodies, database URL, GitHub
token, provider credentials. Logged: operation name, duration and error code.

Not persisted: agent prompts, conversation transcripts, MCP request bodies, MCP
client identity, MCP protocol sessions. Packet 9 needs none of those.

No MCP sampling, elicitation or roots: Reusery tools return deterministic
application results and the calling agent stays in charge of reasoning and
code integration.

There is **no authentication** in Packet 9 — which is exactly why remote MCP
does not exist yet, and why the external-operation switch defaults to off.
Reusery is not a production internet service; see
[`docs/http-api.md`](http-api.md) for that warning on the HTTP side.
