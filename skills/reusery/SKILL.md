---
name: reusery
description: Decide whether a solved engineering capability already exists before writing code. Use when choosing between writing local code, copying, depending on a package, adapting a library or building locally; when licence, dependency, maintenance or provenance facts matter; or when several reasonable implementations exist and you need evidence rather than a guess.
---

# Reusery — check before you rebuild

Reusery answers one question: **does a good implementation of this engineering
capability already exist, and can I justify how I use it?**

Before you spend real reasoning effort reconstructing a commodity solution,
ask Reusery. Afterward, integrate the route it returns and report what
actually happened.

## When to use it

Reach for Reusery when you are about to:

- implement a **bounded engineering capability** that has plausibly been solved
  before (subprocess handling, HTTP clients, retry/backoff, bounded queues,
  parsers, schedulers, and so on);
- choose between **local code, a package, a library or a framework**;
- make a decision where **licence**, **dependency count**,
  **maintenance** or **provenance** matters;
- pick among **several reasonable implementations** and you want attributable
  evidence instead of a popularity guess;
- spend substantial search or reasoning effort rebuilding something a known
  primitive already describes.

## When NOT to use it

Reuse is not a ritual. Skip Reusery when the work is:

- trivial project-specific glue or a one-line transformation;
- purely aesthetic (naming, formatting, comment style);
- entirely unique to this project's own behaviour, where no prior art could
  apply;
- so small that invoking Reusery would obviously cost more than writing the
  tiny local behaviour yourself.

Reusery is not a substitute for reading the code you are integrating.

## The workflow

1. **`reusery_catalog`** with no arguments — see which capabilities exist.
2. If a relevant primitive exists, call **`reusery_catalog`** again with
   `primitive_id` to read its contract and ordered requirements.
3. Formulate **bounded discovery queries** yourself: short, specific, and
   phrased as an engineer would search.
4. **`reusery_discover`** — only if external operations are enabled. If it
   returns `external_operations_disabled`, skip discovery and reason from the
   contract alone.
5. **`reusery_enrich`** — when licence, dependency or maintenance facts would
   change your decision. Skip it when they would not.
6. **`reusery_resolve`** with the candidates you care about. Omit `policy` to
   use the built-in `public-go-baseline/v1`, or supply a complete structured
   policy when you know the constraints.
7. **Interpret the disposition honestly** (see below).
8. If the fit is poor, **`reusery_refine`** with one of the supported reasons.
9. **`reusery_inspect_evidence`** only when uncertainty actually matters — it
   is bounded and costs tokens.
10. Integrate according to the **actual** outcome semantics.
11. **`reusery_report_outcome`** only after something factual happened.

## Reading a decision

| Result | What it means | What you may do |
| --- | --- | --- |
| `implementation_eligible` | Behaviour satisfied and policy allowed it. | Use it as the route the outcome says (`reuse`, `adapt`, `depend`). |
| `reference_only` | Useful as knowledge, not as code. | Read it; do **not** ship it as an implementation. |
| `needs_verification` | Plausible, but behavioural evidence is missing. | Do not use it as if approved. Either verify it yourself or build locally. |
| `needs_review` | Policy said `review`. | A human decides; `review` is not approval and not denial. |
| `blocked` | Policy denied it, a requirement failed, or feedback excluded it. | Do not use it. |

`status: needs_verification` is a **successful** tool result. It is not a
failure and not a permission to proceed as though Reusery had approved the
candidate.

## Semantic warnings — read these every time

- **REFERENCE ≠ verified implementation.** A reference outcome always keeps
  its unresolved behavioural requirements listed. It never means "approved
  dependency" or "recommended implementation".
- **needs_verification ≠ failure.** It means nobody has proven the behaviour
  yet.
- **No known advisories ≠ secure.** It means one source reported zero known
  direct advisory identifiers at the observation time. That is one narrow
  fact, not a security assessment.
- **Licence allowed by policy ≠ legal compatibility advice.** Reusery compares
  an exact expression against exact lists. Compatibility judgements are yours
  or a human's.
- **Provider search rank ≠ quality.** Candidate order is never quality order.
- **BUILD LOCALLY is a legitimate resolution**, not an error and not a
  rejection of you. It means no candidate was usable.
- **Absence of evidence is not evidence of quality.** Unknown stays unknown.
- **Never transform a Reusery `unknown` into your own certainty.** If you
  cannot cite the requirement as satisfied, say it is unknown.

## Structured refinement

`reusery_refine` accepts exactly these reasons, and nothing else:

`not_quite`, `too_many_dependencies`, `licence_not_allowed`,
`avoid_dependency`, `avoid_reference`, `archived_project`

Refinement is stateless. Each call sends the **original base policy**, the
same bounded candidate set, and the **complete accumulated feedback history**.
Do not feed a previously returned `effective_policy` back in as the base: that
applies feedback twice.

## Current limitations (Packet 9)

- **There is no automatic project fingerprint.** Reusery does not know your
  runtime, platform, target architecture or licence requirements. Supply them
  explicitly in the policy when you know them, and never invent constraints
  the project did not state. Project context arrives in Packet 10.
- **Coverage is still narrow.** If no relevant primitive exists, do not force
  the task through an unrelated contract — say so and do normal engineering
  work.
- **Discovery and enrichment may be disabled** on the server
  (`REUSERY_MCP_ENABLE_EXTERNAL_OPERATIONS`). That is a deliberate default,
  not an error. Continue with what catalog and contracts give you.
- **There is no MCP `normalize` tool on purpose.** You are already the
  reasoning model; supply structured resolver inputs directly instead of
  paying for a second model call.

## Reporting an outcome

Call `reusery_report_outcome` only after something factual has happened:

- `adopted` — you decided to use the resolved route.
- `rejected` — you ultimately did not use it.
- `integration_succeeded` — integration completed **and** the relevant local
  tests or checks passed.
- `integration_failed` — you attempted it and it failed.
- `abandoned` — work stopped with no conclusion.

**Never report `integration_succeeded` merely because code was generated.**
If you do not know the outcome, do not report one. An honest "no report" is
worth more than a fabricated success.

Outcome feedback is an append-only log. It is not behavioural Evidence, never
changes a policy, never triggers a re-resolution, and is not automatically
learned from. Say what happened; let the humans decide what it means.

## Example

```
reusery_catalog
  → no relevant primitive
  → proceed with normal engineering work

reusery_catalog {primitive_id: "process/bounded-subprocess"}
  → contract with 11 required requirements

reusery_resolve {primitive_id, contract_id, candidates: [...]}
  → status: needs_verification, 11 required requirements unknown
  → do NOT use this candidate as if it were approved

reusery_refine {…, feedback: [{candidate_id, reason: "licence_not_allowed"}]}
  → a different candidate, or build_locally
```
