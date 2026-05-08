---
layout: default
title: Frontmatter reference
parent: Authoring
nav_order: 3
description: Every field in ARTIFACT.md frontmatter — universal fields, caller-interpreted fields, and type-specific fields.
---

# Frontmatter reference

`ARTIFACT.md` frontmatter is YAML between two `---` lines at the top of the file. Field groups:

- [**Universal fields**](#universal-fields) apply to every artifact regardless of type.
- [**Caller-interpreted fields**](#caller-interpreted-fields) are stored verbatim and read by the host (harness adapter, runtime, etc.) at delivery time.
- [**Type-specific fields**](#type-specific-fields) apply to certain types only.

The prose body below the frontmatter is what the agent reads at load time. It's plain markdown.

---

## Universal fields

These apply to every artifact.

```yaml
---
type: skill | agent | context | command | rule | hook | mcp-server | <extension type>
name: run-variance-analysis
version: 1.0.0                # semver, author-chosen
description: One-line "when should I use this?"
when_to_use:
  - "After month-end close, to flag unusual variance vs. forecast"
tags: [finance, close, variance]
sensitivity: low | medium | high   # informational; not enforced by the registry
license: MIT                       # SPDX identifier
search_visibility: indexed | direct-only   # default: indexed
deprecated: false                  # set to true to mark this version deprecated
replaced_by: finance/close-reporting/run-variance-analysis-v2
release_notes: "Initial release."
---
```

| Field | Required | Description |
|:--|:--|:--|
| `type` | Yes | Artifact type. See [Artifact types](artifact-types). |
| `name` | Yes | Short identifier. The canonical artifact ID is the directory path under the registry root, separate from this field. |
| `version` | Yes | Semver. Once `(artifact_id, version)` is ingested, it's bit-for-bit immutable. |
| `description` | Yes | One-line "when should I use this?" The harness uses this to decide whether the artifact matches a prompt. Vague descriptions get ignored. |
| `when_to_use` | Optional | List of explicit situations. Additional retrieval signal. |
| `tags` | Optional | List of strings. Used for filtering in `search_artifacts`. |
| `sensitivity` | Optional | `low` (default), `medium`, `high`. Informational metadata exposed in search and load responses. Reviewer requirements based on sensitivity are enforced in the Git provider's branch protection rather than by the registry. |
| `license` | Optional | SPDX identifier. |
| `search_visibility` | Optional | `indexed` (default) or `direct-only`. `direct-only` artifacts don't appear in `search_artifacts` results; they're reachable via `load_artifact` if the caller knows the ID. |
| `deprecated` | Optional | Boolean. When `true`, `load_artifact` returns a warning, and the artifact is excluded from default search results. |
| `replaced_by` | Optional | Suggested upgrade target. Surfaced when `load_artifact` returns the deprecation warning. |
| `release_notes` | Optional | Free text. |

---

## Caller-interpreted fields

These fields are stored verbatim and consumed by the host (harness adapter, runtime, etc.) at delivery time. Podium itself doesn't enforce them; the host decides whether and how to honor them.

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

sbom:                              # CycloneDX or SPDX inline or referenced
  format: cyclonedx-1.5
  ref: ./sbom.json
```

| Field | Description |
|:--|:--|
| `mcpServers` | List of MCP servers the artifact wants registered when loaded. The host registers them. |
| `requiresApproval` | List of tools that require user approval before execution. The host enforces. |
| `runtime_requirements` | Map of runtime versions and system packages the bundled scripts depend on. The host refuses to materialize when a requirement isn't satisfied. |
| `sandbox_profile` | Execution sandbox. Hosts with sandbox capability honor it; hosts without it refuse to materialize artifacts whose `sandbox_profile != unrestricted` unless explicitly configured to ignore. |
| `effort_hint` | Advisory hint about the reasoning budget the artifact ideally consumes. See [Hints](hints). |
| `model_class_hint` | Advisory hint about the model capability tier. See [Hints](hints). |
| `sbom` | Software Bill of Materials, inline or referenced. Required by lint for sensitivity ≥ medium. |

---

## Type-specific fields

These only apply to specific types.

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
hook_event: stop                  # event name; valid values are harness-defined
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
| `hook_event` | `hook` | Lifecycle event name. Valid values are harness-defined. See [Hooks](hooks). |
| `hook_action` | `hook` | Shell snippet executed when the event fires; receives event payload on stdin. |
| `server_identifier` | `mcp-server` | Canonical server identifier. Drives the reverse index that links `skill` artifacts referencing the server via `mcpServers:`. |
| `extends` | Any | Inherit and refine another artifact's manifest. Single scalar (no multiple inheritance). See [Extends](extends). |
| `target_harnesses` | Any | Opt out of cross-harness materialization. Set to a list of harness names; the artifact only materializes for harnesses on the list. |

---

## External resources

For artifacts that ship bytes too large to bundle (the per-package soft cap is 10 MB), reference pre-uploaded objects:

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

Prose in `ARTIFACT.md` can declare provenance to enable differential trust at the host:

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

See [Extends](extends) for examples and gotchas.

---

## Where to learn more

- [Artifact types](artifact-types) explains what each `type:` is for.
- [Domains](domains) covers `DOMAIN.md`, the file that organizes artifacts in a folder hierarchy.
- [Bundled resources](bundled-resources) covers the layout and size caps for files alongside `ARTIFACT.md`.
- The full schema is in [`spec/04-artifact-model.md`](https://github.com/lennylabs/podium/blob/main/spec/04-artifact-model.md).
