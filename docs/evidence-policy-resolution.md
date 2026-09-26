# Evidence, policy and resolution quality

Packet 7 is the layer between "these things exist" and "this is the engineering
route Reusery can currently justify". It adds four separate responsibilities
and refuses to blur them.

```text
discovery   →  specimens + relevance observations (Packet 5)
enrichment  →  attributable metadata observations  (Packet 7, networked)
fact extraction → typed facts from stored evidence  (Packet 7, offline)
policy      →  allow / review / deny on those facts (Packet 7, offline)
quality     →  assessment, shortlist, decision     (Packet 7, offline)
```

Everything after discovery is offline. `reusery choose` performs no network
call, which is exactly what makes re-resolution cheap and deterministic.

## Enrichment is not verification

Behavioural verification answers: *does this specimen satisfy this contract?*
Only the Packet 2 evaluator answers that, and only from evidence whose
`applies_to` names a contract requirement.

Enrichment answers a different question: *what can we factually observe about
this already-discovered specimen?* Its trust rules are absolute:

- enrichment providers emit **INFO** or **UNKNOWN** only;
- `applies_to` is always empty;
- PASS and FAIL are never produced;
- a provider failure is an inspectable issue, never candidate evidence.

So a licence, an advisory identifier, a dependency count or an archived flag can
never become a behavioural PASS. That invariant is enforced structurally before
anything is persisted, not merely by convention.

## deps.dev's role

`deps.dev` (provider id `deps.dev`, stable v3 JSON API, code-owned base URL
`https://api.deps.dev`) enriches pkg.go.dev specimens through:

| Endpoint | Used for |
| --- | --- |
| `GET /v3/systems/GO/packages/{module}/versions/{version}` | version, publication time, deprecation, licences, advisories, source link |
| `GET /v3/systems/GO/packages/{module}/versions/{version}:requirements` | declared direct and indirect dependencies |

Module identity is recovered from Packet 5's `package_module` observation — a
code-owned, deterministic claim shape, not free English — and the exact
discovered version comes from `Specimen.Source.Revision`. If either cannot be
established, deps.dev emits an explicit `identity_unresolved` issue and
**does not guess** a module path.

Observations recorded: `package_version`, `package_published_at`,
`package_deprecated`, `package_deprecated_reason` (when present),
`source_license`, `known_advisory`, `known_advisory_count`, `direct_dependency`,
`direct_dependency_count`, `indirect_dependency`, `indirect_dependency_count`,
`source_repository_link` (when supplied). Only fields the provider actually
returned are persisted. There is no HTML scraping, no pagination and no
recursive dependency graph — declared burden is enough.

### Licences from deps.dev

| Returned | Recorded |
| --- | --- |
| zero expressions | one `source_license` **UNKNOWN** — the source licence stays unresolved |
| one expression | one `source_license` INFO with the exact returned SPDX expression |
| several expressions | one INFO per exact expression, **plus** a `licence_relationship` UNKNOWN because deps.dev does not say how they relate |

Expressions are never combined into an invented `AND`/`OR`, never rewritten and
never interpreted. Reusery does not evaluate licence compatibility.

### Advisories from deps.dev

Every advisory identifier for the exact version becomes one INFO
`known_advisory`, plus one INFO `known_advisory_count` that **always exists,
including when the count is zero**. A zero count is phrased as:

> deps.dev reported zero known direct advisory identifiers for this version at
> the observation time

It is never "secure", never "safe", never "vulnerability-free" and never a
pass. **A zero advisory count is not EvidencePass.** There is deliberately no
separate OSV provider in this packet: deps.dev already surfaces
OSV-derived identifiers for the ecosystem.

### Why not GitHub for packages, or deps.dev for repositories

Each provider is asked only what it actually knows. deps.dev speaks Go modules
and versions; `github-metadata` speaks repository state. Adding a second OSV
client, a star counter or a scorecard before they buy product value would be
duplication, not coverage.

## GitHub metadata's role

