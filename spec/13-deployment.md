# 13. Deployment

## 13.1 Reference Topology

- **Stateless front-end:** 3+ replicas behind a load balancer (HTTP).
- **Postgres:** managed (RDS, Cloud SQL, Aurora) or self-run; primary + read replicas. Holds manifest metadata, layer config, admin grants, and audit; also holds embeddings when the default vector backend (pgvector) is in use.
- **Vector backend:** `pgvector` by default, collocated in the Postgres deployment with no separate service to run. The default binary also ships built-ins for `pinecone`, `weaviate-cloud`, and `qdrant-cloud`, selectable via `PODIUM_VECTOR_BACKEND` (each takes its own endpoint + API key env vars). Custom backends register through the `RegistrySearchProvider` SPI (§9.1, §9.2).
- **Embedding provider:** `openai` by default in standard deployments. Text projection from manifest frontmatter (§4.7 _Embedding generation_) is sent to OpenAI's embeddings API. The default binary also ships `voyage`, `cohere`, and `ollama`, selectable via `PODIUM_EMBEDDING_PROVIDER`. Optional when the configured vector backend self-embeds (Pinecone Integrated Inference, Weaviate Cloud vectorizer, Qdrant Cloud Inference).
- **Object storage:** S3-compatible (S3, GCS, MinIO, R2).
- **Helm chart** ships with the registry; bare-metal deployment guide alongside.

For non-prod or standalone use, see §13.10.

### 13.1.1 Evaluation Deployment (Docker Compose)

