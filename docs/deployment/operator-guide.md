---
layout: default
title: Operator guide
parent: Deployment
nav_order: 5
description: Day-two operations for a standard Podium deployment — capacity, monitoring, alerts, backup, upgrades, security review, common pitfalls.
---

# Operator guide

Day-two operations for a standard Podium deployment. Companion to [`spec/13-deployment.md`](https://github.com/lennylabs/podium/blob/main/spec/13-deployment.md), which describes what's true; this page covers how to operate it.

For small-team, single-VM use, see [Small team](small-team) instead.

---

## Capacity planning

Baseline (10K artifacts, 100 QPS, 1 GB Postgres, 500 GB object storage on a 3-replica deployment with `db.m5.large` equivalent) is the starting point. Beyond that:

| Dimension | Threshold | What to do |
|:--|:--|:--|
| Artifacts | 100K | Increase Postgres instance size; review pgvector index parameters; consider sharding embeddings. |
| QPS | 1K | Scale registry replicas horizontally; put a CDN in front of object storage for resource bytes. |
| QPS | 10K | Review search query patterns; consider dedicated Elasticsearch for BM25 with pgvector (or Pinecone/Weaviate/Qdrant) for vector. |
| Tenants | 50 | Confirm `RegistryStore` connection pool is sized appropriately; increase pgbouncer pool if used. |
| Audit volume | 1M events/day | Set retention explicitly; consider streaming to an external sink via `LocalAuditSink` config. |

Embeddings dominate Postgres growth at scale. Each artifact's text projection becomes a 384-dim float vector with `embedded-onnx` or `text-embedding-3-small`. At ~3 KB per row including metadata, 100K artifacts is ~300 MB of embeddings.

Object-storage growth is dominated by bundled resources. Most teams' p99 artifact size sits well under the 256 KB inline cutoff, so the inline manifest body fits in Postgres and only larger resources go to S3.

---

## Monitoring

Both the registry and the MCP server expose Prometheus metrics. The reference Grafana dashboard ships with the registry. Key signals:

**Registry:**

- `podium_request_duration_seconds{handler}` — histograms per endpoint. Watch `load_domain`, `search_artifacts`, `load_artifact`, `load_artifacts` against the SLOs (p99 < 200ms / 200ms / 500ms manifest / 2s with resources).
- `podium_request_total{handler, code}` — error rate. Visibility-denial rate (`code=403, error=visibility.denied`) is informational; a sudden spike usually means a layer config error rather than a real authorization issue.
- `podium_cache_hit_ratio{layer}` — content-cache hit rate at the registry edge. Low values often indicate a CDN misconfig.
- `podium_ingest_total{layer, status}` — ingest success / failure / lint-failed counts per layer. Flag a layer with a recent uptick in `lint_failed`.
- `podium_audit_lag_seconds` — time between event creation and audit-stream commit. Watch for backpressure.
- `podium_postgres_replica_lag_seconds` — drives read-only mode transitions.

**MCP server:**

- `podium_mcp_session_count` — active sessions per workspace.
- `podium_mcp_metaool_duration_seconds{tool}` — per-tool latency at the bridge layer.
- `podium_mcp_offline_total` — count of calls that returned `served_from_cache: true` in `always-revalidate` mode.

---

## Alerting

A reasonable starting set, tuned for the baseline deployment:

```yaml
# Critical — page on-call
- alert: PodiumDown
  expr: up{job="podium-registry"} == 0
  for: 2m

- alert: PodiumPostgresUnreachable
  expr: podium_postgres_up == 0
  for: 1m

- alert: PodiumLoadArtifactSLOBreached
  expr: histogram_quantile(0.99, rate(podium_request_duration_seconds_bucket{handler="load_artifact"}[5m])) > 0.5
  for: 5m

# Warning — investigate within hours
- alert: PodiumIngestFailingForLayer
  expr: increase(podium_ingest_total{status="failed"}[1h]) > 5
  for: 15m

- alert: PodiumReadOnlyMode
  expr: podium_registry_mode{mode="read_only"} == 1
  for: 5m

- alert: PodiumAuditLag
  expr: podium_audit_lag_seconds > 60
  for: 10m

# Informational — review weekly
- alert: PodiumLowDescribeQuality
  expr: podium_lint_thin_descriptions_total > 50
```

The Helm chart ships these as a starter; tune thresholds to your SLOs.

---

## Backup and restore

- **Postgres.** Managed services handle this — enable point-in-time recovery (PITR) with at least 7 days of retention. For self-run Postgres, run logical (`pg_dump`) daily and physical (base backups + WAL archiving) for PITR.
- **Object storage.** Enable cross-region replication or daily snapshots. Resources are content-addressed and immutable, so restore is straightforward — replace the bucket contents from the snapshot.
- **Default RPO 1h / RTO 4h** for a managed-Postgres + replicated-S3 setup. Tighten by reducing PITR granularity or replicating at higher frequency; loosen by extending the PITR window.

Test restores quarterly. The runbook procedure:

```
1. Spin up a non-production registry pointed at a fresh Postgres + a fresh
   S3 bucket.
2. Restore Postgres from PITR to T-1h.
3. Sync the production S3 bucket to the fresh one (rclone or aws s3 sync).
4. Run `podium admin verify --check audit-chain --check signatures` against
   the restored deployment. Fix any reported gaps.
5. Spot-check `load_artifact` for a known-good artifact; should match the
   pre-restore content_hash.
```

---

## Upgrade procedure

Schema migrations are bundled in the registry binary and follow expand-contract. Recommended cadence:

1. **Pre-upgrade.** Read the changelog and the migration notes for the target version. If a migration is non-trivial (reshuffling embeddings, changing the audit schema), schedule a maintenance window.
2. **Canary.** Roll one registry replica to the new version. Watch metrics for 30 min — confirm latency, error rate, and cache hit rate are unchanged.
3. **Roll.** Roll the rest of the replicas. The expand-contract migration design means old and new replicas can coexist during the roll.
4. **Verify.** After the roll completes, run `podium admin verify --check schema --check audit-chain`.
5. **Contract.** Run `podium admin migrate --finalize` once all replicas are on the new version. This drops the now-unused old columns and indexes.

Roll back by reverting the binary; the new schema is forward-compatible with the previous version's binary by design (that's the expand half of expand-contract). If the schema migration itself was the problem, revert the migration via `podium admin migrate --revert` *before* rolling back binaries.

