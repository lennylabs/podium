---
title: Backup and restore
nav_order: 3
description: "What to back up on a single-node and a clustered deployment, the default recovery objectives, and how to test a restore."
---

# Backup and restore

A registry's state lives in its metadata store, its object storage, and its signing key. A restore without the signing key leaves the registry refusing every row the key signed, so each section below lists the key with the store it signs. This page lists what to back up on each deployment mode and gives the restore drill.

## What to back up

### Single node

Take a periodic snapshot of `~/.podium/standalone/`. It holds the SQLite file, the object directory, and the signing keys the deployment generated there:

- `registry-signing.key`, which the first run writes unless signing is off (`--sign none` or `PODIUM_SIGN=none`).
- `audit.key`, when audit anchoring is enabled. `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` enables anchoring, and `PODIUM_AUDIT_SIGNING_KEY_PATH` moves `audit.key`.

Include the file named by `PODIUM_SCIM_STORE_PATH` when SCIM persistence is configured. Include `~/.podium/audit.log` in the same snapshot, because the audit stream sits outside the standalone directory unless `PODIUM_AUDIT_LOG_PATH` moves it.

Keep the anchor keypair distinct from the registry signing key at `PODIUM_SIGN_KEY_PATH`. While anchoring is enabled and signing is on, a shared key refuses startup with `config.audit_anchor_key_shared`.

### Clustered

- **Postgres.** A managed Postgres service handles backups. Enable point-in-time recovery (PITR) with at least 7 days of retention. For self-run Postgres, run a logical backup (`pg_dump`) daily, and run physical backups (base backups plus WAL archiving) for PITR.
- **Object storage.** Enable cross-region replication or daily snapshots. Resources are content-addressed and immutable, so a restore replaces the bucket contents from the snapshot.
- **Registry signing key.** Back up the file at `PODIUM_SIGN_KEY_PATH`, or the key file in the Secret the Helm chart mounts there, together with the store it signs. Back up the rotated file after each [rotation](signing-keys#rotating-the-signing-key). The registry verifies each stored signature before it serves the row, so once the key is lost, it refuses every row the key signed, to every reader and under any consumer policy. A surviving copy of the key's public half repairs those rows, as [Losing the signing key](signing-keys#losing-the-signing-key) states.

### Recovery objectives

The default recovery objectives are an RPO of 1 hour and an RTO of 4 hours for a managed-Postgres and replicated-S3 setup. Reduce PITR granularity or replicate more often to tighten them. Extend the PITR window to loosen them.

## Test a restore

Test restores quarterly with the following runbook procedure:

```
1. Spin up a non-production registry pointed at a fresh Postgres + a fresh
   S3 bucket, with PODIUM_SIGN_KEY_PATH naming a copy of the production
   signing key restored from the backup.
2. Restore Postgres from PITR to T-1h.
3. Sync the production S3 bucket to the fresh one (rclone or aws s3 sync).
4. When PODIUM_AUDIT_LOG_PATH lists a file destination, watch the restored
   registry's log for the scheduled audit-integrity pass. A hash-chain gap
   logs "audit integrity ALERT" and records an audit.gap_detected event.
   When it lists none, as with the chart's stdout default, confirm that the
   receiving system's prev_hash check reports no gap.
5. Run `podium verify <artifact> --registry <restored-url> --provider
   <name>` for a signed artifact and confirm it exits 0.
6. Spot-check `load_artifact` for a known-good artifact; should match the
   pre-restore content_hash.
7. Restoring to an earlier point lowers the artifact revision of every
   artifact whose newest version postdates the restore point. Each podium-mcp
   consumer then fails latest loads of those artifacts with
   materialize.stale_resolution until it stops its MCP servers and runs
   `podium cache reset-revisions`.
```

The following notes apply to the procedure:

- **Step 1.** The drill restores the signing key because a registry with signing on, a Postgres store, and no key path is refused at start. The drill then also exercises the key backup.
- **Step 4.** When the registry's audit destinations include a file, the audit-integrity pass runs inside the registry on the `PODIUM_AUDIT_VERIFY_INTERVAL_SECONDS` schedule, which defaults to 3600 seconds. Lower it on the restored deployment to get the first pass sooner. [Hash chain](observability#hash-chain) describes both checks.
- **Step 5.** `podium verify <artifact>` verifies the registry's delivery signature over its delivery hash. It uses the verification key set that the default `registry-managed` provider resolves: `PODIUM_SIGNATURE_VERIFY_KEY`, one base64 key or a comma-separated list, or, when that variable is unset, the `public:` line and every `verify:` line of the key file at `PODIUM_SIGN_KEY_PATH`. Run step 5 with the production registry's public key in `PODIUM_SIGNATURE_VERIFY_KEY`, extracted as [Deploy the registry](../modes/clustered#2-deploy-the-registry) shows.
- **Step 7.** [Stale `latest` protection](../../consuming/verifying-deliveries#stale-latest-protection) describes the revision marks that `podium cache reset-revisions` clears.

Step 5 with other providers behaves as follows:

- `--provider noop` is refused for every envelope.
- A Sigstore envelope passed with `--signature` takes `--provider sigstore-keyless`. `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE` names a Sigstore `trusted_root.json` that holds the certificate authorities, the transparency-log keys, and the timestamp authorities. `PODIUM_SIGSTORE_CERT_IDENTITY` and `PODIUM_SIGSTORE_CERT_OIDC_ISSUER` name the expected signer.
- Verification fails with `no trust root configured` when the trust root is absent, and with a message naming the variable when either policy variable is unset.
- The delivery signature itself is never a Sigstore envelope, so a restore drill verifies it with `registry-managed`.
