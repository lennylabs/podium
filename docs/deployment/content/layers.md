---
title: Layered composition
nav_order: 2
description: How a registry composes one catalog from several independent layers, in an explicit precedence order, and how to read the composed result.
---

# Layered composition

A catalog usually draws on several sources. An organization keeps shared defaults in one repository, a team keeps its own artifacts in another, and an individual keeps work in progress on their own machine. Layered composition merges those sources into one view per caller, in an order the deployment states explicitly.

This page explains how layers combine: precedence, collisions, the workspace overlay, and the no-inheritance rule. [Concepts → Layer](../../getting-started/concepts#layer) defines the model. [Connecting layer sources](layer-sources) covers connecting Git repositories and local paths as layers and keeping them current.

---

## Precedence

Layers form an ordered list. From lowest precedence to highest, the list holds:

1. **Admin-defined layers**, in the order the deployment's layer list gives them.
2. **User-defined layers**, which are personal layers an authenticated user registers for themselves. The default cap is 3 per identity, and it is configurable per tenant.
3. **The workspace local overlay** at `<workspace>/.podium/overlay/`, which is always highest.

Layer order is declared and never inferred. There is no fixed `org / team / user` hierarchy, and the ordering is whatever the layer list says. The same layer list and the same identity always produce the same view.

Every deployment mode applies the same rules. The deployment modes differ in where the ordered list lives and whether visibility filters it.

| Deployment mode | Where the layer list lives | Visibility filtering |
|:--|:--|:--|
| [Local](../modes/local) | `layer_order:` in `<registry-path>/.registry-config`, defaulting to alphabetical order by subdirectory name | None. Every layer composes. |
| [Single node](../modes/single-node) | `registry.layers:` in `~/.podium/registry.yaml`, plus layers registered at runtime | Applied when an identity provider is configured |
| [Clustered](../modes/clustered) | The tenant's layer config | Always applied |

---

## Collisions

Two registry-side layers that hold the same artifact ID collide, and Podium rejects the collision rather than resolving it silently.

- A server rejects the second contribution at ingest with `ingest.collision`. The rejection names the artifact and directs the author to rename it or to remove the other copy.
- `podium lint` reports the same collision on a local catalog.
- `podium sync` against a local catalog drops the higher-precedence copy, names it on standard error, materializes the rest, and exits 1. It therefore materializes the copy that a server started on the same directory serves.

Replacing an artifact that another layer contributes takes two explicit steps. First remove the lower-precedence copy or rename the higher-precedence one, and then ingest the replacement.

The workspace local overlay is the exception. It is merged on the client and replaces a registry-side artifact that carries the same ID, as the next section describes.

---

## Workspace local overlay

The workspace local overlay (`<workspace>/.podium/overlay/`) holds a developer's own artifacts on top of the registry's layers. An overlay artifact replaces the registry-side artifact with the same ID in `podium-mcp`, in `podium sync`, and in the SDKs, and no consumer reports the replacement.

- The consumer applies no ingest rule to an overlay artifact and serves it as authored.
- The overlay is per-developer and is never ingested or shared, so a replacement affects only the developer who wrote it.
- Copying an overlay artifact into a shared layer subjects it to the collision rule and to the [no-inheritance rule](#inheritance).

[Edge cases](#edge-cases) covers an overlay artifact that carries an `extends` key or a removed type and what `podium lint` reports for the overlay.

---

## Inheritance

No artifact inherits frontmatter, prose, or resources from another artifact.

Ingest rejects an `ARTIFACT.md` whose frontmatter carries a top-level `extends` key, whatever its value:

- The refusal is `ingest.invalid_artifact` with `details.constraint: "extends_removed"`.
- The refusal runs before lint, so it reports the artifact whatever lint rule the artifact also fails.
- A filesystem-source `podium sync` drops such an artifact and exits 1, and `podium lint` reports it as an error.
- An `extends` key in a skill's `SKILL.md` is an ordinary key and is not refused.

A refused artifact is not stored, which has two consequences:

- Another artifact of the same reingest whose prose links to the refused artifact fails `lint.prose_reference`.
- A refused artifact collides with no other artifact. The other copy of its ID is materialized or ingested, and no `ingest.collision` line is printed for it.

Release 0.6.0 removed `extends:`. A store that an earlier release wrote can still hold rows ingested from an `ARTIFACT.md` that declared it, and the registry no longer serves those rows. [Upgrade notes](../operations/upgrade-notes#what-readers-see) describes what a reader sees and how to recover.

---

## Removed types

`command` and `context` are removed artifact types. Ingest rejects an `ARTIFACT.md` whose `type` is either of them:

- The refusal is `ingest.invalid_artifact` with `details.constraint: "type_removed"`.
- The refusal runs before lint and after the no-inheritance check, so an `ARTIFACT.md` that declares both `extends:` and a removed type reports `extends_removed`.
- A filesystem-source `podium sync` drops such an artifact and exits 1, and `podium lint` reports it as an error.

A refused artifact is not stored, with the same consequences as an artifact the no-inheritance rule refuses.

Release 0.7.0 removed both types. A store that an earlier release wrote can still hold rows of either type, and the registry no longer serves those rows. `latest` resolves to the newest other served version of the ID, and an ID with no other served row answers `registry.not_found`. [Artifact types](../../authoring/artifact-types#removed-types) names the replacements, and [Upgrade notes](../operations/upgrade-notes#command-and-context-removal) describes the conversion and the recovery steps.

---

## Unparseable manifests

Ingest refuses an artifact whose `ARTIFACT.md` or `SKILL.md` frontmatter does not parse. Frontmatter does not parse when the file has none, when it is not valid YAML, when its root is not a mapping, or when a field holds a value of a kind its definition does not allow, such as a `SKILL.md` `name` or `description` that is a mapping. A sequence or a mapping under a `SKILL.md` `metadata` key does not make the frontmatter unparseable, as [Values Podium ignores or keeps](../../authoring/frontmatter-reference#values-podium-ignores-or-keeps) describes. An artifact whose `ARTIFACT.md` declares `type: skill` and whose directory holds no `SKILL.md` is refused under the same rule, and its reason names the missing `SKILL.md`.

- The refusal is `ingest.invalid_artifact` with `details.constraint: "manifest_unparseable"`. Its reason names the file and the parse error.
- The refusal runs before every other per-artifact rule, including the no-inheritance and removed-type checks, because neither can be evaluated without the parsed frontmatter.
- The refusal covers that artifact alone. Ingest stores the other artifacts of the layer, `podium lint` lints them, and a filesystem-source `podium sync` writes them.
- A filesystem-source `podium sync` drops such an artifact before the collision rule, names it on standard error, and exits 1. `podium lint` lists it among its refused artifacts and exits 1.
- By default, a filesystem-source `podium sync` keeps the files and lock entry an earlier run wrote for the refused artifact and notes that on its standard-error line, which matches a server that keeps serving the earlier version. It keeps them only when the earlier run used the same harness, the artifact passes the run's scope and toggles, and no other artifact of the run takes them over, and otherwise removes them. An artifact takes them over when it writes a file the kept output holds whole, or a configuration-merge entry under the same key, such as an `mcpServers` server of the same name. Another artifact's entry in a shared file such as `.mcp.json` does not end the keep. `--on-unparseable remove`, `PODIUM_SYNC_ON_UNPARSEABLE`, or `defaults.on_unparseable` in `sync.yaml` removes them instead, as [Previous output of an unparseable artifact](../../reference/cli#previous-output-of-an-unparseable-artifact) describes.
- A row already stored is not parsed again under this rule.

A refused artifact is not stored, with the same consequences as an artifact the no-inheritance rule refuses.

A directory that cannot hold an artifact at all still fails the whole layer. A manifest at the layer root has no canonical ID, so a layer whose root directory holds an `ARTIFACT.md` or a `SKILL.md` fails to ingest, and `podium lint` and `podium sync` fail on it.

---

## Composing from a directory

On the Local deployment mode, each subdirectory of the registry path is a `local`-source layer, and the layer ID defaults to the subdirectory name. `.registry-config` states the order:

```yaml
# ~/podium-artifacts/.registry-config
multi_layer: true        # required; opts the directory into filesystem-registry mode
layer_order:             # optional; lowest-precedence first
  - team-shared
  - personal
```

[Local → Directory layout](../modes/local#directory-layout) covers the full directory layout.

The same directory serves a single-node deployment unchanged. `podium serve --standalone --layer-path ~/podium-artifacts/` ingests each subdirectory as a layer and keeps the same order.

---

## Registering layers against a server

A server-backed deployment registers each layer with its own source. Declare layers in `registry.yaml`, or register them at runtime with `podium layer register`. [Connecting layer sources](layer-sources) covers each task:

- [Git repositories](layer-sources#git-repositories), declared or registered at runtime.
- [Private repositories](layer-sources#private-repositories) and their credentials.
- [Keeping layers current](layer-sources#keeping-layers-current) with webhooks, polling, or a manual reingest.
- [Local paths](layer-sources#local-paths) on the registry host.
- [Agent Skills repositories](layer-sources#agent-skills-repositories) registered without an `ARTIFACT.md` in any skill.

Authoring rights stay with the source. Whoever can merge to a layer's tracked Git ref, or write to a `local` layer's path, publishes there. [Registration rights](layer-sources#registration-rights) states who may register a layer and who may name a host path.

---

## Layer trust policy

Each layer carries a trust policy that states which signers' author attestations its content needs. Under `warn` and `require`, every ingest cycle of the layer verifies the bundles in its attestation directory against the policy, as [Ingest verification](#ingest-verification) below describes.

The policy has three members, which the layer object reports and `podium layer show <id>` prints:

| Member | Meaning |
|:--|:--|
| `trust_mode` | `none`, `warn`, or `require`. An empty value takes `PODIUM_LAYER_TRUST_DEFAULT`, which defaults to `none`. |
| `trust_requirements` | A list that holds at most one requirement. A requirement has `signers` and an optional `predicate_type`. Several requirements are not available yet. |
| `trust_layer_root` | For a `local` layer, the `root` under which its content was signed. A `git` layer takes it from its `root`. |

A signer has an OIDC `issuer` and a certificate `identity`, which are compared byte for byte with the signing certificate. It also has source binding members (`source_repository`, `source_repository_owner`, `source_repository_id`, and `source_repository_owner_id`, or `any_source: true`), a ref binding (`source_repository_ref`, or `any_ref: true`), and an optional `instance`, which names a Sigstore instance of `sigstore.instances` and defaults to `public-good`.

- A signer whose identity is a URI, such as a workflow identity, names a source binding or `any_source`. A shared workflow's identity is issued to every repository that calls it, and the binding names the repository whose content counts.
- A signer with a source binding also names `source_repository_ref` or `any_ref`. A caller on another branch of the bound repository obtains the same workflow identity, and the ref binding refuses it.
- The identifier members hold the host's numeric identifiers. A repository or owner name can be registered again by another party after a rename or a deletion, and an identifier is not assigned twice, so the identifier members are the stronger binding.

A layer declared in `registry.yaml` carries the policy in the keys of the same names:

```yaml
registry:
  layers:
    - id: finance
      source:
        git:
          repo: https://github.com/acme/finance
          ref: podium/signed
      trust_mode: require
      trust_requirements:
        - signers:
            - issuer: https://token.actions.githubusercontent.com
              identity: https://github.com/acme/.github/.github/workflows/podium-attest.yml@refs/heads/main
              source_repository_id: "123456"
              source_repository_owner_id: "7890"
              source_repository_ref: refs/heads/main
```

A layer registered at runtime takes the policy from the [trust policy flags](../../reference/cli#trust-policy-flags) of `podium layer register` and `podium layer update`, or from the `trust_mode`, `trust_requirements`, and `trust_layer_root` members of the [layer endpoints](../../reference/http-api#layer-management).

### Checking bundles against the policy

`podium layer show <id> --requirements` writes a registered layer's requirements, with the binding members the registry derived, as a requirements file. `podium attest check --requirements` reads the file and verifies each bundle in the layer's attestation directory against those signers, so a signing workflow or a pull-request check finds a bundle the policy would refuse before the content reaches the registry:

```
podium layer show finance --requirements > requirements.json
podium attest check --root skills --requirements requirements.json
```

[`podium attest import`](../../reference/cli#podium-attest-import) writes signed bundles into the attestation directory, and [`podium attest check`](../../reference/cli#podium-attest-check) describes the check.

### Ingest verification

A cycle of a layer whose mode resolves to `warn` or `require` reads the bundle files of `.podium/attestations/` in its snapshot and verifies each one against the signers of the requirement. It then checks that the accepted bundles list the layer subject with the layer digest of the snapshot, that the layer subject is not older than the layer's attestation mark, that its attested commit lies in the history of the tracked ref and descends from the commit recorded with the mark (a `git` layer whose signing certificate records the commit), and that every artifact of the snapshot has a subject with its package digest. These checks run before the cycle writes anything.

- Under `require`, a failed check refuses the cycle: nothing is stored or removed, the versions the registry already holds keep serving, and `podium layer reingest` exits 1 with the code, such as `ingest.attestation_missing`, and its `details`. A start whose ingest of a declared `local` or `--layer-path` layer is refused keeps running, and the start log names the layer and the code.
- Under `warn`, a failed check is recorded and the cycle stores the snapshot.
- A layer whose policy has no requirement under a mode above `none`, or names an instance the registry no longer configures, is refused with `ingest.attestation_policy_invalid` on every cycle until the policy is updated.

Each such cycle writes the layer's attestation record, which the layer object reports and `podium layer show` prints: `attestation_mark`, `attested_commit`, `attestation_status` (`ok` or the code of the failure), `attestation_details`, `attestation_dropped`, and `attestation_checked_at`. When the status is not `ok`, `podium layer show` writes the next step for the code on standard error:

```
podium layer show finance
```

The mark only moves forward, so a replay of an older signed state is refused as `ingest.attestation_stale`. A legitimate rollback is a new commit that the signing workflow attests. To accept an older signed state, or after a rewrite of the signed branch, reset the mark with the reingest that applies it:

```
podium layer reingest finance --reset-attestation-mark
podium layer reingest finance --reset-attestation-mark --to-commit <sha>
```

`--to-commit` names the commit the next accepted layer subject must equal or descend from, and applies to a `git` layer. The reset needs the same authority as the reingest. The [error codes](../../reference/error-codes#attestation-codes) list each code and constraint.

A layer that `--layer-path` bootstraps carries no signers of its own. Under a `PODIUM_LAYER_TRUST_DEFAULT` of `warn` or `require`, set [`PODIUM_LAYER_TRUST_REQUIREMENTS_FILE`](../../reference/configuration#layers) to a requirements file, such as the output of `podium layer show <id> --requirements`, whose `layer_roots` object maps a layer ID to its `trust_layer_root`.

### Attestation links and outcomes

A cycle that verifies a layer stores each accepted bundle once and links it to the versions whose subject it lists, one link per predicate type and signer, where the signer is the Sigstore instance under which the bundle verified and the certificate's issuer and identity. A version holds at most 16 links. An unchanged artifact keeps its link across signing runs, so a consumer fetches no new bundle for it after each merge. A layer under `none` whose snapshot holds `.podium/attestations/` links each bundle whose signature, certificate chain, and time verify under a configured instance as `unverified`, so a consumer can verify the bundles under its own policy.

Each version also carries an attestation outcome: `verified` when the cycle that evaluated it found every requirement met, `failed` with the code, the scope, the constraint, and the requirement when it did not, and `not_evaluated` for a version of a layer under `none` and for a version stored before the registry kept outcomes. `load_artifact`, each `ok` item of a bulk load, and `podium artifact show` report the links and the outcome:

```
podium artifact show finance/ap/pay-invoice
```

The default output writes one `attestation_outcome:` line and one `attestation:` line per link on standard error. The [HTTP API](../../reference/http-api#load_artifact) describes the members, and `GET /v1/attestations/{digest}` serves a linked bundle to a caller who can see a version that links it.

A link records the policy under which the registry verified it. When the trust policy of a layer changes through `podium layer update` or a re-registration, at each registry start, which covers a changed `registry.yaml` declaration, `PODIUM_LAYER_TRUST_DEFAULT`, or trusted root, and at the start of each ingest cycle, the registry re-evaluates the links that were verified under another policy: each bundle a requirement still accepts keeps its link, and a link whose bundle no requirement accepts becomes `unverified`, its version's outcome becomes `failed` with `ingest.attestation_missing` and the constraint `link_downgraded`, and the registry records `layer.attestation_links_downgraded`. A change to `none` changes every verified link to `unverified` and every outcome to `not_evaluated`. A policy change through the layer endpoints also starts an ingest cycle of the layer, and a cycle that was already running under the earlier policy stores its content but writes no link and no outcome. A re-evaluation changes an outcome only when it downgrades a link, so a change that adds a requirement, or that moves the only accepting signer to another signer of the requirement, leaves a stored version `verified` until a cycle evaluates it again; a version no longer in the tree keeps its last outcome. A change of policy refuses no stored version: a version with a downgraded link keeps serving, and the next cycle that passes the layer step links the versions still in the tree under the new policy. While a layer's policy is invalid, such as one that names an instance the registry no longer configures, its `verified` versions are served as `failed` with `ingest.attestation_policy_invalid`, and the stored outcome is served again once the policy is repaired.

Stored bundles count toward the tenant's storage quota. A purge of a version deletes its links, and a bundle that no version links is deleted with it.

### Visibility of a `--layer-path` layer

A layer that `podium serve --layer-path` or `PODIUM_LAYER_PATH` loads takes its visibility from the `.layer-config` file of its directory, or from `PODIUM_DEFAULT_LAYER_VISIBILITY` when the file is absent or its `visibility:` block is empty. Under the trust mode `none`, each start applies the file and an ingest cycle applies nothing. Under `warn` and `require`, the registry treats the two directions of a change differently, so a party who can write the directory cannot widen the layer's visibility without a signature:

- A file that admits fewer callers applies at once: at the next start, and in every cycle, before the cycle stores an artifact. The registry applies the meet of the stored visibility and the file, which admits only callers both admit. A file that replaces one group with another therefore admits no caller until a signed state applies it.
- A file that admits more callers waits for a cycle that passes the layer step, which applies the signed file after it has stored its artifacts and applied its removals. Until then the layer object carries the file's visibility in `visibility_pending`, and `podium layer show` prints it beside the visibility in force.
- A file that does not parse, or that holds an empty or control-character `groups` or `users` entry, refuses a start with `config.invalid`. A later cycle reads such a file as a visibility that admits no caller.
- A layer whose row a start creates, such as on a first start or after the directory is renamed, is visible to no caller that the deployment default does not admit. A deployment that restricts layers through `.layer-config` alone keeps `PODIUM_DEFAULT_LAYER_VISIBILITY` at `private`.
- `visibility_source` records whether the visibility was last set from a declaration or from the deployment default. A change of the default applies at the next start, in either direction, to a layer whose source is `default`.
- `podium layer update` with a visibility flag sets the visibility and the source `declared`, and the next start or cycle narrows the update back to the file. To widen a layer whose cycles do not pass the layer step yet, such as one under `warn` before its signing is set up, edit `.layer-config` to the wider visibility and then run the update with the same value.
- A narrowing made through `podium layer update` lasts until the next cycle that passes the layer step, which applies the signed `.layer-config` again. That cycle can be the startup ingest of a restart over the same signed state, and the update prints a warning that says so. A lasting narrowing is made in `.layer-config`: an unsigned file that admits fewer callers applies at once and every later cycle keeps it, until a signed state replaces it.
- A layer whose source is `default` takes the deployment default as its baseline, because the last signed state declared no visibility. An unsigned file that narrows it applies, and removing that file returns the layer to the default without a signature. No unsigned file takes the layer beyond the default.
- `podium layer reorder`, and a `podium layer update` that sets no visibility flag, keep the stored visibility, so a narrowing a cycle writes while either runs stays in force. A start changes the visibility only while it holds the values the start read, so a narrowing that a cycle of another replica writes during the start is read again and kept.

Each change records `layer.config_changed` with the cause `start` or `cycle` and the earlier and new values. A registry in public mode, or one with no identity provider, shows every layer to every caller, so these rules change nothing a caller observes there.

### Derived source binding

A workflow in the layer's own repository needs no explicit binding. When a signer of a `git` layer has a URI identity that begins with the layer's repository URL followed by `/.github/workflows/`, and names no source binding and no `any_source`, the registry sets `source_repository` to the repository URL. When the signer also names no ref binding, the registry sets `source_repository_ref` to the part of the identity after its last `@`. The stored policy holds the derived members, and `podium layer show` prints them.

The registry derives the binding only from a repository URL of the form `https://<host>/<owner>/<repo>`, optionally followed by `.git` or `/` but not both, with no user information, port or empty port, query, fragment, or percent-escape, and with a host that does not end with a dot. The host is a sequence of dot-separated labels, each of ASCII letters, digits, and `-` that neither begins nor ends with `-`. The owner and repository names hold only ASCII letters, digits, `.`, `_`, and `-`, and neither is `.` or `..`. The `.git` suffix is matched in lower case alone, so a URL ending in `.GIT` derives nothing. An SSH URL or an scp-like address derives nothing, so a signer of such a layer names its binding. A URL whose letter case differs from the repository's name on the host derives a binding that matches no certificate.

### Policy rules

A registration or an update whose policy breaks one of these rules is refused with `registry.invalid_argument`, and a declared layer whose policy breaks one refuses the registry start with `config.invalid_layer_trust`:

- A `trust_mode` of `warn` or `require` needs a requirement, and a requirement needs at least one signer. At the layer endpoints an empty `trust_mode` counts as `PODIUM_LAYER_TRUST_DEFAULT`, so while the default is `warn` or `require`, a registration or an update that leaves a layer with no mode and no requirement is refused. A declared layer in that state starts, and the start log names it.
- A policy holds at most one requirement.
- A signer names an `issuer` and an `identity`, and the binding rules above hold after the derivation.
- A signer that sets `any_source` names no source binding member, and one that sets `any_ref` names no `source_repository_ref`.
- A signer's `instance` is one the registry configures.
- A `git` layer sets no `trust_layer_root`. A `trust_layer_root` neither begins nor ends with `/` and holds no empty, `.`, or `..` segment, and is valid UTF-8 with no white space, control character, or backslash.
- A requirement or a signer holds no member of another name, including one reached through a YAML alias.
- A request body or a `registry.yaml` layer entry holds no member that reads as a trust member and is none, such as `trust_mod` or `trust-mode`: a member whose name, with letters folded to lower case and each `-` read as `_`, begins with `trust`. In a layer entry this includes a key taken through a YAML merge key (`<<`), and an entry whose merge keys nest more than 16 deep is refused. A request body matches `trust_mode`, `trust_requirements`, and `trust_layer_root` without regard to letter case and refuses one carried twice, and `trust_mode` and `trust_layer_root` refuse `null`.
- The layer endpoints refuse a trust change for a layer declared in `registry.yaml`, whose declaration sets its policy at each start. A request that restates the stored policy, such as a layer object returned as read, is admitted. A registration under a declared ID without trust members keeps the declared policy, is checked against its source type, and is refused when it changes the layer's source while the declaration holds a requirement.

An update that changes no trust member does not re-check the stored policy, so a policy whose instance was later removed from `registry.yaml` does not block an unrelated update. A layer whose empty mode `PODIUM_LAYER_TRUST_DEFAULT` resolves to `warn` or `require` and that has no requirement is logged as a warning at each start.

Changing the policy of an admin-defined layer requires the tenant `admin` role, and the owner of a user-defined layer changes its own. A change is recorded on the layer's audit event with the previous and the new policy.

---

## Changing the order

`podium layer reorder <id> [<id> ...]` re-sequences the named layers. The argument order is precedence, lowest to highest:

```bash
podium layer reorder alice-scratch alice-personal
```

- A caller reorders their own user-defined layers without special rights.
- An argument list that names an admin-defined layer requires the tenant `admin` role, and the registry answers `auth.forbidden` otherwise.
- A deployment with no identity provider has no authenticated callers, so the local operator reorders any layer.
- A deployment in public mode reorders a layer only for a request that carries `PODIUM_OPERATOR_TOKEN`, as [Public mode](../modes/single-node#public-mode) describes.

A layer declared in the `registry.layers` list of `registry.yaml` is re-seeded from that file in list order at every restart, so a reorder of it reverts. Change its position in `registry.yaml` to make a new order durable.

---

## Reading the composed result

`podium sync --dry-run` prints the artifact set the current identity would materialize, without writing anything. `podium sync --preview` prints the aggregate counts instead.

On a server-backed deployment, `podium admin show-effective [--group <g>]... <user-id>` reports the per-layer result for any identity. It answers why a given artifact did or did not appear. See [Access control → Debugging an effective view](access-control#debugging-an-effective-view).

---

## Edge cases

- **Overlay artifact with `extends`.** An overlay `ARTIFACT.md` that carries an `extends` key is served as authored, and the key has no effect.
- **Overlay artifact of a removed type.** An overlay `ARTIFACT.md` whose `type` is `command` or `context` is served as authored. The `none` adapter writes the canonical layout, the `claude-code` adapter writes it and its bundled resources under `.claude/podium/<artifact-id>/`, and every other adapter writes nothing for it.
- **Lint and the overlay.** `podium lint` reports an artifact that the unparseable-manifest rule, the no-inheritance rule, or the removed-type rule refuses. `podium lint` contacts no registry, so it does not report a collision with an artifact that only a registry holds.
- **Reorder by a caller whose credential fails verification.** Such a caller is refused with `auth.token_expired`, `auth.untrusted_token`, or `auth.untrusted_runtime` before either authorization arm is evaluated. On this operation, `auth.forbidden` therefore names a caller the registry verified and did not authorize. The other layer write operations answer such a caller `auth.forbidden`.

---
