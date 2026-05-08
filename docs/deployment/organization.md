---
layout: default
title: Organization
parent: Deployment
nav_order: 3
description: Standard deployment for larger teams and governed environments. Postgres + object storage + OIDC + replicated registry. Multi-tenancy, freeze windows, signing, hash-chained audit, SCIM.
---

# Organization

The standard deployment topology: a replicated Podium registry behind a load balancer, backed by Postgres + object storage + an OIDC IdP. The right setup for organizations with 20+ users, multi-tenant requirements, governed environments, or compliance constraints.

For day-two operations (capacity, monitoring, alerts, backup, upgrades), see [Operator guide](operator-guide). For a staged on-ramp from permissive standalone to enforced governance, see [Progressive adoption](progressive-adoption).

---

## Reference topology

- **Stateless registry replicas.** 3+ replicas behind a load balancer (HTTP).
- **Postgres.** Managed (RDS, Cloud SQL, Aurora) or self-run; primary + read replicas. Holds manifest metadata, layer config, admin grants, and audit; also holds embeddings when the default vector backend (pgvector) is in use.
- **Vector backend.** `pgvector` by default, collocated in the Postgres deployment with no separate service to run. Built-ins for `pinecone`, `weaviate-cloud`, and `qdrant-cloud` selectable per deployment.
- **Embedding provider.** `openai` by default. Built-ins also include `voyage`, `cohere`, `ollama`, and `embedded-onnx`.
- **Object storage.** S3-compatible (S3, GCS, MinIO, R2).
- **Identity provider.** OIDC IdP that supports device-code flow (Okta, Entra ID, Google Workspace, Auth0, Keycloak); SCIM push optional but recommended for group-based visibility.
- **Helm chart** ships with the registry; bare-metal deployment guide alongside.

---

## What you get

- **Multi-tenancy.** Per-tenant layer lists, admin grants, audit streams, and quotas. Tenant boundary is the org. Each org has its own Postgres schema; cross-org tables use row-level security.
- **Per-layer visibility.** `public`, `organization`, OIDC `groups`, or specific `users`. Visibility is enforced at the registry on every call. Authoring rights stay in the Git provider's branch protection.
- **Hash-chained audit.** Every read, ingest, and admin action recorded with hash-chain integrity. SIEM mirroring supported. Optional anchoring to a public transparency log.
- **Freeze windows.** Per-tenant config for blocking ingest and layer-config changes during critical periods (year-end close, release cuts). Break-glass with dual-signoff.
- **Signing.** Sigstore-keyless (preferred) or registry-managed key (fallback). Configurable per-deployment signature verification on materialization (`PODIUM_VERIFY_SIGNATURES=medium-and-above` is the typical setting).
- **SBOM / CVE tracking.** SBOM ingestion for sensitivity ≥ medium; CVE feed walks dependency graphs and surfaces affected artifacts via `podium vuln list`.
- **SCIM 2.0.** Group membership push from OIDC IdPs that support it. Layer visibility references group claims directly.
- **GDPR erasure.** `podium admin erase <user_id>` unregisters user-defined layers, redacts audit identity, returns a cryptographic receipt.
- **Quotas.** Per-org limits on storage, search QPS, materialization rate, audit volume.

---

## Per-tenant layer model

Each tenant has its own layer list. Layers are an explicit, ordered list configured per tenant; there's no fixed `org / team / user` hierarchy.

```yaml
# Tenant layer config (extract)
layers:
  - id: org-defaults
    source:
      git:
        repo: git@github.com:acme/podium-org-defaults.git
        ref: main
        root: artifacts/
    visibility:
      organization: true

  - id: team-finance
    source:
      git:
        repo: git@github.com:acme/podium-finance.git
        ref: main
    visibility:
      groups: [acme-finance, acme-finance-leads]

  - id: platform-shared
    source:
      git:
        repo: git@github.com:acme/podium-platform.git
        ref: main
    visibility:
      groups: [acme-engineering]
      users: [security-lead@acme.com]

  - id: public-marketing
    source:
      git:
        repo: git@github.com:acme/podium-public.git
        ref: main
    visibility:
      public: true
```

User-defined layers (registered at runtime by individual users) sit above admin-defined layers in precedence; the workspace local overlay sits above those. Default cap is 3 user-defined layers per identity, configurable per tenant.

---

## Setup

### 1. Provision dependencies

- Postgres 14+ with pgvector extension (or your chosen vector backend).
- Object storage bucket (S3 / GCS / MinIO / R2).
- An OIDC IdP with device-code flow support.

