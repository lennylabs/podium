---
title: Frontmatter reference
nav_order: 5
description: "Every frontmatter field an artifact manifest accepts, with the file that holds it, its value type, its effect, and the lint checks that apply to skills."
---

# Frontmatter reference

This page lists every frontmatter field an artifact manifest accepts, the file that holds it, and what it does. Frontmatter is YAML between two `---` lines at the top of a manifest file. `DOMAIN.md` organizes a folder of artifacts rather than describing one, and [Domains](domains#domainmd-schema) lists its fields.

## Manifest files

An artifact has one or two manifest files:

| File | Used by | Contents |
|:--|:--|:--|
| `ARTIFACT.md` | Every non-skill type. Optional overlay for a skill. | Podium's canonical schema: universal, caller-interpreted, and type-specific fields. For a non-skill type, the prose body below the frontmatter is what the agent reads at load time. |
| `SKILL.md` | Skill directories (`type: skill`). | The [agentskills.io](https://agentskills.io/specification) standard's frontmatter: `name` and `description`, plus the optional `license`, `compatibility`, `metadata`, and `allowed-tools`. Its prose body is what the agent reads at load time. |

A directory that holds a `SKILL.md` and no `ARTIFACT.md` is a complete skill. When a skill has an `ARTIFACT.md`, the author writes it by hand, and its body is empty or a single one-line HTML comment.

The fields fall into these groups:

- [Universal fields](#universal-fields) apply to every artifact regardless of type.
- [SKILL.md-only fields](#skillmd-only-fields) come from the agentskills.io standard and apply to skills only.
- [Caller-interpreted fields](#caller-interpreted-fields) are stored verbatim and read by the host (the harness adapter, the runtime, and similar consumers) at delivery time.
- [Type-specific fields](#type-specific-fields) apply to certain types only.

---

## File allocation for skills

A skill's `SKILL.md` carries the agentskills.io fields, and an optional hand-written `ARTIFACT.md` carries the Podium-only fields. The split keeps `SKILL.md` strictly within the agentskills.io specification. The allocation is mechanical:

| Field | SKILL.md | ARTIFACT.md (skill overlay) | ARTIFACT.md (non-skill) |
|:--|:--|:--|:--|
| `name` | Yes (matches parent directory) | Not used | Yes |
| `description` | Yes (≤ 1024 chars) | Not used | Yes |
| `license` | Yes (SPDX) | Not used | Yes |
| `compatibility` | Optional (≤ 500 chars; human-readable) | Not used | Not used |
| `metadata` | Optional (string-to-string map) | Not used | Not used |
| `allowed-tools` | Optional (experimental) | Not used | Not used |
| `type` | Not used | Yes (`type: skill`); implied when `ARTIFACT.md` is absent | Yes |
| `version` | As `metadata.version` (string) | Optional; equal to `metadata.version` when that is a `major.minor.patch` value | Yes |
| `when_to_use`, `tags`, `sensitivity`, `search_visibility`, `deprecated`, `replaced_by`, `release_notes`, `audit_redact`, `lint_suppress`, `source` | Not used | Yes | Yes |
| `mcpServers`, `requiresApproval`, `runtime_requirements`, `sandbox_profile`, `effort_hint`, `model_class_hint`, `sbom`, `external_resources`, `target_harnesses` | Not used | Yes | Yes |
| Type-specific fields (`input`, `output`, `delegates_to`, `rule_*`, `hook_*`, `server_identifier`) | Not used | Yes (when applicable) | Yes (when applicable) |

Non-skill types (`agent`, `context`, `command`, `rule`, `hook`, `mcp-server`, and extension types) have no `SKILL.md`, and `ARTIFACT.md` carries every field.

### Name constraints

The `name` field follows the agentskills.io syntax:

- 1–64 characters.
- Lowercase Unicode alphanumeric (`a-z`, `0-9`) and hyphens.
- No leading or trailing hyphen.
- No consecutive hyphens.

Lint applies these constraints to any `name:` an artifact declares, whatever its type. Lint checks the further agentskills.io requirement, that `name` match the parent directory name, for skills only, against `SKILL.md`.

---

## Universal fields

These fields apply to every artifact. For a skill, [File allocation for skills](#file-allocation-for-skills) gives the file that holds each one.

```yaml
# In SKILL.md (for skills) or ARTIFACT.md (for non-skills):
name: run-variance-analysis
description: Flag unusual variance vs. forecast after month-end close. Use after the close period when reviewing financial performance.
license: MIT                       # SPDX identifier
```

```yaml
# In ARTIFACT.md (every non-skill type; optional overlay for skills):
type: skill | agent | context | command | rule | hook | mcp-server | <extension type>
version: 1.0.0                     # semver, author-chosen
when_to_use:
  - "After month-end close, to flag unusual variance vs. forecast"
tags: [finance, close, variance]
sensitivity: low | medium | high   # public mode rejects ingest above a floor
search_visibility: indexed | direct-only   # default: indexed
deprecated: false                  # set to true to mark this version deprecated
replaced_by: finance/close-reporting/run-variance-analysis-v2
release_notes: "Initial release."
audit_redact: [bank_account, ssn]  # frontmatter field names to mask in audit log entries
lint_suppress: [lint.skill_ref_validate]   # advisory lint rule codes to silence for this artifact
```

| Field | Type | Required | Description |
|:--|:--|:--|:--|
| `type` | enum | Yes (implied for a skill with no `ARTIFACT.md`) | `skill`, `agent`, `context`, `command`, `rule`, `hook`, `mcp-server`, or an extension type. See [Artifact types](artifact-types). |
| `name` | string | Yes | Short identifier that follows the [name constraints](#name-constraints). For a skill, it must match the parent directory name. The canonical artifact ID is the directory path under the registry root and is separate from this field. |
| `version` | semver | Yes, except for a skill | An authored version is `major.minor.patch`, chosen by the author. Once `(artifact_id, version)` is ingested, it is bit-for-bit immutable, so bump the version whenever the content changes. A skill's version comes from `ARTIFACT.md` or `SKILL.md`, or the registry assigns one. See [Skill versions](#skill-versions). |
| `description` | string | Yes | States what the artifact does and when to use it. The harness uses it to decide whether the artifact matches a prompt, and a vague description gets ignored. At most 1024 characters for a skill. |
| `when_to_use` | list of strings | No | Explicit situations the artifact applies to. See [`when_to_use` and search](#when_to_use-and-search). |
| `tags` | list of strings | No | Filter target for `search_artifacts`. |
| `sensitivity` | enum | No | `low` (default), `medium`, or `high`. Exposed in search and load responses. A registry in public mode rejects ingest of an artifact at or above its configured floor. A private deployment treats the field as metadata. The Git provider's branch protection enforces any reviewer requirements based on sensitivity. |
| `license` | string | No | SPDX identifier. |
| `search_visibility` | enum | No | `indexed` (default) or `direct-only`. A `direct-only` artifact does not appear in `search_artifacts` results. A caller that knows its ID still reaches it through `load_artifact`. |
| `deprecated` | bool | No | When `true`, `load_artifact` returns a deprecation warning, and default search results exclude the artifact. |
| `replaced_by` | string | No | Canonical ID of the suggested upgrade target. Every `load_artifact` of the artifact returns it, and it accompanies the deprecation warning when the artifact is deprecated. |
| `release_notes` | string | No | Free text. |
| `audit_redact` | list of strings | No | Frontmatter field names whose values the registry replaces with `[redacted]` in audit log entries that reference this artifact. |
| `lint_suppress` | list of strings | No | Lint rule codes to silence for this artifact. Only advisory (non-error) rules honor the list, and hard-error rules still fire. |
| `source` | string | No | Document-level provenance of the prose body. See [Provenance markers](#provenance-markers). |

### `when_to_use` and search

When the registry has an embedding provider, it computes an embedding for each artifact at ingest from the artifact's `name`, `description`, `when_to_use`, and `tags`. `when_to_use` therefore contributes to the vector half of hybrid search through the embedding computed at ingest.

The SQLite and Postgres metadata stores do not persist `when_to_use`. Lexical (BM25) matching against those stores never sees the field, and `podium admin reembed` recomputes each vector without it. Put the terms a search must match in `description` and `tags`, and use `when_to_use` for concrete situations, such as "After month-end close" rather than "When working on finance stuff."

### Skill versions

A skill's version comes from the first of these that applies:

1. The `ARTIFACT.md` `version`, when that file declares one.
2. A `SKILL.md` `metadata.version` in `major.minor.patch` form. A `metadata.version` in any other form, such as `1.0`, is ignored.
3. A registry-assigned version, when the skill declares no usable version.

When both files set a version and `metadata.version` is a `major.minor.patch` value, the two must be equal (`lint.skill_artifact_field_mismatch`).

A registry-assigned version has the form `0.0.0-auto.<N>`, a semver prerelease of `0.0.0`. Lint reports the `lint.skill_version_assigned` warning for a skill that carries one, and the diagnostic names any ignored `metadata.version` value. Set `metadata.version` in `SKILL.md` to clear the warning. The form is reserved to the registry, so an authored version in that form fails lint with `lint.invalid_version`.

`<id>@0.0.0-auto.<N>` pins an assigned version exactly. A range pin such as `<id>@1.x` never selects one. Assigned versions are candidates for `latest`. [Registry-assigned versions](#registry-assigned-versions) describes how the registry numbers them.

Removing `ARTIFACT.md` from an ingested skill changes its content hash, so a reingest at an unchanged `metadata.version` is rejected with `ingest.immutable_violation`. Bump `metadata.version`, or remove it to let the registry assign a version.

---

## SKILL.md-only fields

These fields appear only in `SKILL.md` and only for skills. They come from the agentskills.io specification. `name`, `description`, and `license` also live in `SKILL.md` for a skill and are listed under [Universal fields](#universal-fields).

```markdown
---
name: run-variance-analysis
description: Flag unusual variance vs. forecast after month-end close. Use after the close period when reviewing financial performance.
license: MIT
compatibility: Requires Python 3.10+ and pandas. Designed for Claude Code or similar.
metadata:
  author: example-org
  version: "1.2.0"
allowed-tools:
  - Bash(python:*)
  - Read
---
```

| Field | Type | Description |
|:--|:--|:--|
| `compatibility` | string | Free-form environment notes, at most 500 characters. SKILL.md-aware tools read it to show preconditions to a reader. When it is omitted, every harness adapter that writes a skill's `SKILL.md` derives a compatibility string from `runtime_requirements` and `sandbox_profile` and adds it to the materialized `SKILL.md`. The `none` adapter copies `SKILL.md` unchanged and leaves the field absent. Published marketplace output carries the same line. |
| `metadata` | map (string to string) | Open-ended map for client-specific properties that the agentskills.io specification does not define. `metadata.version` carries the skill's version (see [Skill versions](#skill-versions)). |
| `allowed-tools` | list of strings | Experimental. Tools the skill is pre-approved to call. Podium's parser rejects a bare string, so write one list entry per tool. Adapter coverage varies by harness. |

The body is the Markdown after the frontmatter. The agentskills.io specification recommends at most 5K tokens and 500 lines, with longer reference content factored into `references/`. [Manifest size lint](bundled-resources#manifest-size-lint) gives the thresholds lint enforces.

### Skill lint checks

`SKILL.md` must exist for `type: skill`. `ARTIFACT.md` is optional, and the `ARTIFACT.md` checks below apply only when it exists. Lint enforces these checks:

| Check | Severity |
|:--|:--|
| `SKILL.md` exists for `type: skill`. | Error |
| `SKILL.md` `name` matches the parent directory. | Error |
| `SKILL.md` `name` syntax follows the agentskills.io constraints. | Error |
| `SKILL.md` `description` is non-empty. The 1024-character cap is checked by the `skills-ref validate` check below and reported as a warning. | Error |
| `SKILL.md` does not contain Podium-only fields (`type`, `version`, `when_to_use`, etc.). The diagnostic recommends `metadata.version` for a version and the `ARTIFACT.md` overlay for every other field. | Error |
| `ARTIFACT.md` does not contain `name`, `description`, or `license`. | Warning |
| When `ARTIFACT.md` does contain `name`, `description`, or `license`, the values match `SKILL.md` exactly. | Error on mismatch |
| A `version` in `ARTIFACT.md` equals `SKILL.md` `metadata.version` when both are set and `metadata.version` is a `major.minor.patch` value. | Error on mismatch (`lint.skill_artifact_field_mismatch`) |
| The `ARTIFACT.md` body is empty or a single HTML comment. | Warning |
| A skill that declares no usable version has its version assigned. | Warning (`lint.skill_version_assigned`) |
| An authored version in the reserved `0.0.0-auto.<N>` form, in either file, is invalid. | Error (`lint.invalid_version`) |
| `skills-ref validate` passes against `SKILL.md`. | Warning; a suppression flag is available |

---

## Caller-interpreted fields

These fields live in `ARTIFACT.md`. The registry stores them verbatim, and the host (the harness adapter, the runtime, and similar consumers) reads them at delivery time. The registry does not enforce them, and the host decides whether and how to honor them.

```yaml
mcpServers:
  - name: finance-warehouse
    transport: stdio
    command: npx
    args: ["-y", "@company/finance-warehouse-mcp"]

requiresApproval:
  - tool: payment-submit
    reason: irreversible

runtime_requirements:
  python: ">=3.10"
  node: ">=20"
  system_packages: []

sandbox_profile: unrestricted | read-only-fs | network-isolated | seccomp-strict

effort_hint: low | medium | high | max
model_class_hint: nano | small | medium | large | frontier

sbom:                              # author-supplied passthrough
  format: cyclonedx-1.5            # informational
  ref: ./sbom.json                 # consumers fetch the SBOM via the bundled-resource path
```

| Field | Type | Description |
|:--|:--|:--|
| `mcpServers` | list of objects | MCP servers the artifact wants registered when loaded. The host registers them. |
| `requiresApproval` | list of objects | Tools that require user approval before execution. The host enforces the list. |
| `runtime_requirements` | map | Runtime versions and system packages the bundled scripts depend on. [Execution model](bundled-resources#execution-model) describes which consumers check it. |
| `sandbox_profile` | enum | `unrestricted` (default), `read-only-fs`, `network-isolated`, or `seccomp-strict`. [Sandbox profiles](bundled-resources#sandbox-profiles) describes each profile and what a host without sandbox capability does. |
| `effort_hint` | enum | `low`, `medium`, `high`, or `max`. Advisory hint about the reasoning budget the artifact ideally consumes. See [Hints](hints). |
| `model_class_hint` | enum | `nano`, `small`, `medium`, `large`, or `frontier`. Advisory hint about the model capability tier. See [Hints](hints). |
| `sbom` | object | Author-supplied SBOM hint, informational only. Podium stores the field verbatim and exposes it on `load_artifact`. It does not parse, validate, or scan the referenced SBOM. A consumer that wants vulnerability scanning fetches the SBOM through the bundled-resource path and feeds its own pipeline. |

---

## Type-specific fields

These fields live in `ARTIFACT.md` and apply to specific types only.

```yaml
# For type: agent — declared input/output schemas
input: { $ref: ./schemas/input.json }
output: { $ref: ./schemas/output.json }

# For type: agent — well-known delegation targets (advisory)
delegates_to:
  - finance/procurement/vendor-compliance-check@1.x

# For type: rule — controls when the harness loads this rule
rule_mode: always | glob | auto | explicit   # default: always
rule_globs: "src/**/*.ts,src/**/*.tsx"        # required when rule_mode: glob
rule_description: "Apply when working with database migrations"  # required when rule_mode: auto

# For type: hook — lifecycle observer
# hook_event is a canonical event name; the Hooks page lists them.
hook_event: stop
hook_action: |                    # shell snippet executed when the event fires
  echo "[hook] $hook_event triggered"

# For type: mcp-server — canonical server identifier (drives reverse index)
server_identifier: npx:@company/finance-warehouse-mcp

# Adapter targeting: opt out of cross-harness materialization for this artifact
target_harnesses: [claude-code, opencode]
```

| Field | Applies to | Description |
|:--|:--|:--|
| `input` | `agent` | Reference to the JSON Schema the agent expects as input, written as `{ $ref: ./schemas/input.json }`. A bare path scalar also parses. |
| `output` | `agent` | Reference to the JSON Schema the agent produces, in the same form as `input`. |
| `delegates_to` | `agent` | List of canonical artifact IDs this agent delegates to, usually other agents. The field is advisory: ingest records the dependency edges and checks neither the target's type nor that the target resolves. |
| `rule_mode` | `rule` | One of `always` (default), `glob`, `auto`, or `explicit`. See [Rule modes](rule-modes). |
| `rule_globs` | `rule` | Comma-separated glob patterns. Required when `rule_mode: glob`. |
| `rule_description` | `rule` | Trigger statement for the harness's autoload heuristic. Required when `rule_mode: auto`. |
| `hook_event` | `hook` | Required. One of the canonical event names that [Hooks](hooks#canonical-events) lists. A missing value, or a value outside the list, is an ingest error. The adapter translates the name to the harness's native event. |
| `hook_action` | `hook` | Required; a missing value is an ingest error. Shell snippet that runs when the event fires and receives the event payload on stdin. [Hooks](hooks#moving-the-action-into-a-bundled-script) describes how the adapter rewrites a reference to a bundled script. |
| `server_identifier` | `mcp-server` | Canonical server identifier. It keys the reverse index: any artifact whose `mcpServers:` entry resolves to this identifier gets a dependency edge to the registration, whatever the referencing artifact's type. |
| `target_harnesses` | Any | Opts the artifact out of cross-harness materialization. Set it to a list of harness names, and the artifact materializes only for harnesses on the list. |

---

## External resources

An artifact whose bytes are too large to bundle references pre-uploaded objects in `ARTIFACT.md`. Each entry carries the path the file would have in the package, the URL, the SHA-256, the size in bytes, and an optional signature:

```yaml
external_resources:
  - path: ./model.onnx
    url: s3://company-models/variance/v1/model.onnx
    sha256: 9f2c...
    size: 145000000
    signature: "sigstore:..."
```

The registry stores the URL, hash, size, and signature, and the bytes do not transit the registry. [External resources](bundled-resources#external-resources) covers when to reference a file externally and who fetches it.

---

## Provenance markers

An artifact declares the provenance of its prose so the host can apply differential trust. Two markers carry it.

The `source:` frontmatter field sets the document-level default. It lives in `ARTIFACT.md` for every type, skills included, and adapters read it from there:

```markdown
---
type: context
version: 1.0.0
source: authored
---
```

`authored`, or an omitted field, leaves the prose trusted. Any other value, such as `imported`, marks the whole body untrusted.

Inline markers in the prose body (`SKILL.md` for skills, `ARTIFACT.md` for non-skills) mark one region. They override the document-level default for that region:

```markdown
<authored prose>

<!-- begin imported source="https://wiki.example.com/policy/payments" -->
<imported text>
<!-- end imported -->
```

Adapters propagate provenance markers to harnesses that recognize trust regions, such as Claude's `<untrusted-data>` convention. Hosts can then treat imported content as data rather than as instructions. This is the primary defense against prompt injection from manifests that aggregate external content.

---

## Registry-assigned versions

This section describes how a registry numbers the versions it assigns to skills that declare none, for operators who reason about stored rows, deletes, and restores. [Skill versions](#skill-versions) covers what an author needs.

The registry derives `<N>` in `0.0.0-auto.<N>` at ingest from the rows it stores for the artifact:

| Ingested bytes | Result |
|:--|:--|
| Match the layer's most recently ingested non-deleted row of the artifact, other than a row the [no-inheritance rule](../deployment/content/layers#inheritance) retires | The ingest reuses that row's version and is a no-op. |
| Match no stored or soft-deleted row of the artifact | `<N>` is one more than the highest `<N>` any such row carries, or `0` when none carries one. |
| Match another stored or soft-deleted row of the artifact under a different version | Rejected with `ingest.invalid_artifact` and `details.constraint: "auto_version_duplicate_content"`. |

The third case rejects a revert to earlier bytes, including a revert across an interval in which the skill declared a version. `latest` keeps serving the newer row. To publish such a skill, set `metadata.version` in `SKILL.md` or change the content.

A filesystem source keeps no ingest history and reports `0.0.0-auto.0` for every skill whose version is assigned.

### Assigned versions after deletes and purges

A soft-deleted row counts toward these rules, because `podium layer restore` serves it again. The rule has these consequences:

- A layer unregistered and registered again under its ID rejects its unchanged version-less skills until the retention purge removes the earlier rows or the author sets `metadata.version`. Within the recovery window, `podium layer restore` serves the earlier rows instead.
- A purge that removes the row holding the highest `<N>` lets a later ingest assign that `<N>` again.
- A purge of every row carrying rejected bytes lets a later ingest accept them.
- An MCP server whose content cache held the purged row for those bytes keeps reporting the purged version for them until the cache drops the record.

---

## Related pages

- [Artifact types](artifact-types) explains what each `type:` is for.
- [Domains](domains) covers `DOMAIN.md`, the file that organizes artifacts in a folder hierarchy.
- [Bundled resources](bundled-resources) covers the layout and size caps for files alongside `ARTIFACT.md` and `SKILL.md`.
- The agentskills.io standard is at [agentskills.io/specification](https://agentskills.io/specification).
