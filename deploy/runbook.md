# Podium operator runbook

Operational procedures for the standard-topology Podium deployment.
Each scenario carries detection signals, impact, and mitigation steps.

## Read-only mode (Postgres primary outage)

Per spec §13.2.1.

**Detection.**
- `/readyz` returns `{"mode":"read_only"}`.
- Response headers `X-Podium-Read-Only: true` and
  `X-Podium-Read-Only-Lag-Seconds: <n>` on read responses.
- Audit events `registry.read_only_entered` /
  `registry.read_only_exited` bracket the window.

**Impact.** Read endpoints serve from the replica. Write endpoints
(ingest webhooks, layer admin operations, freeze toggles, admin
grants, podium login local IdP-mediated tokens) reject with
`registry.read_only`.

**Mitigation.**
1. Confirm the Postgres primary is unreachable; check infrastructure
   alarms.
2. The registry probes the primary every 5 seconds and flips back
   automatically after three consecutive successes.
3. If failover is permanent, promote the replica via the cloud
   provider tooling, then restart the registry pods so they reattach
   to the new primary.

## Public mode (intentional or accidental)

Per spec §13.2.2.

**Detection.** `/healthz` returns `{"mode":"public"}`. Audit events
record `caller.public_mode: true`.

**Impact.** All artifacts visible to all callers without
authentication. Ingest of `sensitivity: medium` and `sensitivity:
high` is rejected.

**Mitigation.**
1. Confirm public mode was intentional (a demo registry, evaluation
   pilot, or internal-public catalog).
2. If accidental, stop the registry, remove `--public-mode` /
   `PODIUM_PUBLIC_MODE`, and restart. The registry refuses mid-run
   flips, so a restart is mandatory.
3. If the registry was internet-exposed without
   `--allow-public-bind`: treat as a security incident. Rotate any
   signing keys in scope, audit the access log for unfamiliar IPs,
   and proceed per the org's incident-response procedure.

## Object-storage outage

**Detection.** Sustained `materialize.*` errors in audit;
object-storage SLA dashboard shows degradation.

**Impact.** `load_artifact` returns inline manifest bodies; bundled
resources fail to materialize. Already-cached resources continue to
serve from the MCP cache.

**Mitigation.**
1. Verify object-storage health at the provider.
2. Affected hosts: check `~/.podium/cache` hit rate via `podium
   cache stats`. Cached content remains usable.
3. Once recovered, no registry-side action needed; clients retry on
   next call.

## IdP outage

**Detection.** OIDC discovery requests fail; new `podium login`
sessions stall.

**Impact.** Existing tokens continue to work until expiry (15 min
default). Refresh fails on expired tokens; new logins fail.

**Mitigation.**
1. Confirm the IdP outage with the provider.
2. Recommend users keep their current sessions running.
3. For `injected-session-token` runtimes: the runtime's own token
   issuance path runs independently of the user-facing IdP; managed
   workflows continue.

## Full-disk on registry node

**Detection.** Disk-usage alerts; ingest writes start failing.

**Impact.** Object-storage writes succeed (S3 has its own quota); the
registry's local disk pressure affects logs and the WAL.

**Mitigation.**
1. Compact / rotate logs.
2. Increase the node's disk allocation.
3. Audit retention: §8.4 defaults are 1 year; reduce if appropriate.

## Audit-stream backpressure

**Detection.** `audit.outbox_lagging` events; the outbox table grows.

**Impact.** Audit events queue locally; reads continue.

**Mitigation.**
1. Inspect the SIEM destination.
2. Raise the outbox flush concurrency.
3. Set a temporary higher retention for the local sink so events
   are not lost.

## Runaway search QPS

**Detection.** `search_artifacts` p99 latency rises; Prometheus
`podium_request_duration_seconds_bucket` shows tail growth.

**Impact.** Search latency degrades; other endpoints unaffected.

**Mitigation.**
1. Identify the calling client via audit.
2. Apply per-tenant quota (`quota.search_qps_quota`).
3. Increase replica count.

## Signature verification failure storm

**Detection.** `materialize.signature_invalid` events spike.

**Impact.** Affected artifacts fail to materialize. Other artifacts
unaffected.

**Mitigation.**
1. Verify the artifact signatures are correct via `podium verify
   <id>`.
2. If a key was compromised, rotate it via `podium admin rotate-key`.
3. Affected ingests can be replayed once the keys are correct.
