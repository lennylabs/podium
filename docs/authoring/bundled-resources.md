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

The ingest-time linter resolves the target of every inline link and image (`[text](path)` and `![alt](path)`) in the prose body, as CommonMark parses it:

- A relative link must name a bundled file, a directory of the package, one of the artifact's own manifest files, or the canonical ID of another artifact in the catalog. A link to a directory, with or without a trailing `/`, resolves when at least one bundled file lies below it, so `[templates](assets/templates/)` names the bundled templates. A directory that holds no bundled file does not resolve.
- An `http` or `https` link is validated with an HTTP HEAD request that must return 200 or a 3xx redirect. `podium lint --offline` skips this probe.
- A link that resolves to none of these is an ingest error. A link to a bundled file the package does not contain is such an error.
- A relative link that leaves the package, such as `../other-skill/SKILL.md`, is a warning. It names no bundled file, and it resolves only while the artifacts sit side by side in one source tree, which a consumer that installs one of them does not reproduce. The diagnostic recommends naming the other artifact by its canonical ID.

Before the lookup, the linter removes the link's fragment (from `#`) and query (from `?`) and decodes its percent-encoding, so `references/api.md#errors` resolves to the bundled file `references/api.md`. A link that is only a fragment, such as `#usage`, refers to the same document and is not checked. A target with a scheme other than `http` or `https`, such as `mailto:`, is not checked. A link whose target begins with `/`, such as `[analytics](/features/analytics)`, is root-relative. It names a path on a website and never a bundled file or an artifact, because bundled-file and artifact references are relative to the package, so it is not checked.

A path written as inline code rather than as a markdown link is not checked. Link syntax inside a code span, a fenced code block, or an indented code block is example text and is not checked, including a block nested in a list item or a block quote. A link whose bracket is escaped, such as `\[text](path)`, is text. A reference-style link, such as `[text][ref]`, is not checked.

---

## Size thresholds

| Threshold | Limit | Behavior |
|:--|:--|:--|
| Per-file soft cap | 1 MB | Lint warning above this size. |
| Per-package soft cap | 10 MB | Lint error above this size, which fails ingest. |

The limits are defaults. An operator changes them with `PODIUM_LINT_PER_FILE_SOFT_CAP_BYTES` and `PODIUM_LINT_PER_PACKAGE_SOFT_CAP_BYTES`, and changes the [manifest size thresholds](#manifest-size-lint) with `PODIUM_LINT_SKILL_BODY_WARN_TOKENS`, `PODIUM_LINT_SKILL_BODY_WARN_LINES`, and `PODIUM_LINT_MANIFEST_ERROR_TOKENS`. Registry ingest and `podium lint` read the same variables, which [Lint](../reference/configuration#lint) in the configuration reference lists. Above the per-package cap, use [external resources](#external-resources). The size thresholds never remove a file from the bundled set.

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

Manifest content has a cap of 20,000 estimated tokens by default. For a skill, the cap applies to the `SKILL.md` body. The agentskills.io specification recommends keeping that body under 5K tokens and at most 500 lines, with longer reference material moved into `references/`. Reference material that no single skill owns can become a separate skill whose description says that it carries reference material, and a domain-wide description belongs in a `DOMAIN.md` body. [Reference material](artifact-types#reference-material) lists the locations.

Lint estimates a token count as the byte length divided by four, rounded up. Each threshold below is a default, and the variable in its row changes it:

| Threshold | Default | Variable | Lint result |
|:--|:--|:--|:--|
| Manifest content | 20,000 estimated tokens | `PODIUM_LINT_MANIFEST_ERROR_TOKENS` | Ingest error (`lint.manifest_size`). |
| `SKILL.md` body | 5,000 estimated tokens | `PODIUM_LINT_SKILL_BODY_WARN_TOKENS` | Warning. |
| `SKILL.md` body | 500 lines | `PODIUM_LINT_SKILL_BODY_WARN_LINES` | Warning. |

When an artifact hits a threshold, decide whether the prose is manifest-level material, such as instructions and `when_to_use` details, or reference material that belongs in its own artifact.

---

## Near-duplicate content

`lint.near_duplicate_content` warns when two artifacts carry near-identical prose. It compares units: an artifact's prose body, which is the `SKILL.md` body for a skill and the `ARTIFACT.md` body for any other type, and each of its prose resources. A prose resource is a bundled resource that meets all of these conditions:

- Its file name ends in `.md`, `.markdown`, `.txt`, or `.rst`, compared without regard to case.
- It is valid UTF-8 and contains no NUL byte.
- It is not a license file. The linter lowercases the file name, removes its final extension, splits the rest on `-`, `_`, and `.`, and drops every part that consists only of digits. The name is a license file when its parts are `license`, `licence`, `copying`, or `notice` alone or followed by exactly one more part, or when `ofl` is its first or last part. `LICENSE.txt`, `LICENSE-MIT`, `LICENSE_1_0.txt`, `LICENSE-APACHE-2.0.txt`, `NOTICE.md`, `OFL.txt`, and `ArsenalSC-OFL.txt` are license files and are not compared. `notice-of-changes.md`, `privacy-notice.md`, and `license-compliance-guide.md` are prose resources.

Scripts, data files, and binary resources are not units. A skill carries its own copy of every file it runs, and every package carries its own license file, so neither is reported. A unit's shingles are the sequences of five consecutive words of its normalized text, and two units of different artifacts whose shingle sets have a similarity of 0.8 or higher draw the warning. `PODIUM_LINT_CONTENT_SIMILARITY` changes the threshold. A unit of fewer than 50 words is not compared, and two units of one artifact are not compared.

A shared prose resource bundled in several artifacts, such as a `references/style-guide.md` of 50 words or more, draws the warning in every artifact that bundles it. The copies can drift apart as each artifact is edited. The diagnostic names the units and the other artifacts, and abbreviates a list of more than 10 artifacts to the first 10 and a count of the rest. The warning neither blocks ingest nor changes the `podium lint` exit status under the default `--fail-on error`. To clear it, keep the copies in step and list `lint.near_duplicate_content` under `lint_suppress` in the `ARTIFACT.md` of each artifact that keeps a copy, or move the shared text into one artifact and reference that artifact by its canonical ID.

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
| `skill` | Inside the skill folder. A skill whose `invocation` is `user` materializes on OpenCode and Gemini as a command file, and its bundled files go to the `.podium/resources/<artifact-id>/` bucket. |
| `agent` and `hook` | The harness-neutral `.podium/resources/<artifact-id>/` bucket. Claude Code places an agent's files under `.claude/podium/<artifact-id>/`. |
| `rule` | None. The rule materializes as a translated rule file or as a block injected into `AGENTS.md` or `GEMINI.md`, depending on the harness. |
| `mcp-server` | None. The registration materializes as a merged config entry. |

To ship bytes with a `rule` or an `mcp-server`, ship them as a separate artifact, or materialize with `harness: none`, which writes the canonical layout including bundled files.

A skill's prose finds its files at the package-relative paths it names, because they land inside the skill folder. The adapters copy `agent` prose verbatim and do not rewrite the paths in it, so a prose reference to a bundled file names the installed location when the agent needs to open the file. [Your first agent](your-first-agent#step-4-ship-a-helper-script) shows the pattern. A hook's `hook_action` is the one field the adapters rewrite to the installed location. [Configure your harness](../consuming/configure-your-harness) lists the locations for each harness, and [Handling artifact responses](../consuming/handling-artifact-responses#inline-content-and-materialized-files) describes how the bytes reach the consumer.

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
