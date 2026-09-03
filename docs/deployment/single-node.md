---
title: Single node
nav_order: 2
description: One binary on one machine, run with podium serve --standalone. Fits anyone who needs runtime discovery or a single audit log without running Postgres, object storage, and an IdP.
---

# Single node

The single-node tier runs one binary on one machine. SQLite, sqlite-vec, and filesystem object storage are embedded in the process. Bind it to localhost or to an address behind your VPN.

Suitable for:

- Teams that want runtime discovery, with agents calling MCP meta-tools mid-session, without standing up Postgres, object storage, and an identity provider.
- Teams that want a single audit log capturing every load across the team.
- Offline and air-gapped development.
- Anyone evaluating Podium's discovery and search at small scale.

Teams whose catalog does not require access control or progressive discovery can stay on the [local](local) tier by committing the catalog to Git. Move to single node when runtime discovery or centralized audit becomes necessary.

---

## What's running

```
podium serve --standalone --layer-path /path/to/podium-artifacts/
```

That command runs a single process. It wires these backends by default:

| Integration | Backend |
|:--|:--|
| Metadata store | SQLite at `~/.podium/standalone/podium.db` |
| Object storage | Local filesystem at `~/.podium/standalone/objects/` |
| Vector index | `sqlite-vec`, collocated with the SQLite file. Pinecone, Weaviate Cloud, or Qdrant Cloud can be swapped in without adding other infrastructure; see [Vector backends](vector-backends). |
| Embedding provider | `ollama` against a local model. Any configured provider replaces it, and `--no-embeddings` disables embeddings so search runs BM25-only. |
| Identity provider | None. The registry-process providers `oidc-jwt` and `trusted-headers` can be enabled; see [Server-side integrations](integrations#identity). |

A single-node deployment requires no Postgres, no S3, and no external identity provider.

The `--standalone` flag selects a configuration of the same registry binary the clustered tier runs. It is not a separate build. [Server-side integrations](integrations) lists what each backend can be replaced with.

---

## Setup

### Server side

On the host that will run the registry:

```bash
podium serve --standalone --layer-path /path/to/podium-artifacts/
```

`podium serve` writes a default config to `~/.podium/registry.yaml` and starts serving on `127.0.0.1:8080`. The startup banner prints the bind address, ingests every layer under the path, and logs one line per layer with the resulting `accepted` / `idempotent` / `rejected` counts.

Setting `PODIUM_LAYER_PATH` in the environment is equivalent to passing `--layer-path` on the command line and is useful for systemd units, container deployments, and any other context where flags are awkward. The same value also accepts a `layer_path` key under the top-level `registry:` mapping in `~/.podium/registry.yaml`. Precedence is CLI flag > env var > config file (see the [CLI reference](../reference/cli)).

For a multi-user team, change the bind address and (optionally) enable auth:

```yaml
# ~/.podium/registry.yaml
registry:
  endpoint: https://podium.your-team.example
  bind: 0.0.0.0:8080

  # Optional: verify IdP-issued tokens so the audit log carries a subject
  identity_provider:
    type: oidc-jwt
    issuer: https://your-idp.example/oauth2/default   # must be https
    audience: https://podium.your-team.example
```

Every server-side key nests under the top-level `registry:` mapping. A document that starts at `endpoint:` or `identity_provider:` parses to an empty config and the registry ignores it without reporting an error.

`oidc-jwt` is the registry's side of the flow: it verifies each presented token against the issuer's JWKS and requires the token's `aud` claim to carry one of the values configured under `audience:`. A CLI, an SDK, or another API client obtains that token by running `podium login`, which completes the device-code flow against the same IdP, and on a registry that enables the browser flow a browser obtains it through the registry's own authorization-code exchange, which the registry returns in the `__Host-podium_session` cookie. Setting `oauth-device-code` as the registry's own provider stops startup with `config.identity_provider_unverified`, because the registry ships no request-time verifier for it. The [OIDC cookbooks](oidc/) give the per-IdP values for `issuer:` and `audience:`.

Run behind a TLS-terminating reverse proxy in production. The registry listens on HTTP only.

### Client side

Each developer's workspace points at the server URL:

```bash
cd your_workspace
podium init --registry https://podium.your-team.example --harness claude-code
podium sync
```

For runtime discovery via the MCP server, add the Podium MCP entry to the harness's MCP config. See [Configure your harness](../consuming/configure-your-harness) for per-harness recipes.

---

## Authoring source: filesystem path or git repo

A single-node deployment supports the built-in `local` and `git` layer source types. [Layered composition](layers) covers registering several of them and ordering the result.

**`local` source**: the easiest setup. The registry reads files directly from a filesystem path. Re-scanned on demand:

```bash
podium serve --standalone --layer-path /var/podium/team-artifacts/
podium layer reingest team-artifacts
```

For continuous updates, `podium layer watch --id <id>` re-triggers ingest on an interval set with `--interval` (default 1m).

**`git` source**: the registry mirrors a tracked Git ref. Configure layers in `~/.podium/registry.yaml`, under the same top-level `registry:` mapping the example above uses:

```yaml
registry:
  layers:
    - id: team-shared
      source:
        git:
          repo: git@github.com:your-org/podium-team-artifacts.git
          ref: main
      visibility:
        organization: true
```

Set up a webhook from the Git host to the registry's ingest endpoint, or rely on `podium layer reingest team-shared` triggered manually or on a schedule. Git providers without a webhook capability, such as offline mirrors and internal Git that cannot reach the registry, work through a scheduled `podium layer reingest`.

For a developer machine without a public ingress, use `podium layer watch` or a scheduled `reingest`. Webhooks are not required.

---

## Migrating from local

Migration is mechanical:

1. On a chosen host, run `podium serve --standalone --layer-path /path/to/podium-artifacts/`. The host can be a small VM behind your VPN, or any always-on machine.
2. Each developer changes `<workspace>/.podium/sync.yaml`:
   - Replace `defaults.registry: ./.podium/registry/` (or whatever path is configured) with `defaults.registry: https://podium.your-team.example`.
   - Optionally add the Podium MCP server entry to the harness's MCP config so the agent can call meta-tools at runtime.
3. The authoring loop is unchanged. Authors open a PR and merge against the same registry repo, and the server picks up changes through `podium layer reingest` or a watcher.

The shared library does the same parsing, composition, and adapter work in both tiers. Output is bit-identical for the same target and profile, so end-user behavior is preserved across the cut-over.

---

## What the tier adds over local

- **Runtime discovery** through the Podium MCP server. Agents call `load_domain`, `search_domains`, `search_artifacts`, and `load_artifact` to materialize only what they need.
- **Hybrid retrieval.** BM25 hits and vector hits are fused by reciprocal rank fusion. BM25 runs over manifest text, and vectors come from the configured embedding provider. `--no-embeddings` disables embeddings so search runs BM25-only.
- **Single audit log** capturing every read, ingest, and admin action. It is an append-only hash-chained file at `~/.podium/audit.log`, relocated by `PODIUM_AUDIT_LOG_PATH`, with retention enforced daily against `PODIUM_AUDIT_RETENTION_MAX_AGE_DAYS` (default 365).
- **Cross-type dependency graph.** `extends:`, `delegates_to:`, and `mcpServers:` references are all tracked.
- **Layer composition.** Several sources compose into one ordered catalog, so a deployment that starts with a single permissive layer already has the layer system in place for the second one. Visibility filtering runs once an identity provider is configured; without one the registry resolves every caller as anonymous and admits every layer. See [Layered composition](layers) and [Access control](access-control).
- **Lint as a CI check.** Run `podium lint` against the registry repo's PRs.

---

## What the tier omits

Single node omits the capabilities that need external services or a multi-tenant model:

- **Multi-tenancy.** A single-node deployment is single-tenant.
- **Transparency-log anchoring.** Sigstore-keyless signing requires public OIDC infrastructure.

When any of these starts mattering, see [Clustered](clustered).

---

## Public mode

For demos, evaluation pilots, and intentionally open internal knowledge bases, a single-node deployment supports a public mode that bypasses both authentication and the visibility model:

```bash
podium serve --standalone --public-mode --layer-path /path/to/artifacts/
```

Public mode is mutually exclusive with an identity provider, and setting both fails at startup. It binds to `127.0.0.1` by default. Pass `--allow-public-bind` to bind a non-loopback address, typically behind an authenticated reverse proxy.

Public mode applies a sensitivity ceiling: ingest of `sensitivity: medium` and `sensitivity: high` artifacts is rejected. The audit log records `caller.identity = "system:public"` for every call so downstream consumers can filter on it.

Public mode is appropriate when both of the following hold:

- The deployment is intentionally open beyond a single user, such as a demo registry, an evaluation pilot, or an internal-public catalog.
- The audit log should record that anonymous-public access was the deployment's intent rather than a misconfiguration.

For everyday team use, enable the `oidc-jwt` provider instead. Each developer authenticates the CLI once with `podium login`, and the audit log then records the authenticated subject on every call. See [Access control](access-control).

---

## Operational notes

- **Backup.** A periodic snapshot of `~/.podium/standalone/` captures the SQLite file, the object directory, and any signing keys the deployment generated there (`audit.key` when audit anchoring is enabled, `registry-signing.key` when `--sign registry-key` is set). Include `~/.podium/audit.log` in the same snapshot, because the audit stream sits outside that directory unless `PODIUM_AUDIT_LOG_PATH` moves it.
- **Upgrades.** Replace the binary and restart. Schema migrations run on first start of the new version.
- **Performance.** A single-node deployment is sized for tens of developers rather than thousands of QPS. For higher scale, see [Clustered](clustered).
- **Observability.** A Prometheus endpoint is served on `/metrics` unless `PODIUM_METRICS=false` turns it off. The reference Grafana dashboard is in the repository at `deploy/grafana-dashboard.json`.

---

## Migrating to clustered

When single node no longer fits, typically because multi-tenancy, OIDC group claims through SCIM, or production-grade availability is required, `podium admin migrate-to-standard` exports the single-node state into the standard stack:

```bash
podium admin migrate-to-standard --postgres <dsn> --object-store <url>
```

[Clustered](clustered) documents the same migration path from the receiving side. The artifact directory is unchanged, and layer config moves from `~/.podium/registry.yaml` to the tenant config.
