---
title: Single node
nav_order: 2
description: Run the registry as one binary on one machine with podium serve --standalone, for runtime discovery and a single audit log without Postgres, object storage, or an identity provider.
---

# Single node

The single-node deployment mode runs the registry as one binary on one machine. SQLite, sqlite-vec, and filesystem object storage are embedded in the process. Bind it to localhost or to an address behind a VPN.

Single node fits these cases:

- Teams that want runtime discovery, with agents calling MCP meta-tools mid-session, without standing up Postgres, object storage, and an identity provider.
- Teams that want a single audit log capturing every load across the team.
- Offline and air-gapped development.
- Evaluating Podium's discovery and search at small scale.

A team whose catalog requires neither access control nor progressive discovery can stay on the [Local](local) deployment mode by committing the catalog to Git. Move to single node when runtime discovery or centralized audit becomes necessary.

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

The `--standalone` flag selects a configuration of the same registry binary the Clustered deployment mode runs, rather than a separate build. [Server-side integrations](integrations) lists what each backend can be replaced with.

---

## Setup

### Server side

On the host that will run the registry, start the server:

```bash
podium serve --standalone --layer-path /path/to/podium-artifacts/
```

`podium serve` writes a default config to `~/.podium/registry.yaml` and starts serving on `127.0.0.1:8080`. The startup banner prints the bind address. The server ingests every layer under the path and logs one line per layer with the resulting `accepted`, `idempotent`, and `rejected` counts.

For a multi-user team, change the bind address in `~/.podium/registry.yaml`, and optionally enable authentication:

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

Run the registry behind a TLS-terminating reverse proxy in production. The registry listens on HTTP only.

`PODIUM_LAYER_PATH` in the environment is equivalent to `--layer-path` on the command line. It suits systemd units, container deployments, and any other context where flags are awkward. The same value is also accepted as a `layer_path` key under the top-level `registry:` mapping in `~/.podium/registry.yaml`. The CLI flag takes precedence over the environment variable, which takes precedence over the config file (see the [CLI reference](../reference/cli)).

Every server-side key nests under the top-level `registry:` mapping. A document that starts at `endpoint:` or `identity_provider:` parses to an empty config, and the registry ignores it without reporting an error.

#### How oidc-jwt authenticates callers

`oidc-jwt` is the registry's side of the flow. It verifies each presented token against the issuer's JWKS and requires the token's `aud` claim to carry one of the values configured under `audience:`.

- A CLI, an SDK, or another API client obtains the token by running `podium login`, which completes the device-code flow against the same IdP.
- On a registry that enables the browser flow, a browser obtains the token through the registry's own authorization-code exchange. The registry returns it in the `__Host-podium_session` cookie.
- Setting `oauth-device-code` as the registry's own provider stops startup with `config.identity_provider_unverified`, because the registry ships no request-time verifier for it.

The [OIDC cookbooks](oidc/) give the per-IdP values for `issuer:` and `audience:`.

### Client side

Point each developer's workspace at the server URL:

```bash
cd your_workspace
podium init --registry https://podium.your-team.example --harness claude-code
podium sync
```

For runtime discovery through the MCP server, add the Podium MCP entry to the harness's MCP config. [Configure your harness](../consuming/configure-your-harness) has per-harness recipes.

#### Signature verification on the client

The registry signs every artifact at ingest by default, and `podium-mcp` verifies each load under `PODIUM_VERIFY_SIGNATURES=always` by default. The MCP entry therefore carries the registry's verification key set.

