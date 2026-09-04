---
title: Clustered
nav_order: 3
description: Replicated registry behind a load balancer, backed by Postgres, object storage, and an OIDC IdP. Adds multi-tenancy, freeze windows, signing, hash-chained audit, and SCIM.
---

# Clustered

The clustered tier runs the standard stack: replicated Podium registry replicas behind a load balancer, backed by Postgres, object storage, and an OIDC IdP. It fits organizations with 20 or more users, multi-tenant requirements, governed environments, or compliance constraints.

For day-two operations covering capacity, monitoring, alerts, backup, and upgrades, see [Operator guide](operator-guide). For a staged on-ramp from a permissive deployment to enforced governance, see [Progressive adoption](progressive-adoption).

---

## Reference topology

- **Stateless registry replicas.** 3+ replicas behind a load balancer (HTTP).
- **Postgres.** Managed (RDS, Cloud SQL, or Aurora) or self-run, with a primary and read replicas. It holds manifest metadata, dependency edges, layer config, and admin grants. It also holds embeddings when the default vector backend (pgvector) is in use. The audit stream has its own sink: each replica appends a hash-chained file at `~/.podium/audit.log` unless `PODIUM_AUDIT_LOG_PATH` moves it. Set that variable to an `http(s)` URL so every replica ships one stream to a SIEM instead of writing a file inside its own pod.
- **Vector backend.** `pgvector` by default, collocated in the Postgres deployment with no separate service to run. Built-ins for `pinecone`, `weaviate-cloud`, and `qdrant-cloud` are selectable per deployment. See [Vector backends](vector-backends) for the per-backend recipes.
- **Embedding provider.** `openai` by default. Built-ins also include `voyage`, `cohere`, and `ollama`.
- **Object storage.** S3-compatible: S3, GCS, MinIO, or R2.
- **Identity provider.** An OIDC IdP that supports the device-code flow (Okta, Entra ID, Google Workspace, Auth0, or Keycloak). SCIM push is optional and recommended for group-based visibility.
- **Helm chart** at `deploy/helm/podium` in the repository, alongside the failure-scenario runbook at `deploy/runbook.md`.

[Server-side integrations](integrations) lists each backing service with what ships by default and what can replace it.

---

## What the tier adds over single node

- **Multi-tenancy.** Per-tenant layer lists, admin grants, audit streams, and quotas. The tenant boundary is the org. Each org has its own Postgres schema, and cross-org tables use row-level security.
- **Visibility evaluated against a verified identity.** A clustered deployment sets the registry's own identity provider to `oidc-jwt` or `trusted-headers`, so `public`, `organization`, OIDC `groups`, and `users` are evaluated against a verified caller on every call. A single-node deployment runs the same evaluator once it configures one of those providers. Authoring rights stay in the Git provider's branch protection. That scope statement is about writing content into a source the registry already reads; which caller may declare a layer that makes the registry read a given filesystem path is authorized to a tenant admin, because the registry process reads that path with its own rights rather than with the registrant's. See [Access control](access-control) and [Layers](layers#who-may-register-a-local-source-layer).
- **Audit across replicas.** Every read, ingest, and admin action carries the same hash-chain integrity a single-node deployment writes, and each replica maintains its own chain, which is why the topology above centralizes the stream on one SIEM endpoint. Anchoring a chain head to a public transparency log applies to a replica that keeps the on-disk sink, because the anchor and verify passes walk the file.
- **Freeze windows.** A `freeze_windows:` list under `registry:` in `registry.yaml` rejects ingest with `ingest.frozen` during critical periods such as year-end close and release cuts. A single-node deployment reads the same list. `podium layer reingest --break-glass --justification <text> --approver <approver-id> <layer-id>` overrides an active window. The override needs a justification and two distinct approvers, and the authenticated caller counts as one of them.
- **Signing.** Sigstore-keyless (preferred) or a registry-managed key (fallback). Signature verification on materialization is configurable per deployment, and `PODIUM_VERIFY_SIGNATURES=medium-and-above` is the typical setting.
- **SCIM 2.0.** Group membership push from OIDC IdPs that support it. Layer visibility references group claims directly.
- **GDPR erasure.** `podium admin erase --salt <tenant-salt> <user-id>` unregisters the user's user-defined layers, redacts their identity across the registry audit stream behind a `redacted-<sha256(user_id+salt)>` tombstone, and returns the purged layer ids plus the count of redacted audit events.
- **Quotas.** Per-org limits on storage, search QPS, materialization rate, and audit volume.

---

## Per-tenant layer model

Each tenant has its own layer list. Layers are an explicit ordered list configured per tenant, with no fixed `org / team / user` hierarchy. [Layered composition](layers) covers the composition rules that apply in every tier.

