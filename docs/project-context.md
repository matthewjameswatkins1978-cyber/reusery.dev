# Project context and remembered decisions

Packet 10 adds a bounded, inspectable memory of *which project* Reusery is
deciding for. It is the only place where a previous human or agent choice can
influence a later decision, and it is deliberately small.

The rule the whole packet is built around:

> **PROJECT CONTEXT MAY CHANGE FIT. IT MUST NOT CHANGE TRUTH.**

Project context may

1. add a review requirement,
2. add an inspectable trade-off, or
3. break a tie among candidates that are already implementation-eligible.

It can never turn *blocked* into *eligible*, *unknown* behaviour into
*satisfied*, or a policy *review* into an *allow*. Nothing a project says
becomes Evidence, and nothing a project says becomes a behavioural claim.

## Project identity

A project is identified by the manifest facts Reusery can read, never by where
the files happen to live.

- **Project ID** is `project/go/` + `sha256("reusery-project-go-v1\n"` + the
  sorted unique module paths `)`.
- Two checkouts of the same module, or the same module read locally and from a
  public repository, therefore share one project ID.

## Fingerprint

A fingerprint is the typed, versioned set of derived manifest facts:

| Field | Contents |
| --- | --- |
| `schema_version` | fingerprint schema version |
| `language` | `go` |
| `modules[].module_path` | module path |
| `modules[].go_version` | declared Go version, or the workspace's |
| `modules[].toolchain` | declared toolchain, or the workspace's |
| `modules[].requirements[]` | `{module_path, version, indirect}` |
| `modules[].replacements[]` | `{old_module_path, old_version, new_module_path, new_version, local_replacement}` |

Deliberately **outside** the fingerprint, so they can never enter the hash:
storage IDs, `observed_at`, the scan root, scan warnings, source kind and
source locator.

The canonical hash is SHA-256 over canonical JSON: modules sorted by
`module_path`, requirements sorted by `module_path,version`, replacements
sorted by `old_module_path,old_version`. The **fingerprint hash** covers only
those facts; the **project context hash** additionally covers the active
preference effects a decision saw.

A local `replace foo => ../bar` records only `local_replacement: true`. The
filesystem path is never written anywhere.

### Bounds

| Bound | Value |
| --- | --- |
| workspace modules | 32 |
| bytes per manifest | 512 KiB |
| manifest files per scan | 33 (`go.work` + `go.mod`s) |
| total bytes per scan | 8 MiB |
| scan wall clock | 5s |
| candidates per decision | 24 |
| active preferences per project | 100 |
| recent resolutions | 100 stored, 20 returned by MCP, 50 by CLI |

## Sources

Only two source kinds exist: `local` and `github_public`.

### Local

- Reads `go.work` (and the `go.mod`s it names) or, without a workspace, the
  root `go.mod`.
- Never reads `*.go`, `vendor/` or `.git/`.
- Never runs `go`, `git` or any shell.
- Never stores the local root: `source_locator` is always empty and the
  project row carries no path.
- Workspace entries outside the root are skipped with a path-free warning.

### Public GitHub

- Uses the unauthenticated client only. A token is never attached, so a
  private repository fails as `not_found_or_private_repository_unsupported`
  rather than leaking that it exists.
- Resolves the ref to an immutable commit SHA **first**, then reads only
  `/repos/{owner}/{repo}`, `/commits/{ref}` and `/contents/{path}`.
- Never clones, never walks the tree, never reads a blob larger than 512 KiB
  or a response larger than 1 MiB.
- Bound: 40 requests, 15 seconds, 512 KiB decoded per file.

Because both sources read the same manifest bytes, identical manifests produce
the identical fingerprint hash and project ID.

## Remembered preferences

A preference is an explicit, reversible steering decision made by a person or
an agent. It is **never** inferred.

- The only way to create one is `reusery project remember` or the
  `reusery_project_remember` tool.
- `reusery_refine` and `reusery_report_outcome` never write memory. Feedback
  and outcome history stay in their own tables.
- The caller supplies a **structured reason**, never a value. The value is
  derived from facts Reusery already stored, so a caller cannot invent a
  licence, a dependency count or an archived flag.

### Closed vocabulary

| Reason | Derived kind | Rule |
| --- | --- | --- |
| `not_quite` | `exclude_candidate` | scoped to project + primitive + candidate |
| `too_many_dependencies` | `max_direct_dependencies` | `N-1`; rejected when the count is unknown or zero |
| `licence_not_allowed` | `deny_licence` | only with exactly one established licence, otherwise `exclude_candidate` |
| `avoid_dependency` | `avoid_dependency` | removes dependency reuse from the effective policy |
| `avoid_reference` | `avoid_reference` | removes reference reuse from the effective policy |
| `archived_project` | `deny_archived` | only when the candidate is observed archived |

Any reason the facts cannot support fails with
`preference cannot be derived from stored facts`. Nothing is stored.

### Revocation

