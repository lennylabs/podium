---
title: Moving between modes
nav_order: 6
description: "Move a catalog from local to single node or clustered, and from single node to clustered, carrying the signing key and the stored artifacts."
---

# Moving between modes

This page covers moving a catalog from the [local](local) deployment mode to [single node](single-node) or [clustered](clustered), and from single node to clustered. Each move keeps the artifact directory and the authoring loop unchanged.

The shared library does the same parsing, composition, and adapter work in every deployment mode, so output is bit-identical for the same target and profile, and end-user behavior is preserved across the cut-over. Skills that declare no version are the exception that [Skills that declare no version](#skills-that-declare-no-version) describes.

## Local to single node

Single node becomes necessary when the team needs runtime discovery through the MCP server, identity-based visibility filtering, or a single audit log.

On a chosen host, point a server at the same directory. The host can be a small VM behind a VPN, or any always-on machine:

```bash
podium serve --standalone --layer-path ~/podium-artifacts/
```

Then switch the consumers to the server:

1. In each developer's `<workspace>/.podium/sync.yaml`, replace the `defaults.registry` path, such as `./.podium/registry/`, with the server URL, such as `https://podium.your-team.example`.
2. Optionally, add the Podium MCP server entry to the harness's MCP config so the agent can call meta-tools at runtime.

The directory layout and the authoring loop are unchanged, and the consumer paths gain MCP and SDK support. Authors keep opening PRs and merging against the same registry repo, and the server picks up changes through `podium layer reingest` or a watcher. The visibility each layer's `.layer-config` declares takes effect once the server serves the directory. [Single node](single-node) covers TLS, running the server as a service, and client setup, and [Connecting layer sources](../content/layer-sources) covers keeping layers current.

## Local to clustered

A clustered registry reads its layers from Git repositories. To serve a local catalog from a clustered registry:

1. Push the catalog directory to a Git repository, if it is not one already.
2. Deploy the registry as [Clustered](clustered) describes.
3. Register each layer subdirectory of the catalog as a `git`-source layer with `podium layer register`, setting `--root` to the subdirectory. Register the layers in the order the catalog composes them, lowest precedence first: the order `layer_order:` in `.registry-config` lists, or alphabetical order without it. A single-layer catalog registers as one layer rooted at the repository.
4. Set each layer's visibility with the registration flags. The registry reads `.layer-config` only for the layers that `--layer-path` loads, so a `git`-source layer does not take its visibility from the file.
5. Switch each developer's `defaults.registry` to the registry URL, as [Local to single node](#local-to-single-node) does.

Moving to single node first and then to clustered, as the next section describes, reaches the same result and carries the audit log and the layer configuration with it.

## Single node to clustered

Single node stops fitting when the deployment needs production-grade availability across replicas, Postgres-backed tenant isolation, or scale beyond tens of developers. `podium admin migrate-to-standard` exports the single-node state into the standard stack. The artifact directory is unchanged, and the target registry signs with the source's key.

### What the migration carries

The migration carries every tenant on the single node, active or deactivated. For each tenant it carries the tenant's record, with its name, quotas, and active state, and the tenant's artifact manifests, layer configuration, and admin grants. It copies the artifact content blobs and, with `--target-audit-log`, the whole audit log. The target computes embeddings again against its own vector backend on first ingest.

- A clustered target routes requests to a tenant other than the `default` org only when it is started with `PODIUM_MULTI_TENANT` and an identity provider that authenticates each caller.
- Instance-operator grants come from the target's own `PODIUM_OPERATOR_ADMINS`.

### Prepare a chart deployment

On a Helm chart deployment, run `migrate-to-standard` before any chart install on the target store. A chart release that already serves the target store is held at zero replicas, and its pods are deleted, as step 2 of [Upgrade with signing on](../operations/upgrade-notes#upgrade-with-signing-on) shows, before the command runs.

### Export the single-node state

Run the export against the target's Postgres store and object store:

```bash
podium admin migrate-to-standard --postgres <dsn> --object-store <url>
```

### Carry the signing key

Before the target's first start after the migration, place the source's registry signing key at the target's `PODIUM_SIGN_KEY_PATH`, replacing any key file already there. Set `TARGET_SIGN_KEY_PATH` to the file the target's `PODIUM_SIGN_KEY_PATH` names, and copy the key on the source host:

```bash
install -m 600 ~/.podium/standalone/registry-signing.key "$TARGET_SIGN_KEY_PATH"
```

A source started with `PODIUM_SIGN_KEY_PATH` copies the file that variable names instead. On a chart deployment, create the signing Secret from the source's key file, replacing any Secret created from another key, as [Deploy the registry](clustered#2-deploy-the-registry) shows for a generated key.

### Rewrite of stored content hashes

`migrate-to-standard` clears the target store's record of the rewrite of stored content hashes:

- Where the target is the SQLite store in the key file's directory, the target's next start runs the rewrite over the copied rows.
- A target store elsewhere, such as the Postgres store of a clustered deployment, holds the copied rows and no completion record. Its registry is refused at start in either signing mode until `sign-stored-rows` records the rewrite, as [The registry refuses to start and names `sign-stored-rows`](../operations/signing-keys#the-registry-refuses-to-start-and-names-sign-stored-rows) describes.

Before such a target's first start, and after the key is in place, run `podium-server sign-stored-rows --dry-run` against the target's configuration and review its report. Then run `podium-server sign-stored-rows` with the report's `--plan-digest`. With signing on, repeat the dry run's `--include-unsigned` setting. With signing off, put `PODIUM_SIGN=none` in the commands' environment.

On a chart deployment, the migrate Job runs both commands. Install, or upgrade, at zero replicas with `migration.previousImage` set to an image other than this release's, such as `ghcr.io/lennylabs/podium-server:v0.4.0`. Then follow steps 3 to 5 of [Upgrade with signing on](../operations/upgrade-notes#upgrade-with-signing-on), with `signing.mode=none` and `migration.includeUnsigned=false` for a deployment with signing off.

### Switch consumers

Switch consumer endpoints to the new registry URL and decommission the old host. For the staged rollout of governance features covering identity, sensitivity labels, signing, and freeze windows, follow [Progressive adoption](../progressive-adoption).

### If the key step was skipped

The target must hold the source's key before the `sign-stored-rows` run. A target that holds another key, such as a key an earlier start generated or a Secret created from a different key, leaves every copied signed row untouched. The outcome and its repair depend on how far the migration went:

| Situation | Result | Repair |
|:--|:--|:--|
| The target has no key file. | The target is refused at start, naming `PODIUM_SIGN_KEY_PATH`. | Place the source's key. |
| The target holds another key, and no `sign-stored-rows` run has completed. | The target is refused at start. The `sign-stored-rows --dry-run` it requires lists every copied signed row as `class=signature_unverified` before any write. | Place the source's key before the run. |
| A run under another key completed, and the source had already rewritten the row. | The row keeps the rewritten content hash and the source's envelope. The target refuses it with `materialize.signature_invalid`, to every reader and under any consumer policy. | Place the source's key at the target's `PODIUM_SIGN_KEY_PATH`, or create the Secret from it, replacing the other key, and restart the target. The registry loads its key only at start. No new version is needed. |
| A run under another key completed, and the source never started on this release. | The row stays at the previous content hash. The target refuses it with `materialize.content_hash_mismatch`, and placing the source's key alone does not repair it. | List the source key's public half on a `verify:` line of the target's key file, restart the target, and run `podium-server sign-stored-rows` or `podium admin sign-stored-rows`. The command moves each such row to the new content hash and re-signs it under the target's key. |

An alternative repair for the last case is to recreate the target store empty and run `podium admin migrate-to-standard` again with the source's key at the target's `PODIUM_SIGN_KEY_PATH`. The command clears the target's record of the rewrite, so the rewrite runs again and re-signs the copied rows. Where the target is the SQLite store in the key file's directory, that happens at the next start. Otherwise it happens through the `sign-stored-rows` dry run and the run with its plan digest, before the target's start. On a Helm chart deployment, hold the release at zero replicas before the command runs and run the rewrite in the migrate Job, as [Rewrite of stored content hashes](#rewrite-of-stored-content-hashes) describes.

A new version of each affected artifact is the repair only where neither the source store nor any copy of the source key's public half survives.

## Skills that declare no version

A filesystem source records a skill that declares no version as `0.0.0-auto.0`. A registry ingesting the same directory for the first time assigns the same value. It assigns a higher N once it has ingested changed bytes for the skill, so the lock-file versions then differ between the deployment modes. While the registry serves the directory's current bytes as the skill's latest row, the content hash and every harness's output stay identical.

A revert is the case where output differs. A registry rejects a change of such a skill back to content it already stored under another version, and it keeps serving its newer version, while a filesystem source serves the reverted content. After a revert, the content hash, the body, and every harness's output differ between the deployment modes until the author sets `metadata.version` in `SKILL.md` or changes the content.
