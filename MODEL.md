# Reusery Core Model

Reusery separates an engineering idea from implementations and from claims about those implementations.

## Primitive

A bounded engineering behaviour or capability.

Examples:

- atomic file replacement
- bounded subprocess execution
- process-tree termination
- structured retry
- ring buffer

## Contract

The behavioural semantics required of a primitive. Contracts describe inputs, outputs, side effects, failure modes, platform expectations, invariants and verification requirements.

## Specimen

A concrete implementation of a primitive. A specimen may be reusable by copying, depending on it, adapting it, or using it as a reference.

## Evidence

An attributable observation about a subject. Evidence is recorded independently rather than collapsed into a star rating.

Conceptual shape:

```text
Evidence
  subject
  kind
  claim
  result
  source
  observed_at
  applies_to
  methodology
  artifact
  provenance
```

Examples include unit tests, property tests, fuzzing, platform verification, production use, dependency state, licence findings, vulnerability queries and contract-test results.

## Provenance

The trace from source material through extraction, adaptation, verification and local use. Reusery should be able to explain where an implementation or claim came from.

## Policy

Project or organisation constraints applied deterministically where possible. Examples include permitted licences, required platforms, forbidden dependencies and minimum evidence requirements.

A policy is a set of explicit rules whose outcomes are exactly:

- **allow**
- **review**
- **deny**

A policy decision is **not Evidence**. It never satisfies, fails or refutes a
behavioural contract requirement, and it never becomes a behavioural PASS. It
is a statement about a project's constraints applied to facts extracted from
Evidence, phrased as "allowed by policy `<id>`" or "denied by policy `<id>`".

Policy decisions carry no score, no weight and no confidence. `review` does not
mean deny, and it does not mean automatic approval either: it only blocks
automatic selection for direct implementation.

## VerificationRun

A reproducible attempt to test a specimen against some part of a contract, with environment, inputs, results, artifacts and timestamps recorded.

## Resolution

The resolver's output. A resolution contains requirements, candidates considered, evidence, rejection reasons, uncertainty and an outcome:

- REUSE
- ADAPT
- DEPEND
- REFERENCE
- BUILD LOCALLY

Since Packet 7 a resolution also records **`policy_id`**: the deterministic
policy profile that justified the decision. Resolutions written before Packet 7
carry an empty `policy_id`, which is recorded honestly rather than backfilled.

**REFERENCE does not claim the contract is satisfied.** It means the
implementation is useful engineering knowledge that should not be copied or
depended upon directly. A REFERENCE resolution may preserve unresolved
behavioural requirements in `unknowns`, provided there is no required failure
and no conflicting evidence — and it states explicitly that behavioural
satisfaction has not been established.

**`needs_verification` is an intermediate resolver state, not an Outcome.**
When plausible candidates exist but required behavioural evidence is missing,
the quality layer returns `needs_verification` and persists no Resolution at
all. There is no `needs_verification` value in `model.Outcome`, and absence of
evidence is never converted into `BUILD LOCALLY`.

## Decision

A remembered project or organisation choice. Decisions prevent future agents from restarting the same investigation without context.

## Fundamental relationship

**Concept → Contract → Specimens → Evidence → Provenance**
