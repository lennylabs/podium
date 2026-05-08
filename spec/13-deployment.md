# 13. Deployment

## 13.1 Reference Topology

- **Stateless front-end:** 3+ replicas behind a load balancer (HTTP).
- **Postgres:** managed (RDS, Cloud SQL, Aurora) or self-run; primary + read replicas. Holds manifest metadata, layer config, admin grants, and audit; also holds embeddings when the default vector backend (pgvector) is in use.
- **Vector backend:** `pgvector` by default, collocated in the Postgres deployment with no separate service to run. The default binary also ships built-ins for `pinecone`, `weaviate-cloud`, and `qdrant-cloud`, selectable via `PODIUM_VECTOR_BACKEND` (each takes its own endpoint + API key env vars). Custom backends register through the `RegistrySearchProvider` SPI (§9.1, §9.2).
- **Embedding provider:** `openai` by default in standard deployments. Text projection from manifest frontmatter (§4.7 _Embedding generation_) is sent to OpenAI's embeddings API. The default binary also ships `voyage`, `cohere`, `ollama`, and `embedded-onnx`, selectable via `PODIUM_EMBEDDING_PROVIDER`. Optional when the configured vector backend self-embeds (Pinecone Integrated Inference, Weaviate Cloud vectorizer, Qdrant Cloud Inference).
- **Object storage:** S3-compatible (S3, GCS, MinIO, R2).
- **Helm chart** ships with the registry; bare-metal deployment guide alongside.

For non-prod or standalone use, see §13.10.

### 13.1.1 Evaluation Deployment (Docker Compose)

