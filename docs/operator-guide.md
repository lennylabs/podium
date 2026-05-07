# Operator's guide

Running Podium in production. This is the companion to [§13 of the spec](../spec/13-deployment.md) — the spec describes what's true; this guide describes how to operate it day-to-day. Read §13 first if you haven't.

The audience here is platform engineers and SREs running a standard deployment for an organization. For small-team, single-VM use, the [team rollout guide](team-rollout.md) is more appropriate.

## Capacity planning

The §13.6 baseline (10K artifacts, 100 QPS, 1 GB Postgres, 500 GB object storage on a 3-replica deployment with `db.m5.large` equivalent) is the starting point. Beyond that:

| Dimension | Threshold | What to do |
| --- | --- | --- |
| Artifacts | 100K | Increase Postgres instance size; review pgvector index parameters; consider sharding embeddings (per §13.6). |
| QPS | 1K | Scale registry replicas horizontally; put a CDN in front of object storage for resource bytes (§13.7). |
| QPS | 10K | Review search query patterns; consider dedicated Elasticsearch for BM25 with pgvector (or Pinecone/Weaviate/Qdrant) for vector. |
| Tenants | 50 | Confirm `RegistryStore` connection pool is sized appropriately; increase `pgbouncer` pool if used. |
| Audit volume | 1M events/day | Set retention explicitly (default policy is conservative); consider streaming to an external sink via `LocalAuditSink` config. |

Embeddings dominate Postgres growth at scale. Each artifact's text projection (§4.7) becomes a 384-dim float vector with `embedded-onnx` or `text-embedding-3-small`. At ~3 KB per row including metadata, 100K artifacts is ~300 MB of embeddings.

Object-storage growth is dominated by bundled resources (scripts, templates, model files referenced by URL+hash). Most teams' p99 artifact size sits well under the 256 KB inline cutoff, so the inline manifest body fits in Postgres and only larger resources go to S3.

## Monitoring

Both the registry and the MCP server expose Prometheus metrics. The reference Grafana dashboard ships with the registry (§13.8). Key signals:

**Registry:**

- `podium_request_duration_seconds{handler}` — histograms per endpoint. Watch `load_domain`, `search_artifacts`, `load_artifact`, `load_artifacts` against the SLOs in §7.1 (p99 < 200ms / 200ms / 500ms manifest / 2s with resources).
- `podium_request_total{handler, code}` — error rate. Visibility-denial rate (`code=403, error=visibility.denied`) is informational; a sudden spike usually means a layer config error rather than a real authorization issue.
- `podium_cache_hit_ratio{layer}` — content-cache hit rate at the registry edge. Low values often indicate a CDN misconfig.
- `podium_ingest_total{layer, status}` — ingest success / failure / lint-failed counts per layer. Flag a layer with a recent uptick in `lint_failed`.
- `podium_audit_lag_seconds` — time between event creation and audit-stream commit. Watch for backpressure.
- `podium_postgres_replica_lag_seconds` — drives §13.2.1 read-only mode transitions.

**MCP server:**

- `podium_mcp_session_count` — active sessions per workspace.
- `podium_mcp_metaool_duration_seconds{tool}` — per-tool latency at the bridge layer.
- `podium_mcp_offline_total` — count of calls that returned `served_from_cache: true` in `always-revalidate` mode.

## Alerting

A reasonable starting set, tuned for teams running the §13.6 baseline:

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

The full Helm chart ships these as a starter; tune thresholds to your SLOs.

## Backup and restore

Per §13.3:

- **Postgres**: managed services handle this — enable point-in-time recovery (PITR) with at least 7 days of retention. For self-run Postgres, run logical (`pg_dump`) daily and physical (base backups + WAL archiving) for PITR.
- **Object storage**: enable cross-region replication or daily snapshots. Resources are content-addressed and immutable, so restore is straightforward — replace the bucket contents from the snapshot.
- **Default RPO 1h / RTO 4h** is for a managed-Postgres + replicated-S3 setup. Tighten by reducing PITR granularity or replicating at higher frequency; loosen by extending PITR window.

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

## Upgrade procedure

Schema migrations are bundled in the registry binary and follow expand-contract (§13.4). The recommended cadence:

1. **Pre-upgrade**: read the changelog and the migration notes for the target version. If a migration is non-trivial (reshuffling embeddings, changing the audit schema), schedule a maintenance window.
2. **Canary**: roll one registry replica to the new version. Watch metrics for 30 min — confirm latency, error rate, and cache hit rate are unchanged.
3. **Roll**: roll the rest of the replicas. The expand-contract migration design means old and new replicas can coexist during the roll.
4. **Verify**: after the roll completes, run `podium admin verify --check schema --check audit-chain`.
5. **Contract**: run `podium admin migrate --finalize` once all replicas are on the new version. This drops the now-unused old columns / indexes.

