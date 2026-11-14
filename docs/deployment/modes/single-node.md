---
title: Single node
nav_order: 2
description: Run the registry as one binary on one machine with podium serve --standalone, for runtime discovery and a single audit log without Postgres, object storage, or an identity provider.
---

# Single node

The single-node deployment mode runs the registry as one binary on one machine. SQLite, sqlite-vec, and filesystem object storage are embedded in the process. Bind it to localhost behind a TLS-terminating reverse proxy, or to an address behind a VPN.

Single node fits these cases:

- Teams that want runtime discovery, with agents calling MCP meta-tools mid-session, without standing up Postgres, object storage, and an identity provider.
- Teams that want a single audit log capturing every load across the team.
- Offline and air-gapped development.
- Evaluating Podium's discovery and search at small scale.

A team whose catalog requires neither access control nor progressive discovery can stay on the [Local](local) deployment mode by committing the catalog to Git. Move to single node when runtime discovery or centralized audit becomes necessary. [Local to single node](migrating#local-to-single-node) covers the move.

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
| Vector index | `sqlite-vec`, collocated with the SQLite file. Pinecone, Weaviate Cloud, or Qdrant Cloud can be swapped in without adding other infrastructure; see [Vector backends](../services/vector-backends). |
| Embedding provider | `ollama` against a local model. Any configured provider replaces it, and `--no-embeddings` disables embeddings so search runs BM25-only. |
| Identity provider | None. The registry-process providers `oidc-jwt` and `trusted-headers` can be enabled; see [Identity](../identity/). |

A single-node deployment requires no Postgres, no S3, and no external identity provider. The `--standalone` flag selects a configuration of the same registry binary the Clustered deployment mode runs. [Backing services](../services/) lists what each backend can be replaced with.

---

## Setup

### Before you start

- Install the `podium` CLI on the host that will run the registry and on each developer machine, as [Install the CLI](../../getting-started/quickstart#1-install-the-cli) describes. The same binary runs the server.
- Prepare a catalog directory on the host, laid out as [Directory layout](local#directory-layout) describes.

### Server side

On the host that will run the registry, start the server:

```bash
podium serve --standalone --layer-path /path/to/podium-artifacts/
```

`podium serve` writes a default config to `~/.podium/registry.yaml` and starts serving on `127.0.0.1:8080`. The startup banner prints the bind address. The server ingests every layer under the path and logs one line per layer with the resulting `accepted`, `idempotent`, and `rejected` counts.

For a multi-user team, set the registry's external URL and bind address in `~/.podium/registry.yaml`, and optionally enable authentication:

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

`endpoint:` is the URL clients reach the registry at, and the registry builds the absolute URLs it advertises from it, such as the webhook URL `podium layer register` returns. The example binds every interface, which suits a reverse proxy or a VPN on another host. A reverse proxy on the same host reaches the default `127.0.0.1:8080` bind, so the `bind:` line can be omitted. Every server key nests under the top-level `registry:` mapping, as [File layout](../../reference/configuration#file-layout) describes.

`PODIUM_LAYER_PATH` in the environment is equivalent to `--layer-path` on the command line. It suits systemd units, container deployments, and any other context where flags are awkward. The same value is also accepted as a `layer_path` key under `registry:` in `~/.podium/registry.yaml`. The CLI flag takes precedence over the environment variable, which takes precedence over the config file (see the [CLI reference](../../reference/cli)).

`oidc-jwt` verifies each presented token against the issuer's JWKS and requires the token's `aud` claim to carry one of the values configured under `audience:`. Each developer obtains a token by running `podium login`, which completes the device-code flow against the same IdP. [OIDC](../identity/oidc/) gives the per-IdP values for `issuer:` and `audience:`, and [Identity](../identity/) describes the consumer and registry sides of the flow.

### Serve over TLS

The registry listens on plain HTTP only. Run it behind a TLS-terminating reverse proxy whenever a client reaches it over a network. A minimal [Caddy](https://caddyserver.com/) configuration on the same host serves the registry at the `endpoint:` URL and obtains a certificate for the host name automatically:

```text
# /etc/caddy/Caddyfile
podium.your-team.example {
	reverse_proxy 127.0.0.1:8080
}
```

Any reverse proxy that forwards requests to the bind address and terminates TLS for the `endpoint:` host serves the same purpose.

### Run as a service

The repository ships no service unit. On a Linux host with systemd, a unit such as the following keeps the registry running and restarts it after a failure:

```text
# /etc/systemd/system/podium.service
[Unit]
Description=Podium registry
After=network-online.target
Wants=network-online.target

[Service]
User=podium
Environment=PODIUM_LAYER_PATH=/srv/podium-artifacts
ExecStart=/usr/local/bin/podium serve --standalone
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

The registry keeps its state under the service account's home directory: `~/.podium/registry.yaml`, the store, the object directory, and the signing key under `~/.podium/standalone/`, and the audit log at `~/.podium/audit.log`. Give the account a persistent home directory and read access to the catalog directory, and adjust `ExecStart` to the path of the installed binary. Enable and start the unit with `sudo systemctl enable --now podium`. The registry stops cleanly on the `SIGTERM` that `systemctl stop` sends.

### Client side

Point each developer's workspace at the server URL:

```bash
cd your_workspace
podium init --registry https://podium.your-team.example --harness claude-code
podium sync
```

For runtime discovery through the MCP server, add the Podium MCP entry to the harness's MCP config. [Configure your harness](../../consuming/configure-your-harness) has per-harness recipes.

#### Signature verification on the client

The registry signs every artifact at ingest by default, and `podium-mcp` verifies each load under `PODIUM_VERIFY_SIGNATURES=always` by default. The MCP entry therefore carries the registry's verification key set.

- A client on another machine sets `PODIUM_SIGNATURE_PROVIDER=registry-managed` and `PODIUM_SIGNATURE_VERIFY_KEY` to the registry's public key. Extract the key from the server's key file with the command [Clustered](clustered#2-deploy-the-registry) shows. The key file is `~/.podium/standalone/registry-signing.key` unless `PODIUM_SIGN_KEY_PATH` moves it.
- A client on the server host, under the same user account, resolves the set from the `public:` line and every `verify:` line of the registry's own key file, and sets neither variable.

Without the key, and without `PODIUM_VERIFY_SIGNATURES=never`, `podium-mcp` refuses to start with `config.signature_provider_unavailable`. [Signing keys](../operations/signing-keys) covers distributing the key set during a rotation.

---

## Connecting sources

A single-node deployment reads layers from the built-in `local` and `git` source types. `--layer-path` connects a directory on the host. `registry.layers:` in `~/.podium/registry.yaml`, or `podium layer register`, connects a Git repository.

[Connecting layer sources](../content/layer-sources) covers Git repositories, private repositories, local paths, and keeping layers current. A developer machine without a public ingress needs no webhook: `podium layer watch` or a scheduled `podium layer reingest` keeps each layer current. [Layered composition](../content/layers) covers ordering several layers.

---

## What single node adds over local

- **Runtime discovery** through the Podium MCP server. Agents call `load_domain`, `search_domains`, `search_artifacts`, and `load_artifact` to materialize only what they need.
- **Hybrid retrieval.** Reciprocal rank fusion combines BM25 hits over manifest text with vector hits from the configured embedding provider. `--no-embeddings` disables embeddings so search runs BM25-only.
- **A single audit log** capturing every read, ingest, and admin action. [Audit log on a single node](#audit-log-on-a-single-node) describes its defaults.
- **Cross-type dependency graph.** `delegates_to:` and `mcpServers:` references are both tracked.
- **Layer composition.** Several sources compose into one ordered catalog, so a deployment that starts with a single permissive layer already has the layer system in place for the second one. Visibility filtering runs once an identity provider is configured. Without one, the registry resolves every caller as anonymous and admits every layer. See [Layered composition](../content/layers) and [Access control](../content/access-control).
- **Lint as a CI check.** Run `podium lint` against the registry repo's PRs.

### Audit log on a single node

By default the audit log is an append-only, hash-chained file at `~/.podium/audit.log`. Retention is enforced daily against `PODIUM_AUDIT_RETENTION_MAX_AGE_DAYS`, which defaults to 365.

`PODIUM_AUDIT_LOG_PATH` takes a comma-separated list of destinations: a file path, which relocates the file, an `http` or `https` URL, an `otlp+http` or `otlp+https` URL, and `stdout`. To ship the stream to another system, list a stdout, HTTP, or OTLP destination beside the file. [Audit destinations](../operations/observability#audit-destinations) describes each destination and why a shipper does not tail the file.

---

## What single node omits

A single-node deployment is single-tenant by default. Setting `PODIUM_MULTI_TENANT=true` with an identity provider the registry verifies turns on multi-tenant routing on a single node, and the SQLite store scopes each row by its tenant. [Tenants and admins](../identity/tenants) covers multi-tenant mode. A registry started with `PODIUM_MULTI_TENANT` fails to start in these cases, unless another startup check refuses the configuration first:

| Configuration | Startup error |
|:--|:--|
| No identity provider, a provider value the registry does not recognize, or public mode with no identity provider | `config.multitenant_no_verifier` |
| A recognized provider the registry does not verify, such as `oauth-device-code` | `config.identity_provider_unverified` |

A single node omits the Postgres schema-per-org isolation and row-level security the Clustered deployment mode provides for multi-tenant deployments, and it runs no replicas.

Artifact signing and local chain-head anchoring of the audit log run on a single node. The registry signs every artifact with its registry-managed key by default. When `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` is above 0 and `PODIUM_AUDIT_LOG_PATH` lists a file destination, the registry also anchors the audit chain head with its anchor key. No deployment mode submits the chain head to a public transparency log.

When a multi-tenant deployment needs Postgres-backed isolation, or the registry needs replicas, move to [Clustered](clustered). `podium admin migrate-to-standard` exports the single-node state into the standard stack:

```bash
podium admin migrate-to-standard --postgres <dsn> --object-store <url>
```

[Single node to clustered](migrating#single-node-to-clustered) gives the full procedure, including carrying the signing key and the stored-row rewrite that the target needs before its first start.

---

## Public mode

Public mode bypasses both authentication and the visibility model. It is intended for demos, evaluation pilots, and intentionally open internal knowledge bases:

```bash
podium serve --standalone --public-mode --layer-path /path/to/artifacts/
```

- Public mode is mutually exclusive with an identity provider, and setting both fails at startup.
- Public mode is also mutually exclusive with `PODIUM_MULTI_TENANT`, and setting both fails at startup. The registry reports `config.multitenant_no_verifier` when `PODIUM_IDENTITY_PROVIDER` is unset or `none`, unless another startup check refuses the configuration first.
- Public mode binds to `127.0.0.1` by default. Pass `--allow-public-bind` to bind a non-loopback address, typically behind an authenticated reverse proxy.
- Public mode applies a sensitivity ceiling: ingest of `sensitivity: medium` and `sensitivity: high` artifacts is rejected.
- The audit log records `caller.identity = "system:public"` for every call, so downstream consumers can filter on it.

Public mode is appropriate when both of the following hold:

- The deployment is intentionally open beyond a single user, such as a demo registry, an evaluation pilot, or an internal-public catalog.
- The audit log should record that anonymous-public access was the deployment's intent rather than a misconfiguration.

For everyday team use, enable the `oidc-jwt` provider instead. Each developer authenticates the CLI once with `podium login`, and the audit log then records the authenticated subject on every call. See [Access control](../content/access-control).

---

## Operations

- **Performance.** A single-node deployment is sized for tens of developers rather than thousands of QPS. For higher scale, see [Clustered](clustered).
- **Observability.** A Prometheus endpoint is served on `/metrics` unless `PODIUM_METRICS=false` turns it off. The reference Grafana dashboard is in the repository at `deploy/grafana-dashboard.json`. [Audit and observability](../operations/observability) covers metrics, tracing, and the audit stream.
- **Backup.** Snapshot `~/.podium/standalone/` and the audit log together, as [Backup and restore](../operations/backup-restore) describes. The snapshot holds the signing key, which the registry needs to serve the rows it signed.
- **Upgrades.** Replace the binary and restart it, after taking a backup. [Upgrading](../operations/upgrading) covers releases that migrate stored values, and [Upgrade notes](../operations/upgrade-notes) gives the release-specific steps.
- **Signing key.** [Signing keys](../operations/signing-keys) covers rotating the key and recovering from a lost one.
