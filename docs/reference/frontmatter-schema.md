---
layout: default
title: Frontmatter schema
parent: Reference
nav_order: 3
description: Concise field-by-field schema for ARTIFACT.md and DOMAIN.md.
---

# Frontmatter schema

This page is a concise reference. For prose-style explanations of when to use each field, see [Authoring → Frontmatter reference](../authoring/frontmatter-reference) and [Authoring → Domains](../authoring/domains).

---

## ARTIFACT.md

### Universal fields

| Field | Type | Required | Description |
|:--|:--|:--|:--|
| `type` | enum | yes | `skill`, `agent`, `context`, `command`, `rule`, `hook`, `mcp-server`, or extension type. |
| `name` | string | yes | Short identifier. |
| `version` | semver | yes | Author-chosen semver. Once `(artifact_id, version)` is ingested, it's bit-for-bit immutable. |
| `description` | string | yes | One-line "when should I use this?" |
| `when_to_use` | list of strings | no | Explicit situations the artifact applies to. |
| `tags` | list of strings | no | Filter target for `search_artifacts`. |
| `sensitivity` | enum | no | `low` (default), `medium`, `high`. |
| `license` | string | no | SPDX identifier. |
| `search_visibility` | enum | no | `indexed` (default) or `direct-only`. |
| `deprecated` | bool | no | When `true`, `load_artifact` returns a deprecation warning. |
| `replaced_by` | string | no | Suggested upgrade target (canonical artifact ID). |
| `release_notes` | string | no | Free text. |

### Caller-interpreted fields

| Field | Type | Description |
|:--|:--|:--|
| `mcpServers` | list of objects | MCP servers the artifact wants registered when loaded. |
| `requiresApproval` | list of objects | Tools that require user approval before execution. |
| `runtime_requirements` | map | Runtime versions and system packages bundled scripts depend on. |
| `sandbox_profile` | enum | `unrestricted` (default), `read-only-fs`, `network-isolated`, `seccomp-strict`. |
| `effort_hint` | enum | `low`, `medium`, `high`, `max`. Advisory. |
| `model_class_hint` | enum | `nano`, `small`, `medium`, `large`, `frontier`. Advisory. |
| `sbom` | object | CycloneDX or SPDX inline or referenced. Required by lint for sensitivity ≥ medium. |

### Type-specific fields

| Field | Applies to | Description |
|:--|:--|:--|
| `input` | `agent` | JSON Schema for the agent's input. |
| `output` | `agent` | JSON Schema for the agent's output. |
| `delegates_to` | `agent` | List of agent IDs this agent can call. Constrained to `agent`-type targets. |
| `expose_as_mcp_prompt` | `command` | When `true`, exposed via MCP's `prompts/get` for slash-menu support. |
| `rule_mode` | `rule` | `always` (default), `glob`, `auto`, `explicit`. |
| `rule_globs` | `rule` | Required when `rule_mode: glob`. Comma-separated glob patterns. |
| `rule_description` | `rule` | Required when `rule_mode: auto`. Drives the harness's autoload heuristic. |
| `hook_event` | `hook` | Lifecycle event name (e.g., `stop`, `preCompact`, `sessionStart`). Valid values harness-defined. |
| `hook_action` | `hook` | Shell snippet executed when the event fires. |
| `server_identifier` | `mcp-server` | Canonical server identifier. Drives the reverse index that links `skill` artifacts referencing the server via `mcpServers:`. |

### Cross-cutting fields

| Field | Type | Description |
|:--|:--|:--|
| `extends` | string | Inherit and refine another artifact's manifest. Single scalar (no multiple inheritance). Pin syntax: `<id>`, `<id>@<semver>`, `<id>@<semver>.x`, `<id>@sha256:<hash>`. |
| `target_harnesses` | list of strings | Opt out of cross-harness materialization. Set to a list of harness names; the artifact only materializes for harnesses on the list. |
| `external_resources` | list of objects | External resources (URL + sha256 + size + signature) too large to bundle. |

### External resources object shape

```yaml
external_resources:
  - path: ./model.onnx
    url: s3://company-models/variance/v1/model.onnx
    sha256: 9f2c...
    size: 145000000
    signature: "sigstore:..."
```