Roll back by reverting the binary; the new schema is forward-compatible with the previous version's binary by design (that's the expand half of expand-contract). If the schema migration itself was the problem, revert the migration via `podium admin migrate --revert` *before* rolling back binaries.

## Security review checklist

Before launching to a tenant that handles sensitive content, walk through these. Each maps to a spec section that defines the underlying guarantee.

| Item | Check | Spec |
| --- | --- | --- |
| OAuth identity flow | Device-code flow tested for every IdP in production use. Token lifetimes set to ≤15 min. Revocation propagates within 60s. | §6.3 |
| OIDC group claim mapping | Group claims actually produced by your IdP arrive in the registry's audit log. Test with a non-admin user. | §6.3.1 |
| Per-layer visibility | Each layer's `visibility:` declaration is correct. Test by impersonating a non-member identity (via `injected-session-token` test harness). | §4.6 |
| Sensitivity enforcement | `PODIUM_VERIFY_SIGNATURES` is `medium-and-above` (or stricter). Test that a tampered artifact fails materialization with `materialize.signature_invalid`. | §6.6, §13.10 defaults |
| Audit hash chain | Run `podium admin verify --check audit-chain` weekly via cron. Detect gaps automatically. | §8.6 |
| Webhook signing | Git provider webhook HMAC secret is unique per layer. Test with an invalid signature — expect `ingest.webhook_invalid`. | §7.3.1 |
| Sandbox profile honoring | The hosts in production honor `sandbox_profile` for non-`unrestricted` artifacts. Test with a `read-only-fs` artifact and confirm the host enforces. | §4.3 (sandbox profiles) |
| Object-storage credentials | IAM roles or short-lived credentials, never static keys. Bucket policy denies public access. | §13.5 / §13.11 |
| Backup encryption | Postgres backups + S3 object versioning encrypted at rest. PITR window matches your RTO. | §13.3 |
| Scope preview gating | `tenant.expose_scope_preview` is set deliberately per tenant — `false` for tenants where aggregate visibility counts would leak signal. | §3.5 |

Re-run the checklist after every major release and after any change to layer config, IdP, or sandbox enforcement settings.

## Common operational pitfalls

These come up a few times a year for most operators:

- **Embedding provider rate limits.** OpenAI and Voyage rate-limit aggressively under bulk reingest. Stagger `podium layer reingest` across layers, or switch to `embedded-onnx` for local inference during reingest storms.
- **pgvector index bloat.** After many embeddings have churned (artifacts updated, layers reordered), `REINDEX` the vector index quarterly or set up `auto_vacuum` aggressively.
- **MCP server cache pinning** (`PODIUM_CACHE_DIR` on slow disks). Developer machines with cache on a network filesystem will see materialization latency well above the SLO. Default to `~/.podium/cache/` on local disk.
- **Webhook retries during read-only mode.** GitHub will retry webhooks for ~24 h with exponential backoff. If your read-only window exceeds that, ingests will be permanently lost — trigger manual `podium layer reingest` after recovery.
- **Force-push on a Git source layer.** Default policy is tolerant (`layer.history_rewritten` event emitted, prior commits preserved in the content store). If you've configured `force_push_policy: strict`, expect ingest rejections after force-pushes — coordinate with authors.
- **OIDC token clock skew.** The registry tolerates ±60s of skew. NTP drift on a registry node beyond that window starts causing intermittent `auth.token_expired` errors. Monitor clock skew on registry hosts.
- **SCIM lag.** OIDC group membership changes (a person added/removed from a team) propagate via SCIM push from the IdP. If your IdP doesn't push, group membership only updates on the user's next login. Force a refresh with `podium admin scim-sync --user <id>`.

## When to escalate to support / open an issue

- Audit chain gap detected (`podium admin verify --check audit-chain` reports a hash mismatch). Treat as a security incident; capture evidence before any cleanup.
- Repeated `materialize.signature_invalid` for artifacts you authored. Either your signing pipeline broke or someone is tampering — investigate before continuing.
- Sustained latency degradation that doesn't track CPU / memory / DB load. Often indicates a query-plan regression after a Postgres major upgrade.
- Out-of-band ingest events (artifacts appear in the registry without a corresponding `artifact.published` outbound webhook). Indicates webhook config or processing failure.

For all of these, capture: relevant log lines (with trace IDs), the affected tenant id, the affected artifact id(s), and a brief timeline. The more of those you have ready, the faster the fix.