```yaml
# Tenant layer config, the `layers:` list alone. This is not a registry.yaml
# document; a registry.yaml nests every server-side key under `registry:`.
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

- Postgres 14+ with the pgvector extension, or the vector backend you chose instead.
- An object storage bucket on S3, GCS, MinIO, or R2.
- An OIDC IdP with device-code flow support.

For a quick stand-up, the repo ships a `docker-compose.yml` that brings up the evaluation stack with `docker compose up -d`: the registry, a pgvector Postgres, MinIO object storage, a Dex OIDC IdP, and a one-shot bootstrap container that creates the MinIO bucket. The stack sets `PODIUM_NO_EMBEDDINGS`, so search runs over manifest text with no embedding provider and no API key. Remove that variable and supply a provider with its key to exercise hybrid search. The registry seeds the first tenant and admin grant itself at boot, from the default tenant plus the identity in `PODIUM_BOOTSTRAP_ADMINS`. The registry service selects no identity provider, so it authenticates no caller and the seeded grant is unreachable until an operator configures one; the service publishes its port on the host loopback interface for that reason. The stack runs single-replica services with default credentials on local volumes, so it is unsuitable for production. It wires the same components a clustered deployment wires, so consumers exercise the same code paths.

### 2. Deploy the registry

The chart lives at `deploy/helm/podium`. Its templates render the backend selectors from the `config.*.type` values and read every credential and per-backend setting from the Kubernetes secret named by `existingSecret`:

```bash
kubectl create secret generic podium-secrets \
  --from-literal=PODIUM_BIND=0.0.0.0:8080 \
  --from-literal=PODIUM_POSTGRES_DSN="$POSTGRES_DSN" \
  --from-literal=PODIUM_S3_BUCKET=acme-podium \
  --from-literal=PODIUM_S3_REGION=us-east-1 \
  --from-literal=PODIUM_OAUTH_ISSUER=https://acme.okta.com/oauth2/default \
  --from-literal=PODIUM_OAUTH_AUDIENCE=https://podium.acme.com \
  --from-literal=OPENAI_API_KEY="$OPENAI_API_KEY"

helm install podium ./deploy/helm/podium \
  --set config.store.type=postgres \
  --set config.objectStore.type=s3 \
  --set config.vectorBackend.type=pgvector \
  --set config.identityProvider.type=oidc-jwt \
  --set existingSecret=podium-secrets
