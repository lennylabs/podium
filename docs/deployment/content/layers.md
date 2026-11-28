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

The policy has these members, which the layer object reports and `podium layer show <id>` prints:

| Member | Meaning |
|:--|:--|
| `trust_mode` | `none`, `warn`, or `require`. An empty value takes the stricter of the [tenant trust floor](#tenant-trust-floor)'s mode and `PODIUM_LAYER_TRUST_DEFAULT`, which defaults to `none`. |
| `trust_requirements` | A list of requirements. A requirement has `signers`, an optional `predicate_type`, an optional `covers_layer`, and an optional `optional`. An artifact is verified when it meets every requirement that is not optional. |
| `trust_require_types` | A list of artifact types, such as `hook` and `mcp-server`, whose failure drops the artifact under `warn` as under `require`. |
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

A layer registered at runtime takes the policy from the [trust policy flags](../../reference/cli#trust-policy-flags) of `podium layer register` and `podium layer update`, or from the `trust_mode`, `trust_requirements`, `trust_require_types`, and `trust_layer_root` members of the [layer endpoints](../../reference/http-api#layer-management).

### Several requirements

A layer requires attestations from more than one signer by listing several requirements, such as a build attestation from the signing workflow and a scan attestation from a scanner with an identity of its own. Exactly one requirement sets `covers_layer: true`. Its signers attest the layer subject, and its statements list every artifact of the layer, so a state that no run of that signer produced is refused as a whole. Each other requirement is decided for each artifact, because a second signer can lag behind the first or decline an artifact:

- Under `require`, an artifact that an additional requirement does not cover is dropped and reported with its code and the position of the requirement. An unchanged artifact keeps serving its stored version.
- Under `warn`, such an artifact is stored with the outcome `failed`, which names the requirement.
- A requirement with `optional: true` fails nothing. The registry verifies and links its bundles, so a consumer whose own policy trusts that signer finds them, and an artifact it does not cover is neither dropped nor reported. The covering requirement cannot be optional.

```yaml
      trust_mode: warn
      trust_require_types: [hook, mcp-server]
      trust_requirements:
        - covers_layer: true
          predicate_type: https://slsa.dev/provenance/v1
          signers:
            - issuer: https://token.actions.githubusercontent.com
              identity: https://github.com/acme/.github/.github/workflows/podium-attest.yml@refs/heads/main
              source_repository_id: "123456"
              source_repository_ref: refs/heads/main
        - predicate_type: https://acme.com/scan/v1
          signers:
            - issuer: https://accounts.acme.com
              identity: scanner@acme.com
        - optional: true
          predicate_type: https://acme.com/review/v1
          signers:
            - issuer: https://accounts.acme.com
              identity: review-bot@acme.com
```

A requirement states that one of its signers signed a statement of its predicate type over the artifact's bytes, and nothing about what the statement records. A scanner or a review signer that a requirement lists therefore signs only on a passing result.

A party who serves the tracked ref can remove a second signer's bundle from a signed state. The changed artifacts that need it are then dropped, the unchanged ones keep their versions, and the removals of the signed state apply, which is also the state a lagging second signer produces. `attestation_dropped` and the output of `podium layer reingest` report the dropped artifacts. A layer that must apply a signed state whole or not at all uses the covering requirement alone.

### Required types

`trust_require_types` lets a layer run `warn` while its riskiest types are enforced. An artifact of a listed type that fails a requirement is dropped under `warn` as under `require`. When the layer step fails under `warn`, every artifact of a listed type is withheld: the cycle stores no version of it, reports it with the layer step's code, counts it in `attestation_dropped`, and removes no stored version of a listed type, so an unsigned or replayed state can neither change nor remove a hook. Each entry is a type the registry knows, a policy that lists one needs a requirement, and its `trust_mode` is not `none`. An artifact counts as a listed type when its manifest declares one or when the version the registry serves for its ID is of one, so a snapshot that declares a stored hook an `agent` is held to the hook's rule.

> [!WARNING]
> `trust_require_types` enforces nothing while the layer's mode resolves to `none`, which runs no attestation step. A layer with an empty `trust_mode` takes `PODIUM_LAYER_TRUST_DEFAULT`, so its list applies only while the default is `warn` or `require`. A registration or an update that leaves a layer in that state answers with a warning.

### Checking bundles against the policy

`podium layer show <id> --requirements` writes a registered layer's requirements, with the binding members the registry derived, as a requirements file. `podium attest check --requirements` reads the file and verifies each bundle in the layer's attestation directory against those signers, so a signing workflow or a pull-request check finds a bundle the policy would refuse before the content reaches the registry. The check reads the layer subject and the packages from the covering requirement's bundles, and it also fails for each package that an additional requirement that is not optional does not cover. The same file, edited, updates a policy of several requirements with `podium layer update --trust-requirements-file`:

```
podium layer show finance --requirements > requirements.json
podium attest check --root skills --requirements requirements.json
```

[`podium attest import`](../../reference/cli#podium-attest-import) writes signed bundles into the attestation directory, and [`podium attest check`](../../reference/cli#podium-attest-check) describes the check.

### Ingest verification

A cycle of a layer whose mode resolves to `warn` or `require` reads the bundle files of `.podium/attestations/` in its snapshot and verifies each one against the signers of each requirement. It then checks that the accepted bundles of the covering requirement list the layer subject with the layer digest of the snapshot, that the layer subject is not older than the layer's attestation mark, that its attested commit lies in the history of the tracked ref and descends from the commit recorded with the mark (a `git` layer whose signing certificate records the commit), and that every artifact of the snapshot has a subject with its package digest in those bundles. These checks run before the cycle writes anything. Each artifact is then checked against the additional requirements, as [Several requirements](#several-requirements) above describes.

- Under `require`, a failed check refuses the cycle: nothing is stored or removed, the versions the registry already holds keep serving, and `podium layer reingest` exits 1 with the code, such as `ingest.attestation_missing`, and its `details`. A start whose ingest of a declared `local` or `--layer-path` layer is refused keeps running, and the start log names the layer and the code.
- Under `warn`, a failed check is recorded and the cycle stores the snapshot, apart from the artifacts of the [required types](#required-types).
- A layer whose policy has no requirement under a mode above `none`, names an instance the registry no longer configures, or does not meet the [tenant trust floor](#tenant-trust-floor), is refused with `ingest.attestation_policy_invalid` on every cycle until the policy is updated. The floor applies in every mode, so a layer whose explicit `trust_mode` is `none` below a floor of `warn` is refused as well.

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

Each version also carries an attestation outcome: `verified` when the cycle that evaluated it found every requirement that is not optional met, `failed` with the code, the scope, the constraint, and the requirement when it did not, and `not_evaluated` for a version of a layer under `none` and for a version stored before the registry kept outcomes. `load_artifact`, each `ok` item of a bulk load, and `podium artifact show` report the links and the outcome:

```
podium artifact show finance/ap/pay-invoice
```

The default output writes one `attestation_outcome:` line and one `attestation:` line per link on standard error. The [HTTP API](../../reference/http-api#load_artifact) describes the members, and `GET /v1/attestations/{digest}` serves a linked bundle to a caller who can see a version that links it.

A link records the policy under which the registry verified it. When the trust policy of a layer changes through `podium layer update` or a re-registration, at each registry start, which covers a changed `registry.yaml` declaration, `PODIUM_LAYER_TRUST_DEFAULT`, or trusted root, and at the start of each ingest cycle, the registry re-evaluates the links that were verified under another policy: each bundle a requirement still accepts keeps its link, and a link whose bundle no requirement accepts becomes `unverified`, its version's outcome becomes `failed` with `ingest.attestation_missing` and the constraint `link_downgraded` when a requirement that is not optional is left with no verified link, and the registry records `layer.attestation_links_downgraded`. A change to `none` changes every verified link to `unverified` and every outcome to `not_evaluated`. A policy change through the layer endpoints also starts an ingest cycle of the layer, and a cycle that was already running under the earlier policy stores its content but writes no link and no outcome. A re-evaluation changes an outcome only when it downgrades a link, so a change that adds a requirement, or that moves the only accepting signer to another signer of the requirement, leaves a stored version `verified` until a cycle evaluates it again; a version no longer in the tree keeps its last outcome. A change of policy refuses no stored version: a version with a downgraded link keeps serving, and the next cycle that passes the layer step links the versions still in the tree under the new policy. While a layer's policy is invalid, such as one that names an instance the registry no longer configures or one that the tenant trust floor does not admit, its `verified` versions are served as `failed` with `ingest.attestation_policy_invalid`, and the stored outcome is served again once the policy is repaired or the floor is lowered. A change of a tenant's floor re-evaluates the links of every layer of the tenant.

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
- A policy with several requirements sets `covers_layer: true` on exactly one of them, and that requirement does not set `optional: true`.
- Each entry of `trust_require_types` names an artifact type the registry knows, no entry is listed twice, and a policy that lists a type has a requirement and a `trust_mode` other than `none`.
- A signer names an `issuer` and an `identity`, and the binding rules above hold after the derivation. A [key-based signer](#key-based-signers) names a `public_key` that holds one PEM block of an ECDSA P-256 key and no other member, an `instance` included.
- A signer that sets `any_source` names no source binding member, and one that sets `any_ref` names no `source_repository_ref`.
- A certificate signer's `instance` is one the registry configures.
- A `git` layer sets no `trust_layer_root`. A `trust_layer_root` neither begins nor ends with `/` and holds no empty, `.`, or `..` segment, and is valid UTF-8 with no white space, control character, or backslash.
- A requirement or a signer holds no member of another name, including one reached through a YAML alias.
- A request body or a `registry.yaml` layer entry holds no member that reads as a trust member and is none, such as `trust_mod` or `trust-mode`: a member whose name, with letters folded to lower case and each `-` read as `_`, begins with `trust`. In a layer entry this includes a key taken through a YAML merge key (`<<`), and an entry whose merge keys nest more than 16 deep is refused. A request body matches `trust_mode`, `trust_requirements`, `trust_require_types`, and `trust_layer_root` without regard to letter case and refuses one carried twice, and `trust_mode` and `trust_layer_root` refuse `null`.
- The layer endpoints refuse a trust change for a layer declared in `registry.yaml`, whose declaration sets its policy at each start. A request that restates the stored policy, such as a layer object returned as read, is admitted. A registration under a declared ID without trust members keeps the declared policy, is checked against its source type, and is refused when it changes the layer's source while the declaration holds a requirement.

An update that changes no trust member does not re-check the stored policy, so a policy whose instance was later removed from `registry.yaml` does not block an unrelated update. A layer whose empty mode `PODIUM_LAYER_TRUST_DEFAULT` resolves to `warn` or `require` and that has no requirement is logged as a warning at each start.

Changing the policy of an admin-defined layer requires the tenant `admin` role, and the owner of a user-defined layer changes its own. A change is recorded on the layer's audit event with the previous and the new policy.

### Key-based signers

A signer can name a public key in place of an issuer and an identity. A key-based signer serves a private repository whose host offers no private Sigstore instance, such as a private GitHub repository outside GitHub Enterprise Cloud, and an air-gapped deployment. Signing with a key contacts no outside service: the bundle carries the public key hint in place of a certificate, and no log entry and no timestamp. The operator distributes the key and rotates it.

A key-based signer has the member `public_key` alone, a string that holds one PEM block of type `PUBLIC KEY`, as `cosign generate-key-pair` writes it to `cosign.pub`. The key is ECDSA on the P-256 curve, and a key of another algorithm or curve, a second block, and other text around the block are refused. In JSON the line breaks of the block are written as `\n`, and in `registry.yaml` the value is a block scalar:

```yaml
      trust_mode: require
      trust_requirements:
        - signers:
            - public_key: |
                -----BEGIN PUBLIC KEY-----
                MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE...
                -----END PUBLIC KEY-----
```

`podium layer register --trust-public-key cosign.pub` and `podium layer update --trust-public-key cosign.pub` read the block from the file. The flag is repeatable and can stand beside `--trust-signer` in one requirement.

A key-signed bundle carries its time inside the signed statement. `podium attest --signed-time` adds the subject `.podium/signed-at/<seconds>`, the time at which the command ran in seconds since the Unix epoch, after the layer subject of each file, and that value is the attested time of the bundle. The registry reads no timestamp of a key-signed bundle, because any holder of the bundle can replace a timestamp, which lies outside the signed bytes. A statement that lists the layer subject and no signed-time subject is refused. The layer step accepts a key-signed layer subject only while its signed time is not later than the registry's clock plus `PODIUM_ATTESTATION_SIGNED_TIME_ALLOWANCE`, which defaults to `5m`. A later one is refused with `ingest.attestation_stale` and the constraint `signed_time_ahead`, so a signing machine whose clock runs ahead of the registry's by more than the allowance fails closed.

The bundle's public key hint and the `keyid` of its signature lie outside the signed bytes, and the registry reads neither: it verifies each bundle under the key of each key-based signer of the policy. The served `signer` of a key-signed link holds an empty `instance` and `issuer` and the key's identifier as `identity`, which is `sha256:` and the SHA-256 digest of the key's DER encoding. Two keys that sign one predicate type have separate links, so a rotation lists the old and the new key until every artifact is signed with the new one. A key carries no proof of the time at which it signed, so a policy trusts a key for as long as it lists the key, and the signatures of a removed key stop counting at once. A key holder who signs an older layer state again gives it a newer signed time, and only the attestation mark limits that. A [tenant trust floor](#tenant-trust-floor) entry names an issuer, so it matches no key-based signer, and a layer under a floor with a signer list cannot name one. A cycle under `none` links no key-signed bundle.

#### Signing with a key outside GitHub Actions

The recipe signs with [cosign](https://github.com/sigstore/cosign). It was tested with cosign 3.0.6. Pin the release your CI installs, and verify the download against the checksum the release publishes, because the signing tool handles the key. A cosign release with a different signing configuration can upload to a transparency log by default, and the registry refuses a key-signed bundle that carries a log entry. Create the key pair once and keep `cosign.key` and its password in the CI secret store:

```bash
cosign generate-key-pair
podium layer register --id finance --repo https://github.com/acme/finance --ref podium/signed \
  --trust require --trust-public-key cosign.pub
```

Each signing run writes the statements of the commit with a signed time, signs each one with the key without a transparency log, and imports the bundles:

```bash
podium attest --root skills --commit HEAD --format statement --signed-time --out out
for f in out/podium-statement-*.json; do
  cosign attest-blob --key cosign.key --statement "$f" --bundle "$f.sigstore.json" \
    --use-signing-config=false --tlog-upload=false --yes
done
podium attest import out/*.sigstore.json --root skills --commit HEAD --public-key cosign.pub
podium attest check --root skills --requirements requirements.json
```

cosign 3.0.6 reports `--tlog-upload` as deprecated in favour of a signing configuration file that names no transparency log, and a later release can require that file in its place. `podium attest import` accepts a key-signed bundle only with `--public-key`, and only when its signature verifies under that key, because the bundle names its key in no signed byte. `requirements.json` is the file `podium layer show finance --requirements` writes. The workflow then commits the attestation directory with the signed content to the ref the layer tracks.

### Tenant trust floor

A tenant trust floor is a minimum that the trust policy of every layer of a tenant meets, a user-defined layer included. A floor with the mode `warn` or `require` and the required type `hook` keeps a tenant member from registering a layer that serves unsigned hooks, and a signer list keeps the member from choosing the signer of those hooks. A floor has three parts:

| Part | Meaning |
|:--|:--|
| mode | `none`, `warn`, `require`, or empty. A layer's resolved mode is not below it, and an empty `trust_mode` resolves to at least it. |
| required types | Artifact types every layer lists in its `trust_require_types`. A floor that lists a type sets the mode `warn` or `require`, because a layer under `none` verifies nothing and its list enforces nothing. |
| signers | Admissible signer entries. When the list is not empty, every signer of every requirement of a layer matches an entry. |

An entry has an `issuer` and optionally an `instance`, a `source_repository_owner` or a `source_repository_owner_id`, a `source_repository_ref`, and an `identity`. A layer's signer matches an entry when the issuers are equal, the instances are equal (an entry with no `instance` admits only signers of `public-good`), and the signer names each member the entry names: the owner, or a `source_repository` directly under the owner, so `https://github.com/acme-tools/x` does not match the owner `https://github.com/acme`; the same owner identifier; the same ref; and the same identity. A signer with `any_source` matches no entry that names an owner, an owner identifier, or an identity, and one with `any_ref` matches no entry that names a ref. A [derived source binding](#derived-source-binding) names the repository and no owner identifier, so it matches an entry that names the owner and not one that names the owner identifier; such a signer names `--trust-source-owner-id`.

An entry with an owner and no ref admits any branch of any repository of that owner. An entry with no owner and no owner identifier admits the repositories of every owner: an entry that names only the identity of a public reusable signing workflow admits a signer bound to any repository that calls that workflow. Name the owner or the owner identifier and the ref in each entry, and the identity of a shared signing workflow where the tenant has one.

On a multi-tenant registry the floor is part of the tenant object, in the members `trust_floor_mode`, `trust_floor_require_types`, and `trust_floor_signers`, which only the instance operator sets through [`PATCH /v1/admin/tenants/{id}`](../../reference/http-api#tenant-management). The per-tenant `admin` role cannot change it. The response lists in `invalid_layers` each layer of the tenant that the new floor makes invalid, with the rule it breaks:

```json
{
  "trust_floor_mode": "require",
  "trust_floor_require_types": ["hook"],
  "trust_floor_signers": [
    {
      "issuer": "https://token.actions.githubusercontent.com",
      "source_repository_owner_id": "7890",
      "source_repository_ref": "refs/heads/main"
    }
  ]
}
```

A single-tenant registry has no tenant endpoints, and the floor of its org is a deployment setting: the JSON file that [`PODIUM_LAYER_TRUST_FLOOR_FILE`](../../reference/configuration#layers) names, or the `registry.yaml` key `trust_floor`, each an object with the members `mode`, `require_types`, and `signers`. The variable takes precedence, and the registry reads the setting at each start:

```yaml
registry:
  trust_floor:
    mode: warn
    require_types: [hook, mcp-server]
    signers:
      - issuer: https://token.actions.githubusercontent.com
        source_repository_owner: https://github.com/acme
        source_repository_ref: refs/heads/main
```

A floor that does not parse, holds a member of another name, names a mode other than `none`, `warn`, or `require`, an unknown or repeated type, required types under a mode below `warn`, an entry with no issuer or with both owner members, or an instance that `sigstore.instances` does not configure, refuses the start with `config.invalid_layer_trust`, and through the tenant endpoint it is refused with `registry.invalid_argument`. A multi-tenant registry refuses to start when either setting is set. A stored floor that breaks one of these rules, such as a tenant row edited by hand, counts as a floor the registry cannot read: the cycles of the tenant's layers fail, and loads of their verified versions answer `registry.unavailable`, until the floor is repaired.

The floor applies at each point a policy is checked:

- A registration, and an update that carries a trust member, whose policy does not meet the floor is refused with `registry.invalid_argument`, and the message names the rule.
- A layer that already exists when the floor is raised keeps its policy. Each of its ingest cycles is refused with `ingest.attestation_policy_invalid` and the constraint `below_floor`, `missing_required_type`, or `signer_outside_floor`, its `verified` versions are served as `failed` with that code until the policy meets the floor, and each start logs it. A declared layer that only the floor makes invalid does not refuse the start.

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
