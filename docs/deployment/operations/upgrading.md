---
title: Upgrading
nav_order: 4
description: "Upgrade a registry: how migrations run, the procedure for a single node and for a rolling clustered upgrade, mixed-version replicas, releases that migrate stored values, and rollback."
---

# Upgrading

This page gives the general upgrade procedure for each deployment mode. A release that needs steps of its own lists them in [Upgrade notes](upgrade-notes), and its `CHANGELOG.md` entry names them. Read both for the target version before an upgrade.

## How migrations run

The registry binary carries its schema migrations and applies them additively at startup. A new version creates tables and columns when they are absent and never drops or rewrites existing ones, so an upgrade migrates the database forward in place with no separate migration step.

The additive guarantee covers the database schema and does not cover the values stored under it. A release whose changelog names a migration of stored values computes those values differently from the previous release and rewrites the stored rows once. Such a release follows [Releases that migrate stored values](#releases-that-migrate-stored-values) in place of the procedure below.

## Upgrade procedure

### Single node

Take the backup that [Backup and restore](backup-restore#single-node) describes, replace the binary, and restart the registry. The schema migrations run on the first start of the new version.

### Clustered rolling upgrade

1. **Prepare.** Read the changelog and the [upgrade notes](upgrade-notes) for the target version. Schedule a maintenance window for a non-trivial migration, such as reshuffling embeddings or changing the audit schema.
2. **Canary.** Roll one registry replica to the new version. Watch metrics for 30 minutes and confirm that latency, error rate, and cache hit rate are unchanged.
3. **Roll.** Roll the rest of the replicas. Because migrations are additive, an older replica ignores the new tables and columns, so old and new replicas coexist during the roll.
4. **Verify.** After the roll completes, confirm that `/readyz` reports `ready` on every replica. When `PODIUM_AUDIT_LOG_PATH` lists a file destination, confirm that the audit-integrity pass logs no gap on its next run. When it lists none, as with the chart's `stdout` default, confirm that the receiving system's `prev_hash` check reports no gap.

On a Helm chart deployment, `helm upgrade` with the new image tag runs this roll through the Deployment's default rolling update, as [Later upgrades](upgrade-notes#later-upgrades) states.

## Mixed-version replicas

Replicas on the previous and the new version run together during a rolling upgrade, with these exceptions:

- A release whose changelog names a migration of stored values does not permit mixed-version replicas.
- A release whose changelog names a rollback floor permits mixed-version replicas only at or above that floor. [Rollback floor for the layer credential columns](upgrade-notes#rollback-floor-for-the-layer-credential-columns) describes such a release.

## Releases that migrate stored values

A release that migrates stored values has no canary phase. The new version computes different values over the stored bytes than a process on the previous version, which still reads and writes them, so every process on the previous version stops before the first process on the new version starts.

The registry rewrites the stored values at the first start of the new version only for the SQLite store in the directory of the signing key file, which is the default single-node layout, and for a store that holds no manifest row. Every other store needs `sign-stored-rows` before that start, in either signing mode, and the registry refuses to start over it until the command has recorded the rewrite. [Stores that need `sign-stored-rows` before a start](signing-keys#stores-that-need-sign-stored-rows-before-a-start) states the rule and what a refused start leaves behind. A multi-replica deployment always falls in this group, and so does a single node whose store sits at a custom `PODIUM_SQLITE_PATH` outside the key file's directory, with signing on or off.

Follow the procedure the release's changelog entry gives, in a maintenance window:

1. Stop every registry process.
2. Back up the store.
3. With signing on, when no file exists yet at `PODIUM_SIGN_KEY_PATH`, create it with `podium admin signing-key generate --key-file <path>`. `sign-stored-rows` never generates a key.
4. Unless the registry rewrites the store at its first start, run `sign-stored-rows --dry-run` and review its report. Then run `sign-stored-rows --plan-digest=<digest>` with the `sha256:` value from the report's last line. With signing on, the run repeats the dry run's `--include-unsigned` setting. With signing off, both commands run with `PODIUM_SIGN=none` in their environment, because `podium serve --sign none` sets the mode only for its own process.
5. Start the registry, with every replica on the new version together.

`sign-stored-rows` runs as `podium admin sign-stored-rows` or, where only the server binary is installed, as `podium-server sign-stored-rows`. [The `sign-stored-rows` command](signing-keys#the-sign-stored-rows-command) describes the command.

A Helm chart deployment runs this procedure through the chart's migrate Job, in either signing mode. The Job holds the Deployment at zero replicas while `sign-stored-rows` rewrites the store. [Upgrading the chart from v0.4.0](upgrade-notes#upgrading-the-chart-from-v040) gives the chart procedure.

- A registry pod over a store that needs the command exits at start, in either signing mode.
- A GitOps controller such as Argo CD runs the same procedure as the value commits that [GitOps controllers](upgrade-notes#gitops-controllers) lists, in that order.
- Pass neither `--rollback-on-failure` (Helm v4) nor `--atomic` (Helm v3) to those upgrades, because an automatic rollback reinstalls the previous binary over rows it cannot read.

## Rolling back

Roll back by reverting the binary. The additive schema stays forward-compatible with the previous version's binary, so an older binary continues to run against a schema-migrated database.

A release that migrates stored values is reverted differently. Restore the store from the backup that the release's upgrade procedure takes, revert the registry and its consumers together, and clear each reverted consumer's cache as the changelog entry states.

Where a changelog entry names a rollback floor, revert to a version at or above that floor, including when the upgrade started from a version below it.
