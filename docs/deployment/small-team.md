---
layout: default
title: Small team
parent: Deployment
nav_order: 2
description: Standalone server on a single VM. This setup fits small teams that want runtime discovery and a single audit log without a full standard deployment.
---

# Small team

The standalone server: a single binary running on one machine. SQLite + sqlite-vec + filesystem object storage + a bundled embedding model, all embedded. Bind to localhost or behind your VPN.

Suitable for:

- Teams that want runtime discovery (agents calling MCP meta-tools mid-session) without standing up the full standard stack.
- Teams that want a single audit log capturing every load across the team.
- Offline / air-gapped development.
- Anyone evaluating Podium's discovery and search capabilities at small scale.

Most small teams can stay on filesystem mode (see [Solo / filesystem](solo-filesystem)) by committing the catalog to git. Reach for standalone when filesystem mode stops fitting.

---

## What's running

```
podium serve --standalone --layer-path /path/to/podium-artifacts/
```

That command runs a single process. The standalone server includes:

| Component | Backend in standalone |
|:--|:--|
| Metadata store | SQLite at `~/.podium/standalone/podium.db` |
| Object storage | Local filesystem at `~/.podium/standalone/objects/` |
| Vector backend | `sqlite-vec` collocated with SQLite |
| Embedding provider | `ollama` pointed at a local model (or any other configured provider). `--no-embeddings` falls back to BM25-only. |
| Identity provider | Optional. Public mode (no auth) by default; `oauth-device-code` can be enabled. |

The standalone mode requires no Postgres, no S3, and no external identity provider.

The same registry binary serves both standalone and standard modes. Standalone is a deployment configuration; it is not a separate build.

---

## Setup

### Server side

On the host that will run the registry:

```bash
podium serve --standalone --layer-path /path/to/podium-artifacts/
```

`podium serve` writes a default config to `~/.podium/registry.yaml` and starts serving on `127.0.0.1:8080`. The startup banner prints the bind address and the layer path.

For a multi-user team, change the bind address and (optionally) enable auth:

```yaml
# ~/.podium/registry.yaml
registry:
  endpoint: https://podium.your-team.example
  bind: 0.0.0.0:8080

  # Optional: enable OIDC auth for identity-based audit
  identity_provider:
    type: oauth-device-code
    audience: https://podium.your-team.example
    authorization_endpoint: https://your-idp.example/oauth2/default
```

Run behind a TLS-terminating reverse proxy in production. The standalone binary listens HTTP only.

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

Standalone supports the built-in `local` and `git` source types.

**`local` source**: the easiest setup. The registry reads files directly from a filesystem path. Re-scanned on demand:

```bash
podium serve --standalone --layer-path /var/podium/team-artifacts/
podium layer reingest team-artifacts
```

For continuous updates, `podium layer watch <id>` polls the source at a configured interval (or use fsnotify on the host for local sources).

**`git` source**: the registry mirrors a tracked Git ref. Configure layers in `~/.podium/registry.yaml`:

```yaml
layers:
  - id: team-shared
    source:
      git:
        repo: git@github.com:your-org/podium-team-artifacts.git
        ref: main
    visibility:
      organization: true
```

Set up a webhook from the Git host to the registry's ingest endpoint, or rely on `podium layer reingest team-shared` triggered manually or on a schedule. Git providers without a webhook capability (offline mirrors, internal Git that can't reach the registry) work fine via scheduled `podium layer reingest`.

For a developer machine without a public ingress, use `podium layer watch` or scheduled `reingest`; webhooks are not required.

---

## Migrating from filesystem mode

Migration is mechanical:

1. On a chosen host, run `podium serve --standalone --layer-path /path/to/podium-artifacts/`. The host can be a small VM behind your VPN, or any always-on machine.
2. Each developer changes `<workspace>/.podium/sync.yaml`:
   - Replace `defaults.registry: ./.podium/registry/` (or whatever path) with `defaults.registry: https://podium.your-team.example`.
   - Optional: add the Podium MCP server entry to the harness's MCP config so the agent can call meta-tools at runtime.
3. The authoring loop is unchanged: git PR and merge against the same registry repo. The standalone server picks up changes via `podium layer reingest` or a watcher.

The shared library does the same parsing, composition, and adapter work in both modes. Output is bit-identical for the same target and profile, so end-user behavior is preserved across the cut-over.

---

## What you get

- **Runtime discovery** via the Podium MCP server. Agents call `load_domain`, `search_domains`, `search_artifacts`, and `load_artifact` to materialize only what they need.
- **Hybrid retrieval.** BM25 + vector embeddings via reciprocal rank fusion. Out of the box: BM25 over manifest text plus vectors via the configured embedding provider; `--no-embeddings` falls back to BM25-only when no provider is wired.
- **Single audit log** capturing every read, ingest, and admin action. SQLite-backed; configurable retention.
- **Cross-type dependency graph.** `extends:`, `delegates_to:`, `mcpServers:` references all tracked.
- **Layer composition with visibility.** Even when you start with a single permissive layer, the layer system is in place for when you add a second.
- **Lint as a CI check.** Run `podium lint` against the registry repo's PRs.

---

## What's not in this mode

Standalone deliberately omits the things that need external services or a multi-tenant model:

- **Multi-tenancy.** A standalone deployment is single-tenant.
- **OIDC group claims via SCIM.** Group membership comes from OIDC claims directly; SCIM push is a standard-deployment feature.
- **Transparency-log anchoring.** Sigstore-keyless signing requires public OIDC infrastructure.
- **Outbound webhooks.** Configurable but typically unused at small-team scale.

When any of these starts mattering, see [Organization](organization) for the standard deployment.

---

## Public mode

For demos, evaluation pilots, or intentionally open internal-knowledge-base deployments, standalone supports a public mode that bypasses both authentication and the visibility model:

```bash
podium serve --standalone --public-mode --layer-path /path/to/artifacts/
```

Public mode is mutually exclusive with an identity provider; setting both fails at startup. It binds to `127.0.0.1` by default; pass `--allow-public-bind` to bind a non-loopback address (typically behind an authenticated reverse proxy).

Sensitivity ceiling: ingest of `sensitivity: medium` and `sensitivity: high` artifacts is rejected in public mode. The audit log records `caller.identity = "system:public"` for every call so downstream consumers can filter.

This is appropriate when:

- The deployment is intentionally open beyond a single user (a demo registry, an evaluation pilot, an internal-public catalog), and
- The audit log should record that anonymous-public access was the deployment's intent rather than a misconfiguration.

For everyday small-team use, default to `oauth-device-code` auth. The added ceremony is small and the audit is sharper.

---

## Operational notes

- **Backup.** SQLite + the object directory. A periodic snapshot of `~/.podium/standalone/` is enough.
- **Upgrades.** Replace the binary, restart. Schema migrations run on first start of the new version.
- **Performance.** Standalone is sized for tens-of-developer scale rather than thousands of QPS. For higher scale, see [Organization](organization).
- **Observability.** Prometheus endpoint on `/metrics`. The reference Grafana dashboard ships with the binary.

---

## Migrating to standard

When standalone no longer fits, typically because multi-tenancy, OIDC group claims via SCIM, or production-grade availability is required, `podium admin migrate-to-standard` exports the standalone state to a standard deployment:

```bash
podium admin migrate-to-standard --postgres <dsn> --object-store <url>
```

Same migration path documented in [Organization](organization). The artifact directory is unchanged; layer config moves from the standalone `~/.podium/registry.yaml` to the standard tenant config.
