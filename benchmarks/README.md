# Benchmarks

Packet 7 starts Reusery's benchmark recording machinery. It deliberately does
**not** make any claim that Reusery saved time, tokens or money.

## The invariant

A record separates **MEASURED** fields from **MODELLED/ESTIMATED** fields, and
the two are never summed together.

- Never label an estimate as a measurement.
- Never calculate a savings percentage when a required baseline field is
  absent.
- Never claim Reusery saved money from a single exploratory run.
- Packet 7 only establishes inspectable recording and aggregation.
  **Packet 16 owns release-level validation claims.**

## Record format

See [schema-v1.yaml](schema-v1.yaml). A record is strict YAML:

```yaml
schema_version: 1
task_id: bounded-subprocess
variant: baseline        # baseline | reusery
model: agent-x
exploratory: true
measured:
  success: true
  elapsed_ms: 60000
  input_tokens: 9000
  output_tokens: 2100
  reasoning_tokens: 400
  tool_calls: 40
  test_runs: 3
  generated_lines: 320
  reused_specimen_ids: []
  resolution_id: 0
estimated:               # optional, separate, never merged
  engineering_minutes: 180
  basis: senior engineer estimate
notes: ""
```

Every measured field is optional: an unobserved metric stays **unknown** and is
aggregated as a `missing` count rather than as zero.

## API

`internal/benchmark` provides:

| Function | Purpose |
| --- | --- |
| `LoadRecord(path)` | read one strict record |
| `LoadRecords(dir)` | read every `.yaml` record in a directory, filename order |
| `Pair(baseline, reusery)` | compare two sides of one experiment |
| `AggregateMeasured(records)` | sum observed values only, preserving unknowns |
| `AggregateEstimated(records)` | sum modelled values only, in a separate type |

`Pair` reports `comparable: false` with a reason when a side is missing, and
computes a `savings_percent` only when the baseline value exists and is
non-zero.

## Why there is no exploratory pair here

An exploratory baseline pair requires two agent runs of the same task — one
without Reusery and one with a Reusery decision supplied — plus reliable token
and tool-call metrics from the coding environment.

That environment is not available in this repository, and this packet does not
invent missing values. No exploratory record is therefore stored. When a
harness exists, drop paired `*.yaml` files here and the tests in
`internal/benchmark` already define how they are validated and compared.

## CI

Nothing in `benchmarks/` is executed by CI, and no live model or agent call
belongs in CI. The deterministic tests live in `internal/benchmark`.
