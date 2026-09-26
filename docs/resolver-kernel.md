# The resolver kernel

Packet 4 adds the deterministic resolution kernel around the Packet 2
evaluator. It turns a validated, ordered set of candidate options into one
inspectable `model.Resolution`. It is pure: no PostgreSQL, no HTTP, no
filesystem, no environment variables, no CLI.

```text
Primitive + Contract + ordered candidates + Evidence
        ↓  resolver kernel
Decision { Resolution, Considered }
```

## Candidate order, not ranking

`ResolveInput.Candidates` are considered **in the order the caller supplied
them**. The first candidate that satisfies the acceptance rule wins; later
candidates are never evaluated and never appear in `Decision.Considered`.

This is deliberate and temporary: Packet 4 establishes **acceptable**, not
**best**. There is no score, no confidence value and no preference between
reuse modes. A later packet owns richer evidence, policy and ranking.

A candidate is a `CandidateOption`: a specimen plus the reuse mode under
consideration plus its evidence. The caller states the mode; the kernel never
invents `dependency > copy > adapt > reference`.

## Acceptance rule

For each candidate, in supplied order:

1. run the Packet 2 evaluator over the contract's requirements;
2. a candidate is acceptable only if **every required requirement is
   `satisfied`**.

`failed`, `unknown` and `conflicting` all block selection — that is the
evaluator's rule, reused rather than re-implemented. Optional requirements
never block selection.

| Requirement kind | Satisfied | Failed | Unknown | Conflicting |
| --- | --- | --- | --- | --- |
| required | may be selected | blocks | blocks | blocks |
| optional | ignored for selection | known limitation, still selectable | still selectable | still selectable |

## Reuse mode → outcome

When a candidate is acceptable, the requested mode maps mechanically:

| Requested mode | Outcome |
| --- | --- |
| `copy` | `reuse` |
| `dependency` | `depend` |
| `adapt` | `adapt` |
| `reference` | `reference` |

No mode wins automatically: the caller chose it.

## BUILD LOCALLY

If a valid request yields no acceptable candidate, the kernel returns
`build_locally` as a **successful** resolution — with `SpecimenID` empty and a
precise reason (`no candidate options were supplied`, or `no candidate option
satisfied all required contract requirements`).

Malformed input is a different thing entirely. Empty IDs, mismatched
primitive/contract pairs, duplicate candidates, unsupported reuse modes or a
mode the specimen does not declare return a Go error and never manufacture a
decision.

## Rejection reasons

Rejections are generated from required requirement evaluations only, in
contract requirement order, with no interpretation:

```text
required requirement "bounds-stderr" failed
required requirement "process-tree-semantics-explicit" unknown
```

`model.Rejection` preserves them in candidate order, so negative knowledge
survives persistence.

## Inspecting the decision

`Decision.Considered` records every candidate actually considered, in order,
with its full `CandidateEvaluation`, `Selected` flag and rejection reasons.

`Resolution.Unknowns` carries required `unknown`/`conflicting` states of
rejected candidates and optional `unknown`/`conflicting` states of the selected
candidate, as `"<specimen>: <requirement> <status>"`. Optional `failed` states
become known limitations in `Resolution.Reasons` — a known failure is never
written down as an unknown.

`Resolution.EvidenceIDs` lists determining evidence in candidate order, then
contract requirement order, then evaluator evidence order, de-duplicated on
first occurrence. Evidence belonging to candidates after the selection is
excluded, because those candidates were never considered.

## Why there is no discovery yet

The kernel consumes `CandidateOption`s, so it is provider-agnostic without any
provider abstraction. Packet 5 introduces real discovery once we know what
candidate sourcing actually needs to return; inventing that interface now would
be speculation. Live public discovery, popularity signals and scoring are
therefore deliberately absent, and the development catalogue candidates are
deterministic fixtures rather than claims about real public software.

## The application service

`resolver.Service` sits between storage and the kernel: it loads the primitive,
contract, specimens and evidence through a `Repository` interface defined
outside PostgreSQL, supplies the timestamp from an injected `Clock`, runs the
kernel and persists exactly one `Resolution` — including `build_locally`.
Load or validation failures persist nothing; persistence failures are returned.
See `docs/evidence-evaluation.md` for the per-requirement semantics beneath
this, and `RESOLVER.md` for the product-level flow.

## Packet 7: what the kernel does not do

Packet 4's acceptance rule — **every required behavioural requirement
satisfied** — remains exactly as written above. Nothing in Packet 7 changes it,
and `Resolve` still behaves the way this document describes.

Two things above the kernel were added later, and they are deliberately not
implemented by loosening the kernel:

- **REFERENCE has its own meaning.** `REFERENCE` is useful engineering
  knowledge that should not be copied or depended upon directly. It does not
  mean "this specimen satisfies the complete behavioural contract". A
  REFERENCE result may carry unresolved required requirements, provided there
  is no required `fail`, no `conflicting` evidence, documented discovery
  relevance and inspectable provenance — and provided the resolution says
  explicitly that behavioural satisfaction is not established. The kernel's
  strict rule still applies to `REUSE`, `ADAPT` and `DEPEND`.

- **UNKNOWN is not BUILD LOCALLY.** When plausible candidates remain but
  required behaviour is unknown, the quality layer returns
  `needs_verification` and persists **no** resolution at all. Only an empty
  candidate set, a hard policy deny on every usable candidate, explicit
  required `fail`/`conflicting` evidence on every usable candidate, or explicit
  unsuitability justifies `build_locally`.

Both live in `internal/resolver/quality.go` and
`internal/resolver/quality_service.go`, which consume the evaluator and the
kernel's helpers but never modify them. See
[docs/evidence-policy-resolution.md](evidence-policy-resolution.md).
