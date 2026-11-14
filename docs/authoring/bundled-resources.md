---
title: Bundled resources
nav_order: 10
description: "Files that ship alongside ARTIFACT.md (and SKILL.md, for skills): scripts, references, assets, schemas, and datasets, plus external resources for large files."
---

# Bundled resources

This page covers the files an artifact ships alongside its manifest: how to lay them out, reference them, and keep them within the size limits, and how hosts receive and run them.

A bundled resource is any file in an artifact's directory other than its manifest files. The manifest files are `SKILL.md` and the optional `ARTIFACT.md` for a skill, and `ARTIFACT.md` for every other type. Bundled resources include Python scripts, Jinja templates, JSON schemas, evaluation datasets, binary blobs, and model weights. They are packaged with the manifest and shipped to the host at materialization time.

There is no `resources:` list in frontmatter. Every file in the folder ships. The registry's content hash covers every file in the set, so an incidental file in the directory changes the artifact's hash. Remove any file that should not ship before ingest. [Which files are bundled](#which-files-are-bundled) gives the exact rules.

---

## Layout

For skills, the [agentskills.io](https://agentskills.io/specification) standard recommends these conventional subfolders:

- `scripts/` for executable code.
- `references/` for documentation loaded on demand.
- `assets/` for templates and data files.

Other subfolder names are permitted. SKILL.md-aware tools recognize the conventional ones.

```
finance/close-reporting/run-variance-analysis/   # type: skill
├── SKILL.md
├── ARTIFACT.md                                # optional overlay
├── scripts/
│   ├── variance.py
│   └── helpers.py
├── references/
│   └── variance-explained.md
└── assets/
    ├── variance-report.md.j2
    └── output-schema.json
```

---

## Referencing files from prose

Reference bundled files inline in the prose body:

```markdown
Run `scripts/variance.py` against the closed period. Format the
output using [the report template](assets/variance-report.md.j2).
```

The ingest-time linter resolves every markdown link (`[text](path)`) in the prose body:

- A relative link must name a bundled file, one of the artifact's own manifest files, or the canonical ID of another artifact in the catalog.
- An `http` or `https` link is validated with an HTTP HEAD request that must return 200 or a 3xx redirect. `podium lint --offline` skips this probe.
- A link that resolves to none of these is an ingest error.

A path written as inline code rather than as a markdown link is not checked.

---

## Size thresholds

| Threshold | Limit | Behavior |
|:--|:--|:--|
| Inline cutoff | 256 KB | At or below this size, resource bytes are returned in the `load_artifact` response body. Above it, the response carries a URL to fetch them from, unless the registry holds the resource inline. The registry holds inline every resource of an artifact ingested while no object store was configured. |
| Per-file soft cap | 1 MB | Ingest-time warning above this size. |
| Per-package soft cap | 10 MB | Ingest-time error above this size. |

Each deployment can configure the soft caps. Above the per-package cap, use [external resources](#external-resources). The size thresholds never remove a file from the bundled set.

---

## External resources

An artifact whose bytes are too large to bundle references pre-uploaded objects, with a hash and a signature:

```yaml
external_resources:
  - path: ./model.onnx
    url: s3://company-models/variance/v1/model.onnx
    sha256: 9f2c...
    size: 145000000
    signature: "sigstore:..."
```

The registry stores the URL, hash, size, and signature, and the bytes do not transit the registry. No built-in consumer fetches them: `podium sync`, the MCP server, and the SDKs materialize bundled resources only. A host that needs the external bytes reads the `external_resources` entries from the served manifest, then fetches and verifies them itself.

The size caps do not apply to external resources. Use them for model files, large datasets, and vendored binaries.

---

## Execution model

The consumer writes scripts to disk at materialization time, and the host's runtime executes them. Declare runtime expectations in `runtime_requirements:`:

```yaml
runtime_requirements:
  python: ">=3.10"
  node: ">=20"
  system_packages: ["jq", "curl"]
```

Adapters pass these requirements to the host where the harness's format carries them. A format that keeps only a fixed field set, such as the Codex agent TOML, drops them.

Enforcement depends on the consumer:

- A host that advertises its runtime capabilities to the Podium MCP server, or opts into enforcement, refuses a `load_artifact` it cannot satisfy, with `materialize.runtime_unavailable`.
- A host that advertises no capabilities and does not opt in receives the requirement and proceeds.
- `podium sync` materializes the artifact without checking the requirement.

These environment variables control the MCP server's check:

| Variable | Effect |
|:--|:--|
| `PODIUM_HOST_PYTHON`, `PODIUM_HOST_NODE`, and `PODIUM_HOST_PACKAGES` | Advertise the host's capabilities. |
| `PODIUM_ENFORCE_RUNTIME_REQUIREMENTS` | Opts into enforcement. |
| `PODIUM_IGNORE_RUNTIME_REQUIREMENTS` | Overrides the refusal. |

The [environment-variable reference](../reference/cli#environment-variables) gives each variable's format and precedence.

### Sandbox profiles

The `sandbox_profile:` field declares execution constraints:

| Profile | Meaning |
|:--|:--|
| `unrestricted` | No sandbox constraints. Default for low-sensitivity. |
| `read-only-fs` | Filesystem is read-only outside the materialization destination. |
| `network-isolated` | No outbound network. |
| `seccomp-strict` | Strict syscall allowlist (per a baseline profile shipped with Podium). |

Hosts with sandbox capability honor the profile. A host without it refuses to materialize an artifact whose `sandbox_profile != unrestricted`, unless it is explicitly configured to ignore the profile. In that case it logs a loud warning.

---

## Trust model

Bundled scripts inherit the artifact's `sensitivity` label. A high-sensitivity skill that bundles a Python script ships code that the host runtime executes, and the registry treats it accordingly.

Enforce script-level controls in pre-merge CI run by the source repository: secret scanning, static analysis, dependency scanning, and optional sandbox policy review. Podium reads no in-repo permission files and does not introspect bundle contents. The Git provider's branch protection is the gate. [Connecting layer sources](../deployment/content/layer-sources#git-repositories) covers publishing from a Git repository.

To ship an SBOM, bundle it as an ordinary resource, such as `bom.json` or `sbom.spdx.json`. The `sbom:` frontmatter field is informational and points consumers at the file. Podium does not parse the SBOM, enforce its presence, or run vulnerability scanning. Scanning belongs to the source-repository CI and the deployer's continuous scanning pipeline.

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

Inline markers in the prose body (`SKILL.md` for skills, `ARTIFACT.md` for non-skills) mark one region and override the default for the region they wrap:

```markdown
<authored prose>

<!-- begin imported source="https://wiki.example.com/policy/payments" -->
<imported text>
<!-- end imported -->
```

Adapters propagate provenance markers to harnesses that recognize trust regions, such as Claude's `<untrusted-data>` convention. Hosts apply differential trust and treat imported content as data rather than as instructions. This is the primary defense against prompt injection from manifests that aggregate external content. [Provenance markers](frontmatter-reference#provenance-markers) gives the accepted `source:` values.

---

## Manifest size lint

Manifest content has a cap of about 20K tokens. For a skill, the cap applies to the `SKILL.md` body. The agentskills.io specification recommends keeping that body under 5K tokens and at most 500 lines, with longer reference material moved into `references/`. Larger reference content can also become a separate `type: context` artifact that the prose body references.

| Threshold | Lint result |
|:--|:--|
| 20K tokens of manifest content | Ingest error (`lint.manifest_size`). |
| 5K tokens in a `SKILL.md` body | Warning. |
| 500 lines in a `SKILL.md` body | Warning. |

When an artifact hits a threshold, decide whether the prose is manifest-level material, such as instructions and `when_to_use` details, or reference material that belongs in its own artifact.

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

---

## Storage and delivery

The registry stores bundled resources content-addressed by SHA-256 in object storage. Bytes are deduplicated across all artifact versions within an org's storage namespace. When two artifacts ship the same file, such as a shared schema or a vendored library, the registry stores one copy.

At materialization, the registry hands out a URL per resource. The S3 backend presigns it. The filesystem backend serves the bytes from its own `/objects/<content-hash>` route, which requires the caller's token.

The consumer downloads each resource and writes it atomically (`.tmp` plus rename), so partial downloads cannot corrupt a working set. The consumer is `podium sync`, the MCP server, or an SDK `materialize()` call. All of them use the same materialization pipeline, which runs in the consumer process rather than on the registry.

An adapter writes an artifact's bundled files alongside its translated output:

| Type | Location of bundled files |
|:--|:--|
| `skill` | Inside the skill folder. |
| `context` | `.podium/context/<artifact-id>/`. |
| `agent`, `command`, and `hook` | The harness-neutral `.podium/resources/<artifact-id>/` bucket. Claude Code places an agent's files under `.claude/podium/<artifact-id>/`. |
| `rule` | None. The rule materializes as a translated rule file or as a block injected into `AGENTS.md` or `GEMINI.md`, depending on the harness. |
| `mcp-server` | None. The registration materializes as a merged config entry. |

To ship bytes with a `rule` or an `mcp-server`, ship them as a separate artifact, or materialize with `harness: none`, which writes the canonical layout including bundled files.

---

## Which files are bundled

The bundled set is every file under the artifact's directory, with these rules at the edges:

- **Dot-prefixed files count.** A file or directory whose name begins with a dot, such as `.DS_Store` or an editor swap file, is part of the set.
- **Only the package's own manifests are excluded.** The package's own `ARTIFACT.md`, and for a skill its own `SKILL.md`, sit outside the set. A file named `SKILL.md` in a subfolder such as `references/` is an ordinary bundled resource and starts no package.
- **A nested package is separate.** A nested directory that carries its own `ARTIFACT.md` is excluded from the enclosing artifact's set. It is discovered as a separate artifact whose files belong to it, unless its own name, or the name of a directory between it and the layer or overlay root, begins with a dot.
- **Discovery skips dot-prefixed directories.** Artifact discovery does not descend into a directory below the layer or overlay root whose name begins with a dot. A manifest file (`ARTIFACT.md` or `SKILL.md`) anywhere below such a directory produces no artifact. The files of that nested package ship nowhere and enter no content hash.
- **A dot-prefixed directory inside a package is bundled.** When the directory carries no `ARTIFACT.md` of its own, it is part of that package's set, together with every file below it outside a nested package.
- **The root is always walked.** Discovery walks the layer or overlay root even when its own name, or the name of a directory above it, begins with a dot. A layer at `.podium/registry/` or an overlay at `.podium/overlay/` is therefore discovered.
- **No ignore file applies.** Podium reads no ignore file, and the size thresholds never remove a file from the set.
