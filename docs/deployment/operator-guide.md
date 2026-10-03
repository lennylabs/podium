---
title: Operator guide
nav_order: 8
description: "Day-two operations for a clustered Podium deployment: capacity, monitoring, alerts, backup, upgrades, security review, common pitfalls."
---

# Operator guide

Day-two operations for a [clustered](clustered) Podium deployment.

For a single machine running one binary, see [Single node](single-node) instead.

---

## Capacity planning

Baseline (10K artifacts, 100 QPS, 1 GB Postgres, 500 GB object storage on a 3-replica deployment with `db.m5.large` equivalent) is the starting point. Beyond that:

| Dimension | Threshold | What to do |
|:--|:--|:--|
| Artifacts | 100K | Increase Postgres instance size; review pgvector index parameters; consider sharding embeddings. |
| QPS | 1K | Scale registry replicas horizontally; put a CDN in front of object storage for resource bytes. |
| QPS | 10K | Review search query patterns; consider dedicated Elasticsearch for BM25 with pgvector (or Pinecone/Weaviate/Qdrant) for vector. |
| Tenants | 50 | Confirm `RegistryStore` connection pool is sized appropriately; increase pgbouncer pool if used. |
| Audit volume | 1M events/day | Set retention explicitly; ship the registry audit stream to an external SIEM by setting `PODIUM_AUDIT_LOG_PATH` to an `http(s)://` endpoint, which selects the registry endpoint sink instead of the on-disk hash-chained file. |

Embeddings dominate Postgres growth at scale. Each artifact's text projection becomes a float vector whose dimension depends on the configured provider (768 for `nomic-embed-text` via Ollama, 1024 for `voyage-3` or `embed-v4`, 1536 for `text-embedding-3-small`). At ~6 KB per row including metadata at 1536 dim, 100K artifacts is ~600 MB of embeddings.

Object-storage growth is dominated by bundled resources. Most teams' p99 artifact size sits well under the 256 KB inline cutoff, so the inline manifest body fits in Postgres and only larger resources go to S3.

---

## Monitoring

The registry serves Prometheus metrics at `/metrics` by default, and `PODIUM_METRICS=false` removes the endpoint. The MCP server is a stdio process with no listener of its own, so it serves its metrics only when `PODIUM_MCP_METRICS_ADDR`, the `--metrics-addr` flag, or the `metrics-addr` config-file key names a bind address. The reference Grafana dashboard is in the repository at `deploy/grafana-dashboard.json`. Key signals:

**Registry:**

- `podium_request_duration_seconds{endpoint}`: per-endpoint latency histogram. Watch `load_domain`, `search_domains`, and `search_artifacts` at p99 < 200 ms each, and `load_artifact` at p99 < 500 ms for the manifest alone and p99 < 2 s when bundled resources are fetched on a cache miss. The bulk route `/v1/artifacts:batchLoad` reports under the label `batch_load` and carries no SLO budget.
- `podium_request_total{endpoint}` and `podium_request_errors_total{endpoint}`: request volume and error count per endpoint. The error counter increments for any response status at or above 400, so the per-endpoint error rate is `rate(podium_request_errors_total) / rate(podium_request_total)`.
- `podium_visibility_denied_total`: reads rejected by visibility filtering. This signal is informational; a sudden spike usually means a layer config error rather than an authorization issue.
- `podium_cache_hits_total` and `podium_cache_misses_total`: hits and misses on the registry's cache of `DOMAIN.md` import-glob expansions. The cache key combines the include and exclude patterns with a fingerprint of the caller's visible artifact-ID set, so the ratio falls when ingest changes that set frequently, when callers see different subsets of the catalogue, or when a domain's globs vary from request to request. The MCP server reports its own resolution-cache hits and misses under the same two names.
- `podium_ingest_success_total` and `podium_ingest_failure_total`: ingest attempts that succeeded or failed. Flag a recent uptick in the failure counter.
- `podium_vector_outbox_depth`: pending rows in the external-vector-backend outbox. A rising depth indicates the drain worker is falling behind. The gauge reads 0 on a collocated backend that uses no outbox.

**MCP server:**

- `podium_mcp_requests_total{tool}` and `podium_mcp_request_errors_total{tool}`: per-tool call volume and error count at the bridge.
- `podium_mcp_request_duration_seconds{tool}`: per-tool call latency at the bridge.

---

## Alerting

A reasonable starting set, tuned for the baseline deployment:

```yaml
# Critical: page on-call
- alert: PodiumDown
  expr: up{job="podium-registry"} == 0
  for: 2m

- alert: PodiumLoadArtifactSLOBreached
  expr: histogram_quantile(0.99, rate(podium_request_duration_seconds_bucket{endpoint="load_artifact"}[5m])) > 0.5
  for: 5m

- alert: PodiumHighErrorRate
  expr: sum(rate(podium_request_errors_total[5m])) / sum(rate(podium_request_total[5m])) > 0.05
  for: 5m

# Warning: investigate within hours
- alert: PodiumIngestFailing
  expr: increase(podium_ingest_failure_total[1h]) > 5
  for: 15m

- alert: PodiumVectorOutboxBacklog
  expr: podium_vector_outbox_depth > 1000
  for: 10m

# Informational: review weekly
- alert: PodiumLowCacheHitRatio
  expr: sum(rate(podium_cache_hits_total[1h])) / (sum(rate(podium_cache_hits_total[1h])) + sum(rate(podium_cache_misses_total[1h]))) < 0.5
```

