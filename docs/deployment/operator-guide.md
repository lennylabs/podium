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
- **Registry signing key.** The file at `PODIUM_SIGN_KEY_PATH`, or the key file in the Secret the Helm chart mounts there. Back it up with the store it signs. Once the key is lost, the registry refuses every row it signed, to every reader and under any consumer policy, because the registry verifies each stored signature before it serves the row.
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
```

Step 1 restores the signing key because a registry with signing on, a Postgres store, and no key path is refused at start, and because the drill then also exercises the key backup. In step 5, `podium verify <artifact>` verifies the registry's delivery signature over its delivery hash under the key the default `registry-managed` provider resolves: `PODIUM_SIGNATURE_VERIFY_KEY`, or, when that variable is unset, the `public:` line of the key file at `PODIUM_SIGN_KEY_PATH`. Run step 5 with the production registry's public key in `PODIUM_SIGNATURE_VERIFY_KEY`, extracted as [Clustered](clustered#2-deploy-the-registry) shows. `--provider noop` is refused for every envelope. A Sigstore envelope passed with `--signature` takes `--provider sigstore-keyless` with `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE`, and verification fails with `no trust root configured` when the trust root is absent; the delivery signature itself is never a Sigstore envelope.

The audit-integrity pass runs inside the registry on the `PODIUM_AUDIT_VERIFY_INTERVAL_SECONDS` schedule, which defaults to 3600 seconds. Lower it on the restored deployment to get the first pass sooner.

---

## Upgrade procedure

Schema migrations are bundled in the registry binary and applied additively on startup: a new version creates tables and columns when absent and never drops or rewrites existing ones, so an upgrade migrates the database forward in place. Recommended cadence:

1. **Pre-upgrade.** Read the changelog and the migration notes for the target version. If a migration is non-trivial (reshuffling embeddings, changing the audit schema), schedule a maintenance window.
2. **Canary.** Roll one registry replica to the new version. Watch metrics for 30 min and confirm latency, error rate, and cache hit rate are unchanged. A release whose changelog names a migration of the values stored under the schema has no canary phase, because the first replica on the new version rewrites the stored values that every replica on the previous version still reads and writes. For such a release, follow the upgrade order that changelog entry states, in a maintenance window: stop every replica, back up the store, provision the signing key file first where signing is on, and start the replicas together on the new version.
3. **Roll.** Roll the rest of the replicas. Because migrations are additive, an older replica ignores the new tables and columns, so old and new replicas coexist during the roll. The additive guarantee covers the database schema and does not cover the values stored under it, so a release whose changelog names a migration of those values does not permit mixed-version replicas.
4. **Verify.** After the roll completes, confirm `/readyz` reports `ready` on every replica and that the audit-integrity pass logs no gap on its next run.

Roll back by reverting the binary. The additive schema stays forward-compatible with the previous version's binary, so an older binary continues to run against a schema-migrated database. A release whose changelog names a migration of the values stored under the schema is reverted differently: restore the store from the backup that changelog's upgrade order takes, revert the registry and its consumers together, and clear each reverted consumer's cache as that changelog entry states.

---

## Read-only mode

When the Postgres primary becomes unreachable but a read replica is up, the registry falls back to read-only mode: read endpoints continue to serve from the replica; write endpoints (ingest webhooks, layer admin operations, freeze toggles, admin grants, and tenant management) are rejected with the structured error `registry.read_only`. `GET /v1/admin/tenants` is a read and stays available.

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

The signature a consumer verifies is the registry's delivery signature, which the registry mints with its registry-managed key over the delivery hash of each response it serves. Every node that serves reads therefore holds the registry signing key, and a read-only replica topology, if one is ever deployed, places the key on each replica. A registry running with `PODIUM_SIGN=none` serves each delivery record unsigned, and `podium-mcp` still recomputes the delivery hash on every load. The delivery signature is a registry-managed envelope whatever key model signed the artifact at ingest, so a consumer verifies it with `PODIUM_SIGNATURE_PROVIDER=registry-managed` and the registry's public key in `PODIUM_SIGNATURE_VERIFY_KEY`, and a consumer configured for `sigstore-keyless` refuses it with `materialize.signature_invalid` wherever its policy verifies it. A rotation of the registry key takes effect on the next response. Roll each consumer's `PODIUM_SIGNATURE_VERIFY_KEY` to the new public key with the rotation, and clear each consumer's cache directory.

Stored-row admission has limits against a party that can write the metadata store without holding the signing key. Such a party can withhold, relabel, re-layer, or roll back an admissible row, and it can delete the first-start rewrite's completion record so that the next start signs the unsigned rows it wrote. Restrict write access to the metadata store and to its backups. Both limits are known and are not closed by admission.

Re-run the checklist after every major release and after any change to layer config, IdP, or sandbox enforcement settings.

---

## Common operational pitfalls

These come up a few times a year for most operators:

- **Embedding provider rate limits.** OpenAI and Voyage rate-limit aggressively under bulk reingest. Stagger `podium layer reingest` across layers, or switch to `ollama` pointed at a local model server for inference during reingest storms.
- **pgvector index bloat.** After many embeddings have churned, `REINDEX` the vector index quarterly or set up `auto_vacuum` aggressively.
- **MCP server cache pinning** (`PODIUM_CACHE_DIR` on slow disks). Developer machines with cache on a network filesystem will see materialization latency well above the SLO. Default to `~/.podium/cache/` on local disk.
- **Webhook retries during read-only mode.** GitHub will retry webhooks for ~24 h with exponential backoff. If your read-only window exceeds that, ingests will be permanently lost. Trigger manual `podium layer reingest` after recovery.
- **Force-push on a Git source layer.** Default policy is tolerant (`layer.history_rewritten` event emitted, prior commits preserved in the content store). If `force_push_policy: strict` is configured, expect ingest rejections after force-pushes. Coordinate with authors.
- **OIDC token clock skew.** The registry validates a token's expiry against its own clock with no configured leeway, so any NTP drift on a registry node produces intermittent `auth.token_expired` errors for tokens near expiry. Keep registry hosts on NTP and monitor clock skew.
- **SCIM lag.** OIDC group membership changes propagate via SCIM push from the IdP to `/scim/v2/`. The registry has no pull side and no sync command, so an IdP that does not push leaves group membership to update on the user's next login. Trigger a push from the IdP's provisioning console when a membership change has to land immediately.

### Signing failures

- **`podium-mcp` refuses to start with `config.signature_provider_unavailable`.** Under a policy above `never`, the bridge resolves its verification material once at start, from `PODIUM_SIGNATURE_VERIFY_KEY` or, when that variable is unset, from the registry key file at `PODIUM_SIGN_KEY_PATH` (default `~/.podium/standalone/registry-signing.key`). The usual cause is a missing key: a consumer on a machine other than the registry's with no `PODIUM_SIGNATURE_VERIFY_KEY`. A malformed `PODIUM_SIGNATURE_VERIFY_KEY` and `PODIUM_SIGNATURE_PROVIDER=noop` refuse the start the same way. Supply the key, or set `PODIUM_VERIFY_SIGNATURES=never` against a registry that does not sign. A bridge that starts with the wrong key refuses each load with `materialize.signature_invalid` instead.
- **Loads fail with `materialize.signature_missing`.** The served artifact carries no signature and the policy is `always`. The registry either runs with `PODIUM_SIGN=none`, in which case its consumers set `PODIUM_VERIFY_SIGNATURES=never`, or it stored the row unsigned before signing was on, in which case the registry itself refuses the row and a new version of the artifact is the repair. A load the signature check refuses records no local `artifact.loaded` event. A load whose inline-resource decode or large-resource fetch fails records none either, so an object-storage outage leaves the local stream silent for every large-resource load where it previously carried one line each. A load the sandbox or runtime gate refuses still records one.
- **The key file is present but is not the key that signed the stored rows.** The start is not refused. The first start's rewrite of stored content hashes leaves every such row at its stored hash with one log line per row, classified `signature_unverified`, and restoring the right key alone does not repair a row still at the previous content hash. Restore the backup the upgrade order takes and start with the key that signed the rows, so the rewrite runs again and re-signs them, at the cost of every write made since that backup. A new version of each affected artifact is the repair only where that backup or that key no longer exists.
- **The key file is lost after the first-start rewrite has completed.** The start is not refused, because the generated-key guard returns once the rewrite's completion record is set. Such a start generates a fresh keypair, signs new ingests under it, and logs nothing about the retired key, while the registry refuses every row the retired key signed with `materialize.signature_invalid`, to every reader and under any consumer policy. Setting `PODIUM_VERIFY_SIGNATURES=never` does not make those rows load. Stop the registry, restore the key file from the backup, and start it again, because the registry loads its signing key once at start.
- **The registry refuses to start because the signing key would be generated under the process's home.** A registry with signing on and no `PODIUM_SIGN_KEY_PATH` refuses to start unless its store is the SQLite store beside the default key. The error names `PODIUM_SIGN_KEY_PATH` and `PODIUM_SIGN=none`, says the key would be generated under the process's home, outside the directory that holds the store, and fires whether or not the store holds a signed row. Set `PODIUM_SIGN_KEY_PATH` to a file on the store's persistent storage, or set `PODIUM_SIGN=none`. The published container image run with its default store keeps the key and the store in the same directory, so it starts. Two other refusals name `PODIUM_SIGN_KEY_PATH` and mean that the store holds signed rows: one says the rows need their content hash rewritten while no signer is configured, and one says `PODIUM_SIGN_KEY_PATH` names no file. Each calls for the key that signed those rows.

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
  - A refusal the registry logs, naming the tenant, artifact ID, version, and reason, of `materialize.content_hash_mismatch`, `materialize.signature_invalid`, or `materialize.signature_missing`: compare the artifact ID and version against the first start's per-row summary. A row the summary names is upgrade residue, repaired as the release's upgrade note states, and is not escalated. Compare the registry's key file with the backed-up key as well, because the registry refuses every row signed under a key it no longer holds, and repair a mismatch as `deploy/runbook.md`'s signature-storm entry states.
  - A registry refusal for a row the summary does not name, under the key that signed the store, means the row's stored bytes were altered, in the metadata store or in object storage, or an object-held body is missing. Read the refusal's logged reason, check the object store for the row's bodies, and then escalate.
  - A consumer-returned `materialize.content_hash_mismatch`, or `materialize.fetch_failed` naming a large-resource `content hash mismatch`, means a response or an object-storage body was altered after the registry admitted the row. Escalate it.
  - A consumer-returned `materialize.signature_invalid` or `materialize.signature_missing`: first confirm that the consumer's `PODIUM_SIGNATURE_PROVIDER` is `registry-managed`, that the key it resolves is the registry's current public key, and that the registry does not run with `PODIUM_SIGN=none` under an `always` policy. With that configuration correct, escalate it as a forged or stripped signature.
- Sustained latency degradation that doesn't track CPU / memory / DB load. Often indicates a query-plan regression after a Postgres major upgrade.
- Out-of-band ingest events (artifacts appear in the registry without a corresponding `artifact.published` outbound webhook). Indicates webhook config or processing failure.

For all of these, capture: relevant log lines (with trace IDs), the affected tenant id, the affected artifact id(s), and a brief timeline. The more of those you have ready, the faster the fix.
