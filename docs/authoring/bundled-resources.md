---
layout: default
title: Bundled resources
parent: Authoring
nav_order: 8
description: Files that ship alongside ARTIFACT.md — scripts, templates, schemas, datasets, and how to handle large files via external resources.
---

# Bundled resources

Anything in an artifact's directory other than `ARTIFACT.md` is a bundled resource. Python scripts, Jinja templates, JSON schemas, evaluation datasets, binary blobs, model weights — all packaged together with the manifest and shipped to the host at materialization time.

```
finance/close-reporting/run-variance-analysis/
├── ARTIFACT.md
├── scripts/
│   ├── variance.py
│   └── helpers.py
├── templates/
│   └── variance-report.md.j2
└── schemas/
    └── output.json
```

There is no `resources:` list in frontmatter. What's in the folder ships. Reference files inline in prose:

```markdown
Run `scripts/variance.py` against the closed period. Format the
output using `templates/variance-report.md.j2`.
```

The ingest-time linter validates that prose references resolve to bundled files (existence check) and emits errors for broken paths.

---

## Storage

The registry stores bundled resources content-addressed by SHA-256 in object storage. Bytes are deduplicated across all artifact versions within an org's storage namespace; when two artifacts ship the same file (a shared schema, a vendored library), only one copy is stored.

At materialization, presigned URLs deliver the bytes. The MCP server downloads each resource and writes it atomically (`.tmp` + rename) so partial downloads cannot corrupt a working set.

---

## Size thresholds

Size thresholds:

| Threshold | Limit | Behavior |
|:--|:--|:--|
| Inline cutoff | 256 KB | Below this, resource bytes are returned in the `load_artifact` response body. Above, presigned URL. |
| Per-file soft cap | 1 MB | Ingest-time warning above this. |
| Per-package soft cap | 10 MB | Ingest-time error above this. |

Soft caps are configurable per deployment. Above the per-package cap, use `external_resources:` (below).

---

## External resources

For artifacts that ship bytes too large to bundle, reference pre-uploaded objects with hash and signature:

```yaml
external_resources:
  - path: ./model.onnx
    url: s3://company-models/variance/v1/model.onnx
    sha256: 9f2c...
    size: 145000000
    signature: "sigstore:..."
```

The registry stores the URL, hash, size, and signature. Bytes don't transit the registry. At materialization the MCP server fetches from the URL, verifies the SHA-256 and signature, and writes locally.

Caps don't apply to external resources. They're the right answer for model files, large datasets, vendored binaries.

---

## Trust model

Bundled scripts inherit the artifact's `sensitivity` label. A high-sensitivity skill that bundles a Python script is shipping code that the host runtime executes; the registry treats it accordingly.

Pre-merge CI run by the source repository (secret scanning, static analysis, SBOM generation, optional sandbox policy review) is the right place to enforce script-level controls. Podium reads no in-repo permission files; the Git provider's branch protection is the gate.

For sensitivity ≥ medium, lint requires an `sbom:` field — inline (CycloneDX or SPDX) or referenced from a bundled file. The registry consumes CVE feeds and walks SBOM dependencies to surface affected artifacts via `podium vuln list`.

---

## Execution model

The MCP server materializes scripts; the host's runtime executes them. Authors declare runtime expectations in `runtime_requirements:`:

```yaml
runtime_requirements:
  python: ">=3.10"
  node: ">=20"
  system_packages: ["jq", "curl"]
```

Adapters surface these requirements to the host. Hosts that cannot satisfy a requirement reject the artifact at load time with `materialize.runtime_unavailable`.

The `sandbox_profile:` field declares execution constraints:

| Profile | Meaning |
|:--|:--|
| `unrestricted` | No sandbox constraints. Default for low-sensitivity. |
| `read-only-fs` | Filesystem is read-only outside the materialization destination. |
| `network-isolated` | No outbound network. |
| `seccomp-strict` | Strict syscall allowlist (per a baseline profile shipped with Podium). |

Hosts with sandbox capability honor the profile. Hosts without it refuse to materialize an artifact whose `sandbox_profile != unrestricted` unless explicitly configured to ignore (with a loud warning logged).

---

## Content provenance

Prose in `ARTIFACT.md` can declare its provenance to enable differential trust at the host:

```markdown
---
source: authored
---

<authored prose>

<!-- begin imported source="https://wiki.example.com/policy/payments" -->
<imported text>
<!-- end imported -->
```

Adapters propagate provenance markers to harnesses that support trust regions (Claude's `<untrusted-data>` convention, etc.). Hosts apply differential trust: imported content is treated as data rather than instruction. This is the primary defense against prompt injection from manifests that aggregate external content.

---

## Manifest size lint

A reasonable cap on manifest content is around 20K tokens. Larger reference content should be factored out as a separate `type: context` artifact and referenced from the prose body.

Lint warns on manifests above the size cap. Authors who hit it should ask whether the prose is genuinely manifest-level (instructions, when_to_use details) or whether it's reference material that wants its own artifact.

---

## Patterns

### Skill with a script

```
finance/close-reporting/run-variance-analysis/
├── ARTIFACT.md
└── scripts/
    └── variance.py
```

```yaml
---
type: skill
name: run-variance-analysis
version: 1.0.0
description: Flag unusual variance vs. forecast after month-end close.
runtime_requirements:
  python: ">=3.10"
---

Run `scripts/variance.py` against the closed period. The script
expects FORECAST_FILE and ACTUALS_FILE environment variables...
```

### Skill with a template

```
finance/reports/monthly-summary/
├── ARTIFACT.md
└── templates/
    └── summary.md.j2
```

The prose body references the template:

```markdown
Format the report using `templates/summary.md.j2`. Pass the metrics
dict as `m` and the period string as `period`.
```

### Skill with a JSON schema

```
finance/procurement/vendor-form/
├── ARTIFACT.md
└── schemas/
    └── vendor.json
```

The prose body references the schema:

```markdown
Validate the vendor record against `schemas/vendor.json` before
submitting. The schema defines required fields and value ranges.
```

### Hook with a bundled action script

```
finance/audit/log-session-end/
├── ARTIFACT.md
└── scripts/
    └── log.sh
```

The hook's `hook_action` invokes the script:

```yaml
type: hook
hook_event: stop
hook_action: |
  scripts/log.sh
runtime_requirements:
  system_packages: [jq]
```

Keeps the YAML readable; makes the action testable in isolation.
