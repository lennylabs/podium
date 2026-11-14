---
title: Operator guide
nav_order: 13
description: "Day-two operations for a clustered Podium deployment: capacity planning, backup and restore, signing key rotation, upgrades, read-only mode, outbound webhook receivers, security review, and troubleshooting."
---

# Operator guide

This page covers day-two operations for a [clustered](clustered) Podium deployment. It is for the operators who size, back up, upgrade, and secure a running registry. For a single machine running one binary, see [Single node](single-node).

Audit records, Prometheus metrics, tracing, notifications, and alert rules are covered in [Audit and observability](observability).

---

## Capacity planning

The baseline deployment holds 10K artifacts at 100 QPS, with 1 GB of Postgres and 500 GB of object storage, on 3 replicas with a `db.m5.large` equivalent. Beyond the baseline, act on each threshold as follows:

| Dimension | Threshold | What to do |
|:--|:--|:--|
| Artifacts | 100K | Increase Postgres instance size; review pgvector index parameters; consider sharding embeddings. |
| QPS | 1K | Scale registry replicas horizontally; put a CDN in front of object storage for resource bytes. |
| QPS | 10K | Review search query patterns; consider dedicated Elasticsearch for BM25 with pgvector (or Pinecone/Weaviate/Qdrant) for vector. |
| Tenants | 50 | Confirm `RegistryStore` connection pool is sized appropriately; increase pgbouncer pool if used. |
| Audit volume | 1M events/day | Set retention explicitly. Keep or replace the file destination in `PODIUM_AUDIT_LOG_PATH`, add an `http(s)` or `otlp+http(s)` destination that ships the stream to a SIEM or a collector, or ship `stdout` through the container log pipeline. See [Audit destinations](observability#audit-destinations). |

Embeddings dominate Postgres growth at scale. Each artifact's text projection becomes a float vector whose dimension depends on the configured provider: 768 for `nomic-embed-text` via Ollama, 1024 for `voyage-3` or `embed-v4`, and 1536 for `text-embedding-3-small`. At about 6 KB per row including metadata at 1536 dimensions, 100K artifacts take about 600 MB of embeddings.

Bundled resources dominate object-storage growth. For most teams, the p99 artifact size sits well under the 256 KB inline cutoff. The inline manifest body then fits in Postgres, and only larger resources go to S3.

---

## Monitoring, audit, and alerting

[Audit and observability](observability) documents each of these signals:

- [Audit log](observability#audit-log) and [Audit destinations](observability#audit-destinations): the hash-chained audit stream and where it is written.
- [Metrics](observability#metrics): the Prometheus series and their latency targets.
- [Tracing](observability#tracing): OpenTelemetry export.
- [Notifications](observability#notifications): operational notifications for failed ingests.
- [Alerting](observability#alerting): a starting set of Prometheus alert rules.

---

## Backup and restore

### What to back up

- **Postgres.** A managed Postgres service handles backups. Enable point-in-time recovery (PITR) with at least 7 days of retention. For self-run Postgres, run a logical backup (`pg_dump`) daily, and run physical backups (base backups plus WAL archiving) for PITR.
- **Object storage.** Enable cross-region replication or daily snapshots. Resources are content-addressed and immutable, so a restore replaces the bucket contents from the snapshot.
- **Registry signing key.** Back up the file at `PODIUM_SIGN_KEY_PATH`, or the key file in the Secret the Helm chart mounts there, together with the store it signs. Back up the rotated file after each [rotation](#rotating-the-signing-key). The registry verifies each stored signature before it serves the row, so once the key is lost, it refuses every row the key signed, to every reader and under any consumer policy. A surviving copy of the key's public half repairs those rows, as [Signing failures](#signing-failures) states.

The default recovery objectives are an RPO of 1 hour and an RTO of 4 hours for a managed-Postgres and replicated-S3 setup. Tighten them by reducing PITR granularity or replicating at higher frequency. Loosen them by extending the PITR window.

### Test a restore

Test restores quarterly. The runbook procedure:

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
- **Step 4.** When the registry stream lists a file destination, the audit-integrity pass runs inside the registry on the `PODIUM_AUDIT_VERIFY_INTERVAL_SECONDS` schedule, which defaults to 3600 seconds. Lower it on the restored deployment to get the first pass sooner.
- **Step 5.** `podium verify <artifact>` verifies the registry's delivery signature over its delivery hash. It uses the verification key set that the default `registry-managed` provider resolves: `PODIUM_SIGNATURE_VERIFY_KEY`, one base64 key or a comma-separated list, or, when that variable is unset, the `public:` line and every `verify:` line of the key file at `PODIUM_SIGN_KEY_PATH`. Run step 5 with the production registry's public key in `PODIUM_SIGNATURE_VERIFY_KEY`, extracted as [Clustered](clustered#2-deploy-the-registry) shows.

For other providers in step 5:

- `--provider noop` is refused for every envelope.
- A Sigstore envelope passed with `--signature` takes `--provider sigstore-keyless` with three variables. `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE` names a Sigstore `trusted_root.json` that holds the certificate authorities, the transparency-log keys, and the timestamp authorities. `PODIUM_SIGSTORE_CERT_IDENTITY` and `PODIUM_SIGSTORE_CERT_OIDC_ISSUER` name the expected signer.
- Verification fails with `no trust root configured` when the trust root is absent, and with a message naming the variable when either policy variable is unset.
- The delivery signature itself is never a Sigstore envelope.

---

## Rotating the signing key

The key file at `PODIUM_SIGN_KEY_PATH` holds the registry's signing material:

- A `private:` line and a `public:` line carry the signing keypair.
- Zero or more `verify:` lines each carry a base64 Ed25519 public key trusted for verification only.
- The `public:` key and the `verify:` keys form the registry's verification key set.

The registry signs every new envelope under the `private:` key. It admits a stored row whose signature verifies under any key of the set, so a rotation that keeps the retired public key on a `verify:` line leaves every row the retired key signed loadable. Each envelope names its key in a `key_id` field, the lowercase hex of the first 8 bytes of the SHA-256 digest of the public key.

A consumer accepts a delivery signature under any key of its own set. It reads the set from `PODIUM_SIGNATURE_VERIFY_KEY`, as one key or a comma-separated list, or otherwise from the key file's `public:` and `verify:` lines.

### Rotate the key of a single registry process

1. **Generate the new key.** Run `podium admin signing-key rotate --key-file <path>` against the key file the registry reads.
   - The command reads no environment variable and no default path, so `--key-file` is required.
   - It writes a new signing keypair and moves the previous public key, and every previous `verify:` key, onto `verify:` lines.
   - It prints the new verification key set on its first line as a comma-separated list, signing key first. One `key_id=<hex> role=signing` or `key_id=<hex> role=verify` line per key follows.
   - The running registry keeps signing under the previous key, because it loads the key file only at start.
2. **Give consumers the new key set.** Set every consumer's `PODIUM_SIGNATURE_VERIFY_KEY` to the printed list, so each consumer holds the new key before the registry signs under it. A consumer that reads the key file itself resolves its set only when it starts. Restart each such consumer after step 1 and before step 3, so it reads the new `public:` line and the retired key's `verify:` line before the registry signs under the new key.
3. **Restart the registry.** It signs under the new key from its first response and admits every row the previous key signed.
4. **Re-sign the stored rows.** Run `sign-stored-rows` against the registry's configuration: as `podium admin sign-stored-rows` on a standalone host, or as `podium-server sign-stored-rows` where only the server binary is installed.
   - It re-signs under the new key every stored row that a `verify:` key verifies and whose stored bytes reproduce its stored hash.
   - It prints `rehash: verify key <key_id>: <n> row(s) still signed under it` for each verification-only key.
   - The run may take `--plan-digest=<digest>` from a preceding `sign-stored-rows --dry-run`, which binds it to the reviewed plan. The digest is optional here, because the run passes no `--include-unsigned`. [The `sign-stored-rows` command](#the-sign-stored-rows-command) describes the command.
5. **Prepare offline-only consumers.** For each consumer that runs with `PODIUM_CACHE_MODE=offline-only`, keep the retired key in its set, or clear its cache directory and refill the cache in `always-revalidate` or `offline-first`, before the retired key leaves that consumer's set. An `offline-only` consumer never calls the registry, so it refuses a cached delivery signature made under a key it no longer trusts with `materialize.signature_invalid`. A consumer in `always-revalidate` or `offline-first` treats such a record as a cache miss and refetches it, so its cache needs no clear.
6. **Remove the retired key.** Remove it only after a run of `sign-stored-rows` that exits 0 reports `0 row(s) still signed under it` for its `key_id`. A run that exits non-zero leaves that count incomplete: repair what its log names and run it again. Then delete the retired key's `verify:` line from the key file, restart the registry, and remove the key from every consumer's set. A row still signed under a key that has left the registry's set is refused with `materialize.signature_invalid`.

### Rotate the key across several registry processes

A deployment that runs more than one registry process adds the new public key to every process's set before any process signs under it.

1. Run `podium admin signing-key rotate --key-file <path> --staged-out <staged>`. Besides the rotated file, the command writes a staged file: the previous key file plus a `verify:` line for the new key.
2. Deploy the staged file to every process and restart each.
3. Set every consumer's `PODIUM_SIGNATURE_VERIFY_KEY` to the list the rotate command prints, as step 2 of the single-process procedure states, before the rotated file reaches any process.
4. Deploy the rotated file and restart each process.
5. Follow steps 4 through 6 of the single-process procedure.

[Rotating the signing key across replicas](clustered#rotating-the-signing-key-across-replicas) gives the commands for the Helm chart.

### Compromised keys

A `verify:` line trusts every envelope its key made, including an envelope made after the key was compromised. `sign-stored-rows` also re-signs a row such a key verifies under the current signing key. Remove a compromised key from every set at once, with no `verify:` line kept for it, and restore the rows it signed from a backup or re-ingest them at a new version.

### The `sign-stored-rows` command

`sign-stored-rows` reads the configuration a registry start reads, opens the same store and object storage, binds no listen address, and exits. It never generates a key.

#### Refusals

- **Signing on.** The command refuses with `config.signature_provider_unavailable`, writing nothing, when the key file at `PODIUM_SIGN_KEY_PATH` is absent. A key file with no `private:` or `public:` line, a line that does not decode, or a `public:` line that is not the public half of the `private:` line refuses it with the same code.
- **Signing off (`PODIUM_SIGN=none`).** The command rewrites without signing and signs no row. It refuses `--include-unsigned` given with `--dry-run` or `--plan-digest` with `config.signature_provider_unavailable`, writing nothing. `--include-unsigned` given alone stays a usage error.
- **Unpersisted key.** The command is subject to the refusal for a signing key that is not persisted with the store, as a registry start is.

#### Running beside a serving registry

When the store records that the first-start rewrite of stored content hashes has completed, the command may run while registry processes on this release serve the store. Each write replaces a row only when the row's stored content hash and stored signature are still the ones the command read.

When that record is absent, the command performs the rewrite in place of the first start and records its completion. It then runs only while no registry process on the previous release serves the store. The command binds nothing and cannot detect such a process, so stop every one before it runs. In that case the command also signs each unsigned row the first start would sign, which it does only when the store is the SQLite store in the key file's directory.

While no completion is recorded, a registry start over any other store that holds a manifest row is refused in either signing mode. That start rewrites no manifest row, signs no row, and records no completion, and its error names this command, which performs the rewrite in place of that start. A store that holds no manifest row records completion at its first start.

#### Signing unsigned rows

Beyond the rows it signs in place of the first start, the command signs a row that carries no signature only under `--include-unsigned`. The flag is the operator's attestation for every unsigned row the reviewed dry run lists, because an unsigned row carries no evidence of who stored it.

| Flag | Effect |
|:--|:--|
| `--dry-run` | Lists every write the command would make, with each row's current signature state and stored content hash, and makes none. The output ends with a `dry-run: plan digest sha256:<hex> over <K> row(s)` line. The dry run shows the unsigned rows before `--include-unsigned` attests them. |
| `--include-unsigned` | Signs rows that carry no signature. Outside `--dry-run`, it requires `--plan-digest=<digest>` with the value from the dry run. |
| `--plan-digest=<digest>` | Binds the run to a reviewed dry run. The command computes the digest of the plan it builds before any write. When that digest differs, it writes no row, no audit event, and no completion record, prints its own plan as `plan:` lines in the dry-run format, and exits with status 3. |

The plan digest has these consequences:

- An unsigned row stored between the dry run and the run refuses the run.
- A row stored after the run builds its plan is left as it is, unsigned, until a later reviewed run.
- A change to the key file's `verify:` lines or signing key after the dry run also refuses the run.

The command never signs a row whose envelope fails under every key of the verification key set.

#### Output and exit status

After the rewrite's summary line, with signing on, the command prints `rehash: <n> unsigned left` and one `still signed under it` line per verification-only key. With signing off, it prints only the summary line.

| Exit status | Meaning |
|:--|:--|
| 0 | Success. |
| 1 | A row holds the completion record back, a write fails, or the command is refused. |
| 2 | A usage error. |
| 3 | The plan differs from the one `--plan-digest` names. |

---

## Upgrade procedure

Schema migrations are bundled in the registry binary and applied additively on startup. A new version creates tables and columns when absent and never drops or rewrites existing ones, so an upgrade migrates the database forward in place.

1. **Prepare.** Read the changelog and the migration notes for the target version. If a migration is non-trivial, such as reshuffling embeddings or changing the audit schema, schedule a maintenance window. A release whose changelog names a migration of stored values follows [Releases that migrate stored values](#releases-that-migrate-stored-values) instead of the steps below.
2. **Canary.** Roll one registry replica to the new version. Watch metrics for 30 minutes and confirm that latency, error rate, and cache hit rate are unchanged.
3. **Roll.** Roll the rest of the replicas. Because migrations are additive, an older replica ignores the new tables and columns, so old and new replicas coexist during the roll.
4. **Verify.** After the roll completes, confirm that `/readyz` reports `ready` on every replica. When `PODIUM_AUDIT_LOG_PATH` lists a file destination, confirm that the audit-integrity pass logs no gap on its next run. When it lists none, as with the chart's `stdout` default, confirm that the receiving system's `prev_hash` check reports no gap.

### Mixed-version replicas

The additive guarantee covers the database schema and does not cover the values stored under it. A release whose changelog names a migration of those values does not permit mixed-version replicas. A release whose changelog names a rollback floor permits mixed-version replicas at or above that floor. [Rollback floor for the layer credential columns](#rollback-floor-for-the-layer-credential-columns) describes such a release.

### Releases that migrate stored values

A release whose changelog names a migration of the values stored under the schema has no canary phase. The new version computes different values over the stored bytes than every replica on the previous version, which still reads and writes them. Follow the upgrade procedure that changelog entry gives, in a maintenance window:

1. Stop every replica.
2. Back up the store.
3. Where signing is on, provision the signing key file.
4. Over any store other than the SQLite store in the key file's directory, which includes every multi-replica deployment, run `sign-stored-rows --dry-run`, review its report, and run `sign-stored-rows` with its `--plan-digest`. Do this in either signing mode, because a start over such a store that holds rows and no completion record is refused. Where the store is the SQLite store in the key file's directory, skip this step: the first start rewrites the stored values itself.
5. Start the replicas together on the new version.

A Helm chart deployment runs such a migration through the chart's migrate Job, in either signing mode. The Job holds the Deployment at zero replicas while `sign-stored-rows` rewrites the store. [Upgrading the chart from v0.4.0](clustered#upgrading-the-chart-from-v040) gives the chart procedure.

- In either signing mode, a registry pod over a store that holds manifest rows and no completion record exits at start, rewriting no manifest row, signing no row, and recording no completion.
- A GitOps controller such as Argo CD runs the same procedure as the value commits that [GitOps controllers](clustered#gitops-controllers) lists, in that order.
- Pass neither `--rollback-on-failure` (Helm v4) nor `--atomic` (Helm v3) to those upgrades, because an automatic rollback reinstalls the previous binary over rows it cannot read.

### Rolling back

Roll back by reverting the binary. The additive schema stays forward-compatible with the previous version's binary, so an older binary continues to run against a schema-migrated database.

A release whose changelog names a migration of the values stored under the schema is reverted differently. Restore the store from the backup that changelog's upgrade procedure takes, revert the registry and its consumers together, and clear each reverted consumer's cache as that changelog entry states.

Where a changelog entry names a rollback floor, the reverted binary is at or above that version, including when the upgrade started from a version below it.

---

## Read-only mode

When the Postgres primary becomes unreachable but a read replica is up, the registry falls back to read-only mode. Read endpoints continue to serve from the replica. Write endpoints are rejected with the structured error `registry.read_only`: ingest webhooks, layer admin operations, admin grants, and tenant management. `GET /v1/admin/tenants` is a read and stays available.

A health-state machine governs the transition. The registry probes the primary every 5 seconds and enters read-only mode after three consecutive failures. It leaves read-only mode automatically after three consecutive probe successes once the primary is reachable again. `PODIUM_READONLY_PROBE_INTERVAL` and `PODIUM_READONLY_PROBE_FAILURES` tune the probe interval and the failure count.

Read responses in read-only mode carry two additional headers:

- `X-Podium-Read-Only: true`
- `X-Podium-Read-Only-Lag-Seconds: <n>`: observed replication lag.

The registry records each transition as a `registry.read_only_entered` or `registry.read_only_exited` audit event. These events are logged like any other admin action and carry the same hash-chain integrity guarantees.

---

## Outbound webhook receivers

The registry emits outbound webhooks for change events to receivers that an admin registers per tenant through the receiver CRUD endpoints (`/v1/webhooks`). The registry originates the delivery request, so a receiver URL is an outbound target the registry reaches on a caller's behalf. The following settings govern it.

### Receiver URL policy

The registry validates a receiver URL at registration and re-checks it at delivery. This policy prevents a caller who can register a receiver from pointing the registry at an internal endpoint the caller would not otherwise reach. By default the registry:

- Requires the `https` scheme.
- Rejects a URL that resolves to a loopback, link-local, or private address, for example `127.0.0.0/8`, `::1`, `169.254.0.0/16`, and the RFC 1918 ranges.
- Does not follow a redirect to such a target.

A rejected target returns `registry.invalid_argument` naming the disallowed host.

### Allowing internal receivers

A deployment whose receiver is legitimately internal, such as an in-cluster relay, sets `PODIUM_WEBHOOK_ALLOWED_TARGETS`. The variable is a comma-separated allowlist of hosts or CIDRs that the policy permits in addition to public addresses.

- An entry is either a bare host matched against the URL host or a CIDR matched against the resolved addresses.
- The variable is empty by default, which keeps the strict policy.
- A malformed entry aborts boot, so the registry fails closed rather than silently widening the policy.
- The startup log reports the wired allowlist.

### Receiver registration on a deployment without authentication

The receiver CRUD endpoints require the per-tenant admin role and return `auth.forbidden` for a non-admin caller, the same authorization posture as the admin-grant endpoints. A caller the registry cannot authenticate resolves to the anonymous public identity, which fails the admin check. Receiver registration is therefore closed on a bind with no identity provider. `POST /v1/admin/grants` is itself admin-gated and cannot create the first admin on such a deployment.

To register receivers on such a deployment:

1. Seed the first admin at boot with `PODIUM_BOOTSTRAP_ADMINS`.
2. Configure an identity provider the registry verifies: `injected-session-token`, `oidc-jwt`, or `trusted-headers`.
3. Have that admin present its credential when registering a receiver.

---

## Security review checklist

Review these items before a tenant that handles sensitive content goes live. Re-run the checklist after every major release and after any change to layer config, IdP, or sandbox enforcement settings.

| Item | Check |
|:--|:--|
| OAuth identity flow | Test the device-code flow for every IdP in production use. Set token lifetimes to 15 minutes or less. Confirm that revocation propagates within 60 seconds. |
| OIDC group claim mapping | Confirm that the group claims your IdP produces arrive in the registry's audit log. Test with a non-admin user. |
| Per-layer visibility | Confirm that each layer's `visibility:` declaration is correct. Test by impersonating a non-member identity through the `injected-session-token` test harness. |
| Signature enforcement | Confirm that every consumer resolves its signature policy to `always` and that every SDK client has a verification key configured, as [Signature enforcement](#signature-enforcement) describes. Test tampering as [Tamper checks](#tamper-checks) describes. |
| Audit hash chain | `PODIUM_AUDIT_VERIFY_INTERVAL_SECONDS` is non-zero and `PODIUM_AUDIT_LOG_PATH` lists a file destination, so the registry re-verifies the chain on a schedule. Otherwise, the receiving system applies the `prev_hash` check to the shipped stream (see the [audit events reference](../reference/audit-events#receiver-checks)). Alert on the `audit.gap_detected` event and on the "audit integrity ALERT" log line. [Hash chain](observability#hash-chain) describes both checks. |
| Webhook signing | Use a unique Git provider webhook HMAC secret per layer. Test with an invalid signature, and expect `ingest.webhook_invalid`. |
| Outbound receiver URL policy | Receiver CRUD is admin-gated, and `PODIUM_WEBHOOK_ALLOWED_TARGETS` lists only the internal receivers the deployment intends. Test by registering an `http` or private-address receiver, and expect `registry.invalid_argument`. |
| Sandbox profile honoring | Confirm that the hosts in production honor `sandbox_profile` for non-`unrestricted` artifacts. Test with a `read-only-fs` artifact and confirm the host enforces it. |
| Object-storage credentials | Use IAM roles or short-lived credentials, never static keys. Confirm the bucket policy denies public access. |
| Backup encryption | Encrypt Postgres backups and S3 object versioning at rest. Match the PITR window to your RTO. |
| Scope preview gating | Set `tenant.expose_scope_preview` deliberately per tenant. Set it to `false` for tenants where aggregate visibility counts would leak signal. |

### Signature enforcement

- Each consumer's MCP server resolves the signature policy from `PODIUM_VERIFY_SIGNATURES`, then from `defaults.verify_signatures` in its `sync.yaml`, then from an `always` fallback. The values are `never` and `always`. Confirm that the resolved policy is `always` on every consumer.
- A machine where an earlier release's standalone bootstrap wrote `verify_signatures: never` into `~/.podium/sync.yaml` keeps that setting until the line is removed. `podium-mcp` announces that `never` with a startup line naming the file.
- The registry does not read `PODIUM_VERIFY_SIGNATURES`.
- Server-source `podium sync` applies the same policy and key resolution as `podium-mcp`, with the same `always` default. A filesystem-source sync runs no delivery check.
- The Python and TypeScript SDKs read `PODIUM_VERIFY_SIGNATURES`. They default to `always` with a verification key configured and to `never` otherwise, so confirm that a key is configured on every SDK client.

The signature a consumer verifies is the registry's delivery signature. The registry mints it with its registry-managed key over the delivery hash of each response it serves.

- Every node that serves reads holds the registry signing key. A read-only replica topology, if one is ever deployed, places the key on each replica.
- A registry running with `PODIUM_SIGN=none` serves each delivery record unsigned, and `podium-mcp` still recomputes the delivery hash on every load.
- The delivery signature is a registry-managed envelope whatever key model signed the artifact at ingest. A consumer verifies it with `PODIUM_SIGNATURE_PROVIDER=registry-managed` and the registry's verification key set in `PODIUM_SIGNATURE_VERIFY_KEY`. `podium-mcp` refuses to start under `sigstore-keyless` with `config.invalid`.
- A registry process signs under a rotated key from its next response onward, so each consumer adds the new key to its set before the rotation, as [Rotating the signing key](#rotating-the-signing-key) states.

### Tamper checks

Run each tamper check through a full load, and expect the refusal from the registry. A full load is either a direct HTTP `load_artifact` GET with no `If-None-Match` header or a consumer whose cache is empty.

- **Altered response.** A response altered in transit fails at the consumer with `materialize.content_hash_mismatch`. One whose signature was altered fails with `materialize.signature_invalid`.
- **Stored-row admission.** The registry admits each stored row before it serves the row's content. It recomputes the content hash, checks each bundled resource's stored hash and size against its bytes, and then verifies the signature. Its refusal reaches every reader's full load, `podium sync` and the SDKs included. A HEAD and a matching conditional GET are answered without admission, so a consumer that already holds the row under an unchanged content hash keeps serving that copy.
- **Altered stored row.** On any registry, a row whose stored bytes were altered while its stored content hash was left unchanged is refused with `materialize.content_hash_mismatch`. On a signing registry, a row altered together with its hash is refused with `materialize.signature_invalid`, and one whose stored signature was removed with `materialize.signature_missing`. A registry with `PODIUM_SIGN=none` serves a row altered together with its hash.
- **Altered object-storage body.** A bundled-resource body held in object storage and altered there is refused by the registry on a full load with `materialize.content_hash_mismatch`. The object-storage copy of a resource the row holds inline is never served, because the registry serves that resource inline from the admitted row on `load_artifact` and on each `artifacts:batchLoad` item.
- **Body altered after admission.** An object-held body altered after the registry admitted the row, and a manifest-body object replaced at its key, which admission does not read, fail at the fetch in `podium-mcp` with `materialize.fetch_failed` and a `content hash mismatch` message.
- **Body checks by other clients.** Server-source `podium sync` refuses a manifest-body object whose bytes do not hash to the content hash its link carries. The sync then fails with `load_artifact <id>: manifest body content hash mismatch` without materializing any artifact. `podium sync` compares no fetched bundled-resource body with its hash, and the SDKs compare no fetched body with its hash, so each receives such a body unchecked. An operator who needs that check reads the artifact through `podium-mcp`.

Admission checks a row's content and does not attest which row answers a request. A party that can write the metadata store can therefore withhold a row, relabel an admissible row, or have an older admissible row served in place of a newer one.

### Limits of stored-row admission

Stored-row admission has limits against a party that can write the metadata store without holding the signing key. These limits are known and are not closed by admission.

- Such a party can withhold, relabel, re-layer, or roll back an admissible row.
- Where the store is the SQLite store in the key file's directory, it can also delete the first-start rewrite's completion record, so that the next start signs the unsigned rows it wrote. On every other store, such a start refuses in either signing mode and signs nothing.
- On any store, a later `sign-stored-rows --include-unsigned` signs every unsigned row its reviewed dry run lists, including one such a party stored before that dry run. Read the rows the dry run lists before passing its plan digest.
- An unsigned row such a party stores between the dry run and the run changes the plan digest, and the run refuses with exit status 3. A row stored after the run builds its plan is absent from that plan and stays unsigned.
- A `sign-stored-rows` run also re-signs rows signed under a `verify:` key. While a compromised key stays on a `verify:` line, a run re-signs rows its holder stored.

Restrict write access to the metadata store and to its backups.

---

## Troubleshooting

### Operational issues

| Symptom | Cause | Fix |
|:--|:--|:--|
| Embedding requests are rate-limited during a bulk reingest. | OpenAI and Voyage rate-limit aggressively under bulk reingest. | Stagger `podium layer reingest` across layers, or switch to `ollama` pointed at a local model server for inference during reingest storms. |
| The pgvector index bloats. | Many embeddings have churned. | `REINDEX` the vector index quarterly, or set up `auto_vacuum` aggressively. |
| Stored embeddings come from a different provider or model than the one configured. | `PODIUM_EMBEDDING_PROVIDER`, the embedding model, or a self-embedding backend's hosted model changed. | Run `podium admin reembed` with no scoping flag in every tenant. On Pinecone, Weaviate, or Qdrant, first point the registry at a fresh index or collection, because those backends record no model per row and every tenant shares the index. [Switching backends on a running deployment](vector-backends#switching-backends-on-a-running-deployment) gives the procedure. |
| Materialization latency on developer machines sits well above the SLO. | `PODIUM_CACHE_DIR` points at a cache on a network filesystem or a slow disk. | Keep the cache at the default `~/.podium/cache/` on local disk. |
| Ingests are missing after a read-only window. | The registry refuses webhook deliveries while it is read-only, and the Git host does not deliver them again on its own. GitHub, for example, does not automatically redeliver a failed delivery. | Trigger a manual `podium layer reingest` after recovery. |
| Ingest is rejected after a force-push to a Git source layer. | `force_push_policy: strict` is configured. The default policy is tolerant: it emits a `layer.history_rewritten` event and preserves prior commits in the content store. | Coordinate force-pushes with authors. |
| Intermittent `auth.token_expired` errors for tokens near expiry. | NTP drift on a registry node. The registry validates a token's expiry against its own clock with no configured leeway. | Keep registry hosts on NTP and monitor clock skew. |
| An OIDC group membership change has not taken effect. | Membership changes propagate through SCIM push from the IdP to `/scim/v2/`. The registry has no pull side and no sync command, so an IdP that does not push leaves group membership to update on the user's next login. | Trigger a push from the IdP's provisioning console when a membership change has to land immediately. |

### Signing failures

#### `podium-mcp` refuses to start with `config.signature_provider_unavailable`

Under a policy above `never`, the bridge resolves its verification material once at start. It reads `PODIUM_SIGNATURE_VERIFY_KEY` or, when that variable is unset, the `public:` line and every `verify:` line of the registry key file at `PODIUM_SIGN_KEY_PATH` (default `~/.podium/standalone/registry-signing.key`).

- **Cause.** The usual cause is a missing key: a consumer on a machine other than the registry's, with no `PODIUM_SIGNATURE_VERIFY_KEY`. A `PODIUM_SIGNATURE_VERIFY_KEY` with an entry that is empty or does not decode, and `PODIUM_SIGNATURE_PROVIDER=noop`, refuse the start the same way.
- **Fix.** Supply the key set, or set `PODIUM_VERIFY_SIGNATURES=never` against a registry that does not sign.

A bridge whose set lacks the key the registry signs under starts, and refuses each load with `materialize.signature_invalid` instead.

#### Loads fail with `materialize.signature_missing`

- **Cause.** The served artifact carries no signature and the policy is `always`. Either the registry runs with `PODIUM_SIGN=none`, or it stored the row unsigned before signing was on, in which case the registry itself refuses the row.
- **Fix for an unsigned registry.** Set `PODIUM_VERIFY_SIGNATURES=never` on its consumers.
- **Fix for rows stored before signing.** Run `sign-stored-rows --include-unsigned --dry-run` and review the rows it lists. Then run `sign-stored-rows --include-unsigned --plan-digest=<digest>` with the digest on its last line, as [The `sign-stored-rows` command](#the-sign-stored-rows-command) states. Alternatively, ingest a new version of the artifact.

A load the signature check refuses records no local `artifact.loaded` event. A load whose inline-resource decode or large-resource fetch fails records none either. An object-storage outage therefore leaves the local stream silent for every large-resource load, where it previously carried one line each. A load the sandbox or runtime gate refuses still records one.

#### The key file is present but is not the key that signed the stored rows

- **Cause.** The registry's key file holds a different key from the one that signed the stored rows.
- **Fix on the SQLite store in the key file's directory.** The start is not refused. The first start's rewrite of stored content hashes leaves every such row at its stored hash, with one log line per row classified `signature_unverified`. Restoring the right key alone does not repair a row still at the previous content hash. List the public half of the key that signed the rows on a `verify:` line of the key file, restart the registry, and run `sign-stored-rows`. It moves each such row to the new content hash and re-signs it under the current key.
- **Fix on any other store.** The start is refused while no completion is recorded and the store holds rows, and the `sign-stored-rows` dry run classifies such rows `signature_unverified`. List the signing key's public half on a `verify:` line of the key file, run the dry run, and run `sign-stored-rows` with its `--plan-digest`, repeating the dry run's `--include-unsigned` setting.
- **Fix from a backup.** On the SQLite store in the key file's directory, restoring the backup the upgrade procedure takes and starting with the key that signed the rows also repairs them, at the cost of every write made since that backup. Over any other store, in either signing mode, the restored store holds no completion record. Its start is then refused until a `sign-stored-rows` dry run and a run with its `--plan-digest`, under that key, record completion.

A new version of each affected artifact is the repair only where no copy of that key's public half survives.

#### The key file is lost after the first-start rewrite has completed

- **Symptom.** The registry starts, but refuses every row the retired key signed with `materialize.signature_invalid`, to every reader and under any consumer policy. Setting `PODIUM_VERIFY_SIGNATURES=never` does not make those rows load.
- **Cause.** The start is not refused, because the generated-key guard returns once the rewrite's completion record is set. Such a start generates a fresh keypair, signs new ingests under it, and logs nothing about the retired key.
- **Fix.** Stop the registry, restore the key file from the backup, and start it again, because the registry loads its signing key once at start. Restoring the file leaves refused every row the fresh key signed in the meantime. List the fresh key's public half on a `verify:` line of the restored file and restart the registry to admit those rows.
- **Fix when only the public half survives.** The lost key's public half can survive, for example, in a consumer's `PODIUM_SIGNATURE_VERIFY_KEY`. List it on a `verify:` line of the key file the registry now holds and restart the registry, which admits those rows. Then run `sign-stored-rows`, which re-signs them under the current key, so the line can be removed as [Rotating the signing key](#rotating-the-signing-key) states.

#### The registry refuses to start because the signing key would be generated under the process's home

- **Cause.** Signing is on and `PODIUM_SIGN_KEY_PATH` is unset, and the store is not the SQLite store beside the default key. The error names `PODIUM_SIGN_KEY_PATH` and `PODIUM_SIGN=none`, and says the key would be generated under the process's home, outside the directory that holds the store. It fires whether or not the store holds a signed row.
- **Fix.** Set `PODIUM_SIGN_KEY_PATH` to a file on the store's persistent storage, or set `PODIUM_SIGN=none`. With `PODIUM_SIGN=none`, a store other than the SQLite store beside the default key that holds rows and no completion record is then refused by the unmigrated-store refusal below, whose repair is `sign-stored-rows`.

The published container image run with its default store keeps the key and the store in the same directory, so it starts.

Two other refusals name `PODIUM_SIGN_KEY_PATH` and mean that the store holds signed rows. One says the rows need their content hash rewritten while no signer is configured. The other says `PODIUM_SIGN_KEY_PATH` names no file. The first comes from a start only over the SQLite store in the key file's directory, and otherwise from the `sign-stored-rows` dry run. Each calls for the key that signed those rows.

#### The registry refuses to start and names `sign-stored-rows`

- **Cause.** No completion of the rewrite of stored content hashes is recorded, and the store holds a manifest row, including a soft-deleted one. The store is also not the SQLite store in the directory of the key file at `PODIUM_SIGN_KEY_PATH`, or at the default key location when that variable is unset. The refusal applies in either signing mode. The error names the store, a stored row, and the key location.
- **What the refused start does.** It applies the additive schema and seeds the default tenant and the bootstrap grants. It rewrites no manifest row, signs no row, and records no completion.
- **Other refusals first.** When the key-persistence refusal above, or the refusal for a signing key that did not exist before the start, also applies, the start reports that refusal first.

A co-located SQLite store runs the rewrite at its first start, and a store that holds no manifest row records completion at its first start. For any other store, repair it as follows:

1. Stop every registry process on the previous release.
2. With signing on, when no file exists at the key location, create it with `podium admin signing-key generate --key-file <path>`. Neither the refused start nor `sign-stored-rows` generates a key.
3. Run `sign-stored-rows --dry-run` and review its report. With signing on, add `--include-unsigned` to attest the unsigned rows. With signing off, run it with `PODIUM_SIGN=none` in its environment, because `podium serve --sign none` sets the mode only for its own process.
4. Run `sign-stored-rows` with `--plan-digest` set to the digest on the report's last line, with the same `--include-unsigned` setting and environment.
5. Start the registry again.

On a Helm chart deployment, the migrate Job runs both commands, as [Upgrading the chart from v0.4.0](clustered#upgrading-the-chart-from-v040) shows.

#### The registry or `sign-stored-rows` refuses with `config.signature_provider_unavailable`, naming the key file

- **Cause.** The key file has no `private:` line or no `public:` line, one of its lines does not decode, or its `public:` line is not the public half of its `private:` line.
- **Fix.** Restore the file from the backup, or repair the line the message names.

### Startup refusals

#### The registry refuses to start with `config.scim_store_unavailable`

- **Cause.** The error names `PODIUM_SCIM_STORE_PATH` and the cause: a non-empty file that does not parse, a path that is a directory or has a non-directory parent, or a parent directory that cannot be created or written.
- **Fix.** Fix the permissions, or point the variable at a valid path. For a corrupt file, move it aside and start the registry, which loads a missing file as an empty directory. Then trigger a full push from the IdP's provisioning console to repopulate users and groups, as the SCIM entry under [Operational issues](#operational-issues) describes.

Unsetting the variable keeps the directory in memory, so it is lost on every restart.

When the store becomes unwritable after startup, the receiver answers a create or replace push with HTTP 400 `invalidValue`, and that record is served until restart and then lost. It answers a delete push with HTTP 500, and the deleted record returns on restart. In either case, fix the directory and trigger a full push.

#### Audit configuration refusals

The refusals `config.audit_sink_unavailable`, `config.audit_anchor_key_unavailable`, and `config.audit_anchor_key_shared` are described in [Troubleshooting](observability#troubleshooting) on the Audit and observability page.

### Public-mode misconfiguration

A misconfigured public-mode deployment is the most common security-relevant operational anomaly, because the registry serves correctly, to everyone.

**Detection:**

- `/healthz` returns `mode: public`.
- Audit events for read calls show `caller.identity: "system:public"` and the flag `caller.public_mode: true`.
- The startup banner shows the public-mode warning.
- `podium status` surfaces the flag.

**Mitigation:**

1. Confirm whether public mode was the intended deployment posture. If it was, no action is needed, because the audit log already records the intent.
2. If public mode was not intended, such as through a misconfigured environment variable, a copy-pasted CLI flag, or an accidental container image tag, stop the registry. Remove `--public-mode` or unset `PODIUM_PUBLIC_MODE`, and restart. The registry refuses mid-run flips, so a restart is mandatory.
3. If public mode was running on an internet-exposed registry, which the safety check prevents unless `--allow-public-bind` was set, treat it as a security incident. Rotate any signing keys that were in scope, audit the access log for unfamiliar IPs, and proceed per the organization's incident-response procedure.

**Prevention.** Set `PODIUM_NO_AUTOSTANDALONE=1` in container-image and Helm-chart deployments, and use `--strict` to refuse anything but explicitly configured deployments. Make production CI templates fail fast when `PODIUM_PUBLIC_MODE` appears in an environment list.

---

## Upgrading from an earlier release

### Rollback floor for the layer credential columns

From 0.7.0, the SQLite and Postgres metadata stores write a `git` layer's URL credential apart from the rest of its remote. The lowest version that runs against a database 0.7.0 has written to is 0.6.0, both for a rollback and for mixed-version replicas.

#### How 0.7.0 stores the credential

- For a `repo` that is a URL with a scheme and userinfo, and that a layer read does not report as `[redacted]`, `layer_configs.repo_userinfo` receives the userinfo and `layer_configs.repo` receives the rest of the URL.
- Every other `repo` is held whole in `repo`. This covers an scp-like remote, a filesystem path, and a value reported as `[redacted]`.
- `repo_userinfo` holds the credential unencrypted. A SQL export of `layer_configs` needs both columns to reproduce a layer's remote, and it carries a secret when it includes `repo_userinfo`.

Each 0.7.0 start whose listener binds rewrites, before it serves, every row that holds such a URL whole in `repo` into that form, a soft-deleted layer's row included. It logs how many rows it rewrote.

- The same start sets to NULL a `repo_userinfo` that sits beside a `repo` held whole, such as an scp-like remote or a path, because the registry ignores that column when it reads such a row. The logged count includes those rows.
- The start changes no other column, and the layer is reported and cloned as before.
- A row that another process writes while the start runs is left as that process wrote it.
- The rewrite moves the credential between two columns of one row. It does not remove the credential from a backup, a replica, or storage the database has not yet reused.

The rewrite is a change of stored form that 0.6.0 already reads. It records no completion and needs no `sign-stored-rows` run. [Releases that migrate stored values](#releases-that-migrate-stored-values) does not apply: the canary phase runs as usual, and the store needs no backup restore to roll back.

#### Mixed 0.6.0 and 0.7.0 replicas

Replicas on 0.6.0 and on 0.7.0 that share a Postgres store read and clone identically. A 0.6.0 replica writes the whole URL to `repo` on every layer write it makes, so a row can change form in either direction during the roll. A row that a 0.6.0 replica writes after the last 0.7.0 replica started holds the whole URL until its next 0.7.0 write or the next 0.7.0 start. Restart a 0.7.0 replica after the last 0.6.0 replica has stopped to rewrite those rows at once.

A request that a 0.6.0 replica serves during the roll is answered as 0.6.0 answers it:

- 0.6.0 accepts a `repo` that carries the credential in its URL, which 0.7.0 refuses.
- 0.6.0 ignores the `repo_username`, `repo_token`, and `clear_repo_credential` members. A registration that carries those members therefore stores the remote with no credential. An update that carries them reports success and leaves the stored credential as it was.
- A registration that sends back the remote `podium layer list` reports for a layer that holds a credential is refused with `details.constraint: "redacted_repo"`.

Make every request that sets, rotates, or removes a credential, or that re-registers a layer holding one, after the last 0.6.0 replica has stopped. Repeat at that point any such request made during the roll.

#### Declared layers

0.6.0 and earlier ignore the `source.git.username` and `source.git.token` keys of a declared layer. 0.7.0 reads a credential in the userinfo of `source.git.repo` and one in the two keys.

- Keep a declared private layer's credential in `source.git.repo` through the rollout, and for as long as a 0.6.0 process may start on the configuration. Then move it to the two keys.
- A 0.6.0 process that starts on a configuration that uses the keys, on a rollback or as a replica that restarts during the roll, writes the declared layer with no credential. Its private remote then fails with `ingest.source_unreachable` on every replica that shares the store until a 0.7.0 process starts.
- Before rolling back to 0.6.0, move each declared credential back into the percent-encoded userinfo of `source.git.repo` and remove both keys.

#### Upgrading directly from 0.5.2 or earlier

An upgrade from 0.5.2 or earlier directly to 0.7.0 stops every replica before the first 0.7.0 replica starts, and its rollback target is 0.6.0. On a Helm chart deployment, such an upgrade passes neither `--rollback-on-failure` nor `--atomic`, because an automatic rollback reinstalls a binary below the floor. An upgrade through 0.6.0 has neither restriction.

#### Running 0.5.2 against a database 0.7.0 has written to

This combination is unsupported. A 0.5.2 process does not read `repo_userinfo`, with the following effects:

- **Layers registered through the API or the CLI.** After the first 0.7.0 start, every such layer whose `repo` is a URL with a scheme and userinfo holds its credential apart. 0.5.2 clones such a layer without the credential, so a private remote fails with `ingest.source_unreachable`. A registration that carries the layer's listed remote is not refused.
- **Layers declared with the credential in `source.git.repo`.** The layer is written from the configured URL at each start and clones as before.
- **Layers declared with `source.git.username` and `source.git.token`.** The layer is written with no credential, because 0.5.2 ignores both keys.

On SQLite, a 0.5.2 write of the layer clears `repo_userinfo`. Such a write is an update, a reorder, a re-registration, a start for a declared layer, or a completed ingest. The layer then needs its credential set again:

- On 0.6.0, by a registration with the full URL.
- On 0.7.0, for an `http` or `https` remote, with `podium layer update --repo-username <name> --repo-token-file <path>`. `--repo-username` names the username the provider expects, because the cleared row holds none.
- On 0.7.0, for an `ssh://` remote, by a registration with the full `ssh://` URL, login name included, because `podium layer update` takes no credential member on such a layer.

On Postgres, a 0.5.2 write leaves the column as it is. If a re-registration under 0.5.2 replaced the layer's `repo` with a different URL that carries no userinfo, the column still holds the earlier credential, and 0.6.0 or later joins it to the new remote when it reads the row. Before that layer is ingested on 0.6.0 or later, remove or replace the stale credential:

- **On 0.6.0.** Re-register the layer with the full URL of the new remote, or with the userinfo-free URL and `--force-repo-overwrite` (`force_repo_overwrite: true`). A re-registration with the userinfo-free URL and no override is refused with `details.constraint: "redacted_repo"`. The 0.6.0 CLI has neither update flag, and a 0.6.0 registry answers an update that carries either member with success and changes nothing.
- **On 0.7.0, for an `http` or `https` remote.** Remove the credential with `podium layer update --clear-repo-credential`, or replace it with `podium layer update --repo-username <name> --repo-token-file <path>`. The replacement names the username because an update that omits it keeps the stale one. A re-registration with the userinfo-free URL alone is the re-registration with the listed remote that [Private repositories](layer-sources#private-repositories) describes, and it keeps the stored credential, here the stale one. One that also passes `--clear-repo-credential`, or `--repo-username` with `--repo-token-file`, stores only what it carries.
- **On 0.7.0, for an `ssh://` remote.** Re-register the layer with that URL as wanted, without userinfo or with its login name, which stores no stale credential, because `podium layer update` takes no credential member on such a layer.

An update does not change `repo`, so a layer that was only updated under 0.5.2 needs no re-registration. A layer declared in the registry configuration needs none either, because each start writes both columns from the declaration. 0.6.0 reads the declaration from `source.git.repo` alone, and 0.7.0 reads it from `source.git.repo` or from `source.git.username` and `source.git.token`.

---

## When to escalate

Escalate to support, or open an issue, in the following cases:

- **Audit chain gap.** The scheduled integrity pass records an `audit.gap_detected` event. Treat it as a security incident, and capture evidence before any cleanup.
- **Repeated signature or content-hash refusals.** Check each for an upgrade or key cause first, as the list below describes.
- **Unexplained latency.** Sustained latency degradation that does not track CPU, memory, or database load often indicates a query-plan regression after a Postgres major upgrade.
- **Out-of-band ingest events.** Artifacts that appear in the registry without a corresponding `artifact.published` outbound webhook indicate a webhook configuration or processing failure.

Check signature and content-hash refusals as follows before escalating:

- **A refusal the registry logs.** The registry logs the tenant, artifact ID, version, and reason of a `materialize.content_hash_mismatch`, `materialize.signature_invalid`, or `materialize.signature_missing` refusal. Compare the artifact ID and version against the per-row `rehash:` lines the rewrite logged. Those lines come from the first start's log only where the store is the SQLite store in the key file's directory. Otherwise they come from the `sign-stored-rows` run's log, or from the migrate Job's log on a chart deployment. A row that summary names is upgrade residue, repaired as the release's upgrade note states, and is not escalated. Compare the registry's key file with the backed-up key as well, because the registry refuses every row signed under a key outside its verification key set. Repair a mismatch as the signature-storm entry in `deploy/runbook.md` states.
- **A registry refusal for a row the summary does not name.** Under the key that signed the store, such a refusal means the row's stored bytes were altered, in the metadata store or in object storage, or an object-held body is missing. Read the refusal's logged reason, check the object store for the row's bodies, and then escalate.
- **A consumer-returned `materialize.content_hash_mismatch`.** This, or `materialize.fetch_failed` naming a large-resource `content hash mismatch`, means a response or an object-storage body was altered after the registry admitted the row. Escalate it.
- **A consumer-returned `materialize.signature_invalid` or `materialize.signature_missing`.** First confirm that the consumer's `PODIUM_SIGNATURE_PROVIDER` is `registry-managed`, that the key set it resolves contains the key the registry signs under, and that the registry does not run with `PODIUM_SIGN=none` under an `always` policy. With that configuration correct, escalate it as a forged or stripped signature.

For each case, capture the relevant log lines with their trace IDs, the affected tenant ID, the affected artifact IDs, and a brief timeline.