`github-metadata` applies to `public/github/repository/*` and
`public/github/code/*`, and reuses Packet 5's shared GitHub client — same
optional `REUSERY_GITHUB_TOKEN`, same API version header, same rate-limit
handling, same fixed host, same safe redirects. There is only one GitHub HTTP
stack.

One call: `GET /repos/{owner}/{repo}`, collecting `archived`, `pushed_at`,
`default_branch` and `license.spdx_id`.

- **Popularity is not decoded.** Stars, forks and watchers never reach policy,
  ordering or selection.
- **Licence detection is evidence, not a verdict.** One unambiguous SPDX ID
  becomes INFO `source_license`; missing, `null`, `NOASSERTION` or `NONE`
  becomes UNKNOWN. Licence file contents are never persisted, and repository
  licence never implies anything about dependency licences.
- **Maintenance facts stay facts.** `repository_archived`,
  `repository_pushed_at` and `repository_default_branch` are attributed
  observations. Words like "well maintained", "healthy", "active enough" and
  "abandoned" appear nowhere: those are policy's business.
- **Identity is never rewritten.** A code candidate is blob-pinned. Repository
  metadata may describe current repository state, but
  `SourceRef.Revision` still holds the discovered blob SHA.

## Enrichment bounds and partial failure

| Bound | Value |
| --- | --- |
| specimens per run | 24 |
| providers per specimen | 2 |
| HTTP requests per provider/specimen | 3 |
| provider timeout | 10 s |
| whole run | 30 s |
| response body | 2 MiB |

No unbounded pagination, no hidden retry loops. One provider failing never
erases another's evidence: deps.dev succeeding while GitHub rate-limits
persists deps.dev evidence and reports the rate limit. Only a run in which
*every* applicable provider call failed is an execution failure, and a
specimen no provider supports is reported as unsupported with no manufactured
evidence.

Evidence identity is deterministic: `enrichment/<provider>/<sha256>` over a
length-prefixed canonical form of provider, subject, kind, claim, result,
source, applies-to, methodology, artifact and observation time. A new
observation time legitimately creates a new record; the same observation at the
same instant is idempotent; an ID colliding with different content is an error.

## Machine-readable fact artifacts

Policy never parses English. For Packet 7 enrichment evidence,
`Evidence.Artifact` carries a small versioned canonical JSON value:

```json
{"schema_version": 1, "value": "MIT"}
{"schema_version": 1, "value": 3}
{"schema_version": 1, "value": false}
```

`Claim` stays human-readable. `Artifact` is the deterministic machine input.
This convention applies to Packet 7 enrichment facts only — historical
`Artifact` values are left alone, and a fact-shaped artifact that cannot be
read marks the fact **unknown** rather than guessing.

## Fact extraction

`policy.ExtractFacts(specimen, evidence)` aggregates stored evidence into typed
facts, each preserving status, value(s) and the evidence IDs behind it.

`LicenceFact`, `AdvisoryFact`, `DependencyFact`, `ArchivedFact`,
`LastPushFact`, `DeprecatedFact`, `RevisionFact`, `PublishedAtFact` and
`DiscoveryRelevanceFact` each report **known**, **unknown**, **multiple**
(licences whose relationship is unestablished) or **conflicting**.

Conflicting facts are never silently resolved:

- **licence** — two different expressions from two sources is `conflicting`;
  several expressions from one source that admits it cannot relate them is
  `multiple`;
- **archived / deprecated** — disagreement is `conflicting` and reports the
  risk-bearing state, so policy cannot pass a candidate on disagreement;
- **revision** — two different revisions is `conflicting`;
- **advisories** — identifiers accumulate rather than contradict: the union of
  observed identifiers and the largest reported count are the conservative
  reading;
- **dependency counts** — when counts disagree the larger one wins, so a
  configured maximum cannot be cleared by disagreement;
- **last push** — disagreement uses the **oldest** observation, so freshness
  thresholds can never be satisfied by "latest wins".

## Policy: allow / review / deny

`policies/public-go-baseline-v1.yaml` is a conservative **baseline** for public
Go reuse. It is not legal advice, not a security assurance and not a universal
company policy, and it contains no subjective taste: no "good dependency
count", no "good project age", no popularity signal.