- A client on another machine sets `PODIUM_SIGNATURE_PROVIDER=registry-managed` and `PODIUM_SIGNATURE_VERIFY_KEY` to the registry's public key. Extract the key from the server's key file with the command [Clustered](clustered#2-deploy-the-registry) shows. The key file is `~/.podium/standalone/registry-signing.key` unless `PODIUM_SIGN_KEY_PATH` moves it.
- During a key rotation the variable takes a comma-separated list of keys, as [Rotating the signing key](operator-guide#rotating-the-signing-key) states.
- A client on the server host, under the same user account, resolves the set from the `public:` line and every `verify:` line of the registry's own key file, and sets neither variable.

Without the key, and without `PODIUM_VERIFY_SIGNATURES=never`, `podium-mcp` refuses to start with `config.signature_provider_unavailable`.

---

## Connecting sources

A single-node deployment reads layers from the built-in `local` and `git` source types. `--layer-path` connects a directory on the host. `registry.layers:` in `~/.podium/registry.yaml`, or `podium layer register`, connects a Git repository.

[Connecting layer sources](layer-sources) covers [Git repositories](layer-sources#git-repositories), [private repositories](layer-sources#private-repositories), [local paths](layer-sources#local-paths), and [keeping layers current](layer-sources#keeping-layers-current). A developer machine without a public ingress needs no webhook: `podium layer watch` or a scheduled `podium layer reingest` keeps each layer current. [Layered composition](layers) covers ordering several layers.

---

## Migrating from local

The migration keeps the directory and the authoring loop:

1. On a chosen host, run `podium serve --standalone --layer-path /path/to/podium-artifacts/`. The host can be a small VM behind a VPN, or any always-on machine.
2. In each developer's `<workspace>/.podium/sync.yaml`, replace `defaults.registry: ./.podium/registry/`, or whatever path is configured, with `defaults.registry: https://podium.your-team.example`.
3. Optionally, add the Podium MCP server entry to the harness's MCP config so the agent can call meta-tools at runtime.

Authors keep opening PRs and merging against the same registry repo. The server picks up changes through `podium layer reingest` or a watcher.

The shared library does the same parsing, composition, and adapter work in both deployment modes. Output is bit-identical for the same target and profile, so end-user behavior is preserved across the cut-over. Skills that declare no version are the exception that [Local](local#skills-that-declare-no-version) describes.

---

## What single node adds over local

- **Runtime discovery** through the Podium MCP server. Agents call `load_domain`, `search_domains`, `search_artifacts`, and `load_artifact` to materialize only what they need.
- **Hybrid retrieval.** Reciprocal rank fusion combines BM25 hits over manifest text with vector hits from the configured embedding provider. `--no-embeddings` disables embeddings so search runs BM25-only.
- **A single audit log** capturing every read, ingest, and admin action. [Audit log on a single node](#audit-log-on-a-single-node) describes its defaults.
- **Cross-type dependency graph.** `delegates_to:` and `mcpServers:` references are both tracked.
- **Layer composition.** Several sources compose into one ordered catalog, so a deployment that starts with a single permissive layer already has the layer system in place for the second one. Visibility filtering runs once an identity provider is configured. Without one, the registry resolves every caller as anonymous and admits every layer. See [Layered composition](layers) and [Access control](access-control).
- **Lint as a CI check.** Run `podium lint` against the registry repo's PRs.

### Audit log on a single node

By default the audit log is an append-only, hash-chained file at `~/.podium/audit.log`. Retention is enforced daily against `PODIUM_AUDIT_RETENTION_MAX_AGE_DAYS`, which defaults to 365.

`PODIUM_AUDIT_LOG_PATH` takes a comma-separated list of destinations:

- A file path, which relocates the file.
- An `http` or `https` URL.
- An `otlp+http` or `otlp+https` URL.
- `stdout`.

Retention and erasure rewrite the file destination. To ship the stream to another system, list a stdout, HTTP, or OTLP destination beside the file rather than tailing the file. A tool that tails the file re-ships or skips records when a retention or erasure rewrite renames a new file over the old one. The records it already shipped keep their unredacted identities, because erasure rewrites only the file. [Audit destinations](observability#audit-destinations) and [Audit events](../reference/audit-events) describe each destination.

---

## What single node omits

A single-node deployment is single-tenant by default. Setting `PODIUM_MULTI_TENANT=true` with an identity provider the registry verifies turns on multi-tenant routing on a single node, and the SQLite store scopes each row by its tenant. A registry started with `PODIUM_MULTI_TENANT` fails to start in these cases, unless another startup check refuses the configuration first:

| Configuration | Startup error |
|:--|:--|
| No identity provider, a provider value the registry does not recognize, or public mode with no identity provider | `config.multitenant_no_verifier` |
| A recognized provider the registry does not verify, such as `oauth-device-code` | `config.identity_provider_unverified` |

A single node omits the Postgres schema-per-org isolation and row-level security the Clustered deployment mode provides for multi-tenant deployments, and it runs no replicas.

Artifact signing and local chain-head anchoring of the audit log run on a single node. The registry signs every artifact with its registry-managed key by default. When `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` is above 0 and `PODIUM_AUDIT_LOG_PATH` lists a file destination, the registry also anchors the audit chain head with its anchor key. No deployment mode submits the chain head to a public transparency log.

When a multi-tenant deployment needs Postgres-backed isolation, or the registry needs replicas, see [Clustered](clustered).

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

For everyday team use, enable the `oidc-jwt` provider instead. Each developer authenticates the CLI once with `podium login`, and the audit log then records the authenticated subject on every call. See [Access control](access-control).

---

## Operational notes

### Backup

Take a periodic snapshot of `~/.podium/standalone/`. It holds the SQLite file, the object directory, and the signing keys the deployment generated there:

- `registry-signing.key`, which the first run writes unless signing is off (`--sign none` or `PODIUM_SIGN=none`).
- `audit.key`, when audit anchoring is enabled. `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` enables anchoring, and `PODIUM_AUDIT_SIGNING_KEY_PATH` moves `audit.key`.

Include the file named by `PODIUM_SCIM_STORE_PATH` when SCIM persistence is configured. Include `~/.podium/audit.log` in the same snapshot, because the audit stream sits outside that directory unless `PODIUM_AUDIT_LOG_PATH` moves it.

Keep the anchor keypair distinct from the registry signing key at `PODIUM_SIGN_KEY_PATH`. While anchoring is enabled and signing is on, a shared key refuses startup with `config.audit_anchor_key_shared`.

### Losing the signing key

The registry signing key belongs in the backup. What happens when it is missing depends on whether the release's first-start rewrite of stored content hashes has completed:

- Before that rewrite has completed, a start with the key file missing is refused when the store holds signed rows.
- Once that rewrite has completed, a start with the key file missing generates a fresh keypair and proceeds. The registry then refuses every row the retired key signed with `materialize.signature_invalid`, to every reader and under any consumer policy.

To repair, stop the registry, restore the key file from the backup, and start it again. The registry loads its signing key once at start. Restoring the file leaves refused every row the fresh key signed in the meantime. To admit those rows, list the fresh key's public half on a `verify:` line of the restored file and restart the registry.

When only the lost key's public half survives, list it on a `verify:` line of the key file the registry now holds and restart the registry, which admits those rows. Then run `podium admin sign-stored-rows`, which re-signs them under the current key. [Signing failures](operator-guide#signing-failures) states both repairs.

When the store holds no record that the first-start rewrite of stored content hashes has completed, `sign-stored-rows` performs that rewrite. It must therefore run only while no registry process on the previous release serves the store, and it cannot detect such a process ([The `sign-stored-rows` command](operator-guide#the-sign-stored-rows-command)).

### Upgrades

Replace the binary and restart. Schema migrations run on first start of the new version. Take the backup described above before replacing the binary, because returning to the previous binary requires restoring it.

A release whose changelog names a migration of the values stored under the schema also rewrites those values on that first start, in either signing mode, in two cases:

- The SQLite store sits in the directory of the key file, which is `~/.podium/standalone/` unless `PODIUM_SIGN_KEY_PATH` moves it.
- The store holds no manifest row.

A store elsewhere that holds rows, including a signing-off store at a custom `PODIUM_SQLITE_PATH`, is refused at start until `sign-stored-rows` records the rewrite. The refused start rewrites no manifest row, signs no row, and records no completion. For such a store, before the first start of the new binary:

1. Stop the previous binary.
2. With signing on, when no file exists yet at `PODIUM_SIGN_KEY_PATH`, create it with `podium admin signing-key generate --key-file <path>`. `sign-stored-rows` never generates a key.
3. Run `podium-server sign-stored-rows --dry-run` and review its report.
4. Run `podium-server sign-stored-rows --plan-digest=<digest>` with the `sha256:` value from the report's last line. With signing on, the run repeats the dry run's `--include-unsigned` setting.

With signing off, both commands run with `PODIUM_SIGN=none` in their environment, because `podium serve --sign none` sets the mode only for its own process. The changelog entry for that release states the order.

### Performance

A single-node deployment is sized for tens of developers rather than thousands of QPS. For higher scale, see [Clustered](clustered).

### Observability

A Prometheus endpoint is served on `/metrics` unless `PODIUM_METRICS=false` turns it off. The reference Grafana dashboard is in the repository at `deploy/grafana-dashboard.json`. [Audit and observability](observability) covers metrics, tracing, and the audit stream.

---

## Migrating to clustered

Single node stops fitting when the deployment needs production-grade availability across replicas, Postgres-backed tenant isolation, or scale beyond tens of developers. `podium admin migrate-to-standard` exports the single-node state into the standard stack:

```bash
podium admin migrate-to-standard --postgres <dsn> --object-store <url>
```

The migration carries every tenant on the single node. For each tenant it carries the artifact manifests, layer configuration, admin grants, quotas, and active state. It copies the artifact content blobs and, with `--target-audit-log`, the whole audit log.

- A clustered target routes requests to a tenant other than the `default` org only when it is started with `PODIUM_MULTI_TENANT` and an identity provider that authenticates each caller.
- Instance-operator grants come from the target's own `PODIUM_OPERATOR_ADMINS`.

[Clustered](clustered) documents the same migration path from the receiving side. The artifact directory is unchanged, and layer config moves from `~/.podium/registry.yaml` to the tenant config.

### Carry the signing key

The target registry signs with the source's key. Before the target's first start after the migration, place the source's registry signing key at the target's `PODIUM_SIGN_KEY_PATH`, replacing any key file already there. Set `TARGET_SIGN_KEY_PATH` to the file the target's `PODIUM_SIGN_KEY_PATH` names, and copy the key on the source host:

```bash
install -m 600 ~/.podium/standalone/registry-signing.key "$TARGET_SIGN_KEY_PATH"
```

A source started with `PODIUM_SIGN_KEY_PATH` copies the file that variable names instead. On a chart deployment, create the signing Secret from the source's key file, as [Clustered](clustered#2-deploy-the-registry) shows for a generated one.

### Rewrite of stored content hashes

`migrate-to-standard` clears the target store's record of the rewrite of stored content hashes:

- Where the target is the SQLite store in the key file's directory, the target's next start runs the rewrite over the copied rows.
- A target store elsewhere, such as the Postgres store of a standard deployment, holds the copied rows and no completion record. Its registry is refused at start in either signing mode until `sign-stored-rows` records the rewrite. The refused start rewrites no manifest row, signs no row, and records no completion.

Before such a target's first start, run `podium-server sign-stored-rows --dry-run` against the target's configuration and review its report. Then run `podium-server sign-stored-rows` with the report's `--plan-digest`. With signing on, repeat the dry run's `--include-unsigned` setting. With signing off, put `PODIUM_SIGN=none` in the commands' environment. On a chart deployment, the migrate Job runs both commands.

### If the key step was skipped

A target with no key file is refused at start, naming `PODIUM_SIGN_KEY_PATH`.

A target holding any other key leaves every copied signed row untouched. That includes a key an earlier start generated and a Secret created from a different key. The `sign-stored-rows` dry run such a target requires classifies each of those rows `signature_unverified`, and a run under that key leaves each at its stored hash. The outcome then depends on the source's state:

- **A row the source had already rewritten** keeps the rewritten content hash and the source's envelope. While the target runs with the other key, the target registry refuses every such row with `materialize.signature_invalid`, to every reader and under any consumer policy. To repair those rows with no new version, place the source's key at the target's `PODIUM_SIGN_KEY_PATH`, replacing the other key, and restart the target. The registry loads its key only at start.
- **A row still at the previous content hash**, from a source that never started on this release, stays at that hash, and placing the source's key alone does not repair it. List the source key's public half on a `verify:` line of the target's key file, restart the target, and run `podium-server sign-stored-rows` or `podium admin sign-stored-rows`. The command moves each such row to the new content hash and re-signs it under the target's key.

An alternative repair for the second case is to recreate the target store empty and run `podium admin migrate-to-standard` again with the source's key at the target's `PODIUM_SIGN_KEY_PATH`. The command clears the target's record of the rewrite, so the rewrite runs again and re-signs the copied rows. Where the target is the SQLite store in the key file's directory, that happens at the next start. Otherwise it happens through the `sign-stored-rows` dry run and the run with its plan digest, before the target's start. On a Helm chart deployment, hold the release at zero replicas before the command runs and run the rewrite in the migrate Job, as [Migration from single node](clustered#migration-from-single-node) on the clustered page states.

A new version of each affected artifact is the repair only where neither the source store nor any copy of the source key's public half survives.
