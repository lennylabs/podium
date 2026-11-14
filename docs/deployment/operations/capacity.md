---
title: Capacity and availability
nav_order: 6
description: "Size a registry deployment and keep it serving through a store outage with read-only mode."
---

# Capacity and availability

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

## Read-only mode

When the Postgres primary becomes unreachable but a read replica is up, the registry falls back to read-only mode. Read endpoints continue to serve from the replica. Write endpoints are rejected with the structured error `registry.read_only`: ingest webhooks, layer admin operations, admin grants, and tenant management. `GET /v1/admin/tenants` is a read and stays available.

A health-state machine governs the transition. The registry probes the primary every 5 seconds and enters read-only mode after three consecutive failures. It leaves read-only mode automatically after three consecutive probe successes once the primary is reachable again. `PODIUM_READONLY_PROBE_INTERVAL` and `PODIUM_READONLY_PROBE_FAILURES` tune the probe interval and the failure count.

Read responses in read-only mode carry two additional headers:

- `X-Podium-Read-Only: true`
- `X-Podium-Read-Only-Lag-Seconds: <n>`: observed replication lag.

The registry records each transition as a `registry.read_only_entered` or `registry.read_only_exited` audit event. These events are logged like any other admin action and carry the same hash-chain integrity guarantees.

---