Omitted numeric thresholds mean **no threshold**. `dependencies.max_direct`,
`maintenance.max_days_since_push` and `maintenance.max_days_since_release` are
evaluated exactly when present and simply do not apply when absent — no default
day count exists anywhere.

Dimensions are evaluated in a fixed order with no score and no weights:

`reuse`, `licence`, `security`, `dependencies`, `maintenance_archived`,
`maintenance_deprecated`, `maintenance_stale`, `source_revision`.

### Licence

Exact string comparison only, in this order: deny list → allow list →
`unlisted` action → allow. Unknown applies `license.unknown`; several or
conflicting expressions apply `license.multiple`. Wording is always "allowed by
policy `<id>`" or "denied by policy `<id>`". Reusery never says a licence is
*compatible*.

### Security

One or more observed advisory identifiers apply `security.known_advisory`. A
successful lookup reporting zero allows the candidate **for one narrow
statement only**: "no known direct advisory IDs were reported by this source at
this time". No `EvidencePass` is created, and no wording says secure. When
advisory state is unknown, `security.unknown` applies.

### Dependencies

Only when `max_direct` is configured: ≤ maximum allows, > maximum denies,
unknown applies `dependencies.unknown`. With no maximum, the count is recorded
as a **trade-off** and no quantity decision is made at all.

### Maintenance

Archived and deprecated facts always evaluate. Staleness only evaluates when a
threshold exists, and it speaks in facts:

> last push is 850 days old and policy limit is 365 days

Never "unmaintained" merely because something is old. Where a threshold exists
but the underlying fact does not, `maintenance.unknown` applies.

### Source / provenance

Reuse modes listed in `source.require_revision_for` (`copy`, `dependency`,
`adapt` in the baseline) require an immutable or versioned revision. A missing
revision applies `source.missing_revision`. Reference-only candidates may stay
useful without a pinned revision, but the weakness is still surfaced as a
review decision — a pinned blob is stronger provenance than an unpinned
repository reference, though it is never evidence that the code is better.

### review is not allow

For `REUSE`, `ADAPT` and `DEPEND`, automatic selection requires **no deny**,
**no unresolved review on applicable hard dimensions**, and **every required
behavioural requirement satisfied**. A candidate with a policy review remains
inspectable and shortlisted, but is not automatically selected for direct use.

## Candidate dispositions

| Disposition | Meaning |
| --- | --- |
| `implementation_eligible` | all required behaviour satisfied, no deny, no review |
| `reference_only` | relevant, attributable, policy-clean; behaviour **not** claimed |
| `needs_review` | behaviour fully satisfied, but a policy rule awaits a human |
| `needs_verification` | behaviour is missing, not rejected |
| `blocked` | required fail, required conflicting, policy deny, or invalid mode |

A reference candidate also needs at least one attributable discovery/relevance
observation and inspectable provenance; without them it is blocked rather than
presented as a reference.

## Why UNKNOWN may mean needs_verification

If plausible direct-use candidates exist but their required behavioural
requirements are UNKNOWN, Reusery does not know that `BUILD LOCALLY` is better.
The honest state is `needs_verification`, and **no Resolution is persisted**.

`build_locally` is justified only when:

- no candidates exist;
- every usable candidate is blocked by policy;
- every usable candidate has explicit required FAIL or CONFLICTING evidence;
- explicit constraints make reuse unsuitable;
- or all remaining candidates were excluded by user feedback.

Each of those produces its own reason rather than a generic fallback.

## REFERENCE and BUILD LOCALLY

**REFERENCE** says: this is useful engineering knowledge; do not copy it or
depend on it directly; and behavioural satisfaction has **not** been
established. The resolution carries `selected as reference-only engineering
knowledge` and `behavioural contract satisfaction is not established`, and its
`unknowns` preserve the unresolved required requirements.

**BUILD LOCALLY** says something specific — `no candidate options were
supplied`, `all remaining candidate options were denied by policy`, `all
remaining candidate options had explicit required behavioural failures`,
`candidate options were excluded by user feedback` — never one blended
"nothing worked".

