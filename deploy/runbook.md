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
grants, and tenant management) reject with `registry.read_only`.

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
`registry.unavailable` on `load_artifact` and on `artifacts:batchLoad`
items; `materialize.fetch_failed` on a consumer's fetch of a manifest
body or a large resource; the object-storage SLA dashboard shows
degradation.

**Impact.** The registry admits each stored row before it serves the
row's content, and admission reads the object-held bodies of every row
in the requested artifact's `extends:` chain, parents included. A full
`load_artifact` of an artifact whose chain holds any object-held
bundled-resource body is refused with `registry.unavailable`. A full
load whose manifest document goes through the manifest-body channel (a
document above the 256 KiB inline cutoff) is refused with
`registry.unavailable` when the registry's object-store write or stat
fails. A full load whose presigned manifest-body or large-resource
fetch fails returns `materialize.fetch_failed` at the consumer. Only a
load whose whole chain holds inline bundled resources, and whose
manifest document is below the cutoff, loads in full. A HEAD
revalidation and a matching conditional GET read no object storage and
are answered from the stored content hash, so a consumer in any cache
mode keeps serving a cached copy whose content hash is unchanged, and an
`offline-first` or `offline-only` consumer serves a resolution hit
without calling the registry.

An object store opened at the wrong root or bucket is a different
condition. That store reports every body absent rather than failing its
reads, so the registry refuses every object-held row with
`materialize.content_hash_mismatch` rather than `registry.unavailable`.
Point the object store at the right root or bucket. Where the store
is the SQLite store in the key file's directory, restart: the first
start after an upgrade that met this condition runs the rewrite of
stored content hashes again, because that condition held the rewrite's
completion record back. Outside that store, in either signing mode, a
restart while no completion is recorded is refused, rewriting no
manifest row, signing no row, and recording no completion; rerun
`sign-stored-rows` instead, as a `--dry-run`, a review of its report,
and a run with its `--plan-digest`, and then start the registry. With
signing on, the run repeats the dry run's `--include-unsigned`
setting, because the plan digest covers it and a run with a different
setting exits with status 3. With signing off, run both commands with
`PODIUM_SIGN=none` in their environment, because
`podium serve --sign none` sets the mode only for its own process. On a
Helm chart deployment, the migrate Job ran the rewrite; correct the
object-store values in the values file and rerun steps 3 to 5 of the
upgrade procedure in `docs/deployment/clustered.md` instead of
restarting.

**Mitigation.**
1. Verify object-storage health at the provider.
2. Affected hosts: check `~/.podium/cache` hit rate via `podium
   cache stats`. Cached content remains usable in every cache mode
   for an artifact whose content hash is unchanged.
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

**Detection.** `materialize.signature_invalid` or
`materialize.signature_missing` events spike, at the registry, which
logs each stored-row refusal with the tenant, artifact ID, version, and
reason, or at consumers. `podium-mcp` processes exit at start with
`config.signature_provider_unavailable`.

**Impact.** Affected artifacts fail to materialize. Other artifacts
unaffected. A `podium-mcp` that refuses to start serves nothing.

**Mitigation.**
1. Verify the artifact signatures are correct via `podium verify
   <id>`, with `PODIUM_SIGNATURE_VERIFY_KEY` set to the registry's
   verification key set. For a consumer-side refusal, confirm the
   consumer's `PODIUM_SIGNATURE_PROVIDER`, that its verification key
   set contains the key the registry signs under, and that the
   registry does not run with `PODIUM_SIGN=none` under an `always`
   policy.
2. For a row signed under a key that is lost or rotated out of the
   registry's key file: restore the key file that holds that key,
   where it still exists, to `PODIUM_SIGN_KEY_PATH`, and restart the
   registry, which loads its key only at start. Restoring the file
   leaves refused every row the key that replaced it signed; list that
   key's public half on a `verify:` line of the restored file and
   restart the registry to admit those rows. Where only the key's
   public half survives, for example in a consumer's
   `PODIUM_SIGNATURE_VERIFY_KEY`, list it on a `verify:` line of the
   key file the registry holds and restart the registry, which then
   admits those rows. Then run `podium-server sign-stored-rows`, or
   `podium admin sign-stored-rows` on a standalone host, which
   re-signs them under the current key, including a row the
   first-start rewrite left at the previous content hash as
   `signature_unverified`. Remove the `verify:` line only after a
   run that exits 0 reports `0 row(s) still signed under it` for that
   key's `key_id`. Do not list a compromised key.
3. For a row stored unsigned before signing was turned on
   (`materialize.signature_missing`): run `sign-stored-rows
   --dry-run --include-unsigned`, read the rows it lists, and then
   run `sign-stored-rows --include-unsigned --plan-digest=<digest>`
   with the `sha256:` value on the dry run's last line, which attests
   and signs every unsigned row the reviewed dry run lists. The run
   refuses with exit status 3, writing nothing, when its plan differs
   from the reviewed one. When the store holds no record that the
   first-start rewrite completed, the command performs that rewrite,
   so it runs only while no registry process on the previous release
   serves the store; the command cannot detect such a process. Outside
   the SQLite store in the key file's directory, the command is
   required before a registry start over such a store that holds
   rows, because that start is refused in either signing mode. On a
   Helm chart deployment whose store holds no such record, the chart
   runs both commands as its migrate Job, and a `signing.mode=none`
   release runs the same Job with `migration.includeUnsigned=false`.
   Run steps 2 to 5 of the upgrade procedure in
   `docs/deployment/clustered.md`: hold the Deployment at
   `replicaCount=0` on the new image and back up the store, run the
   `migration.mode=dry-run` upgrade and review its log, run the
   `migration.mode=run` upgrade with `migration.planDigest` set to the
   `sha256:` value on the dry-run log's last `dry-run: plan digest`
   line, streaming the run Job's log to `run.log` as step 4 shows,
   and then run the serving upgrade without migration values, which
   scales the Deployment back up. An object-store outage that begins
   after a clean dry run changes the plan, so the run Job exits with
   status 3 before any write, leaves the record unset, and prints
   `plan:` lines, which can carry `class=body_unavailable`, with no
   `rehash:` summary. The Deployment stays at zero replicas; restore
   the object store, then rerun steps 3 to 5.
4. On a migration target whose rows the source signed under a key
   the target does not hold, place the source's key and restart, or
   recreate the target store empty and re-run
   `podium admin migrate-to-standard` with the source's key in place.
   Where the target is the SQLite store in the key file's directory,
   the restart after the re-run runs the rewrite. For any other
   target, in either signing mode, `migrate-to-standard` clears the
   target's record, so the target's start is refused until a
   `sign-stored-rows` dry run and a run with its `--plan-digest`
   record completion; run that pair before the start. With signing
   on, the run repeats the dry run's `--include-unsigned` setting,
   because the plan digest covers it and a run with a different
   setting exits with status 3. With signing off, run both commands
   with `PODIUM_SIGN=none` in their environment.
   On a Helm chart deployment, hold the release at zero replicas
   before the command runs and run the rewrite in the migrate Job, as
   the Migration from single node section of
   `docs/deployment/clustered.md` states.
5. Where no copy of the signing key's public half survives, or the
   key was compromised, restore the rows from a backup or ingest a
   new version of each affected artifact.
