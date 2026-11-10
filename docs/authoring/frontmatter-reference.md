---
title: Frontmatter reference
nav_order: 5
description: "Every field in Podium artifact frontmatter (ARTIFACT.md and, for skills, SKILL.md): universal fields, caller-interpreted fields, and type-specific fields."
---

# Frontmatter reference

Podium artifact frontmatter is YAML between two `---` lines at the top of a manifest file. Manifest files:

- **`ARTIFACT.md`** (every non-skill type; optional overlay for skills). Carries Podium's canonical schema (universal, caller-interpreted, and type-specific fields). For non-skill types, the prose body below the frontmatter is what the agent reads at load time.
- **`SKILL.md`** (skill directories, `type: skill`). Carries the [agentskills.io](https://agentskills.io/specification) standard's frontmatter (`name` and `description`, plus the optional `license`, `compatibility`, `metadata`, and `allowed-tools`). Its prose body is what the agent reads at load time. A directory that holds a `SKILL.md` and no `ARTIFACT.md` is a complete skill. When a skill has an `ARTIFACT.md`, that file's body is empty.

Field groups:

- [**Universal fields**](#universal-fields) apply to every artifact regardless of type.
- [**Caller-interpreted fields**](#caller-interpreted-fields) are stored verbatim and read by the host (harness adapter, runtime, etc.) at delivery time.
- [**Type-specific fields**](#type-specific-fields) apply to certain types only.

---

## File allocation for skills

A skill's `SKILL.md` carries the agentskills.io fields, and an optional `ARTIFACT.md` that the author writes by hand carries the Podium-only fields, so that `SKILL.md` stays strictly within the agentskills.io specification. The allocation is mechanical:

| Field | SKILL.md | ARTIFACT.md (skill, optional overlay) | ARTIFACT.md (non-skill) |
|:--|:--|:--|:--|
| `name` | Yes (matches parent directory) | — | Yes |
| `description` | Yes (≤ 1024 chars) | — | Yes |
| `license` | Yes (SPDX) | — | Yes |
| `compatibility` | Optional (≤ 500 chars; human-readable) | — | — (every adapter that writes a skill's `SKILL.md`, except `none`, derives it from `runtime_requirements` and `sandbox_profile`) |
| `metadata` | Optional (string-to-string map) | — | — |
| `allowed-tools` | Optional (experimental) | — | — |
| `type` | — | Yes (`type: skill`); implied when `ARTIFACT.md` is absent | Yes |
| `version` | As `metadata.version` (string) | Optional; equal to `metadata.version` when that is a `major.minor.patch` value | Yes |
| `when_to_use`, `tags`, `sensitivity`, `search_visibility`, `deprecated`, `replaced_by`, `release_notes`, `audit_redact`, `lint_suppress` | — | Yes | Yes |
| `mcpServers`, `requiresApproval`, `runtime_requirements`, `sandbox_profile`, `effort_hint`, `model_class_hint`, `sbom`, `external_resources`, `extends`, `target_harnesses` | — | Yes | Yes |
| Type-specific fields (`input`, `output`, `delegates_to`, `rule_*`, `hook_*`, `server_identifier`) | — | Yes (when applicable) | Yes (when applicable) |

For non-skill types (`agent`, `context`, `command`, `rule`, `hook`, `mcp-server`, extension types), `ARTIFACT.md` carries every field. There is no `SKILL.md`.

The `name` field carries the agentskills.io syntax constraints:

- 1–64 characters.
- Lowercase Unicode alphanumeric (`a-z`, `0-9`) and hyphens.
- No leading or trailing hyphen.
- No consecutive hyphens.

Lint applies these constraints to any `name:` an artifact declares, whatever its type. The further agentskills.io requirement that `name` match the parent directory name is checked for skills, against `SKILL.md`.

---

## Universal fields

These apply to every artifact. The "where it lives" column above governs which file holds each field for skills.

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

| Field | Required | Description |
|:--|:--|:--|
| `type` | Yes (implied for a skill with no `ARTIFACT.md`) | Artifact type. See [Artifact types](artifact-types). |
| `name` | Yes | Short identifier. For skills, must match the parent directory name (per agentskills.io). The canonical artifact ID is the directory path under the registry root, separate from this field. |
| `version` | Yes, except for a skill | Semver. Once `(artifact_id, version)` is ingested, it's bit-for-bit immutable. A skill's version is its `ARTIFACT.md` `version` or its `SKILL.md` `metadata.version`, and the registry assigns one when neither declares a usable value. See [Skill versions](#skill-versions). |
| `description` | Yes | "When should I use this?" The harness uses this to decide whether the artifact matches a prompt. Vague descriptions get ignored. ≤ 1024 chars for skills (per agentskills.io). |
| `when_to_use` | Optional | List of explicit situations. Additional retrieval signal. |
| `tags` | Optional | List of strings. Used for filtering in `search_artifacts`. |
| `sensitivity` | Optional | `low` (default), `medium`, `high`. Exposed in search and load responses. A registry in public mode rejects ingest of an artifact at or above its configured floor, and the floor reads the value an artifact declaring `extends:` inherits from its parent; a private deployment treats the field as metadata. Reviewer requirements based on sensitivity are enforced in the Git provider's branch protection. |
| `license` | Optional | SPDX identifier. |
| `search_visibility` | Optional | `indexed` (default) or `direct-only`. `direct-only` artifacts don't appear in `search_artifacts` results; they're reachable via `load_artifact` if the caller knows the ID. |
| `deprecated` | Optional | Boolean. When `true`, `load_artifact` returns a warning, and the artifact is excluded from default search results. |
| `replaced_by` | Optional | Suggested upgrade target. Returned by every `load_artifact` of the artifact, and alongside the deprecation warning when the artifact is deprecated (§4.7.4). |
| `release_notes` | Optional | Free text. |
| `audit_redact` | Optional | List of frontmatter field names whose values the registry replaces with `[redacted]` in audit log entries that reference this artifact. |
| `lint_suppress` | Optional | List of lint rule codes to silence for this artifact. Only advisory (non-error) rules honor the list; hard-error rules still fire. |

### Skill versions

A skill's version is the `version` field of its `ARTIFACT.md` when that file declares one, and otherwise the `SKILL.md` `metadata.version` when that value is in `major.minor.patch` form. The Agent Skills standard leaves `metadata` free-form, so a `metadata.version` in any other form, such as `1.0`, is ignored. When both are set and `metadata.version` is a `major.minor.patch` value, the two must be equal (`lint.skill_artifact_field_mismatch`).

A skill that declares no usable version carries a registry-assigned version `0.0.0-auto.<N>`, a semver prerelease of `0.0.0`, and lint reports the `lint.skill_version_assigned` warning for it. The form is reserved to the registry, so an authored version in that form fails lint with `lint.invalid_version`. The registry derives `<N>` at ingest from the rows it stores for the artifact:

- Bytes that the layer's most recently ingested non-deleted row of the artifact holds reuse that row's version, and the ingest is a no-op.
- Bytes that no stored or soft-deleted row of the artifact holds receive one more than the highest `<N>` any such row carries, or `0` when none carries one.
- Bytes that another stored or soft-deleted row of the artifact holds under a different version are rejected with `ingest.invalid_artifact` and `details.constraint: "auto_version_duplicate_content"`. A revert to earlier bytes is rejected this way, including a revert across an interval in which the skill declared a version, and so is a second layer's byte-identical version-less package. `latest` keeps serving the newer row. To publish such a skill, set `metadata.version` in `SKILL.md` or change the content.

`<id>@0.0.0-auto.<N>` pins an assigned version exactly, a range pin such as `<id>@1.x` never selects one, and assigned versions are candidates for `latest`.

A soft-deleted row counts, because `podium layer restore` serves it again. A layer unregistered and registered again under its ID therefore rejects its unchanged version-less skills until the retention purge removes the earlier rows or the author sets `metadata.version`, and `podium layer restore` within the recovery window serves the earlier rows instead. A purge that removes the row holding the highest `<N>` lets a later ingest assign that `<N>` again, and a purge of every row carrying rejected bytes lets a later ingest accept them. An MCP server whose content cache held the purged row for those bytes keeps reporting the purged version for them until the cache drops the record.

A filesystem source keeps no ingest history and reports `0.0.0-auto.0` for every skill whose version is assigned.

Removing `ARTIFACT.md` from an ingested skill changes its content hash, so a reingest at an unchanged `metadata.version` is rejected with `ingest.immutable_violation`. Bump `metadata.version`, or remove it to let the registry assign a version.

For a skill with no `ARTIFACT.md`, the registry serves a synthesized `ARTIFACT.md` carrying `type: skill` and the resolved version, which the [HTTP API](../reference/http-api) reference states byte for byte.

---

## SKILL.md-only fields (skills)

These fields appear only in `SKILL.md` and only for skills. They come from the agentskills.io specification.

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

| Field | Description |
|:--|:--|
| `compatibility` | Free-form environment notes (≤ 500 chars). Read by SKILL.md-aware tools to surface preconditions to a reader. When it is omitted, every harness adapter that writes a skill's `SKILL.md` derives a compatibility string from `runtime_requirements` and `sandbox_profile` and adds it to the materialized `SKILL.md`. The `none` adapter copies `SKILL.md` unchanged. Published marketplace output carries the same line. |
| `metadata` | Open-ended string-to-string map. Use for client-specific properties not defined by the agentskills.io spec. A `version` key in `major.minor.patch` form sets the skill's version when `ARTIFACT.md` declares none (see [Skill versions](#skill-versions)). |
| `allowed-tools` | Experimental. YAML list of tools the skill is pre-approved to call. Podium's parser rejects a bare string here, so write one list entry per tool. Adapter support varies by harness. |

---

## Caller-interpreted fields

These fields live in `ARTIFACT.md`. They are stored verbatim and consumed by the host (harness adapter, runtime, etc.) at delivery time. Podium itself doesn't enforce them; the host decides whether and how to honor them.

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

| Field | Description |
|:--|:--|
| `mcpServers` | List of MCP servers the artifact wants registered when loaded. The host registers them. |
| `requiresApproval` | List of tools that require user approval before execution. The host enforces. |
| `runtime_requirements` | Map of runtime versions and system packages the bundled scripts depend on. A Podium MCP server whose host advertises its runtime capabilities or opts into enforcement refuses a `load_artifact` whose requirements those capabilities do not satisfy, with `materialize.runtime_unavailable`. `podium sync` does not check requirements. See [Bundled resources](bundled-resources). |
| `sandbox_profile` | Execution sandbox. Hosts with sandbox capability honor it; hosts without it refuse to materialize artifacts whose `sandbox_profile != unrestricted` unless explicitly configured to ignore. |
| `effort_hint` | Advisory hint about the reasoning budget the artifact ideally consumes. See [Hints](hints). |
| `model_class_hint` | Advisory hint about the model capability tier. See [Hints](hints). |
| `sbom` | Author-supplied SBOM hint. Informational only — Podium stores the field verbatim and exposes it on `load_artifact` but does not parse, validate, or scan the referenced SBOM. Consumers that want vulnerability scanning fetch the SBOM via the bundled-resource path and feed their own pipeline. |

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
# `hook_event` is one of the canonical event names; the adapter translates to the harness's native event.
# Session: session_start, session_end.
# Prompt: user_prompt_submit.
# Tool (generic): pre_tool_use, post_tool_use, post_tool_use_failure.
# Tool (subtype): pre_shell_execution, post_shell_execution, pre_mcp_execution, post_mcp_execution,
#                 pre_read_file, post_file_edit.
# Permission: permission_request, permission_denied.
# Subagent: subagent_start, subagent_stop.
# Turn: stop.
# Compaction: pre_compact, post_compact.
# Notification: notification.
# See [Hooks](hooks) for descriptions and the per-event coverage caveat.
hook_event: stop
hook_action: |                    # shell snippet executed when the event fires
  echo "[hook] $hook_event triggered"

# For type: mcp-server — canonical server identifier (drives reverse index)
server_identifier: npx:@company/finance-warehouse-mcp

# Inheritance: explicitly extend another artifact's manifest (cross-layer merge)
extends: finance/ap/pay-invoice@1.2.x

# Adapter targeting: opt out of cross-harness materialization for this artifact
target_harnesses: [claude-code, opencode]
```

| Field | Applies to | Description |
|:--|:--|:--|
| `input` / `output` | `agent` | JSON Schemas the agent expects (input) and produces (output). |
| `delegates_to` | `agent` | List of agent IDs this agent can delegate to. Advisory; the target may be any artifact type and is not enforced at ingest. |
| `rule_mode` | `rule` | One of `always`, `glob`, `auto`, `explicit`. See [Rule modes](rule-modes). |
| `rule_globs` | `rule` | Required when `rule_mode: glob`. Comma-separated glob patterns. |
| `rule_description` | `rule` | Required when `rule_mode: auto`. Drives the harness's autoload heuristic. |
| `hook_event` | `hook` | Required for `type: hook`; a missing value is an ingest error. One of the canonical event names. Session: `session_start`, `session_end`. Prompt: `user_prompt_submit`. Generic tool: `pre_tool_use`, `post_tool_use`, `post_tool_use_failure`. Tool subtypes: `pre_shell_execution`, `post_shell_execution`, `pre_mcp_execution`, `post_mcp_execution`, `pre_read_file`, `post_file_edit`. Permission: `permission_request`, `permission_denied`. Subagent: `subagent_start`, `subagent_stop`. Turn: `stop`. Compaction: `pre_compact`, `post_compact`. Notification: `notification`. A value outside this list is an ingest error. The adapter translates to the harness's native event. See [Hooks](hooks). |
| `hook_action` | `hook` | Required for `type: hook`; a missing value is an ingest error. Shell snippet executed when the event fires; receives event payload on stdin. |
| `server_identifier` | `mcp-server` | Canonical server identifier. Drives the reverse index that links `skill` artifacts referencing the server via `mcpServers:`. |
| `extends` | Any | Inherit and refine another artifact's manifest. Single scalar (no multiple inheritance). See [Extends](extends). |
| `target_harnesses` | Any | Opt out of cross-harness materialization. Set to a list of harness names; the artifact only materializes for harnesses on the list. |

---

## External resources

For artifacts that ship bytes too large to bundle (the per-package soft cap is 10 MB), reference pre-uploaded objects in `ARTIFACT.md`:

```yaml
external_resources:
  - path: ./model.onnx
    url: s3://company-models/variance/v1/model.onnx
    sha256: 9f2c...
    size: 145000000
    signature: "sigstore:..."
```

The registry stores the URL, hash, size, and signature; bytes don't transit the registry. See [Bundled resources](bundled-resources) for the full bundled-vs-external decision.

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

`authored` (and an omitted field) leaves the prose trusted. Any other value, `imported` for example, marks the whole body untrusted.

Inline markers in the prose body (`SKILL.md` for skills, `ARTIFACT.md` for non-skills) mark one region and override the document-level default for that region:

```markdown
<authored prose>

<!-- begin imported source="https://wiki.example.com/policy/payments" -->
<imported text>
<!-- end imported -->
```

Adapters propagate provenance markers to harnesses that support trust regions (Claude's `<untrusted-data>` convention, etc.). Hosts can apply differential trust, treating imported content as data rather than as instruction. This is the primary defense against prompt injection from manifests that aggregate external content.

---

## Cross-layer merge

When two layers contribute artifacts with the same canonical ID, the higher-precedence one can declare `extends:` to inherit and refine the lower one. Field merge semantics:

| Field | Merge |
|:--|:--|
| `description`, `name`, `release_notes` | Scalar; child wins. |
| `tags` | List; append unique. |
| `when_to_use` | List; append. |
| `sensitivity` | Scalar; most-restrictive (high > medium > low). |
| `mcpServers` | List of objects; deep-merge by `name`. |
| `requiresApproval` | List; append. |
| `runtime_requirements` | Map; deep-merge with child wins. |
| `sandbox_profile` | Scalar; the parent's value unless it is unset or `unrestricted`. A child that sets a different restricting profile is rejected; see [Sandbox profile](../authoring/extends.md#sandbox-profile). |
| `delegates_to` | List; append. |
| `external_resources` | List; append. |
| `license` | Scalar; child wins (lint warning if changed across layers). |
| `search_visibility` | Scalar; most-restrictive (`direct-only` > `indexed`). |

If a child omits a field of its `ARTIFACT.md` frontmatter, or sets an empty scalar, the parent's value is inherited unchanged. This holds for every frontmatter field, including the fields in the table above, except that a child whose parent comes from another layer restates the [executable fields](extends#executable-fields-across-layers) that parent sets. When both the parent and the child declare a value, a field in the table merges per its row, and every field outside the table takes the child's value. The child's `type:` must match the parent's, and the child's `version:` is independent of the parent's.

For skills, the merge applies to the `ARTIFACT.md` frontmatter alone. A skill that declares `extends:` therefore carries an `ARTIFACT.md`, and the parent it names may be a skill without one. The registry serves the child's `SKILL.md` verbatim, so `name`, `description`, `license`, and the other agentskills.io fields come from the child's `SKILL.md` and are not inherited from the parent's, whether or not the child's `SKILL.md` declares them.

See [Extends](extends) for examples and gotchas.

---

## Where to learn more

- [Artifact types](artifact-types) explains what each `type:` is for.
- [Domains](domains) covers `DOMAIN.md`, the file that organizes artifacts in a folder hierarchy.
- [Bundled resources](bundled-resources) covers the layout and size caps for files alongside `ARTIFACT.md` and `SKILL.md`.