## Shortlisting without a universal score

The shortlist is bounded by `policy.selection.max_options` with a hard ceiling
of five. Options are diversified by reuse mode first, then filled in
deterministic order.

Ordering is **lexicographic, never numeric**:

1. claim strength — `implementation_eligible`, `reference_only`,
   `needs_review`, `needs_verification`, `blocked` (only the first two are
   automatically selectable);
2. within `implementation_eligible`: authored preferred reuse-mode order, then
   stable specimen ID;
3. within `reference_only`: pinned revision before unpinned, then stable
   specimen ID.

No stars, no provider search rank, no summed evidence, no weighted score.

### Tie-break transparency

When stable specimen ID is the *only* remaining distinction between two
otherwise indistinguishable candidates, the decision says so:

> available policy and evidence do not distinguish these candidates; stable
> specimen ID was used as the deterministic tie-break

Determinism is not evidence of quality, and arbitrary tie-breaking is never
hidden behind "best".

## Structured feedback and next-best-fit

`--feedback` accepts a bounded vocabulary that this packet can actually act on:
`not_quite`, `too_many_dependencies`, `licence_not_allowed`,
`avoid_dependency`, `avoid_reference`, `archived_project`. Reasons that would
change nothing are rejected rather than silently accepted.

Feedback is **not pagination**. It excludes the named candidate *and*, where
the facts allow it, refines the effective policy for everyone else:

| Feedback | Effect |
| --- | --- |
| `not_quite` | exclude only that candidate; infer no broader preference |
| `too_many_dependencies` | set `max_direct = N - 1` (keeping any stricter existing maximum), then exclude. Unknown or zero count is a structured error, never an invented threshold |
| `licence_not_allowed` | deny that exact single known expression, then exclude. Unknown/multiple/conflicting licence excludes the candidate but reports that no broader rule could be derived |
| `avoid_dependency` | remove `dependency` from the allowed reuse modes, then exclude |
| `avoid_reference` | remove `reference` from the allowed reuse modes, then exclude |
| `archived_project` | set `maintenance.archived = deny`, then exclude. Only valid when the candidate is observed archived |

The worked shape is:

```text
candidate A: dependency, 8 direct dependencies
candidate B: dependency, 2 direct dependencies
candidate C: reference

initial policy: no dependency maximum
initial selection: A

feedback: too_many_dependencies on A
effective policy: max_direct = 7
A excluded
re-resolution: B selected      ← materially different, not "next array element"
```

Every feedback-rejected candidate appears in `Resolution.Rejected` as
`user_feedback:<reason>` plus the factual context used for the refinement, so
negative knowledge survives re-resolution.

## Policy identity is part of the decision

`model.Resolution.PolicyID` names the profile that justified the outcome, and
migration `00002_resolution_policy.sql` adds the column with an empty default.
Resolutions written before Packet 7 keep loading with an empty `policy_id`
rather than being backfilled with an identity they never had.

Candidate assessments, shortlists, policy decisions and trade-offs are
**not** persisted in this packet: `Resolution` + `Evidence` + `Rejections` +
`PolicyID` are enough for durable decisions, and transient comparisons are
returned to the caller.

## Benchmark records

`benchmarks/` holds a durable record format that separates **MEASURED** from
**MODELLED** fields. An estimate is never labelled a measurement, a savings
percentage is never calculated when a required baseline field is absent, and no
single exploratory run is ever presented as proof of savings. Packet 7 owns
recording and aggregation only; **Packet 16 owns release-level validation
claims.**

## Commands

```text
reusery discover --profile discovery/process/bounded-subprocess-v1.yaml
reusery enrich   --request examples/enrich-bounded-subprocess.json
reusery choose   --request examples/quality-bounded-subprocess.json \
                 --policy policies/public-go-baseline-v1.yaml \
                 --feedback examples/quality-feedback-not-quite.json
```

`normalize` remains a separate phase: Reusery does not yet claim one-command
natural-language resolution.
