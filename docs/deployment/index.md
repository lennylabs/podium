---
title: Deployment
nav_order: 4
description: Choose a Podium deployment mode, then connect content, configure identity, and operate the registry with the task pages in this section.
actions:
  - label: Choose a deployment mode
    href: modes/
  - label: Connecting layer sources
    href: content/layer-sources
---

# Deployment

This section is for operators who run Podium. It covers choosing and installing a deployment mode, configuring how the registry identifies callers, connecting the content the registry serves, operating the registry once it runs, and the backing services it depends on. [Deployment modes](modes/) compares the local, single-node, and clustered modes and is the starting point for a new deployment.

## Common tasks

- Choose a deployment mode: [Deployment modes](modes/).
- Run a registry on one machine: [Single node](modes/single-node).
- Deploy replicated registries on Kubernetes: [Clustered](modes/clustered).
- Move a catalog from one mode to another: [Moving between modes](modes/migrating).
- Connect a Git repository or a local directory, and keep each layer current: [Connecting layer sources](content/layer-sources).
- Restrict who sees each layer: [Access control](content/access-control).
- Set up an OIDC identity provider: [OIDC](identity/oidc/).
- Create tenants and grant admins: [Tenants and admins](identity/tenants).
- Send audit records to a file, standard output, an HTTP receiver, or an OpenTelemetry Collector: [Audit and observability](operations/observability).
- Generate, back up, or rotate the signing key: [Signing keys](operations/signing-keys).
- Back up a registry and test a restore: [Backup and restore](operations/backup-restore).
- Upgrade a registry: [Upgrading](operations/upgrading) and [Upgrade notes](operations/upgrade-notes).
- Diagnose a startup refusal or an error code: [Troubleshooting](operations/troubleshooting).

## Pages in this section

### Deployment modes

| Page | What it covers |
|:--|:--|
| [Deployment modes](modes/) | What runs in each mode and how to choose between them. |
| [Local](modes/local) | Running the CLI against a directory of artifacts, with no server. |
| [Single node](modes/single-node) | Running one registry process with embedded storage. |
| [Clustered](modes/clustered) | Deploying replicated registries on Kubernetes with Postgres, object storage, and an OIDC identity provider. |
| [Helm chart reference](modes/helm-chart) | The chart's values, how they reach the registry pod, and the startup refusals they produce. |
| [Docker Compose evaluation](modes/docker-compose) | Running the clustered components on one machine for evaluation. |
| [Moving between modes](modes/migrating) | Moving a catalog from local to single node or clustered, and from single node to clustered. |

### Identity

| Page | What it covers |
|:--|:--|
| [Identity](identity/) | The identity providers, the consumer and registry sides, the default layer visibility, the web UI, and the startup guards. |
| [OIDC](identity/oidc/) | The `oidc-jwt` settings and per-provider recipes for Okta, Entra ID, Google Workspace, Auth0, and Keycloak. |
| [Gateway (trusted headers)](identity/trusted-headers) | Running the registry behind a gateway that authenticates the caller. |
| [Tenants and admins](identity/tenants) | Single-tenant and multi-tenant registries, the operator and admin roles, and provisioning tenants. |
| [Groups and SCIM](identity/groups) | Resolving group membership from OIDC group claims or a SCIM 2.0 push. |

### Catalog content

| Page | What it covers |
|:--|:--|
| [Catalog content](content/) | An overview of the content pages. |
| [Connecting layer sources](content/layer-sources) | Registering Git repositories, private remotes, local paths, and Agent Skills repositories as layers, and keeping each layer current. |
| [Layered composition](content/layers) | How the registry merges layers in an explicit precedence order. |
| [Access control](content/access-control) | Declaring who can read and change each layer, and debugging a caller's effective view. |
| [Publishing marketplace repositories](content/publishing) | Rendering the catalog into harness marketplace repositories. |

### Operations

| Page | What it covers |
|:--|:--|
| [Operations](operations/) | An overview of the day-two operations pages. |
| [Audit and observability](operations/observability) | Audit destinations, Prometheus metrics, tracing, notifications, and alerting. |
| [Signing keys](operations/signing-keys) | Generating, distributing, backing up, and rotating the registry signing key, and the `sign-stored-rows` command. |
| [Backup and restore](operations/backup-restore) | What to back up in each mode and how to test a restore. |
| [Upgrading](operations/upgrading) | The upgrade procedure, mixed-version replicas, and rollback. |
| [Upgrade notes](operations/upgrade-notes) | Release-specific upgrade steps. |
| [Capacity and availability](operations/capacity) | Sizing a deployment and read-only mode. |
| [Security review checklist](operations/security-review) | The checks to run before a security review. |
| [Outbound webhooks](operations/outbound-webhooks) | Sending registry events to webhook receivers. |
| [Troubleshooting](operations/troubleshooting) | Symptoms and error codes, with the page that resolves each. |

### Backing services

| Page | What it covers |
|:--|:--|
| [Backing services](services/) | The metadata store, object storage, vector index, embedding provider, and the other services a registry uses, with what can replace each one. |
| [Vector backends](services/vector-backends) | Configuring Pinecone, Weaviate Cloud, or Qdrant Cloud, and switching backends on a running deployment. |
| [Extending](services/extending) | The plugin SPIs and the patterns for tooling built on the HTTP API, SDKs, CLI, and webhooks. |

### Progressive adoption

| Page | What it covers |
|:--|:--|
| [Progressive adoption](progressive-adoption) | A staged path from a permissive single-node deployment to enforced governance: identity, sensitivity labels, signing, and freeze windows, in that order. |
