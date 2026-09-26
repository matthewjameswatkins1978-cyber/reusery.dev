# Reusery Resolver

The resolver is Reusery's core product.

## Canonical flow

```text
engineering need
      ↓
normalise intent
      ↓
identify primitive / contract
      ↓
discover candidate specimens
      ↓
enrich evidence
      ↓
extract facts
      ↓
apply project policy
      ↓
compare / select
      ↓
verify where unknown blocks direct use
      ↓
resolve
      ↓
remember decision
```

Two rules sit underneath that flow.

**UNKNOWN alone cannot justify BUILD LOCALLY.** When plausible candidates exist
but their required behavioural evidence is missing, Reusery returns
`NEEDS_VERIFICATION` and persists no resolution. Absence of evidence is never
converted into "local code is better".

**A policy decision is not Evidence.** Licence, advisory, dependency,
maintenance and provenance rules are deterministic allow/review/deny
statements about a project's constraints. They never satisfy, fail or refute a
behavioural contract requirement, and no provider metadata can create a
behavioural PASS.

## Inputs

A human may provide natural language:

```text
I need a cross-platform Go subprocess runner with bounded output,
cancellation and process-tree termination.
```

An agent may provide equivalent structured requirements directly.

## Candidate evaluation

The resolver should consider behavioural fit before popularity. Relevant dimensions may include:

- contract coverage
- platform behaviour
- licence and attribution obligations
- dependencies
- maintenance state
- vulnerability evidence
- tests
- fuzzing
- benchmarks
- production evidence
- known limitations
- freshness
- provenance

These dimensions remain inspectable evidence. They are not reduced to a universal quality score.

## Outcomes

**REUSE**: use the specimen substantially as-is.

**ADAPT**: reuse with bounded changes while preserving provenance and re-running relevant contract verification.

**DEPEND**: consume the maintained upstream package/library.

**REFERENCE**: implementation is useful engineering knowledge but should not be copied or depended upon directly.

REFERENCE is not a weaker pass mark on the contract. It means the specimen is
genuinely relevant, attributable and policy-clean enough to learn from, and
that behavioural satisfaction has **not** been established — usually because
required requirements remain UNKNOWN. A REFERENCE resolution says so
explicitly: it never claims the implementation satisfies the complete
behavioural contract, and it is only produced when there is no explicit
required FAIL and no CONFLICTING evidence.

REUSE, ADAPT and DEPEND remain strict: every required behavioural requirement
must actually be satisfied **and** policy must permit the route.

**BUILD LOCALLY**: existing candidates are worse under the stated constraints than a small local implementation.

BUILD LOCALLY is justified only when there are no candidates, when every usable
candidate is explicitly blocked by policy, when every usable candidate has
required FAIL or CONFLICTING evidence, or when explicit constraints make reuse
unsuitable. It is not the answer to "we do not yet have enough evidence".

BUILD LOCALLY is not resolver failure.

## Explainability

Every resolution should answer:

- Why this candidate?
- Why not the alternatives?
- Which requirements are proven?
- Which are inferred?
- Which remain unknown?
- What source and version were examined?
- What verification was run?
- What could invalidate this decision?

## Safety rule

Reusery never converts absence of evidence into evidence of quality.

“No known vulnerabilities” does not mean “secure.” “Has tests” does not mean “well tested.” A detected licence does not by itself establish compatibility with a particular reuse.