---

## Read-only mode

When the Postgres primary becomes unreachable but a read replica is up, the registry falls back to read-only mode: read endpoints continue to serve from the replica; write endpoints (ingest webhooks, layer admin operations, freeze toggles, admin grants, login-driven token issuance) are rejected with the structured error `registry.read_only`.

A health-state machine drives the transition. The registry probes the primary every 5 s and flips to read-only after three consecutive failures (tunable via `PODIUM_READONLY_PROBE_INTERVAL` and `PODIUM_READONLY_PROBE_FAILURES`). It flips back automatically after three consecutive probe successes once the primary is reachable again.

Read responses in read-only mode carry two additional headers:

- `X-Podium-Read-Only: true`
- `X-Podium-Read-Only-Lag-Seconds: <n>` — observed replication lag.

Audit events for state transitions (`registry.read_only_entered`, `registry.read_only_exited`) are logged like any other admin action and carry the same hash-chain integrity guarantees.

---

## Security review checklist

Walk through these before launching to a tenant that handles sensitive content. Each maps to a spec section that defines the underlying guarantee.

| Item | Check | Spec |
|:--|:--|:--|
| OAuth identity flow | Device-code flow tested for every IdP in production use. Token lifetimes set to ≤15 min. Revocation propagates within 60s. | §6.3 |
| OIDC group claim mapping | Group claims actually produced by your IdP arrive in the registry's audit log. Test with a non-admin user. | §6.3.1 |
| Per-layer visibility | Each layer's `visibility:` declaration is correct. Test by impersonating a non-member identity (via `injected-session-token` test harness). | §4.6 |
| Sensitivity enforcement | `PODIUM_VERIFY_SIGNATURES` is `medium-and-above` (or stricter). Test that a tampered artifact fails materialization with `materialize.signature_invalid`. | §6.6, §13.10 |
| Audit hash chain | Run `podium admin verify --check audit-chain` weekly via cron. Detect gaps automatically. | §8.6 |
| Webhook signing | Git provider webhook HMAC secret is unique per layer. Test with an invalid signature — expect `ingest.webhook_invalid`. | §7.3.1 |
| Sandbox profile honoring | The hosts in production honor `sandbox_profile` for non-`unrestricted` artifacts. Test with a `read-only-fs` artifact and confirm the host enforces. | §4.3 |
| Object-storage credentials | IAM roles or short-lived credentials, never static keys. Bucket policy denies public access. | §13.5 / §13.11 |
| Backup encryption | Postgres backups + S3 object versioning encrypted at rest. PITR window matches your RTO. | §13.3 |
| Scope preview gating | `tenant.expose_scope_preview` is set deliberately per tenant — `false` for tenants where aggregate visibility counts would leak signal. | §3.5 |

