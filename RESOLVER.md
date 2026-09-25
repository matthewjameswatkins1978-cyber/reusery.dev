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
collect evidence + provenance
      ↓
apply project policy
      ↓
verify where necessary
      ↓
resolve
      ↓
remember decision
```

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

**BUILD LOCALLY**: existing candidates are worse under the stated constraints than a small local implementation.

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
