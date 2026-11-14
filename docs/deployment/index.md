---
title: Deployment
nav_order: 4
description: Choose a Podium deployment mode and find the operator task pages for layer sources, access control, audit and observability, backing services, and day-two operations.
actions:
  - label: Connecting layer sources
    href: layer-sources
  - label: Audit and observability
    href: observability
---

# Deployment

This section is for operators who run a Podium registry. It helps choose a deployment mode, connect content to the registry, control who sees it, and operate the server once it runs.

## Common tasks

- Connect a Git repository or a local directory to a registry: [Connecting layer sources](layer-sources#git-repositories).
- Keep layers current with webhooks, polling, or a manual reingest: [Keeping layers current](layer-sources#keeping-layers-current).
- Send audit events to a file, stdout, an HTTP receiver, or an OpenTelemetry Collector: [Audit destinations](observability#audit-destinations).
- Export Prometheus metrics and OTLP traces, and configure ingest-failure notifications: [Audit and observability](observability).
- Restrict who sees each layer: [Access control](access-control).
- Set up an OIDC identity provider: [OIDC cookbooks](oidc/).
- Back up, upgrade, or rotate the signing key of a running registry: [Operator guide](operator-guide).

## Choosing a deployment mode

The deployment modes are Local, Single node, and Clustered. Each mode keeps everything the mode before it does and adds server-side capability.

| Deployment mode | Server-side deployment | Catalog source | Materialization | What the mode adds |
|:--|:--|:--|:--|:--|
| [Local](local) | None | A folder, read from disk | User-driven sync | Authoring, lint, sync, domains, profiles, and ordered layers from disk |
| [Single node](single-node) | One binary | One or more folders or remote Git repos | User-driven sync, or agent-driven on demand | Everything in local, plus discovery through MCP or the SDKs, hybrid search, registered and remote layers with visibility, and one audit log |
| [Clustered](clustered) | Replicas, Postgres, and object storage | One or more folders or remote Git repos | User-driven sync, or agent-driven on demand | Everything in single node, plus Postgres schema-per-org tenant isolation and high availability across registry replicas |

Pick the mode that matches the requirement:

- **Local** fits a team or an individual whose catalog needs no access control and no progressive discovery. The catalog can live in a Git repository that every member syncs from.
- **Single node** fits a team that wants agents to discover artifacts at runtime through MCP, or wants one audit log for every load, without running Postgres, object storage, and an identity provider.
- **Clustered** fits an organization with 20 or more users, multi-tenant requirements, a governed environment, or compliance constraints.

Artifacts are the same in every mode. The catalog on disk does not change when the deployment changes. The same shared Go library parses, composes, and materializes it everywhere, so a given target and profile produce bit-identical output. [Local](local#skills-that-declare-no-version) lists the exceptions for skills that declare no version.

## What runs in each mode

- **Local** runs the `podium` CLI against a directory. There is no server process, no database, and no identity provider.
- **Single node** runs `podium serve --standalone`, one process with SQLite, sqlite-vec, and filesystem object storage embedded.
- **Clustered** runs the standard stack: stateless registry replicas behind a load balancer, Postgres, S3-compatible object storage, and an OIDC identity provider.

`--standalone` and the standard stack are configurations of the same registry binary. They are not separate builds.

## Moving between modes

Each move keeps the artifact directory and the authoring loop unchanged.

- **Local to single node:** run `podium serve --standalone --layer-path /path/to/dir` against the same directory, then point each workspace's `defaults.registry` at the server URL.
- **Single node to clustered:** run `podium admin migrate-to-standard --postgres <dsn> --object-store <url>` to export the single-node state into the standard stack. [Migration from single node](clustered#migration-from-single-node) covers the signing key and the stored-row migration that follow.

## Pages in this section

### Deployment modes

| Page | What it covers |
|:--|:--|
| [Local](local) | Running the CLI against a directory, with no server. |
| [Single node](single-node) | Running one registry process with embedded storage, and migrating to clustered. |
| [Clustered](clustered) | Deploying replicated registries on Kubernetes with the Helm chart, Postgres, object storage, and an OIDC identity provider. |
| [Progressive adoption](progressive-adoption) | A staged path from a permissive single-node deployment to enforced governance: identity, sensitivity labels, signing, and freeze windows, in order. |

### Content and access

| Page | What it covers |
|:--|:--|
| [Connecting layer sources](layer-sources) | Hooking Git repositories, private repositories, local paths, and Agent Skills repositories into a registry, and keeping them current. |
| [Layered composition](layers) | Composing one catalog from several independent sources, with deterministic merge and explicit precedence. |
| [Access control](access-control) | Declaring who can see each layer: public, organization-wide, scoped to OIDC groups, or scoped to named users. Includes the enforcement boundary and how to debug an effective view. |
| [Gateway-delegated identity](gateway-delegated-identity) | Running the registry behind a gateway that has already authenticated the caller, with the `oidc-jwt` and `trusted-headers` identity providers. |
| [OIDC cookbooks](oidc/) | Per-IdP setup recipes for Okta, Entra ID, Google Workspace, Auth0, and Keycloak. |

### Operations and extension

| Page | What it covers |
|:--|:--|
| [Audit and observability](observability) | Audit destinations, including OTLP, plus Prometheus metrics, tracing, notifications, and alerting. |
| [Server-side integrations](integrations) | The backing services a registry uses, with what ships by default and what can replace each one. |
| [Vector backends](vector-backends) | Configuring Pinecone, Weaviate Cloud, or Qdrant Cloud as the vector backend, and switching backends on a running deployment. |
| [Operator guide](operator-guide) | Day-two operations: capacity planning, backup and restore, signing key rotation, upgrades, the security review checklist, and troubleshooting. |
| [Extending](extending) | The plugin SPIs, the forward-compatibility constraints for out-of-process plugins, and the external-extension patterns. |