Re-run the checklist after every major release and after any change to layer config, IdP, or sandbox enforcement settings.

---

## Common operational pitfalls

These come up a few times a year for most operators:

- **Embedding provider rate limits.** OpenAI and Voyage rate-limit aggressively under bulk reingest. Stagger `podium layer reingest` across layers, or switch to `embedded-onnx` for local inference during reingest storms.
- **pgvector index bloat.** After many embeddings have churned, `REINDEX` the vector index quarterly or set up `auto_vacuum` aggressively.
- **MCP server cache pinning** (`PODIUM_CACHE_DIR` on slow disks). Developer machines with cache on a network filesystem will see materialization latency well above the SLO. Default to `~/.podium/cache/` on local disk.
- **Webhook retries during read-only mode.** GitHub will retry webhooks for ~24 h with exponential backoff. If your read-only window exceeds that, ingests will be permanently lost — trigger manual `podium layer reingest` after recovery.
- **Force-push on a Git source layer.** Default policy is tolerant (`layer.history_rewritten` event emitted, prior commits preserved in the content store). If you've configured `force_push_policy: strict`, expect ingest rejections after force-pushes — coordinate with authors.
- **OIDC token clock skew.** The registry tolerates ±60s of skew. NTP drift on a registry node beyond that window causes intermittent `auth.token_expired` errors. Monitor clock skew on registry hosts.
- **SCIM lag.** OIDC group membership changes propagate via SCIM push from the IdP. If your IdP doesn't push, group membership only updates on the user's next login. Force a refresh with `podium admin scim-sync --user <id>`.

---

## Public-mode misconfiguration

A misconfigured public-mode deployment is the most common security-relevant operational anomaly because the registry serves correctly — it just serves to everyone.

**Detection:**

- `/healthz` returns `mode: public`.
- Audit events for read calls show `caller.identity: "system:public"` and the flag `caller.public_mode: true`.
- The startup banner shows the public-mode warning.
- `podium status` surfaces the flag.

**Mitigation:**

1. Confirm public mode was the intended deployment posture. If it was, no action needed — the audit log already records the intent.
2. If public mode was *not* intended (a misconfigured environment variable, copy-pasted CLI flag, or accidental container image tag), stop the registry, remove `--public-mode` / unset `PODIUM_PUBLIC_MODE`, restart. The registry refuses mid-run flips, so a restart is mandatory.
3. If public mode was running on an internet-exposed registry (which the safety check should have prevented unless `--allow-public-bind` was set), treat as a security incident: rotate any signing keys that were in scope, audit the access log for unfamiliar IPs, and proceed per the org's incident-response procedure.

**Prevention.** Container-image and Helm-chart consumers should set `PODIUM_NO_AUTOSTANDALONE=1` and use `--strict` to refuse anything but explicitly-configured deployments. Production CI templates should fail-fast on the presence of `PODIUM_PUBLIC_MODE` in environment lists.

---

## When to escalate to support / open an issue

- Audit chain gap detected (`podium admin verify --check audit-chain` reports a hash mismatch). Treat as a security incident; capture evidence before any cleanup.
- Repeated `materialize.signature_invalid` for artifacts you authored. Either your signing pipeline broke or someone is tampering — investigate before continuing.
- Sustained latency degradation that doesn't track CPU / memory / DB load. Often indicates a query-plan regression after a Postgres major upgrade.
- Out-of-band ingest events (artifacts appear in the registry without a corresponding `artifact.published` outbound webhook). Indicates webhook config or processing failure.

For all of these, capture: relevant log lines (with trace IDs), the affected tenant id, the affected artifact id(s), and a brief timeline. The more of those you have ready, the faster the fix.
