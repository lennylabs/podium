---
title: Deployment
nav_order: 4
description: The Podium deployment tiers. Local needs no server, single node runs one binary, and clustered runs replicas against Postgres and object storage. Covers the tiers, the server-side integrations, layered composition, and access control.
---

# Deployment

Podium runs in tiers. Each tier keeps everything the tier below it does and adds server-side capability.

| Tier | Server-side deployment | Catalog source | Materialization | What the tier adds |
|:--|:--|:--|:--|:--|
| [Local](local) | None | A folder, read from disk | User-driven sync | Authoring, lint, sync, domains, profiles, and ordered layers from disk |
| [Single node](single-node) | One binary | One or more folders or remote Git repos | User-driven sync, or agent-driven on demand | Everything in local, plus discovery through MCP or the SDKs, hybrid search, registered and remote layers with visibility, and one audit log |
| [Clustered](clustered) | Replicas, Postgres, and object storage | One or more folders or remote Git repos | User-driven sync, or agent-driven on demand | Everything in single node, plus multi-tenancy, SCIM group sync, signing with a transparency log, and high availability |

Artifacts are the same in every tier. The catalog on disk does not change when the deployment changes, and the same shared Go library parses, composes, and materializes it everywhere, so a given target and profile produce bit-identical output.

## What runs in each tier

- **Local** runs the `podium` CLI against a directory. There is no server process, no database, and no identity provider.
- **Single node** runs `podium serve --standalone`, one process with SQLite, sqlite-vec, and filesystem object storage embedded.
- **Clustered** runs the standard stack: stateless registry replicas behind a load balancer, Postgres, S3-compatible object storage, and an OIDC identity provider.

`--standalone` and the standard stack are configurations of the same registry binary. They are not separate builds.

## Moving between tiers

Migration is mechanical in both directions of growth.

- Local to single node: run `podium serve --standalone --layer-path /path/to/dir` against the same directory, then point each workspace's `defaults.registry` at the server URL. The directory layout and the authoring loop are unchanged.
- Single node to clustered: run `podium admin migrate-to-standard --postgres <dsn> --object-store <url>` to export the single-node state into the standard stack.

---

## Server-side capability

| Page | What it covers |
|:--|:--|
| [Server-side integrations](integrations) | The backing services a server-side tier uses: metadata store, object storage, vector index, embeddings, identity, and layer sources. Each row names what ships by default and what can replace it. |
| [Layered composition](layers) | Composing one catalog from several independent sources, with deterministic merge and explicit precedence. Registering layers, ordering them, and reading the result. |
| [Access control](access-control) | Declaring who can see each layer: public, organization-wide, scoped to OIDC groups, or scoped to named users. Includes the enforcement boundary and how to debug an effective view. |

---

## Operations and extension

| Page | What it covers |
|:--|:--|
| [Progressive adoption](progressive-adoption) | A staged on-ramp from a permissive single-node deployment to enforced governance. Adds identity, sensitivity labels, signing, and freeze windows in order. |
| [Operator guide](operator-guide) | Day-two operations for a clustered deployment: capacity planning, monitoring, alerting, backup and restore, upgrades, the security review checklist, and common pitfalls. |
| [Extending](extending) | The plugin SPIs, the forward-compatibility constraints that keep out-of-process plugins on the table, and the external-extension patterns. |
| [Vector backends](vector-backends) | Configure Pinecone, Weaviate Cloud, or Qdrant Cloud as the registry's vector backend. Covers self-embedding and storage-only modes, switching backends on a running deployment, and the local-overlay side path. |
| [Gateway-delegated identity](gateway-delegated-identity) | Run the registry behind a gateway that has already authenticated the caller, using the `oidc-jwt` and `trusted-headers` identity providers. |
| [OIDC cookbooks](oidc/) | Per-IdP setup recipes for Okta, Entra ID, Google Workspace, Auth0, and Keycloak. |
