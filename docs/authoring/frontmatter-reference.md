---
layout: default
title: Frontmatter reference
parent: Authoring
nav_order: 3
description: Every field in Podium artifact frontmatter (ARTIFACT.md and, for skills, SKILL.md): universal fields, caller-interpreted fields, and type-specific fields.
---

# Frontmatter reference

Podium artifact frontmatter is YAML between two `---` lines at the top of a manifest file. Manifest files:

- **`ARTIFACT.md`** — present in every artifact directory. Carries Podium's canonical schema (universal, caller-interpreted, and type-specific fields). For non-skill types, the prose body below the frontmatter is what the agent reads at load time.
- **`SKILL.md`** — present additionally in skill directories (`type: skill`). Carries the [agentskills.io](https://agentskills.io/specification) standard's frontmatter (`name`, `description`, plus optional `license`, `compatibility`, `metadata`, `allowed-tools`). Its prose body is what the agent reads at load time. For skills, `ARTIFACT.md`'s body is empty.

Field groups:

- [**Universal fields**](#universal-fields) apply to every artifact regardless of type.
- [**Caller-interpreted fields**](#caller-interpreted-fields) are stored verbatim and read by the host (harness adapter, runtime, etc.) at delivery time.
- [**Type-specific fields**](#type-specific-fields) apply to certain types only.

---

## File allocation for skills

Skills split their frontmatter between `SKILL.md` and `ARTIFACT.md` so that `SKILL.md` stays strictly within the agentskills.io specification. The split is mechanical:

| Field | SKILL.md | ARTIFACT.md (skill) | ARTIFACT.md (non-skill) |
|:--|:--|:--|:--|
| `name` | Yes (matches parent directory) | — | Yes |
| `description` | Yes (≤ 1024 chars) | — | Yes |
| `license` | Yes (SPDX) | — | Yes |
| `compatibility` | Optional (≤ 500 chars; human-readable) | — | — (Podium derives from `runtime_requirements` and `sandbox_profile`) |
| `metadata` | Optional (string-to-string map) | — | — |
| `allowed-tools` | Optional (experimental) | — | — |
| `type` | — | Yes (`type: skill`) | Yes |
| `version`, `when_to_use`, `tags`, `sensitivity`, `search_visibility`, `deprecated`, `replaced_by`, `release_notes` | — | Yes | Yes |
| `mcpServers`, `requiresApproval`, `runtime_requirements`, `sandbox_profile`, `effort_hint`, `model_class_hint`, `sbom`, `external_resources`, `extends`, `target_harnesses` | — | Yes | Yes |
| Type-specific fields (`input`, `output`, `delegates_to`, `expose_as_mcp_prompt`, `rule_*`, `hook_*`, `server_identifier`) | — | Yes (when applicable) | Yes (when applicable) |

For non-skill types (`agent`, `context`, `command`, `rule`, `hook`, `mcp-server`, extension types), `ARTIFACT.md` carries every field. There is no `SKILL.md`.

The agentskills.io `name` field has stricter constraints than Podium's:

- 1–64 characters.
- Lowercase Unicode alphanumeric (`a-z`, `0-9`) and hyphens.
- No leading or trailing hyphen.
- No consecutive hyphens.
- Matches the parent directory name.

Lint enforces all of the above for skills.

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
# In ARTIFACT.md (every type):
type: skill | agent | context | command | rule | hook | mcp-server | <extension type>
version: 1.0.0                     # semver, author-chosen
when_to_use:
  - "After month-end close, to flag unusual variance vs. forecast"
tags: [finance, close, variance]
sensitivity: low | medium | high   # informational; not enforced by the registry
search_visibility: indexed | direct-only   # default: indexed
deprecated: false                  # set to true to mark this version deprecated
replaced_by: finance/close-reporting/run-variance-analysis-v2
release_notes: "Initial release."
```

| Field | Required | Description |
|:--|:--|:--|
| `type` | Yes | Artifact type. See [Artifact types](artifact-types). |
| `name` | Yes | Short identifier. For skills, must match the parent directory name (per agentskills.io). The canonical artifact ID is the directory path under the registry root, separate from this field. |
| `version` | Yes | Semver. Once `(artifact_id, version)` is ingested, it's bit-for-bit immutable. |
| `description` | Yes | "When should I use this?" The harness uses this to decide whether the artifact matches a prompt. Vague descriptions get ignored. ≤ 1024 chars for skills (per agentskills.io). |
| `when_to_use` | Optional | List of explicit situations. Additional retrieval signal. |
| `tags` | Optional | List of strings. Used for filtering in `search_artifacts`. |
| `sensitivity` | Optional | `low` (default), `medium`, `high`. Informational metadata exposed in search and load responses. Reviewer requirements based on sensitivity are enforced in the Git provider's branch protection rather than by the registry. |
| `license` | Optional | SPDX identifier. |
| `search_visibility` | Optional | `indexed` (default) or `direct-only`. `direct-only` artifacts don't appear in `search_artifacts` results; they're reachable via `load_artifact` if the caller knows the ID. |
| `deprecated` | Optional | Boolean. When `true`, `load_artifact` returns a warning, and the artifact is excluded from default search results. |
| `replaced_by` | Optional | Suggested upgrade target. Surfaced when `load_artifact` returns the deprecation warning. |
| `release_notes` | Optional | Free text. |

---

## SKILL.md-only fields (skills)

These fields appear only in `SKILL.md` and only for skills. They come from the agentskills.io specification.

```yaml
---
name: run-variance-analysis
description: Flag unusual variance vs. forecast after month-end close. Use after the close period when reviewing financial performance.
license: MIT
compatibility: Requires Python 3.10+ and pandas. Designed for Claude Code or similar.
metadata:
  author: example-org
allowed-tools: Bash(python:*) Read
---
```

| Field | Description |
|:--|:--|
| `compatibility` | Free-form environment notes (≤ 500 chars). Read by SKILL.md-aware tools to surface preconditions to a reader. If omitted, the Podium adapter derives a compatibility string from `runtime_requirements` and `sandbox_profile` at materialization time for harnesses that consume only the agentskills.io subset. |
| `metadata` | Open-ended string-to-string map. Use for client-specific properties not defined by the agentskills.io spec. |
| `allowed-tools` | Experimental. Space-separated list of tools the skill is pre-approved to call. Adapter support varies by harness. |

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
| `runtime_requirements` | Map of runtime versions and system packages the bundled scripts depend on. The host refuses to materialize when a requirement isn't satisfied. |
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

# For type: agent — well-known delegation targets (constrained to agent-type)
delegates_to:
  - finance/procurement/vendor-compliance-check@1.x

# For type: command — opt-in projection as MCP prompt
expose_as_mcp_prompt: true

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
extends: finance/ap/pay-invoice@1.2

# Adapter targeting: opt out of cross-harness materialization for this artifact
target_harnesses: [claude-code, opencode]
```

| Field | Applies to | Description |
|:--|:--|:--|
| `input` / `output` | `agent` | JSON Schemas the agent expects (input) and produces (output). |
| `delegates_to` | `agent` | List of agent IDs this agent can delegate to. Constrained to `agent`-type targets at lint time. |
| `expose_as_mcp_prompt` | `command` | When `true`, the MCP server exposes the command via MCP's `prompts/get` for slash-menu support. The field name keeps MCP's protocol vocabulary. |
| `rule_mode` | `rule` | One of `always`, `glob`, `auto`, `explicit`. See [Rule modes](rule-modes). |
| `rule_globs` | `rule` | Required when `rule_mode: glob`. Comma-separated glob patterns. |
| `rule_description` | `rule` | Required when `rule_mode: auto`. Drives the harness's autoload heuristic. |
| `hook_event` | `hook` | One of the canonical event names. Session: `session_start`, `session_end`. Prompt: `user_prompt_submit`. Generic tool: `pre_tool_use`, `post_tool_use`, `post_tool_use_failure`. Tool subtypes: `pre_shell_execution`, `post_shell_execution`, `pre_mcp_execution`, `post_mcp_execution`, `pre_read_file`, `post_file_edit`. Permission: `permission_request`, `permission_denied`. Subagent: `subagent_start`, `subagent_stop`. Turn: `stop`. Compaction: `pre_compact`, `post_compact`. Notification: `notification`. The adapter translates to the harness's native event. See [Hooks](hooks). |
| `hook_action` | `hook` | Shell snippet executed when the event fires; receives event payload on stdin. |
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

Prose in the manifest body (`SKILL.md` for skills, `ARTIFACT.md` for non-skills) can declare provenance to enable differential trust at the host:

```markdown
---
source: authored
---

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
| `sandbox_profile` | Scalar; most-restrictive. |
| `delegates_to` | List; append. |
| `external_resources` | List; append. |
| `license` | Scalar; child wins (lint warning if changed across layers). |
| `search_visibility` | Scalar; most-restrictive (`direct-only` > `indexed`). |

For skills, the merge applies to fields in their canonical files: `name`, `description`, and `license` merge across `SKILL.md` files; everything else merges across `ARTIFACT.md` files.

Fields not in this table merge as "child wins": if the child sets the field its value replaces the parent's, otherwise the parent's value is inherited. The child's `type:` must match the parent's, and the child's `version:` is independent of the parent's.

See [Extends](extends) for examples and gotchas.

---

## Where to learn more

- [Artifact types](artifact-types) explains what each `type:` is for.
- [Domains](domains) covers `DOMAIN.md`, the file that organizes artifacts in a folder hierarchy.
- [Bundled resources](bundled-resources) covers the layout and size caps for files alongside `ARTIFACT.md` and `SKILL.md`.
