---
title: Clustered
nav_order: 3
description: Deploy replicated Podium registries on Kubernetes behind a load balancer, backed by Postgres, object storage, and an OIDC identity provider, with per-org tenant isolation and high availability.
---

# Clustered

The clustered deployment mode runs the standard stack: Podium registry replicas behind a load balancer, backed by Postgres, object storage, and an OIDC identity provider. It fits organizations with 20 or more users, multi-tenant requirements, governed environments, or compliance constraints. This page covers the reference topology, what the mode adds over [single node](single-node), and the setup procedure with the Helm chart.

[Helm chart reference](helm-chart) documents the chart's values. To try the stack on one machine first, see [Docker Compose evaluation](docker-compose). To move an existing single-node deployment here, see [Moving between modes](migrating). For a staged on-ramp from a permissive deployment to enforced governance, see [Progressive adoption](../progressive-adoption).

---

## Reference topology

- **Stateless registry replicas.** Three or more replicas behind an HTTP load balancer.
- **Postgres.** Managed (RDS, Cloud SQL, or Aurora) or self-run, with a primary and read replicas. It holds manifest metadata, dependency edges, layer config, and admin grants. It also holds embeddings when the default vector backend, pgvector, is in use.
- **Object storage.** S3-compatible: S3, GCS, MinIO, or R2.
- **Vector backend.** `pgvector` by default, collocated in the Postgres deployment with no separate service to run. The built-in `pinecone`, `weaviate-cloud`, and `qdrant-cloud` backends are selectable per deployment. [Vector backends](../services/vector-backends) has the per-backend recipes.
- **Embedding provider.** `openai` by default. The built-ins also include `voyage`, `cohere`, and `ollama`.
- **Identity provider.** An OIDC IdP that supports the device-code flow, such as Okta, Entra ID, Google Workspace, Auth0, or Keycloak. SCIM push is optional and recommended for group-based visibility.
- **Audit destinations.** Each replica writes its audit records to the destinations that `PODIUM_AUDIT_LOG_PATH` lists, and the Helm chart sets `stdout` by default. [Audit across replicas](#audit-across-replicas) describes the options.
- **Helm chart** at `deploy/helm/podium` in the repository, alongside the failure-scenario runbook at `deploy/runbook.md`.

[Backing services](../services/) lists each backing service with what ships by default and what can replace it.

---

## What clustered adds over single node

A clustered deployment runs several registry replicas against one Postgres store and one object store, so the registry stays available when a replica fails. On Postgres, each org has its own schema, and cross-org tables use row-level security. A single node runs one process, and its SQLite store scopes each row by its tenant without schema-per-org isolation.

The governance features below run on a clustered deployment. Each of them also runs on a single-node deployment once it is configured for it.

| Feature | What it provides |
|:--|:--|
| [Multi-tenancy and quotas](#multi-tenancy-and-quotas) | Per-tenant layer lists, admin grants, audit streams, and quotas. |
| [Visibility against a verified identity](#visibility-against-a-verified-identity) | Layer visibility evaluated against a verified caller on every call. |
| [Audit across replicas](#audit-across-replicas) | A hash-chained audit stream from every replica, delivered to the destinations each replica lists. |
| [Freeze windows](#freeze-windows) | Ingest rejected during declared critical periods, with a two-approver override. |
| [Signing](#signing) | Every replica signs at ingest under one key mounted from one Secret. |
| [SCIM 2.0](#scim-20) | Group membership pushed from the IdP for layer visibility to reference. |
| [GDPR erasure](#gdpr-erasure) | One command that unregisters a user's layers and redacts the user's identity in the audit records. |

### Multi-tenancy and quotas

The tenant boundary is the org. Each tenant has its own layer list, admin grants, audit stream, and quotas. A registry started with `PODIUM_MULTI_TENANT=true` and an identity provider the registry verifies routes each request to the tenant its organization names. Without `PODIUM_MULTI_TENANT`, the registry serves one tenant, the `default` org. [Tenants and admins](../identity/tenants) covers both modes and provisioning.

Quotas are per-tenant limits on storage, search QPS, materialization rate, and audit volume. Search QPS, materialization rate, and audit volume are charged to the tenant each request resolves to. Requests that resolve to no tenant share one set of budgets at the deployment defaults.

### Visibility against a verified identity

A clustered deployment sets the registry's own identity provider to `oidc-jwt` or `trusted-headers`. The registry then evaluates `public`, `organization`, OIDC `groups`, and `users` against a verified caller on every call.

Authoring rights stay in the Git provider's branch protection. That scope covers writing content into a source the registry already reads. Declaring a layer that makes the registry read a given filesystem path is authorized to a tenant admin, because the registry process reads that path with its own rights rather than with the registrant's. See [Access control](../content/access-control) and [Connecting layer sources](../content/layer-sources).

### Audit across replicas

Each replica chains its own audit records and delivers them to the destinations its list names, so a receiver that collects every replica's stream holds one chain per replica process.

The chart sets `config.auditLogPath`, which renders `PODIUM_AUDIT_LOG_PATH`, to `stdout` by default. Each pod then writes its audit records to the container log, and the cluster's log pipeline holds them under the platform's retention and outside the reach of erasure. A file path on a writable volume, an `http(s)` URL, or an `otlp+http(s)` URL replaces the default or joins it in a comma-separated list. [Audit destinations](../operations/observability#audit-destinations) covers each destination, including why a shipper should read a stdout, HTTP, or OTLP destination rather than tail the file.

Chain verification depends on whether the list includes a file destination:

- With the chart's `stdout` default, or any list without a file destination, the registry runs no local chain-head anchoring and no scheduled verification. The receiving system applies the `prev_hash` check to the shipped stream, as [Receiver checks](../../reference/audit-events#receiver-checks) describes.
- Local chain-head anchoring (`PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS`) runs only on a replica whose list includes a file destination, because the anchor and verify passes walk the file.

Each registry audit record carries a `tenant` attribute naming the org whose audit stream it belongs to, and a SIEM separates the per-tenant streams by filtering on it. The following records carry no tenant: records that describe the registry as a whole, operator `tenant.managed` records, records of requests that resolve to no provisioned tenant, and records written before the attribute existed.

### Freeze windows

A `freeze_windows:` list under `registry:` in `registry.yaml` rejects ingest with `ingest.frozen` during critical periods, such as year-end close and release cuts. The list has no environment variable, so the registry reads it only from a `registry.yaml` file, and the Helm chart has no value that mounts one.

`podium layer reingest --break-glass --justification <text> --approver <approver-id> --approver <approver-id> <layer-id>` overrides an active window. The override needs a justification and two distinct approvers, each named by its own `--approver` flag. The caller who runs the command does not count as an approver.

### Signing

The registry signs every artifact at ingest with its registry-managed Ed25519 key by default, and every replica signs under the one key the chart mounts from one Secret, as [Deploy the registry](#2-deploy-the-registry) shows. [Signing keys](../operations/signing-keys) covers rotation across replicas and recovery.

### SCIM 2.0

An IdP that supports SCIM pushes group membership to the registry for layer visibility to reference. [Groups and SCIM](../identity/groups) covers the receiver and its configuration.

### GDPR erasure

`podium admin erase --salt <tenant-salt> <user-id>` performs the erasure. It unregisters the user's user-defined layers and redacts the user's identity in the audit records of the tenant the request acts in, behind a `redacted-<sha256(user_id+salt)>` tombstone. It returns the purged layer ids plus the count of redacted audit events.

What the erase rewrites depends on the destination list of the replica that serves the request:

- When that replica's list names a file destination, the erase rewrites that file.
- With a list that names no file destination, such as the chart's `stdout` default, the erase rewrites no record. The receiving system redacts the records already delivered to it.

In both cases, every destination of that replica's stream receives `user.erased`.

On a registry started with `PODIUM_MULTI_TENANT=true`, the erase acts in the tenant the caller's organization selects and redacts only the audit records labeled with that tenant. Records that carry no tenant, such as those written before the `tenant` attribute existed, are left unchanged, and the response reports nothing about them. Operator `tenant.managed` records also carry no tenant. When the replica's list names a file destination, `podium admin erase --local --audit-path` redacts the records that carry no tenant in that file while the replica is stopped. With a list that names no file destination, the receiving system owns their redaction.

---

## Setup

The procedure below deploys the registry on Kubernetes with the Helm chart.

### Before you start

- A Kubernetes cluster, with `kubectl` and `helm` configured against it.
- A checkout of the Podium repository, which holds the chart at `deploy/helm/podium`.
- The `podium` CLI, which generates the signing key and administers the registry.

> [!IMPORTANT]
> A store that a v0.4.0 registry wrote needs the stored-row migration before any pod of this release serves it. Follow [Upgrading the chart from v0.4.0](../operations/upgrade-notes#upgrading-the-chart-from-v040) instead of step 2 for such a store.

### 1. Provision dependencies and the IdP

Provision the backing services:

- Postgres with the pgvector extension, or the vector backend chosen instead.
- An object storage bucket on S3, GCS, MinIO, or R2.
- An OIDC IdP with device-code flow support.

Register the registry with the IdP by following the cookbook for the provider: [Okta](../identity/oidc/okta), [Entra ID](../identity/oidc/entra-id), [Google Workspace](../identity/oidc/google-workspace), [Auth0](../identity/oidc/auth0), or [Keycloak](../identity/oidc/keycloak). Each cookbook covers client registration, scopes and audience, group claim mapping, and optional SCIM push, and it produces the issuer and audience that step 2 puts in the secret.

The cookbooks write the registry's settings as `registry.yaml` keys. The chart configures the registry through environment variables instead, so put each setting in the secret under its variable: `identity_provider.issuer` is `PODIUM_OAUTH_ISSUER`, and `identity_provider.audience` is `PODIUM_OAUTH_AUDIENCE`. [Configuration](../../reference/configuration) lists the variable for each key. An environment variable takes precedence over the same key in a `registry.yaml`.

### 2. Deploy the registry

**Choose the tenancy mode.** A registry started without `PODIUM_MULTI_TENANT` is single-tenant: it serves the `default` org and resolves every authenticated caller to it. A multi-tenant registry routes each request to the tenant its organization names, and an instance operator provisions each tenant at runtime. [Tenants and admins](../identity/tenants) compares the modes. The choice sets which admin variables the install carries:

| Mode | Variables |
|:--|:--|
| Single-tenant | `PODIUM_BOOTSTRAP_ADMINS`, the identities granted the admin role in the `default` tenant at each start. Set it with `--set config.bootstrapAdmins=alice@acme.com` or in the secret. |
| Multi-tenant | `PODIUM_MULTI_TENANT=true`, and `PODIUM_OPERATOR_ADMINS`, the identities granted the instance-operator role at each start. The chart has no value for either, so put both in the secret. [Multi-tenant startup refusals](helm-chart#multi-tenant-startup-refusals) lists the configurations a multi-tenant registry refuses. |

Each identity is the subject the IdP's tokens carry, the `sub` claim unless `PODIUM_OAUTH_SUBJECT_CLAIM` names another claim.

**Generate the signing key.** The chart mounts one operator-supplied signing key from a Secret, so every replica signs under the same key. Write the key file in a scratch directory:

```bash
KEY_DIR="$(mktemp -d)"
podium admin signing-key generate --key-file "$KEY_DIR/registry-signing.key"
```

`podium admin signing-key generate` writes only the file `--key-file` names, with mode `0600`, and refuses to replace an existing file. Its first line is the registry's verification key set. Copy the key file into the deployment's backup, and delete `$KEY_DIR` after the Secret below is created. [Signing keys](../operations/signing-keys) covers why the key is shared, rotation across replicas, and recovery from a lost key.

**Extract the public key.** Every consumer of the deployment verifies under the public half:

```bash
awk '/^public:/{print $2}' "$KEY_DIR/registry-signing.key"
```

The output is the base64 text after the `public:` prefix, which is the form `PODIUM_SIGNATURE_VERIFY_KEY` requires, and the same key as the first line `generate` printed. Every consumer sets it in `PODIUM_SIGNATURE_VERIFY_KEY` (see [Configure your harness](../../consuming/configure-your-harness)).

**Create the Secrets and install the chart:**

```bash
kubectl create secret generic podium-signing-key \
  --from-file=registry-signing.key="$KEY_DIR/registry-signing.key"

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
  --set existingSecret=podium-secrets \
  --set signing.secretName=podium-signing-key
```

The chart renders the backend selectors from the `config.*.type` values and reads every credential and per-backend setting from the secret that `existingSecret` names, which reaches the pod through `envFrom`. Add the following entries to the secret as further `--from-literal` arguments where they apply:

| Variable | When to set it |
|:--|:--|
| `PODIUM_PUBLIC_URL` | Always. It is the registry's external URL from step 3, such as `https://podium.acme.com`. |
| `PODIUM_S3_ACCESS_KEY_ID` and `PODIUM_S3_SECRET_ACCESS_KEY` | When the pods have no cloud credentials. With both unset, the registry reads the AWS credential chain: the environment, the shared credentials file, then the instance role. |
| `PODIUM_MULTI_TENANT` and `PODIUM_OPERATOR_ADMINS` | On a multi-tenant registry. |
| `PODIUM_BOOTSTRAP_ADMINS` | On a single-tenant registry, unless `config.bootstrapAdmins` carries the value. |

Hybrid search is off by default, because `config.vectorBackend.type` and `config.embeddingProvider.type` both default to `none`. The command above selects `pgvector`. To turn hybrid search on, also set `config.embeddingProvider.type`, for example to `openai`, which reads the `OPENAI_API_KEY` the secret carries.

The chart runs `ghcr.io/lennylabs/podium-server` at the chart's `appVersion`. [Helm chart reference](helm-chart) covers the image values, the signing values, which values reach the pod from `--set` and which from the secret, and the audit destination values.

### 3. Expose the registry

The chart's Service is `ClusterIP`, so the registry is reachable only inside the cluster, and the registry listens on plain HTTP. Expose it through an Ingress that terminates TLS. Write the Ingress values to a file:

```yaml
# podium-ingress.yaml
ingress:
  enabled: true
  className: nginx
  host: podium.acme.com
  tls:
    - hosts: [podium.acme.com]
      secretName: podium-tls
```

`podium-tls` is a `kubernetes.io/tls` Secret that holds the certificate for the host, created with `kubectl create secret tls` or issued by cert-manager through `ingress.annotations`. Apply the values to the release:

```bash
helm upgrade podium ./deploy/helm/podium --reuse-values -f podium-ingress.yaml
```

Point DNS for the host at the ingress controller, and confirm that `PODIUM_PUBLIC_URL` in the secret is the `https://` URL. [Exposing the registry](helm-chart#exposing-the-registry) describes the Ingress values and the `LoadBalancer` alternative.

### 4. Grant the first admin

The `podium admin` commands call the registry's admin API with the caller's token, so the caller runs `podium login` against the registry first. `podium login` completes the device-code flow against the IdP. The registry does not publish the IdP's endpoints, so set `PODIUM_OAUTH_CLIENT_ID`, `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT`, and `PODIUM_OAUTH_TOKEN_URL` to the values the cookbook gives, then run `podium login --registry https://podium.acme.com`. The cookbook for each provider shows the full command.

On a single-tenant registry, every identity in `PODIUM_BOOTSTRAP_ADMINS` holds the admin role once the registry starts. An admin grants the role to further identities:

```bash
podium admin grant --registry https://podium.acme.com bob@acme.com
```

On a multi-tenant registry, an identity in `PODIUM_OPERATOR_ADMINS` provisions each tenant at runtime:

```bash
podium admin tenant create acme --registry https://podium.acme.com
```

`podium admin tenant create` derives the org ID from the name and is idempotent. [Tenants and admins](../identity/tenants#provisioning-tenants) lists the commands that list, update, and deactivate tenants.

The operator role confers no admin rights in a tenant. `podium admin grant` grants the admin role in the tenant the caller's organization selects, and only a caller that already holds the admin role there may run it. `PODIUM_BOOTSTRAP_ADMINS` seeds grants in the `default` tenant only, so neither variable grants the first admin of a tenant created at runtime, and the registry offers no other path that does. [Tenants and admins](../identity/tenants) describes this limit.

### 5. Register the tenant's layers

Each tenant has its own ordered list of admin-defined layers, with no fixed `org / team / user` hierarchy. On a multi-tenant registry, the layer endpoints act in the tenant the caller's organization selects, and a tenant's admin manages that tenant's list. Register each layer source with `podium layer register`, or with `POST /v1/layers` directly. Each registration appends the layer above the layers registered before it, so register the lowest-precedence layer first:

```bash
podium layer register --registry https://podium.acme.com --id org-defaults \
  --repo git@github.com:acme/podium-org-defaults.git --ref main --root artifacts/ \
  --organization
podium layer register --registry https://podium.acme.com --id team-finance \
  --repo git@github.com:acme/podium-finance.git --ref main \
  --group acme-finance --group acme-finance-leads
podium layer register --registry https://podium.acme.com --id platform-shared \
  --repo git@github.com:acme/podium-platform.git --ref main \
  --group acme-engineering --user security-lead@acme.com
podium layer register --registry https://podium.acme.com --id public-marketing \
  --repo git@github.com:acme/podium-public.git --ref main \
  --public
```

`podium layer update` patches a registered layer afterwards, and `podium layer reorder` changes the order. [Connecting layer sources](../content/layer-sources) covers each source type and private repositories, and [Layered composition](../content/layers) covers the composition rules. A `layers:` list in a `registry.yaml` declares layers for the `default` tenant at each start instead, and it needs a configuration file that the chart does not mount.

User-defined layers, which individual users register at runtime, sit above admin-defined layers in precedence. The workspace local overlay sits above those. The default cap is 3 user-defined layers per identity, configurable per tenant. `PODIUM_MAX_USER_LAYERS` sets the cap for a tenant whose own value is zero.

### 6. Set up Git webhooks

For each `git`-source layer, register the webhook URL and secret that `podium layer register` returned on the source repository. The registry validates the webhook signature and ingests on each merge to the tracked ref. On a multi-tenant registry the webhook URL carries the layer's tenant ID: `/v1/ingest/webhook/<tenant-id>/<layer-id>`.

[Keeping layers current](../content/layer-sources#keeping-layers-current) covers webhooks and the other ways to keep a layer current.

### 7. Configure CI

Each layer's source repo runs `podium lint` as a required check on PRs. Use the in-repo CI tooling, such as GitHub Actions, GitLab CI, or Buildkite. Podium runs as a CLI dependency within the existing CI framework.

### Next steps

- Follow the [authoring loop](#authoring-loop) to publish the first artifacts.
- Configure audit destinations, metrics, and tracing in [Audit and observability](../operations/observability).
- Plan capacity and backups with [Capacity and availability](../operations/capacity) and [Backup and restore](../operations/backup-restore).

---

## Identity flow

The MCP server, SDKs, and `podium sync` use the same identity providers:

- **`oauth-device-code`** for developer machines. The interactive device-code flow runs on first use, and tokens are cached in the OS keychain and refreshed transparently. The MCP server surfaces the verification URL via MCP elicitation, and the CLI prints it to stderr.
- **`injected-session-token`** for managed runtimes, such as Bedrock Agents, OpenAI Assistants, and custom orchestrators. The runtime issues a signed JWT per session, and the registry verifies the signature on every call.

The registry process reads its own `PODIUM_IDENTITY_PROVIDER` separately. Set it to `oidc-jwt` to verify the IdP-issued tokens developer machines present, or to `trusted-headers` when a gateway authenticates callers and forwards identity headers. Setting the registry's provider to `oauth-device-code` stops startup with `config.identity_provider_unverified`, because that value names the acquisition flow a consumer completes. See [Gateway (trusted headers)](../identity/trusted-headers) and [OIDC](../identity/oidc/).

Setting the registry's provider to `injected-session-token` also requires `PODIUM_RUNTIME_KEYS_PATH`, which names the JSON file holding the trusted runtime signing keys. Write a record into it with `podium admin runtime register --keys-file`, ship the same file to every replica, and restart the process to pick a new key up. On the chart, `runtimeKeys.enabled` and `runtimeKeys.secretName` mount the file from a Secret. A registry that cannot read or parse the file aborts startup with `config.runtime_keys_unavailable`, as does an `injected-session-token` registry whose key set is empty.

For each identity, the registry composes the caller's effective view from every layer that identity is entitled to see, in precedence order. When two layers hold the same artifact ID, ingest rejects the second contribution with `ingest.collision`.

---

## Authoring loop

Each author follows these steps:

1. Edit `SKILL.md` for a skill or `ARTIFACT.md` for other types, plus any overlay and bundled resources, in a checkout of the layer's Git repo.
2. Open a PR against the tracked ref. CI runs `podium lint` as a required check.
3. Reviewers approve per the team's branch protection rules.
4. Merge.
5. The Git provider fires a webhook to the registry. The registry fetches the new commit and walks the diff. It runs lint as defense in depth, validates immutability, hashes content, stores the manifest and bundled resources, and indexes metadata.

Each consumer is set up as follows:

- It authenticates via OIDC: `podium login` runs once, and tokens cache in the keychain.
- It runs with `PODIUM_SIGNATURE_PROVIDER=registry-managed`, the default, and `PODIUM_SIGNATURE_VERIFY_KEY` set to the registry's public key, extracted as [Deploy the registry](#2-deploy-the-registry) shows. Without it, and without `PODIUM_VERIFY_SIGNATURES=never`, `podium-mcp` refuses to start with `config.signature_provider_unavailable`.
- Its consumer paths run as in [Configure your harness](../../consuming/configure-your-harness).
- Its effective view composes the admin layers (visibility-filtered), the user-defined layers, and the workspace local overlay.

---

## Related pages

- [Helm chart reference](helm-chart): the chart's values and how they reach the pod.
- [Backing services](../services/): the backing services and their alternatives.
- [Connecting layer sources](../content/layer-sources): hooking Git repositories and local paths into a registry.
- [Layered composition](../content/layers): composing the catalog from several sources.
- [Access control](../content/access-control): declaring and debugging who can see what.
- [Audit and observability](../operations/observability): audit destinations, metrics, tracing, notifications, and alerts.
- [Operations](../operations/): signing keys, backup and restore, upgrades, capacity, security review, and troubleshooting.
- [Progressive adoption](../progressive-adoption): staged on-ramp for governance features.
- [Extending](../services/extending): SPI plugins, the forward-compatibility constraints, and external-extension patterns.
- [OIDC](../identity/oidc/): per-IdP setup recipes.
