---
title: Deployment
nav_order: 4
description: Choose a Podium deployment mode and find the operator task pages for layer sources, access control, audit and observability, backing services, and day-two operations.
actions:
  - label: Connecting layer sources
    href: content/layer-sources
  - label: Audit and observability
    href: operations/observability
---

# Deployment

This section is for operators who run a Podium registry. It helps choose a deployment mode, connect content to the registry, control who sees it, and operate the server once it runs.

## Common tasks

- Connect a Git repository or a local directory to a registry: [Connecting layer sources](content/layer-sources#git-repositories).
- Keep layers current with webhooks, polling, or a manual reingest: [Keeping layers current](content/layer-sources#keeping-layers-current).
- Send audit events to a file, stdout, an HTTP receiver, or an OpenTelemetry Collector: [Audit destinations](operations/observability#audit-destinations).
- Export Prometheus metrics and OTLP traces, and configure ingest-failure notifications: [Audit and observability](operations/observability).
- Restrict who sees each layer: [Access control](content/access-control).
- Set up an OIDC identity provider: [OIDC cookbooks](identity/oidc/).
- Back up, upgrade, or rotate the signing key of a running registry: [Operator guide](operations/troubleshooting).

## Pages in this section

### Deployment modes

| Page | What it covers |
|:--|:--|
| [Local](modes/local) | Running the CLI against a directory, with no server. |
| [Single node](modes/single-node) | Running one registry process with embedded storage, and migrating to clustered. |
| [Clustered](modes/clustered) | Deploying replicated registries on Kubernetes with the Helm chart, Postgres, object storage, and an OIDC identity provider. |
| [Progressive adoption](progressive-adoption) | A staged path from a permissive single-node deployment to enforced governance: identity, sensitivity labels, signing, and freeze windows, in order. |

### Content and access

| Page | What it covers |
|:--|:--|
| [Connecting layer sources](content/layer-sources) | Hooking Git repositories, private repositories, local paths, and Agent Skills repositories into a registry, and keeping them current. |
| [Layered composition](content/layers) | Composing one catalog from several independent sources, with deterministic merge and explicit precedence. |
| [Access control](content/access-control) | Declaring who can see each layer: public, organization-wide, scoped to OIDC groups, or scoped to named users. Includes the enforcement boundary and how to debug an effective view. |
| [Gateway-delegated identity](identity/trusted-headers) | Running the registry behind a gateway that has already authenticated the caller, with the `oidc-jwt` and `trusted-headers` identity providers. |
| [OIDC cookbooks](identity/oidc/) | Per-IdP setup recipes for Okta, Entra ID, Google Workspace, Auth0, and Keycloak. |

### Operations and extension

| Page | What it covers |
|:--|:--|
| [Audit and observability](operations/observability) | Audit destinations, including OTLP, plus Prometheus metrics, tracing, notifications, and alerting. |
| [Server-side integrations](services/) | The backing services a registry uses, with what ships by default and what can replace each one. |
| [Vector backends](services/vector-backends) | Configuring Pinecone, Weaviate Cloud, or Qdrant Cloud as the vector backend, and switching backends on a running deployment. |
| [Operator guide](operations/troubleshooting) | Day-two operations: capacity planning, backup and restore, signing key rotation, upgrades, the security review checklist, and troubleshooting. |
| [Extending](services/extending) | The plugin SPIs, the forward-compatibility constraints for out-of-process plugins, and the external-extension patterns. |