The chart at `deploy/helm/podium` does not carry alert rules, so add these to your own Prometheus rule set and tune the thresholds to your SLOs.

---

## Backup and restore

- **Postgres.** Managed services handle this. Enable point-in-time recovery (PITR) with at least 7 days of retention. For self-run Postgres, run logical (`pg_dump`) daily and physical (base backups + WAL archiving) for PITR.
- **Object storage.** Enable cross-region replication or daily snapshots. Resources are content-addressed and immutable, so restore is straightforward: replace the bucket contents from the snapshot.
- **Registry signing key.** The file at `PODIUM_SIGN_KEY_PATH`, or the key file in the Secret the Helm chart mounts there. Back it up with the store it signs, and back up the rotated file after each [rotation](#rotating-the-signing-key). Once the key is lost, the registry refuses every row it signed, to every reader and under any consumer policy, because the registry verifies each stored signature before it serves the row. A surviving copy of the key's public half repairs those rows, as [Signing failures](#signing-failures) states.
- **Default RPO 1h / RTO 4h** for a managed-Postgres + replicated-S3 setup. Tighten by reducing PITR granularity or replicating at higher frequency; loosen by extending the PITR window.

Test restores quarterly. The runbook procedure:

```
1. Spin up a non-production registry pointed at a fresh Postgres + a fresh
   S3 bucket, with PODIUM_SIGN_KEY_PATH naming a copy of the production
   signing key restored from the backup.
2. Restore Postgres from PITR to T-1h.
3. Sync the production S3 bucket to the fresh one (rclone or aws s3 sync).
4. Watch the restored registry's log for the scheduled audit-integrity pass.
   A hash-chain gap logs "audit integrity ALERT" and records an
   audit.gap_detected event.
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

Step 1 restores the signing key because a registry with signing on, a Postgres store, and no key path is refused at start, and because the drill then also exercises the key backup. In step 5, `podium verify <artifact>` verifies the registry's delivery signature over its delivery hash under the verification key set the default `registry-managed` provider resolves: `PODIUM_SIGNATURE_VERIFY_KEY`, one base64 key or a comma-separated list, or, when that variable is unset, the `public:` line and every `verify:` line of the key file at `PODIUM_SIGN_KEY_PATH`. Run step 5 with the production registry's public key in `PODIUM_SIGNATURE_VERIFY_KEY`, extracted as [Clustered](clustered#2-deploy-the-registry) shows. `--provider noop` is refused for every envelope. A Sigstore envelope passed with `--signature` takes `--provider sigstore-keyless` with `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE`, a Sigstore `trusted_root.json` that holds the certificate authorities, the transparency-log keys, and the timestamp authorities, and with `PODIUM_SIGSTORE_CERT_IDENTITY` and `PODIUM_SIGSTORE_CERT_OIDC_ISSUER`, which name the expected signer. Verification fails with `no trust root configured` when the trust root is absent, and with a message naming the variable when either policy variable is unset. The delivery signature itself is never a Sigstore envelope.

The audit-integrity pass runs inside the registry on the `PODIUM_AUDIT_VERIFY_INTERVAL_SECONDS` schedule, which defaults to 3600 seconds. Lower it on the restored deployment to get the first pass sooner.

---

## Rotating the signing key

The key file at `PODIUM_SIGN_KEY_PATH` carries the signing keypair on a `private:` line and a `public:` line, and zero or more `verify:` lines, each a base64 Ed25519 public key trusted for verification only. The `public:` key and the `verify:` keys form the registry's verification key set. The registry signs every new envelope under the `private:` key and admits a stored row whose signature verifies under any key of the set, so a rotation that keeps the retired public key on a `verify:` line leaves every row the retired key signed loadable. Each envelope names its key in a `key_id` field, the lowercase hex of the first 8 bytes of the SHA-256 digest of the public key. A consumer accepts a delivery signature under any key of its own set, which it reads from `PODIUM_SIGNATURE_VERIFY_KEY` as one key or a comma-separated list, or otherwise from the key file's `public:` and `verify:` lines.

Rotate the key of a registry that runs as one process in this order:

1. Run `podium admin signing-key rotate --key-file <path>` against the key file the registry reads. The command reads no environment variable and no default path, so `--key-file` is required. It writes a new signing keypair and moves the previous public key, and every previous `verify:` key, onto `verify:` lines. It prints the new verification key set on its first line as a comma-separated list, signing key first, followed by one `key_id=<hex> role=signing` or `key_id=<hex> role=verify` line per key. The running registry keeps signing under the previous key, because it loads the key file only at start.
2. Set every consumer's `PODIUM_SIGNATURE_VERIFY_KEY` to the printed list, so each consumer holds the new key before the registry signs under it. A consumer that reads the key file itself resolves its set only when it starts, so restart each such consumer after step 1 and before step 3. It then reads the new `public:` line and the retired key's `verify:` line before the registry signs under the new key.
3. Restart the registry. It signs under the new key from its first response and admits every row the previous key signed.
4. Run `sign-stored-rows` against the registry's configuration, as `podium admin sign-stored-rows` on a standalone host or as `podium-server sign-stored-rows` where only the server binary is installed. It re-signs under the new key every stored row that a `verify:` key verifies and whose stored bytes reproduce its stored hash, and prints `rehash: verify key <key_id>: <n> row(s) still signed under it` for each verification-only key. The run may take `--plan-digest=<digest>` from a preceding `sign-stored-rows --dry-run`, which binds it to the reviewed plan; the digest is optional here, because the run passes no `--include-unsigned`. The command is described below.
5. For each consumer that runs with `PODIUM_CACHE_MODE=offline-only`, keep the retired key in its set, or clear its cache directory and refill the cache in `always-revalidate` or `offline-first`, before the retired key leaves that consumer's set. An `offline-only` consumer never calls the registry, so it refuses a cached delivery signature made under a key it no longer trusts with `materialize.signature_invalid`. A consumer in `always-revalidate` or `offline-first` treats such a record as a cache miss and refetches it, so its cache needs no clear.
6. Remove the retired key only after a run of `sign-stored-rows` that exits 0 reports `0 row(s) still signed under it` for its `key_id`. A run that exits non-zero leaves that count incomplete: repair what its log names and run it again. Then delete the retired key's `verify:` line from the key file, restart the registry, and remove the key from every consumer's set. A row still signed under a key that has left the registry's set is refused with `materialize.signature_invalid`.

A deployment that runs more than one registry process adds the new public key to every process's set before any process signs under it. `podium admin signing-key rotate --key-file <path> --staged-out <staged>` writes that intermediate file as well: the previous key file plus a `verify:` line for the new key. Deploy the staged file to every process and restart each. Set every consumer's `PODIUM_SIGNATURE_VERIFY_KEY` to the list the rotate command prints, as step 2 states, before the rotated file reaches any process. Then deploy the rotated file and restart each process, and follow steps 4 through 6. [Clustered](clustered#rotating-the-signing-key-across-replicas) gives the commands for the Helm chart.

A `verify:` line trusts every envelope its key made, including an envelope made after the key was compromised, and `sign-stored-rows` re-signs a row such a key verifies under the current signing key. A compromised key is therefore removed from every set at once, with no `verify:` line kept for it, and the rows it signed are restored from a backup or re-ingested at a new version.

### The `sign-stored-rows` command

`sign-stored-rows` reads the configuration a registry start reads, opens the same store and object storage, binds no listen address, and exits. It never generates a key. With signing on, it refuses with `config.signature_provider_unavailable`, writing nothing, when the key file at `PODIUM_SIGN_KEY_PATH` is absent, and a key file with no `private:` or `public:` line, a line that does not decode, or a `public:` line that is not the public half of the `private:` line refuses it with the same code. With signing off (`PODIUM_SIGN=none`), the command rewrites without signing and signs no row, and it refuses `--include-unsigned` given with `--dry-run` or `--plan-digest` with `config.signature_provider_unavailable`, writing nothing; `--include-unsigned` given alone stays a usage error. It is subject to the refusal for a signing key that is not persisted with the store, as a registry start is.

When the store records that the first-start rewrite of stored content hashes has completed, the command may run while registry processes on this release serve the store, because each write replaces a row only when the row's stored content hash and stored signature are still the ones the command read. When that record is absent, the command performs the rewrite in place of the first start and records its completion, so it runs only while no registry process on the previous release serves the store. The command binds nothing and cannot detect such a process, so stop every one before it runs. In that case the command also signs each unsigned row the first start would sign, which it does only when the store is the SQLite store in the key file's directory. While no completion is recorded, a registry start over any other store that holds a manifest row is refused in either signing mode, rewriting no manifest row, signing no row, and recording no completion, and its error names this command, which performs the rewrite in place of that start. A store that holds no manifest row records completion at its first start.

Beyond the rows it signs in place of the first start, the command signs a row that carries no signature only under `--include-unsigned`. The flag is the operator's attestation for every unsigned row the reviewed dry run lists, because an unsigned row carries no evidence of who stored it. `--dry-run` lists every write the command would make, with each row's current signature state and stored content hash, makes none, and ends with a `dry-run: plan digest sha256:<hex> over <K> row(s)` line, so it shows the unsigned rows before `--include-unsigned` attests them. Outside `--dry-run`, `--include-unsigned` requires `--plan-digest=<digest>` with that value. Given `--plan-digest`, the command computes the digest of the plan it builds before any write, and when that digest differs it writes no row, no audit event, and no completion record, prints its own plan as `plan:` lines in the dry-run format, and exits with status 3. An unsigned row stored between the dry run and the run therefore refuses the run, and a row stored after the run builds its plan is left as it is, unsigned, until a later reviewed run. A change to the key file's `verify:` lines or signing key after the dry run also refuses the run. The command never signs a row whose envelope fails under every key of the verification key set. After the rewrite's summary line, with signing on, it prints `rehash: <n> unsigned left` and one `still signed under it` line per verification-only key; with signing off it prints only the summary line. It exits 0 on success, 2 on a usage error, 3 when its plan differs from the one `--plan-digest` names, and 1 when a row holds the completion record back, a write fails, or the command is refused.

---

## Upgrade procedure

Schema migrations are bundled in the registry binary and applied additively on startup: a new version creates tables and columns when absent and never drops or rewrites existing ones, so an upgrade migrates the database forward in place. Recommended cadence:

1. **Pre-upgrade.** Read the changelog and the migration notes for the target version. If a migration is non-trivial (reshuffling embeddings, changing the audit schema), schedule a maintenance window.
2. **Canary.** Roll one registry replica to the new version. Watch metrics for 30 min and confirm latency, error rate, and cache hit rate are unchanged. A release whose changelog names a migration of the values stored under the schema has no canary phase, because the new version computes different values over the stored bytes than every replica on the previous version, which still reads and writes them. For such a release, follow the upgrade procedure that changelog entry gives, in a maintenance window: stop every replica, back up the store, and provision the signing key file first where signing is on. Over any store other than the SQLite store in the key file's directory, which includes every multi-replica deployment, the order then runs `sign-stored-rows --dry-run`, reviews its report, and runs `sign-stored-rows` with its `--plan-digest` before the replicas start, in either signing mode, because a start over such a store that holds rows and no completion record is refused. Then start the replicas together on the new version. Where the store is the SQLite store in the key file's directory, the first start rewrites the stored values itself, and no `sign-stored-rows` run precedes it.
3. **Roll.** Roll the rest of the replicas. Because migrations are additive, an older replica ignores the new tables and columns, so old and new replicas coexist during the roll. The additive guarantee covers the database schema and does not cover the values stored under it, so a release whose changelog names a migration of those values does not permit mixed-version replicas.
4. **Verify.** After the roll completes, confirm `/readyz` reports `ready` on every replica and that the audit-integrity pass logs no gap on its next run.

A Helm chart deployment runs such a migration through the chart's migrate Job, in either signing mode, which holds the Deployment at zero replicas while `sign-stored-rows` rewrites the store, and follows [Upgrading the chart from v0.4.0](clustered#upgrading-the-chart-from-v040). In either signing mode, a registry pod over a store that holds manifest rows and no completion record exits at start, rewriting no manifest row, signing no row, and recording no completion. A GitOps controller such as Argo CD runs the same procedure as the value commits [GitOps controllers](clustered#gitops-controllers) lists, in that order. Pass neither `--rollback-on-failure` (Helm v4) nor `--atomic` (Helm v3) to those upgrades, because an automatic rollback reinstalls the previous binary over rows it cannot read.

Roll back by reverting the binary. The additive schema stays forward-compatible with the previous version's binary, so an older binary continues to run against a schema-migrated database. A release whose changelog names a migration of the values stored under the schema is reverted differently: restore the store from the backup that changelog's upgrade procedure takes, revert the registry and its consumers together, and clear each reverted consumer's cache as that changelog entry states.

---

## Read-only mode

When the Postgres primary becomes unreachable but a read replica is up, the registry falls back to read-only mode: read endpoints continue to serve from the replica; write endpoints (ingest webhooks, layer admin operations, admin grants, and tenant management) are rejected with the structured error `registry.read_only`. `GET /v1/admin/tenants` is a read and stays available.

A health-state machine governs the transition. The registry probes the primary every 5 s and flips to read-only after three consecutive failures (tunable via `PODIUM_READONLY_PROBE_INTERVAL` and `PODIUM_READONLY_PROBE_FAILURES`). It flips back automatically after three consecutive probe successes once the primary is reachable again.

Read responses in read-only mode carry two additional headers:

- `X-Podium-Read-Only: true`
- `X-Podium-Read-Only-Lag-Seconds: <n>`: observed replication lag.

Audit events for state transitions (`registry.read_only_entered`, `registry.read_only_exited`) are logged like any other admin action and carry the same hash-chain integrity guarantees.

---

## Outbound webhook receivers

The registry emits outbound webhooks for change events to receivers an admin registers per tenant through the receiver CRUD endpoints (`/v1/webhooks`). The registry originates the delivery request, so a receiver URL is an outbound target the registry reaches on a caller's behalf. Two operational settings govern this.

**Receiver URL policy (SSRF).** The registry validates a receiver URL at registration and re-checks it at delivery. By default it requires the `https` scheme and rejects a URL that resolves to a loopback, link-local, or private address (for example `127.0.0.0/8`, `::1`, `169.254.0.0/16`, and the RFC 1918 ranges), and it does not follow a redirect to such a target. A rejected target returns `registry.invalid_argument` naming the disallowed host. This prevents a caller who can register a receiver from pointing the registry at an internal endpoint it would not otherwise reach.

**`PODIUM_WEBHOOK_ALLOWED_TARGETS`.** A deployment whose receiver is legitimately internal, such as an in-cluster relay, sets this comma-separated allowlist of hosts or CIDRs that the policy permits in addition to public addresses. An entry is either a bare host matched against the URL host or a CIDR matched against the resolved addresses. The variable is empty by default, which keeps the strict policy. A malformed entry aborts boot, so the registry fails closed rather than silently widening the policy. The startup log reports the wired allowlist.

**Receiver registration on a no-auth deployment.** The receiver CRUD endpoints require the per-tenant admin role and return `auth.forbidden` for a non-admin caller, the same authorization posture as the admin-grant endpoints. A caller the registry cannot authenticate resolves to the anonymous public identity, which fails the admin check, so receiver registration is closed on a bind with no identity provider. `POST /v1/admin/grants` is itself admin-gated and cannot create the first admin on such a deployment. Seed the first admin at boot with `PODIUM_BOOTSTRAP_ADMINS`, configure an identity provider the registry verifies (`injected-session-token`, `oidc-jwt`, or `trusted-headers`), and have that admin present its credential when registering a receiver.

---

## Security review checklist

Walk through these before launching to a tenant that handles sensitive content.

| Item | Check |
|:--|:--|
| OAuth identity flow | Device-code flow tested for every IdP in production use. Token lifetimes set to ≤15 min. Revocation propagates within 60s. |
| OIDC group claim mapping | Group claims actually produced by your IdP arrive in the registry's audit log. Test with a non-admin user. |
| Per-layer visibility | Each layer's `visibility:` declaration is correct. Test by impersonating a non-member identity (via `injected-session-token` test harness). |
| Signature enforcement | Each consumer's MCP server resolves the policy from `PODIUM_VERIFY_SIGNATURES`, then `defaults.verify_signatures` in its `sync.yaml`, then an `always` fallback, and the values are `never` and `always`. Confirm the resolved policy is `always` on every consumer. A machine where an earlier release's standalone bootstrap wrote `verify_signatures: never` into `~/.podium/sync.yaml` keeps it until the line is removed, and `podium-mcp` announces that `never` with a startup line naming the file. The registry does not read the variable, and `podium sync` runs no signature check. The Python and TypeScript SDKs verify no signature and do not read `PODIUM_VERIFY_SIGNATURES`, so the `always` default governs `podium-mcp` alone. Test tampering as the notes below the table state. |
| Audit hash chain | `PODIUM_AUDIT_VERIFY_INTERVAL_SECONDS` is non-zero so the registry re-verifies the chain on a schedule. Alert on the `audit.gap_detected` event and on the "audit integrity ALERT" log line. |
| Webhook signing | Git provider webhook HMAC secret is unique per layer. Test with an invalid signature; expect `ingest.webhook_invalid`. |
| Outbound receiver URL policy | Receiver CRUD is admin-gated. `PODIUM_WEBHOOK_ALLOWED_TARGETS` lists only the internal receivers a deployment intends. Test by registering an `http` or private-address receiver; expect `registry.invalid_argument`. |
| Sandbox profile honoring | The hosts in production honor `sandbox_profile` for non-`unrestricted` artifacts. Test with a `read-only-fs` artifact and confirm the host enforces. |
| Object-storage credentials | IAM roles or short-lived credentials, never static keys. Bucket policy denies public access. |
| Backup encryption | Postgres backups + S3 object versioning encrypted at rest. PITR window matches your RTO. |
| Scope preview gating | `tenant.expose_scope_preview` is set deliberately per tenant; `false` for tenants where aggregate visibility counts would leak signal. |

Tamper checks for the signature-enforcement row:

- A response altered in transit fails at the consumer with `materialize.content_hash_mismatch`, and one whose signature was altered fails with `materialize.signature_invalid`.
- The registry admits each stored row before it serves the row's content: it recomputes the content hash and checks each bundled resource's stored hash and size against its bytes, then verifies the signature. Its refusal reaches every reader's full load, `podium sync` and the SDKs included. A HEAD and a matching conditional GET are answered without admission, so a consumer that already holds the row under an unchanged content hash keeps serving that copy. On any registry, a row whose stored bytes were altered while its stored content hash was left unchanged is refused with `materialize.content_hash_mismatch`. On a signing registry, a row altered together with its hash is refused with `materialize.signature_invalid`, and one whose stored signature was removed with `materialize.signature_missing`. A registry with `PODIUM_SIGN=none` serves a row altered together with its hash. Admission checks a row's content and does not attest which row answers a request, so a party that can write the metadata store can withhold a row, relabel an admissible row, or have an older admissible row served in place of a newer one.
- A bundled-resource body held in object storage and altered there is refused by the registry on a full load with `materialize.content_hash_mismatch`. The object-storage copy of a resource the row holds inline is never served, because the registry serves that resource inline from the admitted row on `load_artifact` and on each `artifacts:batchLoad` item. An object-held body altered after the registry admitted the row, and a manifest-body object replaced at its key, which admission does not read, fail at `podium-mcp`'s fetch with `materialize.fetch_failed` and a `content hash mismatch` message. Server-source `podium sync` refuses a manifest-body object whose bytes do not hash to the content hash its link carries, and the sync fails with `load_artifact <id>: manifest body content hash mismatch` without materializing any artifact. `podium sync` compares no fetched bundled-resource body with its hash, and the SDKs compare no fetched body with its hash, so each receives such a body unchecked, and an operator who needs that check reads the artifact through `podium-mcp`.
- Run each tamper check through a full load, either a direct HTTP `load_artifact` GET with no `If-None-Match` header or a consumer whose cache is empty, and expect the refusal from the registry.

The signature a consumer verifies is the registry's delivery signature, which the registry mints with its registry-managed key over the delivery hash of each response it serves. Every node that serves reads therefore holds the registry signing key, and a read-only replica topology, if one is ever deployed, places the key on each replica. A registry running with `PODIUM_SIGN=none` serves each delivery record unsigned, and `podium-mcp` still recomputes the delivery hash on every load. The delivery signature is a registry-managed envelope whatever key model signed the artifact at ingest, so a consumer verifies it with `PODIUM_SIGNATURE_PROVIDER=registry-managed` and the registry's verification key set in `PODIUM_SIGNATURE_VERIFY_KEY`, and `podium-mcp` refuses to start under `sigstore-keyless` with `config.invalid`. A registry process signs under a rotated key from its next response onward, so each consumer adds the new key to its set before the rotation, as [Rotating the signing key](#rotating-the-signing-key) states.

Stored-row admission has limits against a party that can write the metadata store without holding the signing key. Such a party can withhold, relabel, re-layer, or roll back an admissible row. Where the store is the SQLite store in the key file's directory, it can also delete the first-start rewrite's completion record so that the next start signs the unsigned rows it wrote; on every other store, such a start refuses in either signing mode and signs nothing. On any store, a later `sign-stored-rows --include-unsigned` signs every unsigned row its reviewed dry run lists, including one such a party stored before that dry run, so read the rows the dry run lists before passing its plan digest. An unsigned row such a party stores between the dry run and the run changes the plan digest, and the run refuses with exit status 3. A row stored after the run builds its plan is absent from that plan and stays unsigned. A `sign-stored-rows` run also re-signs rows signed under a `verify:` key, so while a compromised key stays on a `verify:` line, a run re-signs rows its holder stored. Restrict write access to the metadata store and to its backups. These limits are known and are not closed by admission.

Re-run the checklist after every major release and after any change to layer config, IdP, or sandbox enforcement settings.

---

## Common operational pitfalls

These come up a few times a year for most operators:

- **Embedding provider rate limits.** OpenAI and Voyage rate-limit aggressively under bulk reingest. Stagger `podium layer reingest` across layers, or switch to `ollama` pointed at a local model server for inference during reingest storms.
- **pgvector index bloat.** After many embeddings have churned, `REINDEX` the vector index quarterly or set up `auto_vacuum` aggressively.
- **Embedding-model change.** After changing `PODIUM_EMBEDDING_PROVIDER`, the embedding model, or a self-embedding backend's hosted model, run `podium admin reembed` with no scoping flag in every tenant. On Pinecone, Weaviate, or Qdrant, first point the registry at a fresh index or collection, because those backends record no model per row and every tenant shares the index. The procedure is in [Switching backends on a running deployment](vector-backends#switching-backends-on-a-running-deployment).
- **MCP server cache pinning** (`PODIUM_CACHE_DIR` on slow disks). Developer machines with cache on a network filesystem will see materialization latency well above the SLO. Default to `~/.podium/cache/` on local disk.
- **Webhook retries during read-only mode.** GitHub will retry webhooks for ~24 h with exponential backoff. If your read-only window exceeds that, ingests will be permanently lost. Trigger manual `podium layer reingest` after recovery.
- **Force-push on a Git source layer.** Default policy is tolerant (`layer.history_rewritten` event emitted, prior commits preserved in the content store). If `force_push_policy: strict` is configured, expect ingest rejections after force-pushes. Coordinate with authors.
- **OIDC token clock skew.** The registry validates a token's expiry against its own clock with no configured leeway, so any NTP drift on a registry node produces intermittent `auth.token_expired` errors for tokens near expiry. Keep registry hosts on NTP and monitor clock skew.
- **SCIM lag.** OIDC group membership changes propagate via SCIM push from the IdP to `/scim/v2/`. The registry has no pull side and no sync command, so an IdP that does not push leaves group membership to update on the user's next login. Trigger a push from the IdP's provisioning console when a membership change has to land immediately.

### Signing failures

- **`podium-mcp` refuses to start with `config.signature_provider_unavailable`.** Under a policy above `never`, the bridge resolves its verification material once at start, from `PODIUM_SIGNATURE_VERIFY_KEY` or, when that variable is unset, from the `public:` line and every `verify:` line of the registry key file at `PODIUM_SIGN_KEY_PATH` (default `~/.podium/standalone/registry-signing.key`). The usual cause is a missing key: a consumer on a machine other than the registry's with no `PODIUM_SIGNATURE_VERIFY_KEY`. A `PODIUM_SIGNATURE_VERIFY_KEY` with an entry that is empty or does not decode, and `PODIUM_SIGNATURE_PROVIDER=noop`, refuse the start the same way. Supply the key set, or set `PODIUM_VERIFY_SIGNATURES=never` against a registry that does not sign. A bridge whose set lacks the key the registry signs under refuses each load with `materialize.signature_invalid` instead.
- **Loads fail with `materialize.signature_missing`.** The served artifact carries no signature and the policy is `always`. The registry either runs with `PODIUM_SIGN=none`, in which case its consumers set `PODIUM_VERIFY_SIGNATURES=never`, or it stored the row unsigned before signing was on, in which case the registry itself refuses the row. Run `sign-stored-rows --include-unsigned --dry-run`, review the rows it lists, and run `sign-stored-rows --include-unsigned --plan-digest=<digest>` with the digest on its last line, as [The `sign-stored-rows` command](#the-sign-stored-rows-command) states, or ingest a new version of the artifact. A load the signature check refuses records no local `artifact.loaded` event. A load whose inline-resource decode or large-resource fetch fails records none either, so an object-storage outage leaves the local stream silent for every large-resource load where it previously carried one line each. A load the sandbox or runtime gate refuses still records one.
- **The key file is present but is not the key that signed the stored rows.** Where the store is the SQLite store in the key file's directory, the start is not refused. The first start's rewrite of stored content hashes leaves every such row at its stored hash with one log line per row, classified `signature_unverified`, and restoring the right key alone does not repair a row still at the previous content hash. List the public half of the key that signed the rows on a `verify:` line of the key file, restart the registry, and run `sign-stored-rows`, which moves each such row to the new content hash and re-signs it under the current key. For any other store, the start is refused while no completion is recorded and the store holds rows, and the `sign-stored-rows` dry run classifies such rows `signature_unverified`. List the signing key's public half on a `verify:` line of the key file, run the dry run, and run `sign-stored-rows` with its `--plan-digest`, repeating the dry run's `--include-unsigned` setting. Restoring the backup the upgrade procedure takes and starting with the key that signed the rows also repairs them, at the cost of every write made since that backup, where the store is the SQLite store in the key file's directory. Over any other store, in either signing mode, the restored store holds no completion record, so its start is refused until a `sign-stored-rows` dry run and a run with its `--plan-digest`, under that key, record completion. A new version of each affected artifact is the repair only where no copy of that key's public half survives.
- **The key file is lost after the first-start rewrite has completed.** The start is not refused, because the generated-key guard returns once the rewrite's completion record is set. Such a start generates a fresh keypair, signs new ingests under it, and logs nothing about the retired key, while the registry refuses every row the retired key signed with `materialize.signature_invalid`, to every reader and under any consumer policy. Setting `PODIUM_VERIFY_SIGNATURES=never` does not make those rows load. Stop the registry, restore the key file from the backup, and start it again, because the registry loads its signing key once at start. Restoring the file leaves refused every row the fresh key signed in the meantime; list the fresh key's public half on a `verify:` line of the restored file and restart the registry to admit those rows. When only the lost key's public half survives, for example in a consumer's `PODIUM_SIGNATURE_VERIFY_KEY`, list it on a `verify:` line of the key file the registry now holds and restart the registry, which admits those rows, and then run `sign-stored-rows`, which re-signs them under the current key so the line can be removed as [Rotating the signing key](#rotating-the-signing-key) states.
- **The registry refuses to start because the signing key would be generated under the process's home.** A registry with signing on and no `PODIUM_SIGN_KEY_PATH` refuses to start unless its store is the SQLite store beside the default key. The error names `PODIUM_SIGN_KEY_PATH` and `PODIUM_SIGN=none`, says the key would be generated under the process's home, outside the directory that holds the store, and fires whether or not the store holds a signed row. Set `PODIUM_SIGN_KEY_PATH` to a file on the store's persistent storage, or set `PODIUM_SIGN=none`. With `PODIUM_SIGN=none`, a store outside the SQLite store beside the default key that holds rows and no completion record is then refused by the unmigrated-store refusal below, whose repair is `sign-stored-rows`. The published container image run with its default store keeps the key and the store in the same directory, so it starts. Two other refusals name `PODIUM_SIGN_KEY_PATH` and mean that the store holds signed rows: one says the rows need their content hash rewritten while no signer is configured, and one says `PODIUM_SIGN_KEY_PATH` names no file. The first comes from a start only over the SQLite store in the key file's directory, and otherwise from the `sign-stored-rows` dry run. Each calls for the key that signed those rows.
- **The registry refuses to start and names `sign-stored-rows`.** While no completion of the rewrite of stored content hashes is recorded, a registry start refuses, in either signing mode, when its store holds a manifest row, including a soft-deleted one, and is not the SQLite store in the directory of the key file at `PODIUM_SIGN_KEY_PATH`, or at the default key location when that variable is unset. A co-located SQLite store runs the rewrite at its first start, and a store that holds no manifest row records completion at its first start. The refused start applies the additive schema and seeds the default tenant and the bootstrap grants, and it rewrites no manifest row, signs no row, and records no completion. When the key-persistence refusal above, or the refusal for a signing key that did not exist before the start, also applies, the start reports that refusal first. The error names the store, a stored row, and the key location. Stop every registry process on the previous release, then run `sign-stored-rows --dry-run`, review its report, and run `sign-stored-rows` with `--plan-digest` set to the digest on the report's last line, and start the registry again. With signing on, add `--include-unsigned` to both commands to attest the unsigned rows, and when no file exists at the key location, create it first with `podium admin signing-key generate --key-file <path>`, because neither the refused start nor `sign-stored-rows` generates a key. With signing off, run both commands with `PODIUM_SIGN=none` in their environment, because `podium serve --sign none` sets the mode only for its own process. On a Helm chart deployment the migrate Job runs both commands, as [Upgrading the chart from v0.4.0](clustered#upgrading-the-chart-from-v040) shows.
- **The registry or `sign-stored-rows` refuses with `config.signature_provider_unavailable`, naming the key file.** The key file has no `private:` line or no `public:` line, one of its lines does not decode, or its `public:` line is not the public half of its `private:` line. Restore the file from the backup, or repair the line the message names.

### SCIM and audit-anchor startup refusals

- **The registry refuses to start with `config.scim_store_unavailable`.** The error names `PODIUM_SCIM_STORE_PATH` and the cause: a non-empty file that does not parse, a path that is a directory or has a non-directory parent, or a parent directory that cannot be created or written. Fix the permissions, or point the variable at a valid path. For a corrupt file, move it aside and start the registry, which loads a missing file as an empty directory, and then trigger a full push from the IdP's provisioning console to repopulate users and groups, as the SCIM lag entry describes. Unsetting the variable keeps the directory in memory, so it is lost on every restart. When the store becomes unwritable after startup, the receiver answers a create or replace push with HTTP 400 `invalidValue`, and that record is served until restart and then lost. It answers a delete push with HTTP 500, and the deleted record returns on restart. In either case, fix the directory and trigger a full push.
- **The registry refuses to start with `config.audit_sink_unavailable`.** This refusal applies only when `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` is above 0 and `PODIUM_AUDIT_LOG_PATH` is a file path or unset. The error names the path and the cause: the path is a directory, a parent component is a regular file, the parent directory cannot be created, an existing log file cannot be read, or the home directory cannot be resolved for the default `~/.podium/audit.log`. Fix the path or its permissions. Setting the interval to 0 lets the registry start, but it then runs with no audit sink, which disables audit retention, erasure, and verification as well as anchoring. The startup check reads the file and does not write it, so a log that can be read but not written passes startup, and the first audit event reports the failure.
- **The registry refuses to start with `config.audit_anchor_key_unavailable`.** This refusal applies only when `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` is above 0 and the audit sink is a file. The key file at `PODIUM_AUDIT_SIGNING_KEY_PATH` exists and cannot be read, lacks a `private:` line, or does not decode, or it cannot be generated. Restore the file, or move it aside so the registry generates a new keypair. Keep the old public key, because anchors written before the change were signed with it and anyone who checks them needs it.
- **The registry refuses to start with `config.audit_anchor_key_shared`.** The anchor key is also the registry signing key or one of its `verify:` keys. Generate a separate keypair for `PODIUM_AUDIT_SIGNING_KEY_PATH`. The anchor signature carries no purpose label, so the anchor key cannot also be a registry signing or `verify:` key, and an external verifier must not trust one key for both purposes.

---

## Public-mode misconfiguration

A misconfigured public-mode deployment is the most common security-relevant operational anomaly because the registry serves correctly. It serves to everyone.

**Detection:**

- `/healthz` returns `mode: public`.
- Audit events for read calls show `caller.identity: "system:public"` and the flag `caller.public_mode: true`.
- The startup banner shows the public-mode warning.
- `podium status` surfaces the flag.

**Mitigation:**

1. Confirm public mode was the intended deployment posture. If it was, no action needed; the audit log already records the intent.
2. If public mode was *not* intended (a misconfigured environment variable, copy-pasted CLI flag, or accidental container image tag), stop the registry, remove `--public-mode` / unset `PODIUM_PUBLIC_MODE`, restart. The registry refuses mid-run flips, so a restart is mandatory.
3. If public mode was running on an internet-exposed registry (which the safety check should have prevented unless `--allow-public-bind` was set), treat as a security incident: rotate any signing keys that were in scope, audit the access log for unfamiliar IPs, and proceed per the org's incident-response procedure.

**Prevention.** Container-image and Helm-chart consumers should set `PODIUM_NO_AUTOSTANDALONE=1` and use `--strict` to refuse anything but explicitly-configured deployments. Production CI templates should fail-fast on the presence of `PODIUM_PUBLIC_MODE` in environment lists.

---

## When to escalate to support / open an issue

- Audit chain gap detected (the scheduled integrity pass records an `audit.gap_detected` event). Treat as a security incident; capture evidence before any cleanup.
- Repeated signature or content-hash refusals. Check each for an upgrade or key cause first:
  - A refusal the registry logs, naming the tenant, artifact ID, version, and reason, of `materialize.content_hash_mismatch`, `materialize.signature_invalid`, or `materialize.signature_missing`: compare the artifact ID and version against the per-row `rehash:` lines the rewrite logged. Those come from the first start's log only where the store is the SQLite store in the key file's directory; otherwise they come from the `sign-stored-rows` run's log, or from the migrate Job's log on a chart deployment. A row that summary names is upgrade residue, repaired as the release's upgrade note states, and is not escalated. Compare the registry's key file with the backed-up key as well, because the registry refuses every row signed under a key outside its verification key set, and repair a mismatch as `deploy/runbook.md`'s signature-storm entry states.
  - A registry refusal for a row that summary does not name, under the key that signed the store, means the row's stored bytes were altered, in the metadata store or in object storage, or an object-held body is missing. Read the refusal's logged reason, check the object store for the row's bodies, and then escalate.
  - A consumer-returned `materialize.content_hash_mismatch`, or `materialize.fetch_failed` naming a large-resource `content hash mismatch`, means a response or an object-storage body was altered after the registry admitted the row. Escalate it.
  - A consumer-returned `materialize.signature_invalid` or `materialize.signature_missing`: first confirm that the consumer's `PODIUM_SIGNATURE_PROVIDER` is `registry-managed`, that the key set it resolves contains the key the registry signs under, and that the registry does not run with `PODIUM_SIGN=none` under an `always` policy. With that configuration correct, escalate it as a forged or stripped signature.
- Sustained latency degradation that doesn't track CPU / memory / DB load. Often indicates a query-plan regression after a Postgres major upgrade.
- Out-of-band ingest events (artifacts appear in the registry without a corresponding `artifact.published` outbound webhook). Indicates webhook config or processing failure.

For all of these, capture: relevant log lines (with trace IDs), the affected tenant id, the affected artifact id(s), and a brief timeline. The more of those you have ready, the faster the fix.
