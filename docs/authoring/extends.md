---
title: Extends
nav_order: 9
description: Cross-layer artifact inheritance via the extends field.
---

# Extends

When two layers contribute artifacts with the same canonical ID, ingest rejects the collision by default. The higher-precedence artifact can declare `extends:` in its frontmatter to inherit from the lower-precedence one and refine specific fields instead of forking.

```markdown
---
type: skill
name: pay-invoice
version: 2.0.0
extends: finance/ap/pay-invoice@1.x
description: Pay an approved invoice, with team-specific routing.
mcpServers:
  - name: finance-warehouse
    transport: stdio
    command: npx
    args: ["-y", "@team-foo/finance-warehouse-mcp"]
---

Team-specific addendum on top of the org-wide skill...
```

At request time the registry folds the parent's frontmatter into the child's per the field-specific rules below. The child's prose body replaces the parent's; bodies are never concatenated.

---

## Pinning

`extends:` accepts the same pin syntax as other artifact references:

| Syntax | Meaning |
|:--|:--|
| `<id>` | Resolves to `latest` at the child's ingest time, then pins. |
| `<id>@<semver>` | Exact version. |
| `<id>@<semver>.x` | Minor or patch range (e.g., `1.2.x`, `1.x`). |
| `<id>@sha256:<hash>` | Content-pinned. |

Parent version is resolved at the child's ingest time and stored as a hard pin in the ingested manifest's resolved form. Parent updates do not silently propagate. Re-ingesting the child's unchanged bytes is counted idempotent and changes nothing, so the child picks up a newer parent only when it is published at a new `version:`.

An unpinned `<id>` reference resolves to the most recently ingested non-deprecated version of the parent, which is the version with the latest ingest timestamp rather than the highest semver.

### Deprecated parent versions

A child may not extend a deprecated parent version.

- An `extends:` reference that names a deprecated version explicitly, by exact semver (`<id>@1.2.0`) or by content hash (`<id>@sha256:<hash>`), is rejected at ingest with `ingest.invalid_artifact`.
- A range (`<id>@1.x`) or unpinned (`<id>`) reference selects among the parent's non-deprecated versions. Deprecated versions are removed from the candidate set before resolution, so `latest` for such a reference is the most recently ingested non-deprecated version.
- When the parent has stored versions and the filter leaves no candidate, ingest rejects the child with `ingest.invalid_artifact` and reports that the parent's candidate versions are deprecated. The reference does not fall back to the deprecated versions.

Deprecation is per-version and a stored version's flag never changes. Deprecating a parent line means publishing a new parent version that carries `deprecated: true`, which adds a candidate the filter skips. A child that is already stored keeps the parent pin recorded at its own ingest, and a later deprecated parent version leaves that stored pin and its resolution on the read path unchanged.

The refusal therefore applies to the child version being ingested, and it never invalidates a child already stored against a parent version that was live when the child was ingested. A child runs the check again when it is published at a new `version:`. At that point a range or unpinned reference re-pins onto a non-deprecated candidate, and an exact or content-hash pin naming a deprecated version is refused. A pin that names a version already deprecated when the child is first ingested is rejected on that ingest and on every retry until the `extends:` reference names a live version.

---

## Field merge semantics

When the child declares `extends:`, fields merge per the table below. Author-specified fields on the child override or combine with the parent's per the rule.

![Inheritance with extends: a parent manifest in a lower-precedence layer, a child manifest in a higher-precedence layer declaring extends:, and the merged result showing which fields came from each.](../assets/diagrams/extends-inheritance.svg)

