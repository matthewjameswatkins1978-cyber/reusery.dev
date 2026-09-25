# Evidence evaluation

Packet 2 adds `internal/resolver`: a deterministic evaluator that maps a
specimen's evidence onto a contract requirement by requirement. It evaluates;
it does not rank, score or choose between candidates.

## How evidence maps to a requirement

Evidence applies to a requirement only when both hold:

```text
Evidence.SubjectID == Specimen.ID
Evidence.AppliesTo == Requirement.ID
```

- Evidence for another specimen is ignored.
- Evidence without an `AppliesTo` value cannot satisfy a requirement; it stays
  valid evidence in the broader Reusery model but does not map to a contract
  claim.
- Requirement coverage is never inferred from `Claim` text.

The evaluator walks `Contract.Requirements` in order, so output order matches
the contract exactly. Within a requirement, determining evidence IDs preserve
input order. No map iteration affects externally visible results.

## Status semantics

| Matched evidence | Status |
| --- | --- |
| none | `unknown` |
| PASS only | `satisfied` |
| FAIL only | `failed` |
| PASS + FAIL | `conflicting` |
| INFO only | `unknown` |
| UNKNOWN only | `unknown` |
| PASS + UNKNOWN | `satisfied` |
| FAIL + UNKNOWN | `failed` |
| PASS + FAIL (+ UNKNOWN) | `conflicting` |

`EvidenceIDs` lists only the observations that determined the status:

- `satisfied` → the PASS IDs
- `failed` → the FAIL IDs
- `conflicting` → the PASS and FAIL IDs (both sides are preserved)
- `unknown` → the INFO/UNKNOWN IDs, or empty when nothing matched

## Why INFO is not PASS

`EvidenceInfo` is an observation, not a proof. A note, a README statement or a
dependency listing can be worth recording while still leaving the requirement
unproven. INFO is therefore never converted into satisfaction.

## Why missing evidence stays UNKNOWN

Absence of evidence is not evidence of quality. A requirement with no matching
observation is `unknown`, never `satisfied`. Only positive PASS evidence
establishes satisfaction.

## Validation

Malformed inputs return normal Go errors instead of producing misleading
output:

- empty specimen ID → `ErrEmptySpecimenID`
- empty contract ID → `ErrEmptyContractID`
- empty requirement ID → `ErrEmptyRequirementID`
- duplicate requirement IDs → `ErrDuplicateRequirementID`

## Not in Packet 2

This is evaluation only. Packet 2 does not rank candidates, compute scores or
confidence, and does not decide `REUSE` / `ADAPT` / `DEPEND` / `REFERENCE` /
`BUILD LOCALLY`. Candidate selection, discovery, policy and persistence belong
to later packets. See `MODEL.md` and `RESOLVER.md` for the concepts this
implements.
