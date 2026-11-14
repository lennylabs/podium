---
title: Bundled resources
nav_order: 6
description: "Files that ship alongside an artifact's manifest: their layout, how prose references them, the size caps, where they land when materialized, how hosts run them, and external resources for large files."
---

# Bundled resources

This page covers the files an artifact ships alongside its manifest: how to lay them out, reference them, and keep them within the size limits, where they land when materialized, and how hosts run them.

A bundled resource is any file in an artifact's directory other than its manifest files. The manifest files are `SKILL.md` and the optional `ARTIFACT.md` for a skill, and `ARTIFACT.md` for every other type. Bundled resources include Python scripts, Jinja templates, JSON schemas, evaluation datasets, binary blobs, and model weights. They are packaged with the manifest and shipped to the host at materialization time.

There is no `resources:` list in frontmatter. Every file in the folder ships. The registry's content hash covers every file in the set, so an incidental file in the directory changes the artifact's hash. Remove any file that should not ship before ingest. [Which files are bundled](#which-files-are-bundled) covers the edge cases.

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
| Per-file soft cap | 1 MB | Lint warning above this size. |
| Per-package soft cap | 10 MB | Lint error above this size, which fails ingest. |

An operator can change the caps with `PODIUM_LINT_PER_FILE_SOFT_CAP_BYTES` and `PODIUM_LINT_PER_PACKAGE_SOFT_CAP_BYTES`. Above the per-package cap, use [external resources](#external-resources). The size thresholds never remove a file from the bundled set.

A separate 256 KB cutoff decides whether a resource's bytes travel inside the `load_artifact` response or as a URL. It does not limit what an author ships. [The inline cutoff](../consuming/handling-artifact-responses#the-inline-cutoff) describes it.

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

- When the host advertises its runtime capabilities to the Podium MCP server, or opts into enforcement, the MCP server refuses a `load_artifact` whose requirements the host cannot satisfy, with `materialize.runtime_unavailable` naming the missing runtime or package.
- When the host advertises no capabilities and does not opt in, the MCP server delivers the artifact with its requirements and does not check them.
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

Hosts with sandbox capability honor the profile. The Podium MCP server refuses to materialize an artifact whose `sandbox_profile` is other than `unrestricted` on a host that does not enforce that profile, unless the host is explicitly configured to ignore the profile, in which case the server logs a warning and proceeds.

---

## Trust model

Bundled scripts inherit the artifact's `sensitivity` label. A high-sensitivity skill that bundles a Python script ships code that the host runtime executes, and the registry treats it accordingly.

Enforce script-level controls in pre-merge CI run by the source repository: secret scanning, static analysis, dependency scanning, and optional sandbox policy review. Podium reads no in-repo permission files and does not introspect bundle contents. The Git provider's branch protection is the gate. [Connecting layer sources](../deployment/content/layer-sources#git-repositories) covers publishing from a Git repository.

To ship an SBOM, bundle it as an ordinary resource, such as `bom.json` or `sbom.spdx.json`. The `sbom:` frontmatter field is informational and points consumers at the file. Podium does not parse the SBOM, enforce its presence, or run vulnerability scanning. Scanning belongs to the source-repository CI and the deployer's continuous scanning pipeline.

---

## Content provenance

Prose copied from an external source, such as a wiki page or a vendor document, belongs inside an imported-content marker, so the host treats it as data rather than as instructions. The `source:` frontmatter field marks a whole body, and `<!-- begin imported ... -->` and `<!-- end imported -->` comments mark one region. [Provenance markers](frontmatter-reference#provenance-markers) gives the syntax and the accepted values.

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

Materialization writes every bundled file with mode 0644, so the action runs the script through an interpreter rather than executing it directly. The adapter rewrites `scripts/log.sh` in `hook_action` to the installed path, as [Hooks](hooks#moving-the-action-into-a-bundled-script) describes. Moving the body into a script keeps the YAML readable and makes the action testable in isolation.

---

## Where bundled files land

The consumer that materializes an artifact (`podium sync`, the MCP server, or an SDK `materialize()` call) writes its bundled files alongside the adapter's translated output:

| Type | Location of bundled files |
|:--|:--|
| `skill` | Inside the skill folder. |
| `context` | `.podium/context/<artifact-id>/`. |
| `agent`, `command`, and `hook` | The harness-neutral `.podium/resources/<artifact-id>/` bucket. Claude Code places an agent's files under `.claude/podium/<artifact-id>/`. |
| `rule` | None. The rule materializes as a translated rule file or as a block injected into `AGENTS.md` or `GEMINI.md`, depending on the harness. |
| `mcp-server` | None. The registration materializes as a merged config entry. |

To ship bytes with a `rule` or an `mcp-server`, ship them as a separate artifact, or materialize with `harness: none`, which writes the canonical layout including bundled files.

A skill's prose finds its files at the package-relative paths it names, because they land inside the skill folder. The adapters copy `agent` and `command` prose verbatim and do not rewrite the paths in it, so a prose reference to a bundled file names the installed location when the agent needs to open the file. [Your first agent](your-first-agent#step-4-ship-a-helper-script) shows the pattern. A hook's `hook_action` is the one field the adapters rewrite to the installed location. [Configure your harness](../consuming/configure-your-harness) lists the locations for each harness, and [Handling artifact responses](../consuming/handling-artifact-responses#inline-content-and-materialized-files) describes how the bytes reach the consumer.

---

## Which files are bundled

Every file under the artifact's directory ships, dot-files included, except the package's own manifest files and the files of a nested package. Podium reads no ignore file, so delete a file before ingest to keep it out of the package.

These cases sit at the edges of that rule:

| Case | Result |
|:--|:--|
| A dot-prefixed file, such as `.DS_Store` or an editor swap file | Bundled. |
| A dot-prefixed directory inside the package with no `ARTIFACT.md` of its own | Bundled, together with every file below it outside a nested package. |
| A file named `SKILL.md` in a subfolder, such as `references/SKILL.md` | Bundled as an ordinary resource. It starts no package. |
| A subdirectory with its own `ARTIFACT.md` | Excluded from the enclosing package and discovered as a separate artifact that owns its files. |
| A nested package whose own name, or the name of a directory between it and the layer or overlay root, begins with a dot | Excluded from the enclosing package and never discovered. Its files ship nowhere and enter no content hash. |
| A manifest file anywhere below a dot-prefixed directory under the layer or overlay root | Produces no artifact, because discovery does not descend into that directory. |
| A layer or overlay root whose own name, or the name of a directory above it, begins with a dot, such as `.podium/registry/` or `.podium/overlay/` | Walked. Discovery always walks the root. |
| A file above the per-file or per-package cap | Bundled. Lint reports the size, and the thresholds never remove a file. |