```

The chart installs with its defaults. `config.identityProvider.type` is `oidc-jwt`, which the registry verifies at request time; supply its issuer and audience through the secret named in `existingSecret`, which reaches the pod via `envFrom`. Hybrid search is off by default (`config.vectorBackend.type` and `config.embeddingProvider.type` are both `none`) so the registry starts without an embedding-provider credential; set both and supply the key in the same secret to turn it on. The container `env:` block takes precedence over `envFrom:`, so any value the chart renders as `env:` is set through `--set` rather than through the secret.

The templates set `PODIUM_BIND`, `PODIUM_REGISTRY_STORE`, `PODIUM_OBJECT_STORE`, `PODIUM_VECTOR_BACKEND`, `PODIUM_EMBEDDING_PROVIDER`, and `PODIUM_IDENTITY_PROVIDER` on every install, from `config.bind` and those `type` values. Because they always render, blanking one in `values.yaml` emits an empty value that shadows the secret rather than deferring to it. Leave them at their defaults or set them with `--set`.

Most other `config` keys render only where `values.yaml` carries a value, which covers the OIDC issuer, audience, groups claim, and group mapping, the bootstrap admins, and the default layer visibility. A key left blank renders nothing, so the secret supplies it through `envFrom`, and setting it in `values.yaml` overrides whatever the secret carries for the same name. The object-store keys carry a second condition: the S3 bucket, region, endpoint, and path-style flag render only when `config.objectStore.type` is `s3`, and the filesystem root only when it is `filesystem`. `config.publicMode` and `config.allowPublicBind` render only when set to true.

Three `config` keys are rendered by no template. For `config.store.dsn`, supply the connection string as `PODIUM_POSTGRES_DSN` in the secret, or set `postgresql.enabled` to run the bundled evaluation database, which derives the DSN from the service it creates and the password in `postgresql.existingSecret`. Enabling the bundled database resolves the DSN on the container, so it takes precedence over one the secret carries. The other two are `config.endpoint` and `config.embeddingProvider.model`, and setting either has no effect.

Alternatively, run the binary directly with a `registry.yaml` config file (see [CLI reference](../reference/cli) for the environment variables a registry.yaml maps to).

### 3. Configure the IdP

See the [OIDC cookbooks](oidc/) for per-IdP setup steps:

- [Okta](oidc/okta)
- [Entra ID](oidc/entra-id)
- [Google Workspace](oidc/google-workspace)
- [Auth0](oidc/auth0)
- [Keycloak](oidc/keycloak)

Each cookbook covers: client registration, scopes and audience, group claim mapping, optional SCIM push.

### 4. Create the first tenant and admin

Run the registry in multi-tenant mode with `PODIUM_MULTI_TENANT=true`, and seed the first instance operator with `PODIUM_OPERATOR_ADMINS` (comma-separated identities). The operator role authorizes tenant management; it is distinct from the per-tenant `admin` role and from `PODIUM_BOOTSTRAP_ADMINS`. With the registry running, the operator provisions a tenant at runtime and grants the first per-tenant admin:

```bash
podium admin tenant create acme --registry https://podium.acme.com
podium admin grant --registry https://podium.acme.com alice@acme.com
```

`podium admin tenant create` derives the org ID from the name and is idempotent. Use `podium admin tenant list`, `podium admin tenant update <id>`, and `podium admin tenant deactivate <id>` to list, adjust, and deactivate tenants. See the [CLI reference](../reference/cli#podium-admin-tenant) for the full flag set.

### 5. Configure the tenant's layer list

Register the org's layer sources and their visibility with `podium layer register`, or `POST /v1/layers` directly. `podium layer update` patches a registered layer afterwards. [Layered composition](layers#registering-layers-against-a-server) has the flags.

### 6. Set up Git webhooks

For each `git`-source layer, register the webhook URL the registry returned at layer creation. The registry validates the webhook signature and ingests on each merge to the tracked ref.

### 7. Configure CI

Each layer's source repo runs `podium lint` as a required check on PRs. Use the in-repo CI tooling (GitHub Actions, GitLab CI, Buildkite, etc.); Podium runs as a CLI dependency within the existing CI framework.

---

## Identity flow

The MCP server, SDKs, and `podium sync` use the same identity providers:

- **`oauth-device-code`** for developer machines. Interactive device-code flow on first use; tokens cached in the OS keychain. Refreshes transparently. The MCP server surfaces the verification URL via MCP elicitation; the CLI prints it to stderr.
- **`injected-session-token`** for managed runtimes (Bedrock Agents, OpenAI Assistants, custom orchestrators). The runtime issues a signed JWT per session; the registry verifies the signature on every call.

The registry process reads its own `PODIUM_IDENTITY_PROVIDER` separately. Set it to `oidc-jwt` to verify the IdP-issued tokens developer machines present, or to `trusted-headers` when a gateway authenticates callers and forwards identity headers. Setting the registry's provider to `oauth-device-code` stops startup with `config.identity_provider_unverified`, because that value names the acquisition flow a consumer completes. See [Gateway-delegated identity](gateway-delegated-identity) and the [OIDC cookbooks](oidc/).

Setting the registry's provider to `injected-session-token` also requires `PODIUM_RUNTIME_KEYS_PATH`, which names the JSON file holding the trusted runtime signing keys. Write a record into it with `podium admin runtime register --keys-file`, ship the same file to every replica, and restart the process to pick a new key up. A registry that cannot read or parse the file aborts startup with `config.runtime_keys_unavailable`, as does an `injected-session-token` registry whose key set is empty.

For each identity, the registry composes the caller's effective view from every layer their identity is entitled to see, in precedence order. When two layers hold the same artifact ID, ingest rejects the second contribution with `ingest.collision` unless the higher-precedence artifact declares `extends:` against that ID, which lets it inherit and refine the lower one without forking.

---

## Authoring loop (per author)

1. Edit `ARTIFACT.md` (plus `SKILL.md` for skills, plus bundled resources) in a checkout of the layer's Git repo.
2. Open a PR against the tracked ref. CI runs `podium lint` as a required check.
3. Reviewers approve per the team's branch protection rules.
4. Merge.
5. The Git provider fires a webhook to the registry. The registry fetches the new commit, walks the diff, runs lint as defense in depth, validates immutability, hashes content, stores manifest + bundled resources, indexes metadata.

For each consumer:

- Authenticated via OIDC (`podium login` once; tokens cache in the keychain).
- Consumer paths run as in [Configure your harness](../consuming/configure-your-harness).
- Effective view composes admin layers (visibility-filtered) + user-defined layers + workspace local overlay.

---

## Migration from single node

`podium admin migrate-to-standard` exports a [single-node](single-node) deployment into the standard stack:

```bash
podium admin migrate-to-standard --postgres <dsn> --object-store <url>
```

The artifact directory is unchanged. Layer config moves from `~/.podium/registry.yaml` to the tenant config, and the same artifacts ingest into the new metadata store. After the export, switch consumer endpoints to the new registry URL and decommission the old host.

For the staged rollout of governance features covering identity, sensitivity labels, signing, and freeze windows, follow [Progressive adoption](progressive-adoption).

---

## Operational links

- [Server-side integrations](integrations): the backing services and their alternatives.
- [Layered composition](layers): composing the catalog from several sources.
- [Access control](access-control): declaring and debugging who can see what.
- [Operator guide](operator-guide): capacity, monitoring, alerts, backup and restore, upgrades, and security review.
- [Progressive adoption](progressive-adoption): staged on-ramp for governance features.
- [Extending](extending): SPI plugins, the forward-compatibility constraints, and external-extension patterns.
- [OIDC cookbooks](oidc/): per-IdP setup recipes.