For team evaluation, smoke-testing, and local integration testing (anything that wants the standard topology's components without the standalone single-binary shortcut), the repo ships a `docker-compose.yml` that brings up the full stack with one command:

```bash
docker compose up -d
podium init --global --registry http://localhost:8080
podium login    # device-code flow against the bundled Dex IdP
```

The compose file includes:

- **`registry`**: the registry binary, configured against the local services below.
- **`postgres`**: `pgvector/pgvector:pg16` for metadata and embeddings.
- **`minio`**: S3-compatible object storage (path-style URLs, `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD` for auth).
- **`dex`**: OIDC IdP for the OAuth device-code flow.
- **`bootstrap`**: one-shot container that creates the MinIO bucket, registers the registry as an OIDC client with Dex, creates the first tenant and admin user (configurable via env vars), then exits.

**Not production-grade.** Single-replica services, default credentials, local volumes. The compose stack is _standard-topology in shape_ so consumers exercise the same code paths as a real deployment, but it is intended only for evaluation pilots, CI integration tests, and adapter / SDK development. For genuine non-prod or solo use, prefer §13.10's standalone mode (one binary instead of four containers).

## 13.2 Runbook

Coverage for: Postgres failover, object-storage outage, IdP outage, full-disk on registry node, audit-stream backpressure, runaway search QPS, signature verification failure storm. Each scenario gets detection signals, impact, and mitigation steps; full runbook ships with the Helm chart.

### 13.2.1 Read-Only Mode

When the Postgres primary becomes unreachable but a read replica is up, the registry falls back to **read-only mode**: read endpoints (`load_domain`, `search_domains`, `search_artifacts`, `load_artifact`, `load_artifacts`) continue to serve from the replica; write endpoints (ingest webhooks, layer admin operations, freeze toggles, admin grants, `podium login`-driven token issuance against the local IdP-mediated session table) are rejected with the structured error `registry.read_only`.

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
- Consistent restore via PITR + object-storage version history.
- Default RPO 1h / RTO 4h.

## 13.4 Migrations

Schema migrations bundled in the registry binary; expand-contract pattern for online migrations. Type-system migrations versioned alongside the binary.

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

- **Metrics.** Prometheus endpoint on registry and MCP server. Histograms for latency; counters for cache hit rate, error rate, visibility-denial rate, ingest success/failure rate; gauges for queue depths.
- **Tracing.** OpenTelemetry trace export. W3C Trace Context propagation across all calls. One root span per `load_domain` / `search_domains` / `search_artifacts` / `load_artifact`; child spans for registry round-trip, object-storage fetch, adapter translation, materialization.
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
| Embedding provider      | `openai` (default)                              | `embedded-onnx`: bundled BGE-small ONNX model, in-process, no external service                                                                                                                                                                                                                 |
| Object storage          | S3-compatible                                   | Filesystem (`~/.podium/standalone/objects/`)                                                                                                                                                                                                                                                   |
| Identity provider       | OIDC IdP                                        | None. No auth; `127.0.0.1`-only HTTP by default                                                                                                                                                                                                                                                |
| Layers                  | Configured admin layers + user-defined layers   | `--layer-path` is polymorphic (see "**`--layer-path` modes**" below): single-layer mode produces one `local`-source layer rooted at the path; filesystem-registry mode treats each subdirectory as a `local`-source layer per §13.11.1. Additional `local` and `git` layers can be registered via `podium layer register` in either mode.                                                                                                                                                   |
| Git provider / webhooks | Required for `git`-source layers                | `git` source layers work without webhooks; webhooks are optional. Without a webhook (typical for a developer machine without a public ingress), `podium layer reingest <id>` pulls the current state on demand, and `podium layer watch <id>` polls the source at a configured interval.        |
| Signing                 | Sigstore-keyless or registry-managed key        | Disabled by default; opt in via `--sign registry-key`                                                                                                                                                                                                                                          |
| Content cache           | Cross-workspace disk cache (`~/.podium/cache/`) | Disabled; the registry is local, the cache adds nothing                                                                                                                                                                                                                                        |
| Audit                   | Per-tenant Postgres table                       | Same SQLite file (audit table)                                                                                                                                                                                                                                                                 |
| Helm chart / Kubernetes | Required for production deployments             | Not used                                                                                                                                                                                                                                                                                       |

**`--layer-path` modes.** The path passed to `--layer-path` is interpreted as either a single-layer directory or a filesystem-registry root. The standalone server selects between these via an explicit dispatch:

- **Filesystem-registry mode.** When `<path>/.registry-config` exists and sets `multi_layer: true`, and the path contains no manifest files (`ARTIFACT.md`, `SKILL.md`, `DOMAIN.md`) directly at its top level, `<path>` is treated as a filesystem-registry root. Each subdirectory of `<path>` becomes a `local`-source layer; ordering follows §13.11.1 (alphabetical by subdirectory name, optionally overridden by `layer_order:` in `.registry-config`). This is the mode used when migrating a filesystem registry (§13.11.6) to a server pointed at the same directory.
- **Single-layer mode (default).** When `.registry-config` is absent, when it sets `multi_layer: false`, or when it is omitted entirely, `<path>` is treated as a single layer. The directory's contents are the layer's domain hierarchy; one `local`-source layer rooted at `--layer-path` is registered, with a default layer ID derived from the directory name.

If `multi_layer: true` is set but the safety check fails (manifest files are present directly at the top level of `<path>`), the server refuses to start with `config.layer_path_ambiguous`, naming the conflicting top-level manifest paths so the operator can either remove them or unset `multi_layer`.

**Hybrid search.** Standalone runs the same BM25 + vector RRF retriever as the standard registry. Vectors live in `sqlite-vec`; embeddings come from the bundled `embedded-onnx` provider. Both run in-process, so the binary works offline and air-gapped with no external dependency. Operators who want a remote model instead can switch via `PODIUM_EMBEDDING_PROVIDER=openai|voyage|cohere|ollama` (`ollama` is the obvious choice for self-hosted local models). `--no-embeddings` falls back to BM25-only.

**Upgrade path.** A standalone deployment migrates to standard via `podium admin migrate-to-standard --postgres <dsn> --object-store <url>` (covered in §13.4). Layer config, admin grants, and audit history are preserved; embeddings are re-computed against the target vector backend on first ingest.

**Web UI.** When `podium serve` (standalone or standard) is started with `--web-ui` (or `PODIUM_WEB_UI=true`), the same process exposes a single-page web UI at `http://<bind>/ui/`. The UI is a static SPA bundled into the binary; it talks to the registry's HTTP API as any other consumer would. What it surfaces:

- **Domain browser**: hierarchical navigation matching `load_domain`'s structure.
- **Search**: text input that calls `search_artifacts` with the same `type` / `scope` / `tags` filters as the SDK and CLI.
- **Artifact viewer**: manifest body rendered as markdown, frontmatter as a property table, links to extending or dependent artifacts.
- **Layer panel**: list registered layers with their source, visibility, and `last_ingested_at`. Admins can register, reingest, and unregister layers from the UI; users can manage their own user-defined layers (cap per §7.3.1). The UI is a thin client over the same `podium layer …` HTTP endpoints.

Authentication: in standalone deployments without an identity provider, the UI is open on the bind address (default `127.0.0.1`, which is not network-exposed). In standard deployments the UI uses the same OAuth device-code flow as the CLI, with the verification URL handoff handled in-browser.

Behind a flag: opt-in via `--web-ui` so headless deployments (CI runners, managed runtimes) don't pay the binary-size or attack-surface cost when they don't need it. The binary refuses to bind the UI to a non-loopback address unless `--web-ui-allow-public-bind` is also passed _and_ an identity provider is configured, preventing accidental exposure of an unauthenticated UI.

The UI is the recommended consumption path for non-developer users (analysts, prompt authors, reviewers) who want to browse the catalog without installing the SDK or learning the CLI.

**Sensible defaults for permissive deployments.** Standalone deployments shift several defaults toward low-friction rather than secure-by-default, appropriate for the solo and small-team contexts standalone targets:

- **Layer visibility.** New layers registered via `podium layer register` default to `visibility: public` (instead of `users: [<registrant>]` as in standard mode for user-defined layers). Override with `PODIUM_DEFAULT_LAYER_VISIBILITY=users` for multi-user standalone deployments that want the standard behavior.
- **Signature verification.** `PODIUM_VERIFY_SIGNATURES` defaults to `never` (instead of `medium-and-above`). Authors who want enforcement set it explicitly to `medium-and-above` or `always`.
- **Sandbox profile.** `sandbox_profile:` is informational in standalone. Hosts honor it as in standard mode, but the registry does not refuse to ingest artifacts whose profiles can't be enforced locally. Override with `PODIUM_ENFORCE_SANDBOX_PROFILE=true` in multi-user setups.
- **Sensitivity.** Artifacts without an explicit `sensitivity:` field default to `low`. The lint check that flags missing sensitivity is downgraded from a warning to a hint.

Any of these defaults can be flipped to standard-mode behavior via the named env var without otherwise changing the deployment; the same single binary continues to serve.

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

**Out of scope for standalone.** Multi-tenancy, freeze windows, SCIM, SBOM/CVE pipeline, transparency-log anchoring, outbound webhooks. These are present in the binary but inert without the supporting infrastructure (an IdP for SCIM, a CVE feed for vulnerability tracking, etc.). They can be enabled individually when their dependencies are available.

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
- OIDC identity-based visibility filtering.
- Multi-tenancy, SCIM, SBOM/CVE pipeline, transparency-log anchoring.

### 13.11.4 Watch Mode

`podium sync --watch` against a filesystem source uses `fsnotify` to watch the registry path and the workspace overlay; when files change, it re-runs composition and materialization for the affected artifacts.

### 13.11.5 Multi-User via a Shared Directory

The registry directory is just files. Sharing it across multiple developers means sharing the directory however you'd share any folder. Most teams commit it to git, but a network share, a sync service (Dropbox, iCloud, etc.), or a periodically-rsync'd directory all work. Each developer runs `podium sync` independently against their copy; the catalog is read-only from the client's perspective, and mutation goes through whatever review the sharing mechanism enforces.

The git-committed workflow is the typical choice for teams. Commit `<workspace>/.podium/registry/` (or whatever path the project chose) to git, and every developer who clones the project has the same catalog. Authoring goes through git PR + merge. Each developer's `git pull` is their ingest; the shared git history doubles as the audit trail. No shared-state coordination, no conflicts.

Any number of developers can share a project this way without running a server.

### 13.11.6 Migrating to a Server

The filesystem source covers the small-team eager-only path. Migration to a server happens for two clusters of reasons:

**Migrate to any server (local `podium serve --standalone` or remote standard deployment):**

- Progressive disclosure required (agents call MCP meta-tools at runtime to load capabilities incrementally instead of materializing everything ahead of time).

**Migrate specifically to a remote server:**

- Centralized audit independent of clones.
- OIDC identity-based visibility filtering.

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
| `PODIUM_WEAVIATE_GRPC_URL`   | gRPC endpoint                                                                                        | (derived from REST URL)                                   |
| `PODIUM_WEAVIATE_VECTORIZER` | Vectorizer module name (e.g., `text2vec-openai`, `text2vec-weaviate`); set to enable self-embedding | (unset → storage-only mode; `EmbeddingProvider` required) |

`qdrant-cloud`:

| Var                             | Description                                                      | Default                                                   |
| ------------------------------- | ---------------------------------------------------------------- | --------------------------------------------------------- |
| `PODIUM_QDRANT_URL`             | Cluster REST URL                                                 | required                                                |
| `PODIUM_QDRANT_API_KEY`         | API key                                                          | required                                                |
| `PODIUM_QDRANT_COLLECTION`      | Collection name                                                  | required                                                |
| `PODIUM_QDRANT_GRPC_PORT`       | gRPC port                                                        | `6334`                                                    |
| `PODIUM_QDRANT_INFERENCE_MODEL` | Hosted Cloud Inference model name; set to enable self-embedding  | (unset → storage-only mode; `EmbeddingProvider` required) |

### Embedding provider

Selected via `PODIUM_EMBEDDING_PROVIDER` (`embedded-onnx` | `openai` | `voyage` | `cohere` | `ollama`). **Optional** when the configured vector backend self-embeds (any of the `*_INFERENCE_MODEL` / `*_VECTORIZER` env vars above is set); **required** otherwise.

`embedded-onnx`:

| Var                      | Description                | Default                       |
| ------------------------ | -------------------------- | ----------------------------- |
| `PODIUM_ONNX_MODEL_PATH` | Path to an ONNX model file | (bundled `bge-small-en-v1.5`) |
| `PODIUM_ONNX_DIMENSIONS` | Output vector dimensions   | `384`                         |
| `PODIUM_ONNX_POOL_SIZE`  | Concurrent inference slots | `runtime.NumCPU()`            |

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

Identity-provider selection and per-provider config are documented in §6.3 (`PODIUM_IDENTITY_PROVIDER`, `PODIUM_OAUTH_AUDIENCE`, `PODIUM_SESSION_TOKEN_*`, etc.). The same values apply on both the registry and the MCP server.

For server deployments that intentionally run without an identity provider, `PODIUM_PUBLIC_MODE=true` (or `--public-mode`) bypasses authentication and the visibility model entirely; see §13.10. Public mode is mutually exclusive with `PODIUM_IDENTITY_PROVIDER`; setting both fails at startup with `config.public_mode_with_idp`.

Filesystem-source registries (§13.11) have no identity provider by definition. There is no server process to authenticate against and no JWT to verify. `podium login` is a no-op when the resolved registry is a filesystem path; the visibility evaluator short-circuits to `true` for every layer.

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
    type: oauth-device-code
    audience: https://podium.acme.com
    authorization_endpoint: https://acme.okta.com/oauth2/default

  discovery:
    max_depth: 3
    fold_below_artifacts: 3
    fold_passthrough_chains: true
    notable_count: 10
    target_response_tokens: 4000
    allow_per_domain_overrides: true
```

Env vars and CLI flags override file values. Secret values should use `${ENV_VAR}` interpolation rather than being committed in plaintext.
