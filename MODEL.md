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

## VerificationRun

A reproducible attempt to test a specimen against some part of a contract, with environment, inputs, results, artifacts and timestamps recorded.

## Resolution

The resolver's output. A resolution contains requirements, candidates considered, evidence, rejection reasons, uncertainty and an outcome:

- REUSE
- ADAPT
- DEPEND
- REFERENCE
- BUILD LOCALLY

## Decision

A remembered project or organisation choice. Decisions prevent future agents from restarting the same investigation without context.

## Fundamental relationship

**Concept → Contract → Specimens → Evidence → Provenance**