For team evaluation, smoke-testing, and local integration testing (anything that wants the standard topology's components without the standalone single-binary shortcut), the repo ships a `docker-compose.yml` that brings up the full stack with one command:

```bash
docker compose up -d
podium init --global --registry http://localhost:8080
```

The compose file includes:

- **`registry`**: the registry binary, configured against the local services below.
- **`postgres`**: `pgvector/pgvector:pg16` for metadata and embeddings.
- **`minio`**: S3-compatible object storage (path-style URLs, `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD` for auth).
- **`dex`**: OIDC IdP, retained for device-code evaluation. The registry service selects no identity provider, so it does not consult Dex. `podium login` against this stack prints the §7.7 no-auth notice and exits, because §7.7 treats `http://localhost:8080` and `http://127.0.0.1:8080` as no-auth registries. A device-code flow against Dex requires publishing the registry at another address.
- **`bootstrap`**: one-shot container that creates the MinIO bucket, then exits. The registry's OIDC client is registered declaratively in the Dex config, and the default tenant and the admin grant named by `PODIUM_BOOTSTRAP_ADMINS` are seeded by the registry at boot, consistent with §13.10 standalone self-seeding. The grant is a forward-compatibility seed. The evaluation stack selects no identity provider, so no caller presents that identity until an operator configures one.

**Not production-grade.** Single-replica services, default credentials, local volumes. The compose stack is _standard-topology in shape_ so consumers exercise the same code paths as a real deployment, but it is intended only for evaluation pilots, CI integration tests, and adapter / SDK development. For genuine non-prod or solo use, prefer §13.10's standalone mode (one binary instead of four containers).

**The evaluation stack authenticates no caller.** The `registry` service sets no `PODIUM_IDENTITY_PROVIDER`, so the registry resolves every caller as anonymous-public, every layer is visible regardless of its declared `visibility:`, and the layer-management and erase endpoints admit any request. The service publishes its port on the host loopback interface for that reason, and it sets `PODIUM_DEFAULT_LAYER_VISIBILITY=private` so a layer registered against the stack carries a private declaration if an identity provider is added later. Of the §13.2.2 detection signals, `/healthz` reports `ready` rather than `public` on this posture, `podium status` reports the same value, and no public-mode startup banner is emitted, while the audit signals do fire: read calls record `caller.identity: "system:public"` and `caller.public_mode: true`. The registry's startup log line reads `mode=standalone`, which is not one of the §13.2.2 signals. Configure a verified provider (§6.3.2, §6.3.3) before exposing the stack beyond the host.

## 13.2 Runbook

Coverage for: Postgres failover, object-storage outage, IdP outage, full-disk on registry node, audit-stream backpressure, runaway search QPS, signature verification failure storm. Each scenario gets detection signals, impact, and mitigation steps; full runbook ships with the Helm chart.

### 13.2.1 Read-Only Mode

When the Postgres primary becomes unreachable but a read replica is up, the registry falls back to **read-only mode**: read endpoints (`load_domain`, `search_domains`, `search_artifacts`, `load_artifact`, `load_artifacts`) continue to serve from the replica; every `/v1` catalog and administrative endpoint that mutates registry state is rejected with the structured error `registry.read_only`. Ingest webhooks, layer admin operations, freeze toggles, admin grants, and tenant management are named examples and do not bound the rule. The §6.3.1 SCIM 2.0 receiver at `/scim/v2/` is outside this write set; its writes are not gated by read-only mode. Each endpoint's own section states its classification, as §7.3.3 does for the tenant-management endpoints and §4.7 does for the catalog re-embed.

A health-state machine governs the transition. The registry probes the primary every 5 s and flips to read-only after three consecutive failures (tunable via `PODIUM_READONLY_PROBE_INTERVAL` and `PODIUM_READONLY_PROBE_FAILURES`). It flips back automatically after three consecutive probe successes once the primary is reachable again.

Read responses in read-only mode carry two additional headers:

- `X-Podium-Read-Only: true`
- `X-Podium-Read-Only-Lag-Seconds: <n>`: observed replication lag at response time. Clients that need strict freshness can retry once the registry leaves read-only mode (or surface the staleness to a human reviewer via the existing offline/staleness affordance, §7.4).

Audit events for state transitions (`registry.read_only_entered`, `registry.read_only_exited`) are logged like any other admin action and carry the same hash-chain integrity guarantees as ingest and admin events. Ingest events that would have fired during the read-only window are queued by the Git provider's webhook retry policy and replayed on exit; webhooks from receivers that don't retry leave their corresponding ingests pending until the next manual `podium layer reingest`.

The MCP server, SDKs, and `podium sync` propagate the read-only signal: the MCP `health` tool reports `mode: read_only`, SDKs raise `RegistryReadOnly` on attempted writes, and `podium sync` continues to materialize against the cached effective view (the read path is unaffected).

### 13.2.2 Public Mode

A misconfigured public-mode deployment is the most common security-relevant operational anomaly because the registry serves correctly. It just serves to everyone. The runbook entry exists to make it easy to detect and recover from.

**Detection.** `/healthz` returns `mode: public`. Audit events for read calls show `caller.identity: "system:public"` and the flag `caller.public_mode: true`. The registry's startup banner shows the public-mode warning. Operators investigating a deployment can confirm with `podium status`, which surfaces the same flag.

**Impact.** Authentication is skipped; visibility is bypassed (§4.6). Every artifact is reachable to every caller that can connect to the registry's bind address. Ingest of `sensitivity: medium` and `sensitivity: high` artifacts is rejected; existing artifacts at those levels (ingested before public mode was enabled) continue to be served.

**Mitigation.**

1. Confirm public mode was the intended deployment posture. If it was, no action needed; the audit log already records the intent.
2. If public mode was _not_ intended (a misconfigured environment variable, copy-pasted CLI flag, or accidental container image tag), stop the registry, remove `--public-mode` / unset `PODIUM_PUBLIC_MODE`, restart. The registry refuses mid-run flips, so a restart is mandatory.
3. If public mode was running on an internet-exposed registry (which the safety check should have prevented unless `--allow-public-bind` was set), treat as a security incident: rotate any signing keys that were in scope, audit the access log for unfamiliar IPs, and proceed per the org's incident-response procedure.

**Prevention.** Container-image and Helm-chart consumers should set `PODIUM_NO_AUTOSTANDALONE=1` and use `--strict` to refuse anything but explicitly-configured deployments. Public mode requires an explicit flag, so a strict-only deployment cannot accidentally land in it. Production CI templates should fail-fast on the presence of `PODIUM_PUBLIC_MODE` in environment lists.

## 13.3 Backup and Restore

- Postgres: logical + physical backups; point-in-time recovery.
- Object storage: cross-region replication or snapshots.
- Registry signing key: the file at `PODIUM_SIGN_KEY_PATH`, or the Secret a Helm deployment mounts there, backed up beside the store it signs. Losing it leaves every row it signed refused at the §13.4 stored-row admission until the §13.12 repair, or, where that repair is unavailable, until a new version of the artifact is ingested.
- Consistent restore via PITR + object-storage version history.
- Default RPO 1h / RTO 4h.

## 13.4 Migrations

The registry applies its metadata-store schema from the binary on startup. Each backend's setup is idempotent and additive: tables are created when absent (`CREATE TABLE IF NOT EXISTS`) and new columns are added when absent (Postgres uses `ADD COLUMN IF NOT EXISTS`; SQLite, which lacks that syntax, issues `ALTER TABLE ... ADD COLUMN` and ignores the duplicate-column error on an already-migrated database), so a binary upgrade that changes only the schema migrates an existing database forward in place, without a separate migration step and without downtime. The SQLite and Postgres schema live in their respective `applySchema` paths.

The additive guarantee covers the schema and not the values stored under it. A release that changes how a stored value is computed rewrites the affected rows in place, from the bytes the registry holds, on the first start of the new version that binds its listen address, before that start ingests or serves, and records that the rewrite completed so that later starts skip it. Every registry process on the previous version is stopped before the first process on the new version starts, because the two versions compute different values over the same bytes. The release's changelog names the change and the order in which the registry and the consumers roll.

The §4.7.6 content hash is such a value. The registry recomputes each stored row, including a soft-deleted one, from the stored manifest, `SKILL.md`, and bundled-resource bytes, and it changes no stored object and no table other than the manifest rows and the completion record. It rewrites a row only when the row's stored bytes reproduce its stored hash or the digest the previous release computed over them, and, where a §4.7.9 signer is configured, a signed row only when its stored signature verifies over the stored hash under a key of the §4.7.9 verification key set. Where a signer is configured, it signs under the signing key each row it rewrites, including a row whose stored hash already equals the new value and that carries no signature, and each row at the new hash whose stored bytes reproduce that hash and whose stored signature verifies under a verification-only key, and it appends one `artifact.signed` event (§8.1) for each, carrying the §8.2 redaction ingest applies to that event, while the audit sink accepts events. The rewrite a registry start runs, and the rewrite `sign-stored-rows` runs in its place without `--include-unsigned`, signs a row that carries no signature only when the store is the SQLite store in the directory that holds the key file at `PODIUM_SIGN_KEY_PATH`. The rule compares directories only and does not check file modes or ownership; it relies on the operator granting write access to that directory only to accounts that can also read the key file in it. For every other store it moves such a row to the new digest when its bytes reproduce the previous release's digest, leaves it unsigned, logs how many rows it left unsigned on a line of its own after the rewrite's summary line, together with the instruction to run `sign-stored-rows --include-unsigned --dry-run`, review its report, and pass its plan digest, and does not hold the completion record back for it. It leaves every other row untouched and logs it. A row left untouched because its stored bytes or its stored signature fail those checks does not hold the completion record back. A row left untouched because its bytes could not be read, or because signing or writing it failed, holds the record back, and the next start tries that row again. A bundled-resource body that object storage reports as absent counts as unreadable only when no object-storage read in that start returned a body. A row that another writer changed or removed between the read and the rewrite is not a failed write. The registry waits for each object-storage read no longer than the deadline `PODIUM_MIGRATION_OBJECT_READ_TIMEOUT` sets (§13.12).

While no completion is recorded, the registry refuses to start, having rewritten no row and recorded no completion, when the rewrite would strand a stored signature: when no signer is configured and a signed row's stored bytes reproduce the digest the previous release computed over them, and when a stored row, including a soft-deleted one, carries a signature and the configured signing key did not exist before that start. A refused start leaves nothing behind that changes the outcome of the next start with the same configuration. A load of a row still at the previous release's digest fails closed at the registry's stored-row admission check. `podium admin migrate-to-standard` (§13.10) copies the manifest rows it migrates with their stored `content_hash` and signature unchanged and, before it copies the first of them, removes the target store's record of the rewrite, so the next start of the target registry rewrites the copied rows. No registry process runs on the target store while the command runs, and a target registry started before the command's first run is stopped before that run. The target registry is started, or restarted when it was running before the command, only after a run of the command succeeds. The target store is recreated empty before the command runs again when a run fails because the target holds a copied row at a content hash that differs from the source's, or when, at any point after the command's first run against it began, including while a run was in progress, a registry started on the target store or the source registry started on the new version.

The `sign-stored-rows` command, run as `podium-server sign-stored-rows` or `podium admin sign-stored-rows`, applies the rewrite's classification and writes to the store on operator demand and exits without binding a listen address. It reads the configuration a registry start reads and opens the same store and object storage. It refuses with `config.signature_provider_unavailable`, writing nothing, when signing is off or the key file at `PODIUM_SIGN_KEY_PATH` is absent, and it never generates a key. It is subject to the §13.12 refusal for a signing key that is not persisted with the store. When the store holds no record that the rewrite completed, the command runs the rewrite in place of the first start, under the same precondition that no registry process on the previous version serves the store, signs every row that carries no signature that the first start would sign, and records completion under the same rule. When the record is present, the command applies the same rules to every stored row and may run while registry processes on this version serve the store, because each write replaces a row only when its stored content hash and stored signature are the ones the command read. Beyond the rows the rewrite signs in place of the first start, the command signs a row that carries no signature only when it is invoked with `--include-unsigned`, which is the operator's attestation for every unsigned row the reviewed dry run lists, because an unsigned row carries no evidence of who stored it. Outside `--dry-run`, `--include-unsigned` requires `--plan-digest`. Without that flag, a row that carries no signature and that the command does not sign is moved to the new digest when its bytes reproduce the previous release's digest, and is left unsigned. It never signs a row whose envelope fails under every key of the verification key set, and it leaves a row already signed under the signing key at the new hash untouched. With `--dry-run` it reports every write it would make and makes none, the completion record included. It lists every planned row with its stored content hash, in an order that does not depend on the order in which the store lists rows, and it reports a plan digest. The plan digest is a SHA-256 digest, written `sha256:` followed by 64 lowercase hexadecimal digits. It depends on whether the completion record is present, whether `--include-unsigned` is given, the signing key's `key_id` or the absence of a signer, and the set of verification-only `key_id`s. It also depends, for each planned row, on the row's tenant, artifact ID, version, and stored content hash, the outcome the command reached for the row, which determines whether it writes the row, the content hash a write would store, whether the command signs the row, and the `key_id` that verifies its stored envelope, or the envelope's absence, or its failure to verify, or, with no signer configured, the envelope's presence, which the command does not check. A row whose stored bytes reproduce its stored content hash, whose stored signature, where a signer is configured, is absent or verifies under the signing key, and that the command neither writes nor signs does not enter the digest. The digest does not depend on the order in which the store lists rows. Its byte encoding belongs to the release that computes it, so a dry run and a run of different releases can compute different digests for one plan, and the run then refuses. It reports how many rows it left unsigned and, for each verification-only key, identified by its `key_id`, how many stored rows remain signed under that key, counting every row whose envelope that key verifies and that the run did not re-sign, whether or not the row's stored bytes could be read. The count supports removing a key from the verification key set only when the run exits with a success status. It exits with a failure status when a row holds the completion record back or a write fails. Given `--plan-digest`, the command computes the plan digest of the plan it builds before it writes anything. When that digest differs from the given value, it writes no row, no audit event, and no completion record, reports its own plan in the form the dry run reports it, and exits with a failure status distinct from its other failure statuses. `--plan-digest` given with `--dry-run`, a value that is not `sha256:` followed by 64 lowercase hexadecimal digits, and `--include-unsigned` given without `--dry-run` and without `--plan-digest` are usage errors, which the command refuses before it opens the store. The digest binds the plan the command builds to the plan the operator reviewed. A row stored after the command builds its plan is absent from that plan, and the command leaves it as it is.

Before the registry serves an artifact's manifest, `SKILL.md`, or bundled resources, on a `load_artifact` that returns a body and on each item of `artifacts:batchLoad`, it admits every stored row the served record is built from: the requested row and, for an artifact that declares `extends:`, each row of its chain. Admission runs these checks in order. (1) It recomputes the §4.7.6 content hash from the row's stored manifest, `SKILL.md`, and bundled-resource bytes, reading any body held in object storage within the `PODIUM_MIGRATION_OBJECT_READ_TIMEOUT` deadline (§13.12). It requires the row's bundled resources to have distinct paths, and each resource's stored per-resource content hash and size to be those of the bytes it read, whether they came from the row or from object storage. Two resources under one path, a hash that differs from the stored one, a resource whose stored content hash or size differs from its bytes, and a body object storage reports as absent are refused with `materialize.content_hash_mismatch`. Any other failed or timed-out object read is refused with `registry.unavailable`. (2) It checks the row's stored parent pin against the `extends:` reference the admitted manifest declares. The pin is empty exactly when the manifest declares none, and a manifest that is empty or does not parse declares none. Otherwise the pin names the referenced parent ID and, unless the reference is a content-hash reference, a version that satisfies the reference (§4.7.6). A failed pin is refused with `materialize.content_hash_mismatch`. (3) Where a §4.7.9 signer is configured, it verifies the stored signature over the stored content hash under a key of the registry's §4.7.9 verification key set. A row with no stored signature is refused with `materialize.signature_missing`, and a row whose signature does not verify, including one signed under a key outside that set, is refused with `materialize.signature_invalid`. (4) Admission follows a row's pin only after that row has passed checks (1) through (3): it reads the pinned parent row, admits that row by these same checks, and, for a content-hash reference, requires the parent row's admitted content hash to be the one the reference names, refusing a mismatch with `materialize.content_hash_mismatch`. Admission walks a chain from the requested row toward its root, and a row is refused with the code of the first check it fails in this order, so the first refusal in the walk decides the load's code. A pin that passes check (2) but names no stored row, or that returns to a row already walked, fails the load as an unresolvable `extends:` chain, as it does today. A parent row deleted after its child was ingested reaches the first case. A load whose chain holds a refused row is refused with that row's code, and the error names only the requested artifact. The registry serves the body, type, sensitivity, deprecation flag, replacement, and `audit_redact` key set a load returns from the admitted bytes, and for an `extends:` child from the §4.6 merge over the admitted rows. The version a load returns is the stored row's. A row whose manifest is empty or does not parse is served with its stored values; ingest never stores such a row. The registry serves a bundled resource the row holds inline from those admitted bytes, whatever its size, and a presigned object-storage link only for a resource whose bytes admission read from object storage (§7.2, §7.6.2). The registry keeps no admission verdict. A `load_artifact` HEAD, and a conditional `load_artifact` whose validator matches the one the registry publishes to the requesting identity for the resolved row (§7.2), return no content and are answered from the resolved row without admission. Search results, the §4.7.3 reverse index, the §4.5 domain listings, and the §13.12 object route serve no admitted content and are served without admission. Admission checks a row's content and does not check which row answers a request. The stored signature attests the content hash alone (§4.7.9), so admission does not bind a row to its tenant, artifact ID, version, or layer, to its ingest time, or to the soft-delete and deprecation state that version resolution reads. A party that can write the metadata store without the signing key can therefore withhold a row, serve an admissible row under another row's identity, or have an older admissible row served in place of a newer one. Such a party that can also cause a restart can have content it chose signed only where the store is the SQLite store in the key file's directory: the record that the §13.4 first-start rewrite has completed is held in the metadata store, and a start whose listener binds and that finds no record runs the rewrite, which there signs with the registry's key every stored row that carries no signature and whose bytes reproduce the new digest or the previous release's digest. For every other store such a start signs no row that carries no signature. A later `sign-stored-rows --include-unsigned` signs every unsigned row its reviewed dry run lists, including one such a party stored before that dry run. An unsigned row such a party stores after the dry run and before the run builds its plan changes the plan digest, and the command refuses. A row stored after the run builds its plan is absent from that plan and stays unsigned. The start logs the rewrite's summary line, which is where that run is observable. The rewrite and `sign-stored-rows` also re-sign rows signed under a verification-only key, as the paragraph that begins "The §4.7.6 content hash is such a value." states, so while a compromised key stays on a `verify:` line, such a run re-signs rows its holder stored. A pin moved to another stored version of the declared parent that still satisfies an unpinned, `latest`, minor, or major reference is a choice of the same kind, because ingest resolves such a reference against the versions stored at ingest time and the bytes do not record which version it chose. An author who needs the parent fixed pins it by exact version or by content hash.

Type definitions are compiled into the binary and evolve with it. The first-class types and any registered `TypeProvider` validators ship as code, so a binary upgrade is the type-system migration; there is no separate versioned type-migration artifact.

## 13.5 Multi-Region

A deployment is single-region. Cross-region read replicas via Postgres logical replication and object-storage replication; writes route to the primary region.

## 13.6 Sizing

Baseline: 10K artifacts, 100 QPS, 1 GB Postgres, 500 GB object storage handles a typical mid-sized org on a 3-replica deployment + db.m5.large equivalent.

Scale guidance:

- 100K artifacts: pgvector scale; consider sharding embeddings.
- 1K QPS: scale front-end replicas; CDN in front of object storage.
- 10K QPS: review search query patterns; consider dedicated Elasticsearch for BM25.

## 13.7 CDN

Presigned URLs are CDN-friendly. Recommend CloudFront / Fastly / Cloudflare in front of object storage for hot artifacts. Cache headers safe because content_hash keys are immutable.

## 13.8 Observability

- **Metrics.** Prometheus endpoint on registry and MCP server. Histograms for latency; counters for cache hit rate, error rate, visibility-denial rate, ingest success/failure rate; gauges for queue depths. The registry serves `/metrics` by default; set `PODIUM_METRICS=false` to remove it. The MCP server is a stdio process with no HTTP listener of its own, so it serves `/metrics` on a separate address only when `PODIUM_MCP_METRICS_ADDR` (or `--metrics-addr`) names a bind address.
- **Tracing.** OpenTelemetry trace export. W3C Trace Context propagation across all calls. One root span per `load_domain` / `search_domains` / `search_artifacts` / `load_artifact`; child spans for registry round-trip, object-storage fetch, adapter translation, materialization. Tracing is off by default; set `PODIUM_TRACING=otlp` (or a standard `OTEL_EXPORTER_OTLP_ENDPOINT`) to export over OTLP/HTTP, or `PODIUM_TRACING=stdout` for local inspection. The W3C propagator is always installed, so trace context flows between the bridge and the registry even when export is off.
- **Reference Grafana dashboard** ships with the registry.

## 13.9 Health and Readiness

- Registry: `/healthz` (liveness) and `/readyz` (readiness, including Postgres and object-storage reachability).
- `/readyz` reports one of `mode: ready | read_only | not_ready`. `read_only` is healthy from a load-balancer perspective (the registry should stay in rotation to serve reads) but signals upstream tooling that writes are being refused. Response body includes observed replication lag in seconds. See §13.2.1 for the state machine and the corresponding response headers.
- MCP server: `health` MCP tool returning registry connectivity + observed registry mode (`ready` / `read_only` / unreachable) + cache size + last successful call timestamp.

## 13.10 Standalone Deployment

`podium serve --standalone` collapses the full stack into a single binary with no external dependencies. It targets local development, individual contributors, and small-team installations where running Postgres + object storage + an IdP is overkill.

For a lighter setup with no daemon (just the CLI reading the artifact directory directly), see §13.11 (Filesystem Registry).

**Zero-flag default.** Running `podium serve` with no flags is equivalent to `podium serve --standalone` when no server config is found at `~/.podium/registry.yaml` and no `PODIUM_*` server-side environment variables are set. The server emits a clear stderr line on startup ("No config found at `~/.podium/registry.yaml`. Starting in standalone mode at `http://127.0.0.1:8080`. Run `podium serve --strict` to require explicit setup."), creates the standalone defaults (`~/.podium/registry.yaml`, `~/.podium/sync.yaml`, `~/podium-artifacts/`) on first run, and proceeds to serve. This collapses the five-minute install path into a single command, with no `podium init` step required.

`podium serve --strict` retains the prior behavior of refusing to start without explicit configuration. Setting `PODIUM_NO_AUTOSTANDALONE=1` in the environment has the same effect; this is useful in CI and image-building contexts where a missing config should always be a hard error rather than an auto-bootstrap. Auto-bootstrap is also suppressed when `--config <path>` is passed and the file does not exist (the user explicitly named a config; missing config is an error rather than a cue to invent one).

```bash
# Zero-flag — auto-enters standalone mode if no config exists at ~/.podium/registry.yaml
podium serve

# Explicit standalone with custom paths
podium serve --standalone \
  --layer-path /var/podium/artifacts \
  --bind 127.0.0.1:8080

# Refuse to start without explicit config (CI, image builds)
podium serve --strict
```

**What changes from the standard topology:**

| Concern                 | Standard                                        | Standalone                                                                                                                                                                                                                                                                                     |
| ----------------------- | ----------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Metadata store          | Postgres                                        | Embedded SQLite (`~/.podium/standalone/podium.db`)                                                                                                                                                                                                                                             |
| Vector store            | pgvector                                        | `sqlite-vec` extension loaded into the same SQLite file                                                                                                                                                                                                                                        |
| Embedding provider      | `openai` (default)                              | `ollama` pointed at a local model server, or any cloud provider; `--no-embeddings` falls back to BM25-only                                                                                                                                                                                     |
| Object storage          | S3-compatible                                   | Filesystem (`~/.podium/standalone/objects/`)                                                                                                                                                                                                                                                   |
| Identity provider       | OIDC IdP                                        | None by default. No auth; `127.0.0.1`-only HTTP. A standalone server fronted by a gateway can set `PODIUM_IDENTITY_PROVIDER=oidc-jwt` or `trusted-headers` to filter by gateway-delegated identity (§6.3.3); `trusted-headers` on a non-loopback bind fails to start with `config.trusted_headers_public_bind` unless a proxy secret or `--allow-public-bind` is set.                                                                                                                                                                                                                                                |
| Layers                  | Configured admin layers + user-defined layers   | `--layer-path` is polymorphic (see "**`--layer-path` modes**" below): single-layer mode produces one `local`-source layer rooted at the path; filesystem-registry mode treats each subdirectory as a `local`-source layer per §13.11.1. Additional `local` and `git` layers can be registered via `podium layer register` in either mode.                                                                                                                                                   |
| Git provider / webhooks | Required for `git`-source layers                | `git` source layers work without webhooks; webhooks are optional. Without a webhook (typical for a developer machine without a public ingress), `podium layer reingest <id>` pulls the current state on demand, and `podium layer watch <id>` polls the source at a configured interval.        |
| Signing                 | Registry-managed key                            | Enabled by default. The Ed25519 keypair is generated on first run at `PODIUM_SIGN_KEY_PATH` (default `~/.podium/standalone/registry-signing.key`). `--sign none` disables it |
| Content cache           | Cross-workspace disk cache (`~/.podium/cache/`) | Disabled; the registry is local, the cache adds nothing                                                                                                                                                                                                                                        |
| Audit                   | Per-tenant Postgres table                       | Same SQLite file (audit table)                                                                                                                                                                                                                                                                 |
| Helm chart / Kubernetes | Required for production deployments             | Not used                                                                                                                                                                                                                                                                                       |

**`--layer-path` modes.** The path passed to `--layer-path` is interpreted as either a single-layer directory or a filesystem-registry root. The standalone server selects between these via an explicit dispatch:

- **Filesystem-registry mode.** When `<path>/.registry-config` exists and sets `multi_layer: true`, and the path contains no manifest files (`ARTIFACT.md`, `SKILL.md`, `DOMAIN.md`) directly at its top level, `<path>` is treated as a filesystem-registry root. Each subdirectory of `<path>` becomes a `local`-source layer; ordering follows §13.11.1 (alphabetical by subdirectory name, optionally overridden by `layer_order:` in `.registry-config`). This is the mode used when migrating a filesystem registry (§13.11.6) to a server pointed at the same directory.
- **Single-layer mode (default).** When `.registry-config` is absent, when it sets `multi_layer: false`, or when it is omitted entirely, `<path>` is treated as a single layer. The directory's contents are the layer's domain hierarchy; one `local`-source layer rooted at `--layer-path` is registered, with a default layer ID derived from the directory name.

If `multi_layer: true` is set but the safety check fails (manifest files are present directly at the top level of `<path>`), the server refuses to start with `config.layer_path_ambiguous`, naming the conflicting top-level manifest paths so the operator can either remove them or unset `multi_layer`.

**Hybrid search.** Standalone runs the same BM25 + vector RRF retriever as the standard registry. Vectors live in `sqlite-vec`, loaded as a SQLite extension into the standalone database. Embeddings come from the configured provider via `PODIUM_EMBEDDING_PROVIDER` (one of `openai`, `voyage`, `cohere`, `ollama`); `ollama` is the recommended choice for self-hosted local models when the deployment must stay offline. `--no-embeddings` falls back to BM25-only when no provider is configured.

**Upgrade path.** A standalone deployment migrates to standard via `podium admin migrate-to-standard --postgres <dsn> --object-store <url>` (covered in §13.4). Layer config, admin grants, and audit history are preserved; embeddings are re-computed against the target vector backend on first ingest.

**Web UI.** When `podium serve` (standalone or standard) is started with `--web-ui` (or `PODIUM_WEB_UI=true`), the same process exposes a single-page web UI at `http://<bind>/app/`. On that process `GET /` redirects the browser to `/app/`. A process started without the flag mounts neither the UI nor that redirect, and a request for `/` on it is answered as any path the registry does not register is answered on that deployment. The UI is a static SPA bundled into the binary; it talks to the registry's HTTP API as any other consumer would. What it surfaces:

- **Domain browser**: hierarchical navigation matching `load_domain`'s structure.
- **Search**: text input that calls `search_artifacts` with the same `type` / `scope` / `tags` filters as the SDK and CLI.
- **Artifact viewer**: manifest body rendered as markdown, frontmatter as a property table, links to extending or dependent artifacts.
- **Layer panel**: list registered layers with their source, visibility, and `last_ingested_at`. Admins can register, reingest, and unregister layers from the UI; users can manage their own user-defined layers (cap per §7.3.1). The UI is a thin client over the same `podium layer …` HTTP endpoints. The panel renders a control that would take one of the §7.3.1 layer write operations only where the §7.3.4 posture read and the fields carried by that operation's target settle that the §7.3.1 rules admit this caller on it. The target is the layer as the operation would name it: the stored layer for `unregister`, `restore`, `reingest`, and `reorder`; for `update` the stored layer's class and owner together with the fields the patch would carry; and for `register` the registration the dialog would build, whose class is the class that dialog asks for and whose owner is the registrant. The panel reads the target's class, its stored owner, its source type, and its filesystem path, and it reads no other field and predicts no other rule. A control that names a value the registry resolves away rather than refuses, such as the class of a registration, is withheld on the same reading, because the caller is not admitted to the operation as that control would name it. A control whose request the panel narrows to the layers the rule admits is rendered where the rule admits the caller on at least one of them, and it acts on that subset alone. The rule governs every such control the panel renders, including the controls inside its registration and update dialogs and the controls on its recovery table, and the §13.2.1 read-only marker then disables whatever the rule leaves present. A control whose availability turns on the layer record alone, with no dependence on the caller, is outside this rule. Two arms are exceptions. A reordering affordance is presented disabled with its reason named rather than removed, and it is settled over every layer a move from the row carrying it would reorder rather than over that row alone, because the request names all of them and the registry refuses it whole. A refusal the target's own fields do not settle, such as a `git` repository string that resolves to the Git file transport, is presented and answered by the registry. The registry's refusal remains authoritative: the panel predicts and authorizes nothing, and an offered operation can still be refused (§7.3.4), which the panel presents where the operation was attempted.

Authentication: in standalone deployments without an identity provider, the UI is open on the bind address (default `127.0.0.1`, which is not network-exposed). A standard deployment that authenticates its own callers with `oidc-jwt` (§6.3.3) verifies the IdP-signed token that a CLI, an SDK, or another API client acquired through the device-code flow; acquisition happens in the client and verification happens in the registry. Where the browser flow is enabled (`--web-ui-auth` / `PODIUM_WEB_UI_AUTH`), the UI signs the browser in through the registry: the registry redirects the browser to the IdP, performs the authorization-code exchange itself, and returns the resulting IdP-signed token in the `__Host-podium_session` cookie (§6.3.4). That token is the credential §6.3.3 already accepts, verified against the issuer JWKS for the same `aud`, so the browser flow adds no credential kind and no session state in the registry. The registry reads the configured token header first, and it reads `__Host-podium_session` only where that header carries no bearer credential and the browser flow is enabled on that registry. A request that presents neither resolves as anonymous and sees public visibility only (§4.6). Where the browser flow is disabled, the web UI runs no acquisition flow of its own and resolves identity solely from what the request carries, so a UI request that reaches the registry directly carries no credential, resolves as anonymous, and sees public visibility only, unless a gateway forwards the token or injects the identity headers. Where a gateway fronts the registry under `oidc-jwt` or `trusted-headers` (§6.3.3), the UI is served by the same registry process behind the same gateway: the gateway authenticates the request and the registry resolves the caller's identity from the forwarded token or the injected headers, exactly as for any other API request, so the UI inherits the request's resolved identity. Where the registry is directly reachable under `oidc-jwt`, it verifies the IdP-signed token the caller presents itself. A non-loopback web-UI bind under `trusted-headers` is also subject to the provider's bind restriction, so it requires `PODIUM_TRUSTED_PROXY_SECRET` or `--allow-public-bind` in addition to `--web-ui-allow-public-bind`.

Behind a flag: opt-in via `--web-ui` so headless deployments (CI runners, managed runtimes) don't pay the binary-size or attack-surface cost when they don't need it. The binary refuses to bind the UI to a non-loopback address unless `--web-ui-allow-public-bind` is also passed _and_ an identity provider is configured, so a UI reachable beyond the loopback interface is served only by a registry that resolves a caller's identity and filters what it serves by that identity.

**Browser-flow configuration guard.** Enabling the browser flow requires that the web UI is enabled, that `PODIUM_IDENTITY_PROVIDER` is `oidc-jwt`, that public mode is off, that every acquisition value (`PODIUM_WEB_UI_OAUTH_CLIENT_ID`, `PODIUM_WEB_UI_OAUTH_CLIENT_SECRET`, `PODIUM_WEB_UI_REDIRECT_URI`, `PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT`, and `PODIUM_WEB_UI_OAUTH_TOKEN_ENDPOINT`) is non-empty, and that `PODIUM_WEB_UI_REDIRECT_URI` is an `https` URL or an `http` URL whose host is a loopback address. Those conjuncts are the whole guard. A configuration that enables the flow and fails one of them fails to start with `config.web_ui_auth_unconfigured`, naming the failed conjunct. A configuration that enables no browser flow reaches no conjunct and starts, which includes `--web-ui` alone. The guard runs after the public-mode exclusion that refuses public mode together with a configured identity provider, so that exclusion's predicate, its error, and its message are unchanged and a registry configured for public mode with `oidc-jwt` still fails with `config.public_mode_with_idp`. The browser-flow guard therefore reaches its public-mode conjunct only in a configuration where no identity provider is configured, and in that configuration the `oidc-jwt` conjunct is the one that fails and the one the error names, so no configuration reaches `config.web_ui_auth_unconfigured` naming the public-mode conjunct. The guard carries the public-mode conjunct even so, because it states the full set of conditions the flow requires and the public-mode exclusion is keyed on `PODIUM_IDENTITY_PROVIDER` alone, so that exclusion does not fire on the enablement key.

The redirect-URI conjunct follows from the cookie contract: the browser flow's cookies carry the `__Host-` prefix, the prefix forces `Secure` unconditionally, and a browser neither stores nor returns a `Secure` cookie on a non-secure origin. A loopback `http` address is admitted because a browser treats it as a secure context and stores and returns a `Secure` cookie set there. An `https` redirect URI is satisfied whether the registry is reached over TLS directly or through a gateway that terminates TLS and forwards plain HTTP to the registry's listener (§6.3.3), so a gateway-fronted deployment passes the conjunct on a non-loopback plain-HTTP bind. The bind guard above does not imply the conjunct: it admits a non-loopback bind once `--web-ui-allow-public-bind` and an identity provider are set, and it constrains the bind address rather than the registry's browser-facing origin.

**Web-UI configuration keys.** A key that carries both forms is set by the flag or by the variable, and the flag overrides the variable when both are set.

| Key | Forms | What it carries |
|:--|:--|:--|
| `--web-ui` / `PODIUM_WEB_UI` | flag and variable | one boolean, which serves the web UI |
| `--web-ui-allow-public-bind` / `PODIUM_WEB_UI_ALLOW_PUBLIC_BIND` | flag and variable | one boolean, which admits a non-loopback web-UI bind under the bind guard above |
| `--web-ui-auth` / `PODIUM_WEB_UI_AUTH` | flag and variable | one boolean, which enables the browser flow |
| `--web-ui-auth-transaction-ttl` / `PODIUM_WEB_UI_AUTH_TRANSACTION_TTL` | flag and variable | the sign-in window, carried as the pre-authorization cookie's `Max-Age` (§7.3.4) |
| `PODIUM_WEB_UI_OAUTH_CLIENT_ID` | variable | the OAuth client identifier the sign-in redirect sends |
| `PODIUM_WEB_UI_OAUTH_CLIENT_SECRET` | variable | the client credential the callback's token request sends |
| `PODIUM_WEB_UI_REDIRECT_URI` | variable | the callback URL the IdP returns the browser to |
| `PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT` | variable | the IdP endpoint the sign-in route redirects to |
| `PODIUM_WEB_UI_OAUTH_TOKEN_ENDPOINT` | variable | the IdP endpoint the callback exchanges the code at |
| `PODIUM_WEB_UI_OAUTH_SCOPES` | variable | the space-delimited scope set the sign-in redirect sends, defaulting to `openid profile email groups` |
| `PODIUM_WEB_UI_OAUTH_EXCHANGE_TIMEOUT` | variable | the deadline on the callback's token-endpoint request, defaulting to 10 seconds |

None of these keys carries a config-file form. The audience the sign-in redirect sends is the first value of the audience set configured through the `oidc-jwt` keys `PODIUM_OAUTH_AUDIENCE` or `identity_provider.audience` (§6.3.3, §13.12) rather than through a web-UI key.

The UI is the recommended consumption path for non-developer users (analysts, prompt authors, reviewers) who want to browse the catalog without installing the SDK or learning the CLI.

**Sensible defaults for permissive deployments.** Standalone deployments shift several defaults toward low-friction rather than secure-by-default, appropriate for the solo and small-team contexts standalone targets:

- **Layer visibility.** New layers registered via `podium layer register` default to `visibility: public` on a standalone server without an identity provider (instead of `users: [<registrant>]` as in standard mode for user-defined layers). Once an identity provider is enabled (§6.3), the unset default resolves to `private` instead, so admin layers are not public to every caller once the registry filters by identity. Override with `PODIUM_DEFAULT_LAYER_VISIBILITY`, which the registry applies verbatim regardless of the identity provider.
- **Sandbox profile.** `sandbox_profile:` is informational in standalone. Hosts honor it as in standard mode, but the registry does not refuse to ingest artifacts whose profiles can't be enforced locally. Override with `PODIUM_ENFORCE_SANDBOX_PROFILE=true` in multi-user setups.
- **Sensitivity.** Artifacts without an explicit `sensitivity:` field default to `low`. The lint check that flags missing sensitivity is downgraded from a warning to a hint.

Any of these defaults can be flipped to standard-mode behavior via the named env var without otherwise changing the deployment; the same single binary continues to serve.

**Gateway-delegated identity (`oidc-jwt`, `trusted-headers`).** A standalone server fronted by a gateway authenticates callers by setting `PODIUM_IDENTITY_PROVIDER=oidc-jwt`, which verifies a forwarded token, or `trusted-headers`, which trusts gateway-injected identity headers (§6.3.3). Either filters layer visibility (§4.6) by the gateway-delegated identity. Neither adds multi-tenancy, which stays out of scope for standalone, so the registry resolves every caller to its sole tenant. `trusted-headers` reads identity from unverified headers, so it constrains the bind: a loopback bind is always allowed, and a non-loopback bind fails to start with `config.trusted_headers_public_bind` unless `PODIUM_TRUSTED_PROXY_SECRET` or `--allow-public-bind` is set. `oidc-jwt` verifies every token regardless of the network path and carries no bind restriction. Both are mutually exclusive with public mode.

**Public mode (`--public-mode` / `PODIUM_PUBLIC_MODE`).** A registry-level switch that bypasses both authentication and the visibility model in one step. Replaces "progressively disable each governance feature" with a single explicit decision, appropriate for solo demos, evaluation pilots without team context, and intentionally open internal-knowledge-base deployments.

```bash
# Standalone, fully open
podium serve --public-mode --layer-path ~/podium-artifacts

# Or via env var
PODIUM_PUBLIC_MODE=true podium serve
```

Startup banner:

```
⚠  PUBLIC MODE: all artifacts visible to all callers without authentication.
   Bound to 127.0.0.1 by default; pass --allow-public-bind to bind a non-loopback address.
```

What public mode does:

- **Skips OAuth.** No `podium login`, no JWT verification, no OIDC config required. Callers reach the registry without credentials.
- **Bypasses visibility.** The visibility evaluator (§4.6) short-circuits to `true` for every layer and every caller. Layer `visibility:` declarations are still accepted into config (so artifacts remain portable to non-public deployments) but ignored at request time.
- **Records `system:public`** in audit (§8.1). Source IP and any `X-Forwarded-User` header from an upstream proxy are preserved.
- **Leaves ingest unchanged.** `content_hash` immutability, lint, hash-chained audit, and signing (when configured) all behave normally.

Safety constraints:

- **Mutually exclusive with an identity provider.** Setting `PODIUM_PUBLIC_MODE` and `PODIUM_IDENTITY_PROVIDER` (or the equivalent config keys) at the same time fails at startup with `config.public_mode_with_idp`. Public mode is the absence of authentication; it is not an alternative identity provider. The deployment must choose one.
- **Loopback bind by default.** Public mode binds to `127.0.0.1` unless `--allow-public-bind` is _also_ passed. The escape hatch exists for deployments behind an authenticated reverse proxy that enforces who can reach the registry; without the proxy, the operator is taking explicit responsibility for the security model.
- **Sensitivity ceiling.** Ingest of `sensitivity: medium` or `sensitivity: high` artifacts is rejected with `ingest.public_mode_rejects_sensitive`. Public mode is for low-stakes content only. Artifacts already at those levels (ingested before public mode was enabled) continue to be served; public mode does not retroactively delete content.
- **One-way for the deployment's lifetime.** Toggling public mode requires a config change _and_ a registry restart. The registry refuses to flip the mode mid-run, preventing an admin accidentally toggling away protections through a config-reload signal.
- **Loud at every checkpoint.** The mode is surfaced in `/healthz` (`mode: public`), in the MCP `health` tool, in `podium status`, and as a flag (`caller.public_mode: true`) on every audit event so downstream tooling can detect it without inspecting startup config.

When to use public mode vs sensible-defaults standalone:

- **Use sensible defaults** when you're a single user or small team using standalone for productivity. The visibility model is already trivially permissive; no extra ceremony.
- **Use public mode** when (a) the deployment is intentionally open beyond a single user (a demo registry, an internal-public catalog, an evaluation pilot, etc.) and (b) you want the audit log to record that anonymous-public access was the deployment's intent rather than a misconfiguration.

Migration to a governed deployment goes through `podium admin migrate-to-standard --postgres <dsn> --object-store <url>` (§13.4), followed by removing the `--public-mode` flag and reconfiguring layer visibility. Same migration path standalone uses today.

**Out of scope for standalone.** Multi-tenancy, freeze windows, SCIM, transparency-log anchoring, outbound webhooks. These are present in the binary but inert without the supporting infrastructure (an IdP for SCIM, a Sigstore stack for transparency anchoring, etc.). They can be enabled individually when their dependencies are available. Vulnerability scanning is out of scope for every deployment shape, not just standalone — see §1.1, §4.7.7.

**Client setup.** Clients (CLI, MCP server, SDK) don't read `registry.yaml`. That's server-side config. The registry value clients use to reach the server is configured separately on the client side, via `sync.yaml`'s `defaults.registry`, `PODIUM_REGISTRY`, or an SDK constructor param (§7.5.2 covers the lookup order). `podium serve` zero-flag writes both files in one step on first run: `~/.podium/registry.yaml` for the server (`bind: 127.0.0.1:8080`, store/vector defaults) and `~/.podium/sync.yaml` for the client (`defaults.registry: http://127.0.0.1:8080`). For client-only setup (e.g., when the server runs elsewhere), use `podium init --global --registry <url>` (§7.7).

## 13.11 Filesystem Registry

A filesystem registry is a directory tree treated as the registry. `podium sync` reads it directly, applying layer composition (§4.6) and materializing through the harness adapter, with no server intermediary. No daemon, no port, no PID. Only `podium sync` works against a filesystem registry; the MCP server, SDKs, and read CLI require a Podium server.

The audience is solo developers, small teams committing the catalog to git, CI runs, and restricted environments where running a server isn't possible. The dispatch logic that routes a `defaults.registry` value to either server or filesystem is in §7.5.2.

### 13.11.1 Directory Layout

A filesystem registry rooted at `<registry-path>` is a directory of layer directories:

```
<registry-path>/
├── .registry-config            # required; opts the directory into filesystem-registry mode
├── team-shared/                # one layer
│   ├── .layer-config           # optional; per-layer visibility
│   ├── DOMAIN.md
│   ├── finance/
│   │   └── close-reporting/
│   │       └── run-variance-analysis/   # type: skill — SKILL.md + ARTIFACT.md
│   │           ├── SKILL.md
│   │           └── ARTIFACT.md
│   └── platform/
│       └── …
└── personal/                   # another layer (purely a name choice)
    └── …
```

Each subdirectory of `<registry-path>` is treated as a `local`-source layer (§4.6). Layer IDs default to the subdirectory name. Layer order is alphabetical by subdirectory name unless overridden in `.registry-config`. The `.registry-config` file is YAML:

```yaml
# <registry-path>/.registry-config
multi_layer: true        # default: false. Required to treat <path> as a filesystem-registry root.
layer_order:             # optional. When omitted, layer order is alphabetical by subdirectory name.
  - team-shared          # listed lowest-precedence first
  - personal
```

`multi_layer: true` is the opt-in that distinguishes a filesystem-registry root from a single-layer directory. When `.registry-config` is absent, or when it sets `multi_layer: false`, the directory is interpreted as a single-layer setup (one `local`-source layer rooted at `<registry-path>`, per §13.10). The same dispatch applies to `podium sync` against a filesystem path (§7.5.2) and to the standalone server's `--layer-path` (§13.10).

Each layer directory may contain an optional `.layer-config` file that declares the layer's §4.6 visibility:

```yaml
# <registry-path>/team-shared/.layer-config
visibility:
  groups: [acme-finance]   # one or more of public, organization, groups, or users (§4.6)
```

In single-layer mode the file sits at `<registry-path>/.layer-config`. `podium sync` (filesystem source) does not read it, because the filesystem-registry bypass (§4.6) short-circuits visibility to `true` for every layer. A server pointed at the same directory through `--layer-path` (§13.10) reads it and applies the declared visibility to the bootstrap layer when the `visibility:` block is non-empty. A layer with no `.layer-config`, or one whose `visibility:` block is empty, takes the deployment default (`PODIUM_DEFAULT_LAYER_VISIBILITY`, §13.12), which resolves to `public` for a no-identity standalone and to `private` once an identity provider gates access (§13.10).

The workspace local overlay (`<workspace>/.podium/overlay/`, §6.4) sits on top of the filesystem-registry layers, exactly as in server source.

### 13.11.2 Configuration

The client picks filesystem source when `defaults.registry` resolves to a path:

```yaml
# <workspace>/.podium/sync.yaml
defaults:
  registry: ./.podium/registry/ # relative paths are resolved against the workspace
  harness: claude-code
  target: .claude/
```

Absolute paths work too (`registry: /opt/podium-artifacts/`). There is no implicit workspace fallback. If `defaults.registry` is unset across all scopes, the client errors with `config.no_registry` and points the user at `podium init`. Behavior never depends on whether `<workspace>/.podium/registry/` happens to exist.

To override an inherited URL with a filesystem path, set `defaults.registry: ./.podium/registry/` explicitly at a higher-precedence scope (typically the project-shared file). Normal precedence applies.

### 13.11.3 What's Available

What `podium sync` does in filesystem source:

- Layer composition (§4.6) across the registry's layer subdirectories plus the workspace overlay (§6.4).
- Materialization through the configured harness adapter.
- Lock-file write at `<target>/.podium/sync.lock`. `podium sync override` and `podium sync save-as` work the same way as in server source.

The composer, parsers, glob resolver, `extends:` resolver, and harness adapters used here are the same Go module functions the registry runs behind its HTTP API (§2.2 *Shared library code*). There is no separate filesystem-mode reimplementation, which is why migration to a server (§13.11.6) is mechanical and produces equivalent output for the same artifact directory.

What's **not available** in filesystem source:

- The MCP server (§6) and progressive disclosure via meta-tools (§5).
- The language SDKs (§7.6).
- The SDK-backed read CLI: `podium search`, `podium domain show`, `podium artifact show` (§7.6.1).
- Outbound webhooks (§7.3.2).
- Identity-based visibility filtering. The visibility evaluator short-circuits to `true` for every layer.
- `podium login` (no auth to perform).

Features that require **specifically a remote server** (not just any server):

- Centralized audit independent of clones.
- Multi-tenancy, SCIM, and transparency-log anchoring.

Identity-based visibility filtering requires a server but not specifically a remote one: a standalone server fronted by a gateway provides it through `oidc-jwt` or `trusted-headers` (§6.3.3), and a remote standard deployment runs its own OIDC IdP.

### 13.11.4 Watch Mode

`podium sync --watch` against a filesystem source uses `fsnotify` to watch the registry path and the workspace overlay; when files change, it re-runs composition and materialization and reconciles the target through the same stale-file cleanup as a one-shot sync, so the materialized output reflects every change.

### 13.11.5 Multi-User via a Shared Directory

The registry directory is just files. Sharing it across multiple developers means sharing the directory however you'd share any folder. Most teams commit it to git, but a network share, a sync service (Dropbox, iCloud, etc.), or a periodically-rsync'd directory all work. Each developer runs `podium sync` independently against their copy; the catalog is read-only from the client's perspective, and mutation goes through whatever review the sharing mechanism enforces.

The git-committed workflow is the typical choice for teams. Commit `<workspace>/.podium/registry/` (or whatever path the project chose) to git, and every developer who clones the project has the same catalog. Authoring goes through git PR + merge. Each developer's `git pull` is their ingest; the shared git history doubles as the audit trail. No shared-state coordination, no conflicts.

Any number of developers can share a project this way without running a server.

### 13.11.6 Migrating to a Server

The filesystem source covers the small-team eager-only path. Migration to a server happens for two clusters of reasons:

**Migrate to any server (local `podium serve --standalone` or remote standard deployment):**

- Progressive disclosure required (agents call MCP meta-tools at runtime to load capabilities incrementally instead of materializing everything ahead of time).
- Identity-based visibility filtering. A standalone server fronted by a gateway sets `PODIUM_IDENTITY_PROVIDER=oidc-jwt` or `trusted-headers` and filters by the gateway-delegated identity (§6.3.3). A remote standard deployment runs its own OIDC IdP instead.

**Migrate specifically to a remote server:**

- Centralized audit independent of clones.

Migration is mechanical:

1. Run `podium serve --standalone --layer-path /path/to/.podium/registry/` (the same directory) on a chosen host. For remote, set up the standard topology (§13.1) and use `podium admin migrate-to-standard` (§13.4).
2. In each developer's `<workspace>/.podium/sync.yaml`, change `defaults.registry: ./.podium/registry/` to the server URL.
3. Done. Authoring loop unchanged; consumer paths gain MCP / SDK availability.

## 13.12 Backend Configuration Reference

This section covers **server-side** configuration: the registry process's storage backends, vector backend, embedding provider, and identity provider, configured in `registry.yaml` (default `/etc/podium/registry.yaml` for standard deployments and `~/.podium/registry.yaml` for standalone; override via `--config <path>`).

For **client-side** configuration (`sync.yaml`, `defaults.registry`, profiles, scope filters, etc.), see §7.5.2. Client and server configs are independent. Clients don't read `registry.yaml`, servers don't read `sync.yaml`.

Backend selections and their per-backend config values can be set as environment variables, command-line flags, or entries in `registry.yaml`. **Server-side precedence: CLI flag > env var > config file.** All env vars below are also valid config-file keys (snake-cased under the relevant section); a complete YAML example follows the per-backend tables. (Client-side precedence is similar but adds project and project-local config files between env vars and the user-level file; see §7.5.2.)

The same values apply on the MCP server when it's configured to use `LocalSearchProvider` against an external backend (§6.4.1); the workspace-side process reads the same env-var names.

The registry refuses to start when a backend is selected but its required values are missing, naming the missing keys in the error.

### Metadata store

Selected via `PODIUM_REGISTRY_STORE` (`postgres` | `sqlite`).

| Var                   | Description                                  | Default                          |
| --------------------- | -------------------------------------------- | -------------------------------- |
| `PODIUM_POSTGRES_DSN` | Postgres connection string (when `postgres`) | required                       |
| `PODIUM_SQLITE_PATH`  | SQLite file path (when `sqlite`)             | `~/.podium/standalone/podium.db` |

### Object storage

Selected via `PODIUM_OBJECT_STORE` (`s3` | `filesystem`).

| Var                                                       | Description                                                            | Default                                      |
| --------------------------------------------------------- | ---------------------------------------------------------------------- | -------------------------------------------- |
| `PODIUM_S3_BUCKET`                                        | Bucket name (when `s3`)                                                | required                                   |
| `PODIUM_S3_REGION`                                        | AWS / region for the bucket                                            | required                                   |
| `PODIUM_S3_ENDPOINT`                                      | Override URL for S3-compatible services (MinIO, GCS, R2, Backblaze B2) | (unset uses AWS S3)                          |
| `PODIUM_S3_ACCESS_KEY_ID` / `PODIUM_S3_SECRET_ACCESS_KEY` | Static credentials                                                     | (use IAM role / instance profile when unset) |
| `PODIUM_S3_FORCE_PATH_STYLE`                              | `true` for MinIO and similar                                           | `false`                                      |
| `PODIUM_FILESYSTEM_ROOT`                                  | Root directory (when `filesystem`)                                     | `~/.podium/standalone/objects/`              |
| `PODIUM_PRESIGN_TTL_SECONDS`                              | TTL for S3 presigned URLs                                              | 3600                                         |
| `PODIUM_MIGRATION_OBJECT_READ_TIMEOUT`                    | Deadline on each object-storage read the first-start stored-value rewrite, the `sign-stored-rows` command, and the stored-row admission check make (§13.4), as a duration. Environment only; no config-file key. | `30s`                                        |

**URL mechanism by backend.** Both backends return `large_resources[*].url` values that the consumer follows to fetch bytes; the URL's authentication mechanism differs:

- **S3 backend.** URLs are presigned with AWS Signature V4. The URL is self-validating: any caller that holds the URL can fetch until the signature expires (`PODIUM_PRESIGN_TTL_SECONDS`). Consumers do not send credentials when following the URL.
- **Filesystem backend.** URLs point at the registry's authenticated `/objects/{content_hash}` route. There is no embedded signature or expiry; the consumer sends the same session token it used for `load_artifact`. The registry validates the token, confirms that the caller can see an artifact that owns the content hash, and serves the bytes. A bundled resource is owned by each artifact that bundles it. A §6.6 presigned manifest document is owned by each artifact that serves it through that channel. For a skill that document is its `SKILL.md`, which a child declaring `extends:` serves as its own stored `SKILL.md`. For every other type it is the served `ARTIFACT.md`, which for a child declaring `extends:` is the merged document (§4.6) rather than the stored one, so the stored pre-merge document of such a child has no owner. A key the caller can see no owner of is answered exactly as a key that does not exist. The URL has no useful TTL of its own — it is bound to the caller's session, not to a clock.

The choice of backend is transparent to the response shape: both produce `{url, content_hash, size, content_type}` records and the consumer verifies `sha256(bytes) == content_hash` after fetch in both cases. Hosts that share a `large_resources` URL with another caller cannot grant access to bytes the other caller is not entitled to read on either backend.

### Vector backend

Selected via `PODIUM_VECTOR_BACKEND` (`pgvector` | `sqlite-vec` | `pinecone` | `weaviate-cloud` | `qdrant-cloud`).

`pgvector` and `sqlite-vec` reuse the metadata-store connection. No additional config.

`pinecone`:

| Var                               | Description                                                                      | Default                                                   |
| --------------------------------- | -------------------------------------------------------------------------------- | --------------------------------------------------------- |
| `PODIUM_PINECONE_API_KEY`         | Pinecone API key                                                                 | required                                                |
| `PODIUM_PINECONE_INDEX`           | Index name                                                                       | required                                                |
| `PODIUM_PINECONE_HOST`            | Index host URL (Pinecone serverless)                                             | (auto-resolved from index name)                           |
| `PODIUM_PINECONE_NAMESPACE`       | Namespace prefix used per tenant                                                 | `default`                                                 |
| `PODIUM_PINECONE_INFERENCE_MODEL` | Hosted model name to enable Integrated Inference (e.g., `multilingual-e5-large`) | (unset → storage-only mode; `EmbeddingProvider` required) |

`weaviate-cloud`:

| Var                          | Description                                                                                          | Default                                                   |
| ---------------------------- | ---------------------------------------------------------------------------------------------------- | --------------------------------------------------------- |
| `PODIUM_WEAVIATE_URL`        | Cluster REST URL                                                                                     | required                                                |
| `PODIUM_WEAVIATE_API_KEY`    | API key                                                                                              | required                                                |
| `PODIUM_WEAVIATE_COLLECTION` | Collection name                                                                                      | required                                                |
| `PODIUM_WEAVIATE_GRPC_URL`   | gRPC endpoint. Reserved and not currently read; the Weaviate backend uses the REST data plane.                                                                                        | (derived from REST URL)                                   |
| `PODIUM_WEAVIATE_VECTORIZER` | Vectorizer module name (e.g., `text2vec-openai`, `text2vec-weaviate`); set to enable self-embedding | (unset → storage-only mode; `EmbeddingProvider` required) |

`qdrant-cloud`:

| Var                             | Description                                                      | Default                                                   |
| ------------------------------- | ---------------------------------------------------------------- | --------------------------------------------------------- |
| `PODIUM_QDRANT_URL`             | Cluster REST URL                                                 | required                                                |
| `PODIUM_QDRANT_API_KEY`         | API key                                                          | required                                                |
| `PODIUM_QDRANT_COLLECTION`      | Collection name                                                  | required                                                |
| `PODIUM_QDRANT_GRPC_PORT`       | gRPC port. Reserved and not currently read; the Qdrant backend uses the REST data plane.                                                        | `6334`                                                    |
| `PODIUM_QDRANT_INFERENCE_MODEL` | Hosted Cloud Inference model name; set to enable self-embedding  | (unset → storage-only mode; `EmbeddingProvider` required) |

### Embedding provider

Selected via `PODIUM_EMBEDDING_PROVIDER` (`openai` | `voyage` | `cohere` | `ollama`). **Optional** when the configured vector backend self-embeds (any of the `*_INFERENCE_MODEL` / `*_VECTORIZER` env vars above is set); **required** otherwise. Setting it to the empty string disables embedding generation; search degrades to BM25-only.

`openai`:

| Var                      | Description                                         | Default                     |
| ------------------------ | --------------------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`         | OpenAI API key                                      | required                  |
| `PODIUM_OPENAI_MODEL`    | Model name                                          | `text-embedding-3-small`    |
| `PODIUM_OPENAI_BASE_URL` | API base URL (override for Azure OpenAI or proxies) | `https://api.openai.com/v1` |
| `PODIUM_OPENAI_ORG`      | OpenAI organization ID                              | (unset)                     |

`voyage`:

| Var                   | Description       | Default    |
| --------------------- | ----------------- | ---------- |
| `VOYAGE_API_KEY`      | Voyage AI API key | required |
| `PODIUM_VOYAGE_MODEL` | Model name        | `voyage-3` |

`cohere`:

| Var                   | Description    | Default    |
| --------------------- | -------------- | ---------- |
| `COHERE_API_KEY`      | Cohere API key | required |
| `PODIUM_COHERE_MODEL` | Model name     | `embed-v4` |

`ollama`:

| Var                   | Description     | Default                  |
| --------------------- | --------------- | ------------------------ |
| `PODIUM_OLLAMA_URL`   | Ollama endpoint | `http://localhost:11434` |
| `PODIUM_OLLAMA_MODEL` | Model name      | `nomic-embed-text`       |

### Identity provider

Identity-provider selection and per-provider config are documented in §6.3 (`PODIUM_IDENTITY_PROVIDER`, `PODIUM_OAUTH_AUDIENCE`, `PODIUM_SESSION_TOKEN_*`, etc.). `injected-session-token` applies on both the registry and the MCP server. `oauth-device-code` is an MCP-server value, and the registry refuses it at startup with `config.identity_provider_unverified`. `oidc-jwt` and `trusted-headers` are registry-process values that the MCP server's `PODIUM_IDENTITY_PROVIDER` does not admit (§6.3, §6.3.3).

The registry-process providers (§6.3.3) and the `injected-session-token` provider (§6.3.2) introduce the following registry-process variables. `oidc-jwt` also reuses `PODIUM_OAUTH_AUDIENCE` (§6.3) for the `aud` claim, which it requires, and both it and `injected-session-token` verify a token against the whole configured set.

| Var | Description | Default |
| --- | --- | --- |
| `PODIUM_OAUTH_AUDIENCE` | Audience values the registry accepts in a verified token's `aud` claim. Comma-separated; each entry is trimmed, blank entries are dropped, and repeated entries are collapsed. A token is accepted when its `aud` carries at least one member of the set. A setting that resolves to no entry fails startup with `config.oidc_jwt_audience_unset` under `oidc-jwt` and `config.injected_token_audience_unset` under `injected-session-token`. The first value is canonical and is what the registry sends when it initiates a flow itself (§6.3.4). Config-file key `identity_provider.audience`, which accepts a string or a list of strings; a string is one audience verbatim and is not split on any separator. Read under `oidc-jwt` and `injected-session-token`. | (unset; required under both) |
| `PODIUM_OAUTH_ISSUER` | OIDC issuer URL of the IdP that signs the token forwarded under `oidc-jwt`. Must use the `https` scheme; a non-`https` value fails startup with `config.invalid_issuer_scheme`. The registry fetches the JWKS from the issuer's discovery document at `<issuer>/.well-known/openid-configuration`, and validates the token `iss` against this value or against the `access_token_issuer` that same document publishes (§6.3.3). Config-file key `identity_provider.issuer`. Read only under `oidc-jwt`. | (unset; required for `oidc-jwt`) |
| `PODIUM_OAUTH_TOKEN_HEADER` | Header carrying the forwarded JWT, parsed as `Bearer <token>` regardless of header name. Config-file key `identity_provider.token_header`. Read only under `oidc-jwt`. | `Authorization` |
| `PODIUM_OAUTH_SUBJECT_CLAIM` | Claim read as the caller's subject in place of `sub`. When it is set, the registry reads that claim alone and rejects a token that does not carry it with `auth.untrusted_token`. The recorded subject keys `users:` layer visibility, user-defined layer ownership, per-tenant admin grants, and the instance-operator grant, so a deployment that sets this key lists values of the named claim in `PODIUM_OPERATOR_ADMINS` and `PODIUM_BOOTSTRAP_ADMINS` (§6.3.3). Config-file key `identity_provider.subject_claim`. Read only under `oidc-jwt`. | (unset; `sub`) |
| `PODIUM_OAUTH_GROUPS_CLAIM` | Claim read for group membership in place of `groups`. Its values are matched against a layer's `groups:` filter after the `IdpGroupMapping` adapter rewrites them (§6.3.1). The registry reads the claim on every verified token, and a registry that also resolves membership through SCIM matches SCIM-resolved membership in addition. Config-file key `identity_provider.groups_claim`. Read only under `oidc-jwt`. | (unset; `groups`) |
| `PODIUM_OAUTH_JWKS_CACHE_TTL_SECONDS` | Maximum age in seconds of the cached issuer JWKS before refresh; a `kid` absent from the cached set forces an earlier refresh. Config-file key `identity_provider.jwks_cache_ttl_seconds`. Read only under `oidc-jwt`. | 300 |
| `PODIUM_TRUSTED_PROXY_SECRET` | Shared secret the gateway sends in `X-Podium-Proxy-Secret`. When set, identity headers are honored only on a request whose secret matches under a constant-time comparison. Required on a multi-tenant `trusted-headers` registry regardless of bind, otherwise startup fails with `config.trusted_headers_multitenant_no_secret`; on a single-tenant registry a non-loopback bind requires it or `--allow-public-bind`, otherwise `config.trusted_headers_public_bind`. Environment only; no config-file key. Read only under `trusted-headers`. | (unset) |
| `PODIUM_RUNTIME_KEYS_PATH` | Path to the JSON file holding the §6.3.2 trusted runtime signing keys, whose record format §6.3.2 defines. The registry process reads it before it binds a listener and never writes it; the MCP server does not read it. A key added to the file takes effect at the next process start. A file the registry cannot read or parse fails startup with `config.runtime_keys_unavailable` under every identity provider. Under `injected-session-token` a key set is also required, so an unset path, or a path naming a file that carries no key, fails startup with the same code; under the other providers a path naming a file that carries no key, including a missing file and an empty one, resolves to an empty key set and startup proceeds. Environment only; no config-file key. | (unset; required under `injected-session-token`) |
| `PODIUM_IDP_GROUP_MAPPING` | The §6.3.1 registry-side group-mapping table, as a comma-separated list of `<claim-value>=<group-name>` pairs. Whitespace around each name is trimmed and a blank entry is dropped. A group claim value that has a table entry is rewritten to that entry's group name before §4.6 visibility evaluation, and a value with no entry passes through unchanged, so a deployment whose IdP already emits the layer group names needs no table. A non-empty value that carries an entry without a `=`, or with an empty name on either side, or that resolves to no `claim=group` entry, fails startup with `config.invalid_idp_group_mapping` under every identity provider, because a table the registry ignored would leave every §4.6 `groups:` filter evaluating raw claim values. An unset or empty value configures no table and startup proceeds, while a value of whitespace or separators alone is non-empty and is refused. Environment only; no config-file key. Read under `oidc-jwt` (§6.3.3) and `injected-session-token` (§6.3.2); `trusted-headers` does not consult the adapter (§6.3.3). | (unset; no table, and every claim value passes through) |

The `identity_provider:` object holds `type`, `audience`, `authorization_endpoint`, `issuer`, `token_header`, `subject_claim`, `groups_claim`, and `jwks_cache_ttl_seconds`. The `audience` key takes a string or a list of strings; a string configures one audience and is not split on a separator, and the comma-separated form belongs to `PODIUM_OAUTH_AUDIENCE`. The `identity_provider.issuer` key is distinct from the top-level domain-discovery `discovery:` block (§4.5.5), which configures domain-tree rendering and has no bearing on identity.

For server deployments that intentionally run without an identity provider, `PODIUM_PUBLIC_MODE=true` (or `--public-mode`) bypasses authentication and the visibility model entirely; see §13.10. Public mode is mutually exclusive with `PODIUM_IDENTITY_PROVIDER`; setting both fails at startup with `config.public_mode_with_idp`.

Filesystem-source registries (§13.11) have no identity provider by definition. There is no server process to authenticate against and no JWT to verify. `podium login` is a no-op when the resolved registry is a filesystem path; the visibility evaluator short-circuits to `true` for every layer.

### Signing

| Setting | Description | Default |
| --- | --- | --- |
| `PODIUM_SIGN` | Ingest signing mode: `registry-key` or `none`. Unset resolves to `registry-key`. Any other value refuses startup with `config.invalid_sign_mode`. Also settable with `podium serve --sign`, which takes precedence; no config-file key | `registry-key` |
| `PODIUM_SIGN_KEY_PATH` | Path to the registry-managed key file. The file carries the signing keypair on `private:` and `public:` lines and zero or more verification-only `verify:` lines (§4.7.9). A file with no `private:` line or no `public:` line, a line that does not decode, or a `public:` line that is not the public half of the `private:` line refuses the start with `config.signature_provider_unavailable`, naming the key file. The registry process generates the file on first run when absent, and `podium admin signing-key generate` and `rotate` write it; a consumer process on the same machine reads it and generates nothing (§6.2, §4.7.9). Required while signing is on, unless the store is the SQLite store in the directory the default path resolves to. Environment only; no config-file key | `~/.podium/standalone/registry-signing.key` |

Every registry process serving one store resolves `PODIUM_SIGN_KEY_PATH` to a key file on the store's persistent storage. The default resolves per process, under that process's home, so a registry with signing on and `PODIUM_SIGN_KEY_PATH` unset refuses to start, naming `PODIUM_SIGN_KEY_PATH` and `PODIUM_SIGN=none`, unless its store is the SQLite store in the directory the default path resolves to. The refusal runs before the registry generates a key or rewrites a row. A deployment running more than one replica, or whose store is not the default SQLite store, therefore supplies the path explicitly on the store's persistent storage and mounts one key file, or sets `PODIUM_SIGN=none`. Replicas sign under the signing key of that one file; a rotation across replicas follows §4.7.9. A registry that loses a generated key signs under a fresh one on its next start once the §13.4 rewrite has completed, and every row the lost key signed is refused at the §13.4 stored-row admission. The registry loads its key file once, at start, so the repair is to stop the registry, restore the key file, and start it again, or, when only the lost key's public half survives, to list it on a `verify:` line of the key file the registry now holds and start it again, which admits those rows, and then run `sign-stored-rows` (§13.4), which re-signs them under the current key so the line can be removed. While the rewrite has not completed, that start is refused by the §13.4 generated-key rule. Restoring the key file leaves refused every row the fresh key signed in the meantime, and listing the fresh key's public half on a `verify:` line of the restored file admits those rows. Replicas whose signing keys are absent from each other's verification key sets each refuse at admission a row another replica signed, and a consumer whose set lacks a replica's signing key refuses that replica's responses, so loads fail with `materialize.signature_invalid` on some fraction of requests and succeed on the rest.

Signing is producer-side: it determines whether an artifact ingested from now on carries a §4.7.9 envelope. Ingest does not attach one to an already-stored artifact. A stored signature is written at ingest, by the §13.4 first-start rewrite, and by the §13.4 `sign-stored-rows` command. The rewrite and `sign-stored-rows` sign and re-sign rows as §13.4 states and leave every other row untouched with whatever envelope it had. An untouched row is admitted or refused by the §13.4 stored-row admission checks, as any other row is. Turning signing on after the rewrite has completed leaves earlier rows unsigned until `sign-stored-rows --include-unsigned` runs (§13.4). Outside the SQLite store in the key file's directory, the first start of a release that rewrites stored values also leaves unsigned rows unsigned (§13.4). A start that finds the record of the rewrite's completion removed runs the rewrite again, which is the limit §13.4 states for a party that can write the metadata store.

The consumer-side settings that decide whether a signature is checked are client configuration and live in §6.2 and §7.5.2. `PODIUM_SIGN_KEY_PATH` is the one setting both process kinds read. The registry process writes the file it names, and a consumer running under the same user account on the same machine reads the `public:` line and every `verify:` line from it when `PODIUM_SIGNATURE_VERIFY_KEY` is unset (§4.7.9).

### Layers

Layer-list bootstrap and visibility default for the §13.10 standalone deployment.

| Var                              | Description                                                                                                                                                              | Default                                                                |
| -------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------- |
| `PODIUM_LAYER_PATH`              | Filesystem registry root opened at startup. Maps to the `--layer-path` flag. Dispatches between single-layer and filesystem-registry modes per `.registry-config` (§13.10). | (unset disables the bootstrap and starts with an empty registry)        |
| `PODIUM_DEFAULT_LAYER_VISIBILITY`| Fallback `visibility:` applied when a layer is registered without an explicit setting. One of `public`, `organization`, or `private`.                                    | `private` (§13.10 specifies `public` for a standalone deployment without an identity provider; an enabled identity provider leaves the unset default `private`)        |

When `PODIUM_LAYER_PATH` is set, the standalone server ingests every resolved layer at startup and persists a `local`-source `LayerConfig` per layer so the §7.3.1 layer-management endpoints (`GET /v1/layers`, `POST /v1/layers/reingest`, `DELETE /v1/layers`) see them. Without an identity provider, bootstrap layers carry `visibility: public` per the §13.10 standalone default; once an identity provider is enabled and `PODIUM_DEFAULT_LAYER_VISIBILITY` is unset, the resolved default is `private` and a bootstrap layer that carried no explicit `visibility:` is re-stamped private on the next boot. Additional `local` and `git` layers can be registered via `podium layer register` after startup.

### Tenancy

| Var | Description | Default |
| --- | --- | --- |
| `PODIUM_OPERATOR_ADMINS` | Comma-separated identities granted the instance-operator role at boot. The operator role authorizes the `/v1/admin/tenants` tenant-management endpoints (§7.3.3) and the `podium admin tenant` CLI; it confers no per-tenant admin rights. Distinct from `PODIUM_BOOTSTRAP_ADMINS`, which seeds per-tenant admin grants for the bootstrapped tenant. | (unset) |
| `PODIUM_MULTI_TENANT` | When `true`, the registry runs in multi-tenant mode: it binds to a no-data tenant and resolves each request's tenant from the caller's organization (§6.3.1), rejecting an `oidc-jwt` token whose `org_id` names no provisioned tenant with `auth.tenant_unknown`. When unset, the registry binds every request to the single `default` org and does not consult the organization value, and the §7.3.3 tenant-management endpoints are rejected. | (unset → single-tenant) |

**Multi-tenant operator tenant-list (Postgres).** On a multi-tenant Postgres backend, the cross-org `GET /v1/admin/tenants` read (§7.3.3) runs on the registry's existing `PODIUM_POSTGRES_DSN` table-owner connection, with `podium.org_id` set to the `*operator-list*` sentinel that the FORCE'd policy on `public.tenants` admits. The deployment provisions no role or function for this read: extending the FORCE'd policy to admit the sentinel and forcing row-level security require only ownership of `public.tenants`, which the `PODIUM_POSTGRES_DSN` role holds, so the registry's schema setup applies the sentinel policy from the binary with the rest of the row-level-security setup and the list needs no manual step. The registry's `PODIUM_POSTGRES_DSN` role must be the non-superuser owner of `public.tenants`: a superuser DSN role bypasses row-level security entirely, which voids the per-org confinement of every other tenant read.

**Operator CLI.** The `podium admin tenant` group manages tenants at runtime on a multi-tenant registry, authorized as the operator role (§4.7.1, §7.3.3). The operator authenticates as any caller does through the normal client login, and the registry checks the operator grant.

```
podium admin tenant create <name> [--storage-bytes N] [--search-qps N] [--materialize-rate N] [--audit-volume-per-day N] [--max-user-layers N] [--expose-scope-preview true|false] --registry <url>
podium admin tenant list [--json] --registry <url>
podium admin tenant update <id> [--storage-bytes N] [--search-qps N] [--materialize-rate N] [--audit-volume-per-day N] [--max-user-layers N] [--expose-scope-preview true|false] [--active true|false] --registry <url>
podium admin tenant deactivate <id> --registry <url>
```

`podium admin tenant create` derives the org ID from the name and provisions the tenant; repeating the call for an already-provisioned name is a no-op that returns the existing tenant. `podium admin tenant update` sends only the flags the operator passes, so an omitted flag leaves the corresponding field unchanged server-side; it cannot change the tenant name, which is fixed at create. `update --active true` reactivates a deactivated tenant, and `update --active false` deactivates it, the same soft operation as `deactivate`. `list --json` emits the wire array for scripting.

### Config file format

```yaml
# /etc/podium/registry.yaml (or ~/.podium/registry.yaml in standalone)
registry:
  endpoint: https://podium.acme.com
  bind: 0.0.0.0:8080

  store:
    type: postgres
    dsn: ${PODIUM_POSTGRES_DSN} # ${ENV_VAR} interpolation supported

  object_store:
    type: s3
    bucket: acme-podium
    region: us-east-1
    endpoint: ${PODIUM_S3_ENDPOINT} # optional — set for MinIO / R2 / GCS

  vector_backend:
    type: pinecone
    api_key: ${PINECONE_API_KEY}
    index: acme-prod
    namespace: ${PODIUM_TENANT_ID}
    inference_model: multilingual-e5-large # enables self-embedding

  # Optional: omitted because the vector backend above self-embeds.
  # embedding_provider:
  #   type: openai
  #   api_key: ${OPENAI_API_KEY}
  #   model: text-embedding-3-large

  identity_provider:
    type: oidc-jwt
    issuer: https://acme.okta.com/oauth2/default
    audience: https://podium.acme.com

  discovery:
    max_depth: 3
    fold_below_artifacts: 3
    fold_passthrough_chains: true
    notable_count: 10
    target_response_tokens: 4000
    allow_per_domain_overrides: true
```

Env vars and CLI flags override file values. Secret values should use `${ENV_VAR}` interpolation rather than being committed in plaintext.

### Webhooks

Server-side controls for the outbound webhook receivers (§7.3.2).

| Var | Description | Default |
| --- | --- | --- |
| `PODIUM_WEBHOOK_ALLOWED_TARGETS` | Comma-separated allowlist of hosts or CIDRs that the receiver-URL SSRF policy permits in addition to public addresses. | (empty) |
