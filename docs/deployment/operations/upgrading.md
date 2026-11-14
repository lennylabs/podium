---
title: Upgrading
nav_order: 4
description: "Upgrade a registry: the procedure for each mode, mixed-version replicas, releases that migrate stored values, and rollback."
---

# Upgrading

## Upgrades

Replace the binary and restart. Schema migrations run on first start of the new version. Take the backup described above before replacing the binary, because returning to the previous binary requires restoring it.

A release whose changelog names a migration of the values stored under the schema also rewrites those values on that first start, in either signing mode, in two cases:

- The SQLite store sits in the directory of the key file, which is `~/.podium/standalone/` unless `PODIUM_SIGN_KEY_PATH` moves it.
- The store holds no manifest row.

A store elsewhere that holds rows, including a signing-off store at a custom `PODIUM_SQLITE_PATH`, is refused at start until `sign-stored-rows` records the rewrite. The refused start rewrites no manifest row, signs no row, and records no completion. For such a store, before the first start of the new binary:

1. Stop the previous binary.
2. With signing on, when no file exists yet at `PODIUM_SIGN_KEY_PATH`, create it with `podium admin signing-key generate --key-file <path>`. `sign-stored-rows` never generates a key.
3. Run `podium-server sign-stored-rows --dry-run` and review its report.
4. Run `podium-server sign-stored-rows --plan-digest=<digest>` with the `sha256:` value from the report's last line. With signing on, the run repeats the dry run's `--include-unsigned` setting.

With signing off, both commands run with `PODIUM_SIGN=none` in their environment, because `podium serve --sign none` sets the mode only for its own process. The changelog entry for that release states the order.

## Upgrade procedure

Schema migrations are bundled in the registry binary and applied additively on startup. A new version creates tables and columns when absent and never drops or rewrites existing ones, so an upgrade migrates the database forward in place.

1. **Prepare.** Read the changelog and the migration notes for the target version. If a migration is non-trivial, such as reshuffling embeddings or changing the audit schema, schedule a maintenance window. A release whose changelog names a migration of stored values follows [Releases that migrate stored values](#releases-that-migrate-stored-values) instead of the steps below.
2. **Canary.** Roll one registry replica to the new version. Watch metrics for 30 minutes and confirm that latency, error rate, and cache hit rate are unchanged.
3. **Roll.** Roll the rest of the replicas. Because migrations are additive, an older replica ignores the new tables and columns, so old and new replicas coexist during the roll.
4. **Verify.** After the roll completes, confirm that `/readyz` reports `ready` on every replica. When `PODIUM_AUDIT_LOG_PATH` lists a file destination, confirm that the audit-integrity pass logs no gap on its next run. When it lists none, as with the chart's `stdout` default, confirm that the receiving system's `prev_hash` check reports no gap.

### Mixed-version replicas

The additive guarantee covers the database schema and does not cover the values stored under it. A release whose changelog names a migration of those values does not permit mixed-version replicas. A release whose changelog names a rollback floor permits mixed-version replicas at or above that floor. [Rollback floor for the layer credential columns](upgrade-notes#rollback-floor-for-the-layer-credential-columns) describes such a release.

### Releases that migrate stored values

A release whose changelog names a migration of the values stored under the schema has no canary phase. The new version computes different values over the stored bytes than every replica on the previous version, which still reads and writes them. Follow the upgrade procedure that changelog entry gives, in a maintenance window:

1. Stop every replica.
2. Back up the store.
3. Where signing is on, provision the signing key file.
4. Over any store other than the SQLite store in the key file's directory, which includes every multi-replica deployment, run `sign-stored-rows --dry-run`, review its report, and run `sign-stored-rows` with its `--plan-digest`. Do this in either signing mode, because a start over such a store that holds rows and no completion record is refused. Where the store is the SQLite store in the key file's directory, skip this step: the first start rewrites the stored values itself.
5. Start the replicas together on the new version.

A Helm chart deployment runs such a migration through the chart's migrate Job, in either signing mode. The Job holds the Deployment at zero replicas while `sign-stored-rows` rewrites the store. [Upgrading the chart from v0.4.0](upgrade-notes#upgrading-the-chart-from-v040) gives the chart procedure.

- In either signing mode, a registry pod over a store that holds manifest rows and no completion record exits at start, rewriting no manifest row, signing no row, and recording no completion.
- A GitOps controller such as Argo CD runs the same procedure as the value commits that [GitOps controllers](upgrade-notes#gitops-controllers) lists, in that order.
- Pass neither `--rollback-on-failure` (Helm v4) nor `--atomic` (Helm v3) to those upgrades, because an automatic rollback reinstalls the previous binary over rows it cannot read.

### Rolling back

Roll back by reverting the binary. The additive schema stays forward-compatible with the previous version's binary, so an older binary continues to run against a schema-migrated database.

A release whose changelog names a migration of the values stored under the schema is reverted differently. Restore the store from the backup that changelog's upgrade procedure takes, revert the registry and its consumers together, and clear each reverted consumer's cache as that changelog entry states.

Where a changelog entry names a rollback floor, the reverted binary is at or above that version, including when the upgrade started from a version below it.

---