### Provenance markers (in prose body)

```markdown
<authored prose>

<!-- begin imported source="https://wiki.example.com/policy/payments" -->
<imported text>
<!-- end imported -->
```

---

## DOMAIN.md

### Top-level fields

| Field | Type | Description |
|:--|:--|:--|
| `unlisted` | bool | When `true`, removes this folder and its subtree from `load_domain` enumeration. Default `false`. |
| `description` | string | One-line summary used wherever the domain appears as a child or sibling in another `load_domain` response. |
| `discovery` | object | Per-domain overrides of discovery rendering rules. See below. |
| `include` | list of glob patterns or artifact IDs | Imports artifacts from elsewhere into this domain. |
| `exclude` | list of glob patterns | Applied after `include`. Removes paths. |

The prose body below the frontmatter is long-form context returned by `load_domain` only when this domain is the requested path.

### `discovery` block

| Field | Type | Description |
|:--|:--|:--|
| `max_depth` | int (≥1) | Cap on the depth of the rendered subtree below the requested path. |
| `fold_below_artifacts` | int (≥0) | A subdomain whose visible artifact count (recursive) is below this threshold collapses into its parent's leaf set. |
| `fold_passthrough_chains` | bool | Collapse single-child intermediate domains into the deepest non-passthrough ancestor. |
| `notable_count` | int (≥0) | Cap on the notable list per domain in `load_domain` output. |
| `target_response_tokens` | int | Soft budget per `load_domain` response. |
| `featured` | list of canonical artifact IDs | Surfaced first in the notable list. |
| `deprioritize` | list of glob patterns | Children matching are ranked last and excluded from "notable" unless space permits. |
| `keywords` | list of strings | Author-curated terms agents should associate with this domain. Per-domain only; no tenant default. |

Tenant-level defaults for `max_depth`, `fold_below_artifacts`, `fold_passthrough_chains`, `notable_count`, `target_response_tokens` live in `registry.yaml`. Per-domain overrides apply to the subtree rooted at the `DOMAIN.md`. A tenant-level `discovery.allow_per_domain_overrides: false` disables per-domain overrides registry-wide.

### Glob syntax

| Syntax | Matches |
|:--|:--|
| `*` | One path segment. |
| `**` | Recursive (any number of segments). |
| `{a,b,c}` | Brace alternation. |

A bare canonical artifact ID matches that artifact exactly.

---

## Cross-layer merge

When two layers contribute artifacts with the same canonical ID and the higher-precedence one declares `extends:`, fields merge per the table below.

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

Extension types register their own merge semantics via `TypeProvider`.

When two layers contribute a `DOMAIN.md` for the same path:

| Field | Merge |
|:--|:--|
| `description` and prose body | Last-layer-wins. |
| `include` | Additive across layers. |
| `exclude` | Additive across layers; applied after the merged include set. |
| `unlisted` | Most-restrictive-wins. |
| `discovery.max_depth`, `discovery.notable_count`, `discovery.target_response_tokens` | Most-restrictive-wins (lowest value). |
| `discovery.fold_below_artifacts` | Most-restrictive-wins (highest value). |
| `discovery.fold_passthrough_chains` | Most-restrictive-wins (`true` over `false`). |
| `discovery.featured`, `discovery.deprioritize`, `discovery.keywords` | Append-unique. |

---

## Spec sources

- `ARTIFACT.md` schema: [`spec/04-artifact-model.md` §4.3](https://github.com/lennylabs/podium/blob/main/spec/04-artifact-model.md#43-artifact-manifest-schema).
- `DOMAIN.md` schema: [`spec/04-artifact-model.md` §4.5](https://github.com/lennylabs/podium/blob/main/spec/04-artifact-model.md#45-domain-organization).
- Discovery rendering: [`spec/04-artifact-model.md` §4.5.5](https://github.com/lennylabs/podium/blob/main/spec/04-artifact-model.md#455-discovery-rendering).
- Cross-layer merge semantics: [`spec/04-artifact-model.md` §4.6](https://github.com/lennylabs/podium/blob/main/spec/04-artifact-model.md#46-layers-and-visibility).