<!--
ASCII fallback for the diagram above (inheritance with extends:):

  Manifest fields:

  parent (lower precedence)         child (higher precedence)         merged manifest
  finance/ap/pay-invoice@1.2.x        finance/ap/pay-invoice (overlay)  what the caller sees
  type: skill                       extends: finance/ap/...@1.2.x       type: skill           (parent)
  version: 1.2.0                    version: 1.3.0                    version: 1.3.0        (child)
  description: Pay an invoice.      description: Pay an invoice (EU). description: ...(EU). (child)
  tags: [finance, ap]               tags: [eu, vat]                   tags: [finance, ap,   (union)
  sensitivity: medium               requiresApproval: vat-lookup            eu, vat]
  requiresApproval: payment-submit                                    sensitivity: medium   (parent)
                                                                      requiresApproval:     (union)
                                                                        payment-submit, vat-lookup

  Bundled files (scripts/, references/, assets/, etc.):

  parent files                      child files                       materialized file tree
  ARTIFACT.md                       ARTIFACT.md                       ARTIFACT.md            (merged manifest)
  SKILL.md                          SKILL.md                          SKILL.md               (child)
  scripts/validate.py               scripts/submit.py                 scripts/submit.py      (child)
  scripts/submit.py                 scripts/vat-lookup.py             scripts/vat-lookup.py  (child)
  references/process.md             assets/eu-template.j2             assets/eu-template.j2  (child)

  Manifest scalars (version, description) take the child's value;
  set-typed lists (tags, requiresApproval) union; security-relevant
  fields take the most restrictive value. Bundled files do not merge:
  the materialized package carries the child's own files and the
  parent contributes frontmatter only. Hidden parents merge
  server-side without surfacing the parent ID.
-->


| Field | Merge rule |
|:--|:--|
| `description`, `name`, `release_notes` | Scalar; child wins. |
| `tags` | List; append unique. |
| `when_to_use` | List; append. |
| `sensitivity` | Scalar; most-restrictive (high > medium > low). |
| `mcpServers` | List of objects; deep-merge by `name`. |
| `requiresApproval` | List; append. |
| `runtime_requirements` | Map; deep-merge with child wins. |
| `sandbox_profile` | Scalar; the parent's value unless it is unset or `unrestricted`. A child that sets a different restricting profile is rejected; see [Sandbox profile](#sandbox-profile). |
| `delegates_to` | List; append. |
| `external_resources` | List; append. |
| `license` | Scalar; child wins (lint warning if changed across layers). |
| `search_visibility` | Scalar; most-restrictive (`direct-only` > `indexed`). |