`forget` sets `forgotten_at`. The row is never deleted, so the decision
history stays inspectable. Forgetting twice is idempotent; remembering the
same thing again creates a new active row.

### Provenance

Each preference stores `source_reason` (the structured reason that produced
it) and optionally `source_resolution_id`, so a memory can always be traced
back to the decision it came from.

## Effective policy

Active preferences are applied to a **copy** of the base policy. The stored
policy is never mutated. The result is identified as

```
<base-policy-id>+project:<full-context-hash>
```

so a decision states exactly which policy and which project context produced
it. `exclude_candidate` is expressed as ordinary structured feedback, keeping
the exclusion visible in the rejection reasons instead of hiding it.

## Dependency fit

Fit is derived from the project's requirements by **exact version
comparison only**. There is no semantic-version reasoning, no minimum-version
calculation and no dependency graph resolution.

| Fit | Meaning | Effect |
| --- | --- | --- |
| `not_applicable` | candidate is not considered as a dependency | nothing |
| `new_dependency` | no required module provides the candidate package | neutral |
| `existing_exact` | project already requires the module at the candidate revision | positive integration fact, late tie-break |
| `existing_version_change` | module exists at another version | adds a trade-off, disposition → `needs_review` |
| `existing_replaced` | module has a `replace` directive | adds a trade-off, disposition → `needs_review` |

Module matching is a longest module-path prefix on path-segment boundaries:
`github.com/foo` never matches `github.com/foobar/x`.

`existing_exact` is a **late lexicographic tie-break** — after disposition
rank and after the authored preferred reuse mode — never a score. It cannot
rescue a candidate that behaviour or policy already rejected.

Every fit that adds a trade-off also prints it, so the decision says why:

```
project requires github.com/example/dep at v1.4.0; candidate is v1.6.0;
version change requires review
```

## Storage

Migration `00004_project_context.sql` adds four tables and two columns:

| Table | Purpose |
| --- | --- |
| `projects` | one row per project identity |
| `project_fingerprints` | immutable manifest observations, unique per `(project_id, sha)` |
| `project_preferences` | explicit memories, with `forgotten_at` for revocation |
| `project_contexts` | immutable context snapshots keyed by their SHA-256 |
| `resolutions.project_id` | which project a decision was made for (`ON DELETE SET NULL`) |
| `resolutions.project_context_hash` | which context snapshot it actually saw |

A historical resolution keeps its own context hash, so it resolves to the
context it saw even after preferences or the fingerprint later change.

## Surfaces

### CLI

```
reusery project scan   [--source local|github_public] [--root DIR]
                       [--github OWNER/REPO] [--ref REF] [--subdir DIR]
reusery project show      --project-id ID
reusery project remember  --project-id ID --primitive-id ID --candidate-id ID --reason REASON
reusery project forget    --project-id ID --preference-id N
reusery project history   --project-id ID [--limit N]
reusery choose ...        --project-id ID
reusery mcp --project-root DIR
```

Sources are mutually exclusive: a default value must never silently choose the
other source. Without `--project-id`, `choose` is exactly the Packet 7
behaviour.

The local root is process configuration (`--root`, or the MCP server's
`--project-root`). It is never a tool argument and never stored.

### MCP

Four new tools extend the frozen Packet 9 surface; no Packet 9 tool was
renamed.

| Tool | Annotation |
| --- | --- |
| `reusery_project_scan` | read-write, open world |
| `reusery_project_context` | read-only |
| `reusery_project_remember` | read-write, idempotent |
| `reusery_project_forget` | read-write, idempotent |

`reusery_resolve` and `reusery_refine` gained an **optional** `project_id`.
Omitting it is byte-for-byte the Packet 9 call. When present, the decision
carries `project_id`, `project_context_hash` and per-candidate
`project_effects`.

### HTTP

No HTTP endpoint was added and the OpenAPI document did not change. Project
context is agent and operator surface, not an API surface.

## What is deliberately absent

- No private-repository access (Packet 13 owns that).
- No automatic preference learning from `refine` or `report_outcome`.
- No framework detection, no vector search, no AI ranking.
- No second resolver: project context runs through the existing Packet 7
  quality resolver.
- No `Evidence` produced from project context.
- No local filesystem path in any stored row or returned payload.

## Where to look

| Concern | File |
| --- | --- |
| Types, bounds, errors | `internal/project/types.go` |
| Canonicalisation and hashing | `internal/project/canonical.go` |
| Local scanning | `internal/project/local.go` |
| Public GitHub scanning | `internal/project/github.go` |
| Preference derivation and the policy overlay | `internal/project/preferences.go` |
| Context snapshot and effective policy | `internal/project/context.go` |
| Project-aware decisions | `internal/project/decide.go` |
| Storage service | `internal/project/service.go` |
| Dependency fit in the resolver | `internal/resolver/quality.go` |
| Migration | `internal/store/postgres/migrations/00004_project_context.sql` |
| CLI | `internal/cli/project.go` |
| MCP tools | `internal/mcpserver/project.go` |