For a quick stand-up, the repo ships a `docker-compose.yml` that brings up Postgres + MinIO + Dex (OIDC) for evaluation. Not production-grade: single-replica services, default credentials. The compose stack mirrors the standard topology so consumers exercise the same code paths.

### 2. Deploy the registry

The Helm chart sets reasonable defaults:

```bash
helm install podium ./helm/podium \
  --set postgres.dsn=$POSTGRES_DSN \
  --set objectStore.s3.bucket=acme-podium \
  --set objectStore.s3.region=us-east-1 \
  --set identityProvider.oauth.audience=https://podium.acme.com \
  --set identityProvider.oauth.authorizationEndpoint=https://acme.okta.com/oauth2/default
```

Alternatively, run the binary directly with a `registry.yaml` config file (see [§13.12 of the spec](https://github.com/lennylabs/podium/blob/main/spec/13-deployment.md#1312-backend-configuration-reference)).

### 3. Configure the IdP

See the [OIDC cookbooks](oidc/) for per-IdP setup steps:

- [Okta](oidc/okta)
- [Entra ID](oidc/entra-id)
- [Google Workspace](oidc/google-workspace)
- [Auth0](oidc/auth0)
- [Keycloak](oidc/keycloak)

Each cookbook covers: client registration, scopes and audience, group claim mapping, optional SCIM push.

### 4. Create the first tenant and admin

```bash
podium admin tenant create acme --display-name "Acme Corp"
podium admin grant --tenant acme --user joan@acme.com --role admin
```

### 5. Configure the tenant's layer list

Edit the tenant's layer config (via API or `podium admin` commands) to register the org's layer sources and visibility.

### 6. Set up Git webhooks

For each `git`-source layer, register the webhook URL the registry returned at layer creation. The registry validates the webhook signature and ingests on each merge to the tracked ref.

### 7. Configure CI

Each layer's source repo runs `podium lint` as a required check on PRs. Use the in-repo CI tooling (GitHub Actions, GitLab CI, Buildkite, etc.); Podium runs as a CLI dependency within the existing CI framework.

---

## Identity flow

The MCP server, SDKs, and `podium sync` use the same identity providers:

- **`oauth-device-code`** for developer machines. Interactive device-code flow on first use; tokens cached in the OS keychain. Refreshes transparently. The MCP server surfaces the verification URL via MCP elicitation; the CLI prints it to stderr.
- **`injected-session-token`** for managed runtimes (Bedrock Agents, OpenAI Assistants, custom orchestrators). The runtime issues a signed JWT per session; the registry verifies the signature on every call.

For each identity, the registry composes the caller's effective view from every layer their identity is entitled to see, in precedence order. Higher-precedence layers override lower on collisions; `extends:` lets a higher-precedence artifact inherit and refine a lower one without forking.

---

## Authoring loop (per author)

1. Edit `ARTIFACT.md` (and bundled resources) in a checkout of the layer's Git repo.
2. Open a PR against the tracked ref. CI runs `podium lint` as a required check.
3. Reviewers approve per the team's branch protection rules.
4. Merge.
5. The Git provider fires a webhook to the registry. The registry fetches the new commit, walks the diff, runs lint as defense in depth, validates immutability, hashes content, stores manifest + bundled resources, indexes metadata.

For each consumer:

- Authenticated via OIDC (`podium login` once; tokens cache in the keychain).
- Consumer paths run as in [Configure your harness](../consuming/configure-your-harness).
- Effective view composes admin layers (visibility-filtered) + user-defined layers + workspace local overlay.

---

## Migration from standalone

`podium admin migrate-to-standard` exports a standalone deployment to a standard one:

```bash
podium admin migrate-to-standard --postgres <dsn> --object-store <url>
```

The artifact directory is unchanged. Layer config moves from the standalone `~/.podium/registry.yaml` to the standard tenant config; same artifacts ingest into the new metadata store. After the export, switch consumer endpoints to the new registry URL and decommission the standalone host.

For the staged rollout of governance features (identity, sensitivity labels, signing, freeze windows), follow [Progressive adoption](progressive-adoption).

---

## Operational links

- [Operator guide](operator-guide): capacity, monitoring, alerts, backup/restore, upgrades, security review.
- [Progressive adoption](progressive-adoption): staged on-ramp for governance features.
- [Extending](extending): SPI plugins, the forward-compatibility constraints, external-extension patterns.
- [OIDC cookbooks](oidc/): per-IdP setup recipes.
- The full deployment reference is in [`spec/13-deployment.md`](https://github.com/lennylabs/podium/blob/main/spec/13-deployment.md).