**Omitted fields.** If a child omits a frontmatter field, or sets an empty scalar, the parent's value is inherited unchanged. This holds for every frontmatter field, including the fields in the table above. When both the parent and the child declare a value, a field in the table merges per its row, and every other field takes the child's value: `deprecated`, `replaced_by`, `effort_hint`, `model_class_hint`, `sbom`, `rule_mode`, `rule_globs`, `rule_description`, `hook_event`, `hook_action`, `server_identifier`, `target_harnesses`, `input`, `output`, and any extension-type fields. The child's `type:` must match the parent's; ingest rejects an `extends:` chain that crosses types. The child's `version:` is independent; each artifact has its own version, and the parent version is pinned at the child's ingest time. A child whose parent comes from another layer restates the executable fields that parent sets; see [Executable fields across layers](#executable-fields-across-layers).

Extension types register their own merge semantics via `TypeProvider`.

The most-restrictive rules for `sensitivity` and `search_visibility`, and the [sandbox-profile rule](#sandbox-profile), keep a child from loosening a security-relevant field. A parent at `sensitivity: medium` cannot be relaxed to `low`, and a parent with `sandbox_profile: read-only-fs` cannot be widened to `unrestricted`.

---

## Executable fields across layers

A child whose parent comes from another layer restates every executable field that parent sets. An executable field carries something a harness runs, or something an adapter can write verbatim into a harness-native file, where the registry cannot determine whether the harness acts on it.

**The executable fields.** The executable fields are `hook_action`, `server_identifier`, `mcpServers`, and every top-level `ARTIFACT.md` key that the [ARTIFACT.md section of the frontmatter schema](../reference/frontmatter-schema#artifactmd) does not list. That section lists `source`, `audit_redact`, and `lint_suppress`, so they are outside the set. It also lists `name`, `description`, and `license`, so they are outside the set even in a skill's `ARTIFACT.md`. The set includes custom metadata keys, every extension-type field whatever its `TypeProvider` declares, and a key that only `SKILL.md` or `DOMAIN.md` defines when it is written into `ARTIFACT.md`, such as `allowed-tools`, `compatibility`, `metadata`, or `include`.

**Restating a field.** When the parent version the child pins comes from another layer, the child restates each executable field the parent sets. The parent sets a field when the field carries a value in the parent's resolved manifest, so a value the parent inherited from its own `extends:` chain counts as one it sets. The child restates a field under these conditions:

- `hook_action` or `server_identifier`: the child's own frontmatter sets the key to a non-empty value.
- `mcpServers`: for each entry name in the parent's list, the child's list carries an entry under that name, and that entry sets each of `transport`, `command`, and `args` that the parent's entry sets and each `env` key that the parent's entry sets.
- An undeclared key: the child's own frontmatter sets the key to a value other than an empty scalar. An empty list (`[]`) or an empty map (`{}`) restates the key, and an empty string does not.

The restated value may equal the parent's or differ from it. A child cannot remove a parent's `mcpServers` entry, `env` key, or undeclared key, so it restates each one. A child that sets `mcpServers: []` restates no entry, because the merge keeps every parent entry.

**Which edges the rule tests.** The rule compares the child's layer with the layer that contributes the parent version the child pins. A child and a parent in the same layer are unaffected, and the child inherits every field as before. A same-ID overlay crosses layers when its `extends:` reference selects the copy a lower layer contributes, so the overlay restates each executable field that copy sets. When a registry server resolves the reference to an earlier version of the overlay stored from the overlay's own layer, the edge lies within that layer and the server accepts the overlay without a restatement. `podium sync` against a filesystem registry always tests the edge to the next-lower layer's copy, so it drops the same overlay.

**Fields outside the rule.** `hook_event`, `runtime_requirements`, `sandbox_profile`, `external_resources`, and the remaining fields of the merge table follow the merge rules above. `hook_event` selects when an action runs and supplies nothing to run. `runtime_requirements` is an input to the host capability check. `sandbox_profile` constrains execution, and the [sandbox-profile rule](#sandbox-profile) governs its merge. `external_resources` holds pointer records whose bytes the registry neither stores nor serves.

**Rejection.** Ingest rejects a child that leaves an executable field to a parent in another layer with `ingest.invalid_artifact`. The rejection carries a `details` object with `constraint: "executable_inheritance"` and `fields`, which lists each field to restate as `hook_action`, `server_identifier`, `mcpServers[<name>]`, `mcpServers[<name>].transport`, `mcpServers[<name>].command`, `mcpServers[<name>].args`, `mcpServers[<name>].env.<KEY>`, or the bare name of an undeclared key. The reason lists the same fields. Neither the reason nor `details` names the parent's canonical ID, version, or layer. The [reingest endpoint](../reference/http-api#reingest) describes the wire form, and [Error codes](../reference/error-codes) describes the code.

When a field the child would have to restate is named with the ID of an artifact in its parent chain, as an `mcpServers` entry name, an `env` key, or an undeclared key, ingest rejects the child with `ingest.invalid_artifact` and no `details`, and the reason names no field and no parent. A registry server and `podium sync` against a filesystem registry both give the reason `extends: executable fields could not be read`. Such a child would fail every read, because every read of it withholds the IDs in its parent chain.

**Stored versions and filesystem registries.** The rule applies to the version being ingested and never invalidates a version already stored. A stored version keeps its pin and keeps serving, and a reingest that presents it with unchanged bytes classifies it unchanged. A child in the same layer as such a stored version keeps inheriting the executable values that version inherited from another layer, because the child's own edge lies within one layer. `podium sync` against a filesystem registry keeps no ingest state, so it applies the rule on every run and drops each child the rule refuses.

**Hooks.** A registry server already refuses an authored `type: hook` artifact that omits `hook_action`, through lint, before this rule runs. A same-ID overlay of a hook from a lower layer restates both hook fields:

```yaml
# layers/team-foo/ops/session-audit/ARTIFACT.md
---
type: hook
version: 2.0.0
extends: ops/session-audit@1.x
hook_event: session_end
hook_action: |
  INPUT=$(cat)
  echo "$INPUT" >> ~/.podium/team-foo-session-audit.log
---
```

Lint requires `hook_event` on a hook artifact, and the executable-field rule requires `hook_action`, because the parent copy comes from another layer.

---

## Sandbox profile

The profiles other than `unrestricted` each restrict a different resource: `read-only-fs` restricts filesystem writes outside the materialization destination, `network-isolated` restricts outbound network access, and `seccomp-strict` restricts system calls. None of them includes another, so `sandbox_profile` has no order among them. The rule below treats every value other than `unrestricted` as a restricting profile, including a value the [caller-interpreted fields](../reference/frontmatter-schema#caller-interpreted-fields) do not list, and treats two values as the same only when they are byte-identical.

**Merge.** The merged `sandbox_profile` is the child's value when the child sets one and the parent's resolved value is unset or `unrestricted`. In every other case it is the parent's resolved value. The parent's resolved value is the one the parent's resolved manifest carries, so a value the parent inherited from its own `extends:` chain counts. A child can tighten a parent that sets no restriction, and a child that omits the field or sets `unrestricted` keeps the restriction its parent sets.

**Refusal.** Ingest rejects a child whose own frontmatter sets a restricting `sandbox_profile` when the parent's resolved value is also a restricting profile and the two values differ. The rule tests every `extends:` edge, whether the parent comes from the child's own layer or from another layer, and it tests a same-ID overlay as well. A child that sets `read-only-fs` under a grandparent's `network-isolated` is rejected when the parent between them omits the field, because the parent inherits `network-isolated`.

The rule rejects `network-isolated` against `seccomp-strict` on purpose. The baseline syscall allowlist Podium ships for `seccomp-strict` blocks the network system calls, but a host can extend that allowlist, so `seccomp-strict` carries no guarantee that it contains `network-isolated`. Any two different restricting profiles therefore conflict.

**Rejection.** The rejection uses `ingest.invalid_artifact` and carries `details: {"constraint": "sandbox_profile_conflict"}` with no other member. Neither the reason nor `details` names either profile value or the parent's canonical ID, version, layer, content hash, or signature. When the registry cannot read the parent's chain to evaluate the rule, it rejects the child as the [executable-field rule](#executable-fields-across-layers) does. A child that both rules refuse is rejected under the sandbox-profile rule. The [reingest endpoint](../reference/http-api#reingest) describes the wire form.

**Remedy.** The reason directs the author to omit `sandbox_profile` from the child. A child that omits the field inherits the parent's profile, and its other fields merge as before.

**Stored versions and filesystem registries.** The rule applies to the version being ingested and never invalidates a version already stored. A stored version whose chain pairs two conflicting profiles keeps its pin, and the registry serves it with the parent's profile. A reingest that presents it with unchanged bytes classifies it unchanged, and a new version of that child that still conflicts is rejected while the stored version keeps serving. `podium sync` against a filesystem registry keeps no ingest state, so it applies the rule on every run, drops each child the rule refuses, and reports the drop as `rejected: <id> (ingest.invalid_artifact): <reason>`.

**Cached copies in `podium-mcp`.** A version stored before the registry applied this rule, in which the child set `network-isolated` or `seccomp-strict` over a parent's `read-only-fs`, or `seccomp-strict` over a parent's `network-isolated`, now serves the parent's profile, and its content hash does not change. A `podium-mcp` whose content cache already holds such a version keeps delivering the profile it cached, because its revalidation compares the content hash. Publishing a new version of the child changes the content hash, which replaces the cached copy only for an `always-revalidate` load of `latest` that reaches the registry. A pinned load, a load under `offline-first`, and the degraded-network fallback keep delivering the cached record. Run `podium cache prune --days 0` against the consumer's cache directory to remove the cached content; under `always-revalidate` and `offline-first`, the next load that reaches the registry fetches the parent's profile. A consumer that runs with `PODIUM_CACHE_MODE=offline-only` never calls the registry, so it keeps the cached record indefinitely, and after a prune it answers every load from that cache with `network.offline_cache_miss`. For such a consumer, clear its cache directory and refill it under `always-revalidate` or `offline-first`. The [`podium cache prune`](../reference/cli#podium-cache-prune) reference describes the command.

**Host enforcement.** The opt-in ingest gate that `PODIUM_ENFORCE_SANDBOX_PROFILE=true` turns on judges the profile the child's own frontmatter sets. A profile the child inherits from its parent is checked when a host loads the artifact.

---

## Bundled files under extends

`extends:` merges frontmatter fields. Bundled files (everything under the artifact root other than the manifest, including `scripts/`, `references/`, `assets/`, schemas, and templates) do not merge:

- The materialized package carries the child's own bundled files. A file the parent ships is not inherited.
- `SKILL.md` follows the same rule. The registry serves the child's authored `SKILL.md` verbatim.
- A child that needs a file the parent ships copies that file into its own package.

The content hash the registry records covers the child's own package: its `ARTIFACT.md`, its `SKILL.md`, and its bundled files. The parent contributes no bytes to it.

---

## Constraints

- **Single inheritance.** `extends:` is a single scalar; no multiple inheritance. To compose from multiple parents, restructure the parents to chain (`A extends B; B extends C`).
- **Cycle detection.** Cycles in the `extends:` graph are detected at ingest time and rejected.
- **Parent reference.** A child may extend a different canonical ID, inheriting from a separate artifact pinned to the version resolved at the child's ingest time. A child may also extend its own canonical ID, which overlays the artifact contributed by the next-lower-precedence layer; that form is what a cross-layer collision requires. In both forms the child's `type:` must match the parent's.
- **Republishing required for parent updates.** Parent version is pinned at the child's ingest time and is fixed for that version of the child. Bumping the parent does not retroactively update the child's resolved manifest, and re-ingesting the child's unchanged bytes is counted idempotent and changes nothing. Publish a new `version:` of the child to pin the newer parent.

---

## Replacing instead of extending

To replace a parent artifact entirely (rather than refine it), the lower-precedence layer must remove the parent first or rename the higher-precedence one. Silent shadowing between registry-side layers is not permitted. Ingest rejects a same-ID collision across layers when neither artifact declares `extends:` on the ID. A filesystem-source `podium sync` drops the higher-precedence copy in the same case and exits 1.

This is the safety property that prevents an upper layer from quietly overwriting an org-wide artifact. Replacement is an explicit two-step operation: remove (or rename) the parent, then ingest the replacement.

---

## Workspace overlay

The workspace local overlay (`<workspace>/.podium/overlay/`) is an exception to the collision rule. An overlay artifact replaces the registry-side artifact with the same ID in the MCP server, in `podium sync`, and in the SDKs, and no consumer reports the replacement. The replacement is the same whether or not the overlay artifact declares `extends:`: a consumer resolves no `extends:` chain for an overlay artifact and serves it as authored. Copying the artifact into a shared layer subjects it to the collision rule and to `extends:` resolution.

---

## Hidden parents

When a child manifest declares `extends: <parent>` and the requesting identity cannot see the layer that contributes the parent, the registry resolves and merges the parent server-side and serves the merged manifest. A team-shared layer can `extends:` an org-internal layer that the team can see, and a contractor with access to the team layer but not the org layer receives the merged result. The guarantee to that contractor has three parts.

**Withheld.** The parent's canonical ID, version, layer, content hash, and signature are not surfaced on any read path to a requester who cannot see the layer that contributes the parent:

- The merged frontmatter names no parent. The checks in the next section refuse a merged block that still carries a reference to a chain parent.
- The search descriptor of a child carries the child's own frontmatter with its `extends:` key removed, and it carries no frontmatter block when that block still names an ancestor in the child's pinned chain, other than a same-ID overlay, or when the chain cannot be resolved.
- `GET /v1/dependents` returns no edge whose parent endpoint the caller cannot see. For an `extends:` edge the endpoint tested is the parent record the child pinned at ingest.
- A failure to resolve the chain on a load reports the requested artifact and never the parent the registry could not reach.
- The `extends_pin` field on the `load_artifact` response is present only when the caller can see the parent record. Its absence reads the same as an artifact that extends nothing.
- The cross-layer collision rejection names the artifact ID and the `extends:` remedy, and it does not name the layer that already contributes the ID.

A parent whose canonical ID equals the requested ID, the same-ID overlay, is exempt from the canonical-ID withholding, because the requester asked for that ID. The overlay is also exempt from the reference test on the load path and on the search descriptor, so a block that names its own canonical ID is served. Its version, layer, content hash, and signature stay withheld.

**Served.** The parent's frontmatter values reach the requester through the merge by design. Every key the parent declares and the child does not override is served in the merged block per the merge table above, including extension-type keys, and list-valued fields carry the parent's entries beside the child's. The [executable-field rule](#executable-fields-across-layers) limits which executable keys a child may leave to a parent in another layer. The parent's description, tags, sensitivity, and search visibility are folded into the child's indexed record at ingest, so parent-authored text is reachable through search on the child. The parent's prose body, bundled resources, and `SKILL.md` are not served.

**Observable.** A requester can determine that a merge occurred. The served `content_hash` covers the child's pre-merge package, so the merged bytes a requester receives do not reproduce it, and that mismatch shows that the artifact extends something. The guarantee covers the parent's identity and its unmerged content. It does not conceal the existence of an inheritance relationship.

The bytes a consumer receives are attested by the delivery record instead of the content hash. The registry serves `delivery_hash`, a digest over the merged record it served, and `delivery_signature`, its signature over that digest, and `podium-mcp`, server-source `podium sync`, and the language SDKs recompute the digest on every load. The [HTTP API reference](../reference/http-api#materialization) describes both fields.

---

## What the registry refuses to serve

The registry enforces the guarantee on the merged manifest itself. It removes the child's `extends:` reference and then checks the assembled block for the parent's canonical ID. When a key or a value in the block stands as a reference to a chain parent, or the block cannot be rewritten at all, the merged manifest is refused. On a registry server the read fails with `registry.invalid_argument` and no manifest is served. In the filesystem-registry mode the same check fails the walk over the layer, so `podium sync` stops on that artifact and nothing materializes.

The check runs on every merged block either mode assembles, whether or not the requester can see the layer that contributes the parent, and the filesystem-registry mode applies it with no requester identity at all. The guarantee is a property of the served bytes rather than of the requester's visibility, so an author whose parent layer is fully visible gets the same refusal.

The check applies to what the child authored and to what it inherited on the same terms, and it covers the extension-type keys the merge restores as well as the fields in the merge table above. It is bounded to a scalar that resolves to a chain parent's ID once the version pin, together with the whitespace and the slashes around it, is removed, so `shared/parent/` matches. A value that merely mentions the parent, such as a description quoting it or a path below it like `shared/parent/CHARTER.md`, resolves to no artifact and is served unchanged. An overlay of the same canonical ID is exempt, because the requester asked for that ID.

The parser resolves YAML merge keys and aliases, so a reference the child supplies through a merge key (`<<: *base`) is operative: ingest pins the parent, and the merged manifest is then refused on every load. The search descriptor for such a child is served with its frontmatter block omitted, because the descriptor path deletes the `extends:` key from the child's own authored block and re-reads it, and the merge key resolves the reference back into the block. The descriptor is held to the parent-ID scan against every ancestor in the child's pinned chain except a same-ID overlay, and it is served without its frontmatter block on a hit or when the chain cannot be resolved. An anchor on the `extends:` value that a sibling key aliases (`extends: &p shared/parent@1.x` with `note: *p`) is refused on the same terms. The registry reassembles the merged block and expands every alias in the values it restores, so such a reference lands in the served block as the parent's canonical ID and fails the check above. Write `extends:` as a literal top-level key. An anchor on the `extends:` value that no other key aliases is inert, because the reassembled block is written from the merged fields and carries neither the reference nor the anchor.

---

## Examples

### Refining a skill with team-specific MCP servers

The org-wide skill points at the org warehouse; the team-shared layer extends it to use a team-specific warehouse instead.

Org-wide layer:

```yaml
# layers/org-defaults/finance/ap/pay-invoice/SKILL.md
---
name: pay-invoice
description: Pay an approved invoice. Use after invoice approval to submit payment to the vendor.
license: MIT
---

Validate the invoice against the warehouse, then submit payment...
```

```yaml
# layers/org-defaults/finance/ap/pay-invoice/ARTIFACT.md
---
type: skill
version: 1.0.0
sensitivity: medium
mcpServers:
  - name: finance-warehouse
    transport: stdio
    command: npx
    args: ["-y", "@company/finance-warehouse-mcp"]
---

<!-- Skill body lives in SKILL.md. -->
```

Team-shared layer:

```yaml
# layers/team-foo/finance/ap/pay-invoice/SKILL.md
---
name: pay-invoice
description: Pay an approved invoice (team Foo). Use after invoice approval to submit payment with the team cost-center code.
license: MIT
---

Team-specific addendum: also tag payments with the team cost-center
code...
```

```yaml
# layers/team-foo/finance/ap/pay-invoice/ARTIFACT.md
---
type: skill
version: 2.0.0
extends: finance/ap/pay-invoice@1.x
mcpServers:
  - name: finance-warehouse
    transport: stdio
    command: npx
    args: ["-y", "@team-foo/finance-warehouse-mcp"]
---

<!-- Skill body lives in SKILL.md. -->
```

Merged result for a caller who can see both layers: child's `description`, `version`, prose body. `mcpServers` deep-merged by `name`, with the child's entry overriding the parent's (because the merge key matches). `sensitivity: medium` carried from the parent (most-restrictive between unset and `medium`).

The team child restates the parent's only `mcpServers` entry in full, so it satisfies the [executable-field rule](#executable-fields-across-layers).

### Tightening sandbox profile

A child can tighten security-relevant fields in `ARTIFACT.md` without redeclaring the rest:

```yaml
# layers/team-foo/platform/deploy-checks/ARTIFACT.md
---
type: skill
version: 2.0.0
extends: platform/deploy-checks@1.x
sandbox_profile: read-only-fs
---

<!-- Skill body lives in SKILL.md. -->
```

The parent's `sandbox_profile: unrestricted` is overridden by the child's `read-only-fs` because the parent's profile is `unrestricted`. The rest of the parent (description, prose body, tags, etc.) is unchanged.

The child ingests because the parent sets no executable field. A parent that sets one requires the child to restate it.

---

## Lint behavior

- Unresolved parent at ingest time: ingest error (`ingest.invalid_artifact`). The registry pins the parent version when the child is ingested, so the parent must already be ingested under the tenant. Ingest the parent's layer first.
- Cycle detected: ingest error.
- Child declaring `extends:` against a parent in a layer the child's layer cannot see: ingest succeeds when the child restates every executable field the parent sets, and the parent resolves at request time per the visibility rules above.
- Child that leaves an executable field to a parent in another layer: ingest error (`ingest.invalid_artifact`). Ingest and `podium sync` evaluate this rule, and `podium lint` does not, because it resolves no `extends:` chain.
- Child whose `sandbox_profile` differs from the profile its parent resolves to, neither `unrestricted`: ingest error (`ingest.invalid_artifact`). Ingest and `podium sync` evaluate this rule, and `podium lint` does not.
- Child declaring `extends:` with a parent type that doesn't match the child's type: ingest error.
- Child declaring `extends:` with an exact or content-hash pin onto a deprecated parent version, or with a range or unpinned reference when every stored version of the parent is deprecated: ingest error (`ingest.invalid_artifact`).

---

## Where to learn more

- [Frontmatter reference](frontmatter-reference) for the field-by-field schema.
