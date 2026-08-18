---
title: Bundled resources
nav_order: 10
description: "Files that ship alongside ARTIFACT.md (and SKILL.md, for skills): scripts, references, assets, schemas, datasets, plus how to handle large files via external resources."
---

# Bundled resources

Anything in an artifact's directory other than `ARTIFACT.md` (and `SKILL.md` for skills) is a bundled resource. Python scripts, Jinja templates, JSON schemas, evaluation datasets, binary blobs, model weights, all packaged together with the manifest and shipped to the host at materialization time.

For skills, the [agentskills.io](https://agentskills.io/specification) standard recommends three conventional subfolders: `scripts/` for executable code, `references/` for documentation loaded on demand, and `assets/` for templates and data files. Other subfolder names are permitted; these three are recognized by SKILL.md-aware tools.

```
finance/close-reporting/run-variance-analysis/   # type: skill
├── SKILL.md
├── ARTIFACT.md
├── scripts/
│   ├── variance.py
│   └── helpers.py
├── references/
│   └── variance-explained.md
└── assets/
    ├── variance-report.md.j2
    └── output-schema.json
```

There is no `resources:` list in frontmatter. What's in the folder ships. Reference files inline in prose:

```markdown
Run `scripts/variance.py` against the closed period. Format the
output using [the report template](assets/variance-report.md.j2).
```

The ingest-time linter resolves every markdown link in the prose body (`[text](path)`). A relative link must name a bundled file, one of the artifact's own manifest files, or the canonical ID of another artifact in the catalog. An `http` or `https` link is validated with an HTTP HEAD that must return 200 or a 3xx redirect, a probe `podium lint --offline` skips. A link that resolves to none of these is an ingest error. A path written as inline code rather than as a markdown link is not checked.

---

## Storage

The registry stores bundled resources content-addressed by SHA-256 in object storage. Bytes are deduplicated across all artifact versions within an org's storage namespace; when two artifacts ship the same file (a shared schema, a vendored library), only one copy is stored.

At materialization, the registry hands out a URL per resource. The S3 backend presigns it, and the filesystem backend serves the bytes from its own `/objects/<content-hash>` route, which requires the caller's token. The consumer (`podium sync`, the MCP server, or an SDK `materialize()` call) downloads each resource and writes it atomically (`.tmp` + rename) so partial downloads cannot corrupt a working set. The materialization pipeline is the same across all three; it runs in the consumer process rather than on the registry.

An adapter writes an artifact's bundled files alongside its translated output: inside the skill folder for a skill, under `.podium/context/<artifact-id>/` for a context artifact, and in the harness-neutral `.podium/resources/<artifact-id>/` bucket for an agent, a command, or a hook (Claude Code places an agent's files under `.claude/podium/<artifact-id>/`). A `type: rule` artifact materializes into a workspace as a translated rule file or as a block injected into `AGENTS.md` or `GEMINI.md`, depending on the harness, and a `type: mcp-server` artifact as a merged config entry; the adapters write no bundled files for either. Ship those bytes as a separate artifact, or materialize with `harness: none`, which writes the canonical layout including bundled files.

---

## Size thresholds

Size thresholds:

| Threshold | Limit | Behavior |
|:--|:--|:--|
| Inline cutoff | 256 KB | At or below this, resource bytes are returned in the `load_artifact` response body. Above it, the response carries a URL to fetch them from. |
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

The registry stores the URL, hash, size, and signature. Bytes don't transit the registry, and no built-in consumer fetches them: `podium sync`, the MCP server, and the SDKs materialize bundled resources only. A host that needs the external bytes reads the `external_resources` entries from the served manifest and fetches and verifies them itself.

Caps don't apply to external resources. They're the right answer for model files, large datasets, vendored binaries.

---

## Trust model

Bundled scripts inherit the artifact's `sensitivity` label. A high-sensitivity skill that bundles a Python script is shipping code that the host runtime executes; the registry treats it accordingly.

Pre-merge CI run by the source repository (secret scanning, static analysis, dependency scanning, optional sandbox policy review) is the right place to enforce script-level controls. Podium reads no in-repo permission files and does not introspect bundle contents; the Git provider's branch protection is the gate.

