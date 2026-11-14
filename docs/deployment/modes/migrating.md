---
title: Moving between modes
nav_order: 6
description: "Move a catalog from local to single node, and from single node to clustered, carrying the signing key and the stored artifacts."
---

# Moving between modes

## Migration from single node

`podium admin migrate-to-standard` exports a [single-node](single-node) deployment into the standard stack:

```bash
podium admin migrate-to-standard --postgres <dsn> --object-store <url>
```

The artifact directory is unchanged. Layer config moves from `~/.podium/registry.yaml` to the tenant config, and the same artifacts ingest into the new metadata store. The command clears the target store's record of the rewrite of stored content hashes. The target store is outside the SQLite store in the key file's directory, so in either signing mode the target registry's start is refused until `sign-stored-rows` records the rewrite. A refused start rewrites no manifest row, signs no row, and records no completion.

The target registry signs with the source's key. Migrate in this order:

1. On a chart deployment, run `migrate-to-standard` before any chart install on the target store. A chart release that already serves the target store is held at zero replicas, and its pods are deleted, as step 2 of [Upgrade with signing on](../operations/upgrade-notes#upgrade-with-signing-on) shows, before the command runs.
2. Run the export command above.
3. Before the target's first start, create the signing Secret from the source's key file, replacing any Secret created from another key, as [Single node](#migrating-to-clustered) shows.
4. Run `sign-stored-rows --dry-run`, review its report, and run `sign-stored-rows` with its `--plan-digest` before the target's first start. With signing on, repeat the dry run's `--include-unsigned` setting. On a chart deployment, the rewrite runs in the migrate Job: install, or upgrade, at zero replicas with `migration.previousImage` set to an image other than this release's, such as `ghcr.io/lennylabs/podium-server:v0.4.0`. Then follow steps 3 to 5 of [Upgrade with signing on](../operations/upgrade-notes#upgrade-with-signing-on), with `signing.mode=none` and `migration.includeUnsigned=false` for a deployment with signing off.
5. Switch consumer endpoints to the new registry URL and decommission the old host.

A serving pod over the target store before the `sign-stored-rows` run succeeds exits at start and names `sign-stored-rows`.

For the staged rollout of governance features covering identity, sensitivity labels, signing, and freeze windows, follow [Progressive adoption](../progressive-adoption).

### Signing key mismatches after migration

The target must hold the source's key before the `sign-stored-rows` run. [Single node](#migrating-to-clustered) states both repair cases below.

| Situation | Result | Repair |
|:--|:--|:--|
| The target registry has no key file. | The target is refused at start. | Place the source's key. |
| The target holds any other key. | The target is refused at start. The `sign-stored-rows --dry-run` it requires lists every copied signed row as `class=signature_unverified` before any write. | Place the source's key before the run. |
| A run under another key completed. | The run leaves those rows untouched and records completion. A row the source had already rewritten is then refused with `materialize.signature_invalid`. | Create the Secret from the source's key and restart the target. |
| A run under another key completed, and the source never started on this release. | A row the source still held at the previous content hash is refused with `materialize.content_hash_mismatch`. | Put the source key's public half on a `verify:` line of the target's key file, restart, and run `sign-stored-rows`. Alternatively, recreate the target store empty, run the migration again with the source's key in place, and run the `sign-stored-rows` dry run and run before the target's start. |

---

## Migrating to clustered

Single node stops fitting when the deployment needs production-grade availability across replicas, Postgres-backed tenant isolation, or scale beyond tens of developers. `podium admin migrate-to-standard` exports the single-node state into the standard stack:

```bash
podium admin migrate-to-standard --postgres <dsn> --object-store <url>
```

The migration carries every tenant on the single node. For each tenant it carries the artifact manifests, layer configuration, admin grants, quotas, and active state. It copies the artifact content blobs and, with `--target-audit-log`, the whole audit log.

- A clustered target routes requests to a tenant other than the `default` org only when it is started with `PODIUM_MULTI_TENANT` and an identity provider that authenticates each caller.
- Instance-operator grants come from the target's own `PODIUM_OPERATOR_ADMINS`.

[Clustered](clustered) documents the same migration path from the receiving side. The artifact directory is unchanged, and layer config moves from `~/.podium/registry.yaml` to the tenant config.

### Carry the signing key

The target registry signs with the source's key. Before the target's first start after the migration, place the source's registry signing key at the target's `PODIUM_SIGN_KEY_PATH`, replacing any key file already there. Set `TARGET_SIGN_KEY_PATH` to the file the target's `PODIUM_SIGN_KEY_PATH` names, and copy the key on the source host:

```bash
install -m 600 ~/.podium/standalone/registry-signing.key "$TARGET_SIGN_KEY_PATH"
```

A source started with `PODIUM_SIGN_KEY_PATH` copies the file that variable names instead. On a chart deployment, create the signing Secret from the source's key file, as [Clustered](clustered#2-deploy-the-registry) shows for a generated one.

### Rewrite of stored content hashes

`migrate-to-standard` clears the target store's record of the rewrite of stored content hashes:

- Where the target is the SQLite store in the key file's directory, the target's next start runs the rewrite over the copied rows.
- A target store elsewhere, such as the Postgres store of a standard deployment, holds the copied rows and no completion record. Its registry is refused at start in either signing mode until `sign-stored-rows` records the rewrite. The refused start rewrites no manifest row, signs no row, and records no completion.

Before such a target's first start, run `podium-server sign-stored-rows --dry-run` against the target's configuration and review its report. Then run `podium-server sign-stored-rows` with the report's `--plan-digest`. With signing on, repeat the dry run's `--include-unsigned` setting. With signing off, put `PODIUM_SIGN=none` in the commands' environment. On a chart deployment, the migrate Job runs both commands.

### If the key step was skipped

A target with no key file is refused at start, naming `PODIUM_SIGN_KEY_PATH`.

A target holding any other key leaves every copied signed row untouched. That includes a key an earlier start generated and a Secret created from a different key. The `sign-stored-rows` dry run such a target requires classifies each of those rows `signature_unverified`, and a run under that key leaves each at its stored hash. The outcome then depends on the source's state:

- **A row the source had already rewritten** keeps the rewritten content hash and the source's envelope. While the target runs with the other key, the target registry refuses every such row with `materialize.signature_invalid`, to every reader and under any consumer policy. To repair those rows with no new version, place the source's key at the target's `PODIUM_SIGN_KEY_PATH`, replacing the other key, and restart the target. The registry loads its key only at start.
- **A row still at the previous content hash**, from a source that never started on this release, stays at that hash, and placing the source's key alone does not repair it. List the source key's public half on a `verify:` line of the target's key file, restart the target, and run `podium-server sign-stored-rows` or `podium admin sign-stored-rows`. The command moves each such row to the new content hash and re-signs it under the target's key.

An alternative repair for the second case is to recreate the target store empty and run `podium admin migrate-to-standard` again with the source's key at the target's `PODIUM_SIGN_KEY_PATH`. The command clears the target's record of the rewrite, so the rewrite runs again and re-signs the copied rows. Where the target is the SQLite store in the key file's directory, that happens at the next start. Otherwise it happens through the `sign-stored-rows` dry run and the run with its plan digest, before the target's start. On a Helm chart deployment, hold the release at zero replicas before the command runs and run the rewrite in the migrate Job, as [Migration from single node](#migration-from-single-node) on the clustered page states.

A new version of each affected artifact is the repair only where neither the source store nor any copy of the source key's public half survives.

## Migration paths

### To single node

Single node becomes necessary when the team needs runtime discovery through the MCP server, identity-based visibility filtering, or a single audit log. Point a server at the same directory:

```bash
podium serve --standalone --layer-path ~/podium-artifacts/
```

Each developer's `<workspace>/.podium/sync.yaml` then switches `defaults.registry` from the path to `http://podium.your-team.example`, or wherever the server lives. The directory layout and the authoring loop are unchanged, and the consumer paths gain MCP and SDK support. See [Single node](single-node) and [Connecting layer sources](../content/layer-sources).

The shared library does the same parsing, composition, and adapter work in both deployment modes, so output is bit-identical for the same target and profile. Skills that declare no version are the exception that [Skills that declare no version](#skills-that-declare-no-version) describes.

### To clustered

When production availability or Postgres-backed tenant isolation is required, follow [Clustered](clustered) and use `podium admin migrate-to-standard` to export the single-node state.

### Skills that declare no version

A filesystem source records a skill that declares no version as `0.0.0-auto.0`. A registry ingesting the same directory for the first time assigns the same value. It assigns a higher N once it has ingested changed bytes for the skill, so the lock-file versions then differ between the deployment modes. While the registry serves the directory's current bytes as the skill's latest row, the content hash and every harness's output stay identical.

A revert is the case where output differs. A registry rejects a change of such a skill back to content it already stored under another version, and it keeps serving its newer version, while a filesystem source serves the reverted content. After a revert, the content hash, the body, and every harness's output differ between the deployment modes until the author sets `metadata.version` in `SKILL.md` or changes the content.

---

## Moving between modes

Each move keeps the artifact directory and the authoring loop unchanged.

- **Local to single node:** run `podium serve --standalone --layer-path /path/to/dir` against the same directory, then point each workspace's `defaults.registry` at the server URL.
- **Single node to clustered:** run `podium admin migrate-to-standard --postgres <dsn> --object-store <url>` to export the single-node state into the standard stack. [Migration from single node](#migration-from-single-node) covers the signing key and the stored-row migration that follow.
