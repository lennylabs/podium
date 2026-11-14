---
title: Frontmatter schema
nav_order: 3
description: Field-by-field schema for ARTIFACT.md, SKILL.md (for skills), and DOMAIN.md.
---

# Frontmatter schema

This page lists the frontmatter fields of `ARTIFACT.md`, `SKILL.md`, and `DOMAIN.md` in table form, for lookup. For guidance on when to use each field, see [Authoring → Frontmatter reference](../authoring/frontmatter-reference) and [Authoring → Domains](../authoring/domains).

---

## Manifest files

An artifact directory holds one or two manifest files, depending on its type:

| Artifact | Manifest files |
|:--|:--|
| Any type other than `skill` | An `ARTIFACT.md`, which carries every field listed below. |
| A skill (`type: skill`) | A `SKILL.md` that complies with the [agentskills.io](https://agentskills.io/specification) standard, plus an optional `ARTIFACT.md` overlay that the author writes by hand for the Podium-only fields. |

A directory that holds a `SKILL.md` and no `ARTIFACT.md` is a complete skill. The [SKILL.md](#skillmd-for-type-skill) section below summarizes how a skill's fields are split across the two files.

## ARTIFACT.md

### Universal fields

The last column states where each field lives for a skill.

| Field | Type | Required | Description | For skills |
|:--|:--|:--|:--|:--|
| `type` | enum | yes (implied for a skill with no `ARTIFACT.md`) | `skill`, `agent`, `context`, `command`, `rule`, `hook`, `mcp-server`, or an extension type. | In `ARTIFACT.md` when present; implied otherwise |
| `name` | string | yes | Short identifier. For skills, it must match the parent directory name (per agentskills.io). | In `SKILL.md` |
| `version` | semver | yes, except for a skill | An authored version is `major.minor.patch` and chosen by the author. A skill that declares no usable version carries a registry-assigned `0.0.0-auto.<N>`. Once `(artifact_id, version)` is ingested, it is bit-for-bit immutable. | `metadata.version` in `SKILL.md`, or `version` in `ARTIFACT.md`; assigned when neither declares a usable one |
| `description` | string | yes | "When should I use this?" At most 1024 characters for skills. | In `SKILL.md` |
| `when_to_use` | list of strings | no | Explicit situations the artifact applies to. | In `ARTIFACT.md` |
| `tags` | list of strings | no | Filter target for `search_artifacts`. | In `ARTIFACT.md` |
| `sensitivity` | enum | no | `low` (default), `medium`, or `high`. | In `ARTIFACT.md` |
| `license` | string | no | SPDX identifier. | In `SKILL.md` |
| `search_visibility` | enum | no | `indexed` (default) or `direct-only`. | In `ARTIFACT.md` |
| `deprecated` | bool | no | When `true`, `load_artifact` returns a deprecation warning. | In `ARTIFACT.md` |
| `replaced_by` | string | no | Suggested upgrade target (canonical artifact ID). | In `ARTIFACT.md` |
| `release_notes` | string | no | Free text. | In `ARTIFACT.md` |
| `audit_redact` | list of strings | no | Frontmatter field names whose values the registry replaces with `[redacted]` in audit log entries that reference this artifact. | In `ARTIFACT.md` |
| `lint_suppress` | list of strings | no | Lint rule codes to silence for this artifact. Only advisory rules honor the list. | In `ARTIFACT.md` |
| `source` | string | no | Document-level provenance of the prose body. `authored` is the documented value. Any other value marks the body's default trust region as untrusted. Inline `<!-- begin imported ... -->` blocks override it per region. | In `ARTIFACT.md` |

### Caller-interpreted fields

| Field | Type | Description |
|:--|:--|:--|
| `mcpServers` | list of objects | MCP servers the artifact wants registered when loaded. |
| `requiresApproval` | list of objects | Tools that require user approval before execution. |
| `runtime_requirements` | map | Runtime versions and system packages that bundled scripts depend on. |
| `sandbox_profile` | enum | `unrestricted` (default), `read-only-fs`, `network-isolated`, or `seccomp-strict`. |
| `effort_hint` | enum | `low`, `medium`, `high`, or `max`. Advisory. |
| `model_class_hint` | enum | `nano`, `small`, `medium`, `large`, or `frontier`. Advisory. |
| `sbom` | object | Author-supplied SBOM hint (e.g. `{format: cyclonedx-1.5, ref: ./sbom.json}`). Stored verbatim and exposed on `load_artifact`. Podium does not parse, validate, or scan it. Vulnerability scanning is a CI/CD concern. |

### Type-specific fields

| Field | Applies to | Description |
|:--|:--|:--|
| `input` | `agent` | Reference to the agent's input JSON Schema document, written as `{ $ref: ./schemas/input.json }`. A bare path scalar also parses. |
| `output` | `agent` | Reference to the agent's output JSON Schema document, in the same form as `input`. |
| `delegates_to` | `agent` | List of artifact IDs this agent delegates to. Advisory. Ingest records the edges and does not constrain the target's type. |
| `rule_mode` | `rule` | `always` (default), `glob`, `auto`, or `explicit`. |
| `rule_globs` | `rule` | Required when `rule_mode: glob`. Comma-separated glob patterns. |
| `rule_description` | `rule` | Required when `rule_mode: auto`. The harness's autoload heuristic reads it. |
| `hook_event` | `hook` | Canonical lifecycle event name. See [Hook events](#hook-events). |
| `hook_action` | `hook` | Shell snippet executed when the event fires. |
| `server_identifier` | `mcp-server` | Canonical server identifier. It feeds the reverse index: any artifact whose `mcpServers:` entry resolves to this identifier gets a dependency edge to the registration, regardless of the referencing artifact's type. |

### Hook events

`hook_event` takes one of the canonical event names below. The adapter translates the name to the harness's native event, and coverage varies by harness.

| Category | Events |
|:--|:--|
| Session | `session_start`, `session_end` |
| Prompt | `user_prompt_submit` |
| Generic tool | `pre_tool_use`, `post_tool_use`, `post_tool_use_failure` |
| Tool subtypes | `pre_shell_execution`, `post_shell_execution`, `pre_mcp_execution`, `post_mcp_execution`, `pre_read_file`, `post_file_edit` |
| Permission | `permission_request`, `permission_denied` |
| Subagent | `subagent_start`, `subagent_stop` |
| Turn | `stop` |
| Compaction | `pre_compact`, `post_compact` |
| Notification | `notification` |

### Cross-cutting fields

| Field | Type | Description |
|:--|:--|:--|
| `target_harnesses` | list of strings | Opts the artifact out of cross-harness materialization. Set it to a list of harness names, and the artifact materializes only for harnesses on the list. |
| `external_resources` | list of objects | External resources too large to bundle, each with a URL, a sha256, a size, and a signature. |

### External resources object schema

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

For skills, the prose body lives in `SKILL.md`. For other types, it lives in `ARTIFACT.md`.

---

## SKILL.md (for type: skill)

A `SKILL.md` carries the [agentskills.io](https://agentskills.io/specification) standard's frontmatter and the agent-facing prose body. When a skill has an `ARTIFACT.md`, that file's body is empty; a one-line HTML comment pointer is allowed.

### Top-level fields (per agentskills.io)

| Field | Type | Required | Description |
|:--|:--|:--|:--|
| `name` | string | yes | 1–64 characters: lowercase Unicode alphanumerics and hyphens, with no leading, trailing, or consecutive hyphens. Must match the parent directory name. |
| `description` | string | yes | 1–1024 characters. Describes what the skill does and when to use it. |
| `license` | string | no | License name or reference to a bundled license file. |
| `compatibility` | string | no | At most 500 characters. Free-form environment notes. See the note below the table. |
| `metadata` | map (string → string) | no | Open-ended map for client-specific extension. |
| `allowed-tools` | list of strings | no | Experimental. Pre-approved tools, one per entry. A scalar fails to parse. |

When `compatibility` is omitted, every harness adapter that writes a skill's `SKILL.md` derives a string from `runtime_requirements` and `sandbox_profile` at materialization time and adds it to the materialized `SKILL.md`. The `none` adapter copies `SKILL.md` unchanged and leaves the field absent. Published marketplace output carries the same line.

### Skill versions

A skill's version comes from the first of these sources that applies:

1. The `ARTIFACT.md` `version`, when that file declares one.
2. A `SKILL.md` `metadata.version` in `major.minor.patch` form.
3. A version the registry assigns.

A `metadata.version` in any other form, such as `1.0`, is ignored. When both files set a version and `metadata.version` is a `major.minor.patch` value, the two must be equal; `lint.skill_artifact_field_mismatch` reports a difference.

### Body

The body is the Markdown after the frontmatter. The agentskills.io specification recommends at most 5K tokens and 500 lines, with longer reference content factored into `references/`.

### Lint rules

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
| A skill that declares no usable version has its version assigned. The diagnostic names any ignored `metadata.version` value and the fix. | Warning (`lint.skill_version_assigned`) |
| An authored version in the reserved `0.0.0-auto.<N>` form, in either file, is invalid. | Error (`lint.invalid_version`) |
| `skills-ref validate` passes against `SKILL.md`. | Warning; a suppression flag is available |

---

## DOMAIN.md

### Top-level fields

| Field | Type | Description |
|:--|:--|:--|
| `unlisted` | bool | When `true`, removes this folder and its subtree from `load_domain` enumeration. Default `false`. |
| `description` | string | One-line summary used wherever the domain appears as a child or sibling in another `load_domain` response. |
| `discovery` | object | Per-domain overrides of the discovery rendering rules. See the [`discovery` block](#discovery-block). |
| `include` | list of glob patterns or artifact IDs | Imports artifacts from elsewhere into this domain. |
| `exclude` | list of glob patterns | Removes paths. Applied after `include`. |

The prose body below the frontmatter is long-form context. `load_domain` returns it only when this domain is the requested path.

### `discovery` block

| Field | Type | Description |
|:--|:--|:--|
| `max_depth` | int (≥1) | Cap on the depth of the rendered subtree below the requested path. |
| `fold_below_artifacts` | int (≥0) | A subdomain whose visible artifact count (recursive) is below this threshold collapses into its parent's leaf set. |
| `fold_passthrough_chains` | bool | Collapse single-child intermediate domains into the deepest non-passthrough ancestor. |
| `notable_count` | int (≥0) | Cap on the notable list per domain in `load_domain` output. |
| `target_response_tokens` | int | Soft budget per `load_domain` response. |
| `featured` | list of canonical artifact IDs | Surfaced first in the notable list. |
| `deprioritize` | list of glob patterns | Matching children are ranked last and excluded from the notable list unless space permits. |
| `keywords` | list of strings | Author-curated terms agents should associate with this domain. Per-domain only, with no tenant default. |

Tenant-level defaults for `max_depth`, `fold_below_artifacts`, `fold_passthrough_chains`, `notable_count`, and `target_response_tokens` live in `registry.yaml`. Per-domain overrides apply to the subtree rooted at the `DOMAIN.md`. A tenant-level `discovery.allow_per_domain_overrides: false` disables per-domain overrides registry-wide.

### Glob syntax

| Syntax | Matches |
|:--|:--|
| `*` | One path segment. |
| `**` | Recursive (any number of segments). |
| `{a,b,c}` | Brace alternation. |

A bare canonical artifact ID matches that artifact exactly.

---

## DOMAIN.md merge across layers

When two layers contribute a `DOMAIN.md` for the same path, the fields merge as follows:

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

## See also

- [Authoring → Frontmatter reference](../authoring/frontmatter-reference).
- [Authoring → Artifact types](../authoring/artifact-types).
- [Authoring → Domains](../authoring/domains).
- agentskills.io standard: [agentskills.io/specification](https://agentskills.io/specification).