Authors who want to ship an SBOM bundle it as an ordinary resource (e.g. `bom.json` or `sbom.spdx.json`); the `sbom:` frontmatter field is informational and points consumers at the file. Podium does not parse the SBOM, does not enforce its presence, and does not run vulnerability scanning. Scanning belongs to the source-repo CI and the deployer's continuous scanning pipeline.

---

## Execution model

The consumer writes scripts to disk at materialization time; the host's runtime executes them. Authors declare runtime expectations in `runtime_requirements:`:

```yaml
runtime_requirements:
  python: ">=3.10"
  node: ">=20"
  system_packages: ["jq", "curl"]
```

Adapters surface these requirements to the host where the harness's format carries them; a format that keeps only a fixed field set, such as the Codex agent TOML, drops them. A host that advertises its runtime capabilities to the Podium MCP server refuses a `load_artifact` it cannot satisfy with `materialize.runtime_unavailable`. A host that advertises no capabilities receives the requirement and proceeds, and `podium sync` materializes the artifact without checking it.

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

An artifact declares the provenance of its prose so the host can apply differential trust. The `source:` frontmatter field sets the document-level default and lives in `ARTIFACT.md` for every type, skills included:

```markdown
---
type: context
version: 1.0.0
source: authored
---
```

Inline markers in the prose body (`SKILL.md` for skills, `ARTIFACT.md` for non-skills) mark one region and override that default for the region they wrap:

```markdown
<authored prose>

<!-- begin imported source="https://wiki.example.com/policy/payments" -->
<imported text>
<!-- end imported -->
```

Adapters propagate provenance markers to harnesses that support trust regions (Claude's `<untrusted-data>` convention, etc.). Hosts apply differential trust: imported content is treated as data rather than instruction. This is the primary defense against prompt injection from manifests that aggregate external content.

---

## Manifest size lint

A reasonable cap on manifest content is around 20K tokens. For skills, the cap applies to the `SKILL.md` body; the agentskills.io spec recommends keeping that body under 5K tokens and ≤ 500 lines, with longer reference material moved into `references/`. Larger reference content can also be factored out as a separate `type: context` artifact and referenced from the prose body.

Lint fails ingest with an error (`lint.manifest_size`) above the 20K-token cap. The 5K-token and 500-line SKILL.md thresholds are warnings. Authors who hit either should ask whether the prose is genuinely manifest-level (instructions, when_to_use details) or whether it's reference material that wants its own artifact.

---

## Patterns

### Skill with a script

```
finance/close-reporting/run-variance-analysis/
├── SKILL.md
├── ARTIFACT.md
└── scripts/
    └── variance.py
```

`SKILL.md`:

```markdown
---
name: run-variance-analysis
description: Flag unusual variance vs. forecast after month-end close. Use after the close period when reviewing financial performance.
license: MIT
---

Run `scripts/variance.py` against the closed period. The script
expects FORECAST_FILE and ACTUALS_FILE environment variables...
```

`ARTIFACT.md`:

```markdown
---
type: skill
version: 1.0.0
runtime_requirements:
  python: ">=3.10"
---

<!-- Skill body lives in SKILL.md. -->
```

### Skill with a template

```
finance/reports/monthly-summary/
├── SKILL.md
├── ARTIFACT.md
└── assets/
    └── summary.md.j2
```

The `SKILL.md` body references the template:

```markdown
Format the report using `assets/summary.md.j2`. Pass the metrics
dict as `m` and the period string as `period`.
```

### Skill with a JSON schema

```
finance/procurement/vendor-form/
├── SKILL.md
├── ARTIFACT.md
└── assets/
    └── vendor.json
```

The `SKILL.md` body references the schema:

```markdown
Validate the vendor record against `assets/vendor.json` before
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
  bash scripts/log.sh
runtime_requirements:
  system_packages: [jq]
```

Materialization writes every bundled file with mode 0644, so the action runs the script through an interpreter rather than executing it directly. Moving the body into a script keeps the YAML readable and makes the action testable in isolation.
