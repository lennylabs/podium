---
title: Capacity and availability
nav_order: 6
description: "Size a registry deployment against the baseline and its scaling thresholds, and keep it serving reads through a Postgres primary outage with read-only mode."
---

# Capacity and availability

This page gives the sizing baseline for a clustered deployment, the thresholds at which to scale each dimension, and the read-only mode that keeps the registry serving reads while the Postgres primary is unreachable.

## Capacity planning

The baseline deployment holds 10K artifacts at 100 QPS, with 1 GB of Postgres and 500 GB of object storage, on 3 replicas with a `db.m5.large` equivalent. Beyond the baseline, act on each threshold as follows:

| Dimension | Threshold | What to do |
|:--|:--|:--|
| Artifacts | 100K | Increase the Postgres instance size, review the pgvector index parameters, and consider sharding embeddings. |
| QPS | 1K | Scale registry replicas horizontally, and put a CDN in front of object storage for resource bytes. |
| QPS | 10K | Review search query patterns. Consider a dedicated Elasticsearch for BM25, with pgvector or a [managed vector backend](../services/vector-backends) for vector search. |
| Tenants | 50 | Confirm that the `RegistryStore` connection pool is sized for the tenant count, and increase the pgbouncer pool if one is in use. |
| Audit volume | 1M events/day | Set retention explicitly. Keep or replace the file destination in `PODIUM_AUDIT_LOG_PATH`, add an `http(s)` or `otlp+http(s)` destination that ships the stream to a SIEM or a collector, or ship `stdout` through the container log pipeline. See [Audit destinations](observability#audit-destinations). |

### Postgres growth

Embeddings account for most of the Postgres growth at scale. Each artifact's text projection becomes a float vector whose dimension depends on the configured provider: 768 for `nomic-embed-text` via Ollama, 1024 for `voyage-3` or `embed-v4`, and 1536 for `text-embedding-3-small`. At about 6 KB per row including metadata at 1536 dimensions, 100K artifacts take about 600 MB of embeddings.

### Object-storage growth

Bundled resources account for most of the object-storage growth. A resource at or below the 256 KB inline cutoff stays in the manifest row in Postgres, and only larger resources go to object storage. For most teams the p99 artifact size sits well under the cutoff. [The inline cutoff](../../consuming/handling-artifact-responses#the-inline-cutoff) describes how consumers receive each kind.

Object-storage keys are content hashes, and the object under a key never changes, so a CDN in front of object storage caches resource bytes safely.

## Read-only mode

When the Postgres primary becomes unreachable but a read replica is up, the registry falls back to read-only mode. Read endpoints continue to serve from the replica. Every `/v1` endpoint that changes registry state is rejected with the structured error `registry.read_only`, including ingest webhooks, layer admin operations, admin grants, tenant management, and outbound webhook receiver changes. `GET /v1/admin/tenants` is a read and stays available. The SCIM receiver at `/scim/v2/` is outside read-only mode, so identity provider pushes still apply.

A health-state machine governs the transition:

- The registry probes the primary every 5 seconds and enters read-only mode after three consecutive failures.
- It leaves read-only mode automatically after three consecutive probe successes once the primary is reachable again.
- `PODIUM_READONLY_PROBE_INTERVAL` and `PODIUM_READONLY_PROBE_FAILURES` set the probe interval and the failure count.

While the registry is read-only, `/readyz` answers HTTP 200 with `mode: read_only`, so a load balancer keeps the replica in rotation to serve reads. Read responses carry two additional headers:

- `X-Podium-Read-Only: true`
- `X-Podium-Read-Only-Lag-Seconds: <n>`: the observed replication lag.

The registry records each transition as a `registry.read_only_entered` or `registry.read_only_exited` audit event. These events are logged like any other admin action and carry the same hash-chain integrity guarantees.

An ingest webhook that the registry rejects while it is read-only is not applied. A Git host that does not redeliver failed deliveries on its own leaves that ingest pending until a manual `podium layer reingest`, as [Troubleshooting](troubleshooting#operational-issues) states.
