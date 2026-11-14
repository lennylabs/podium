---
title: Clustered
nav_order: 3
description: Deploy replicated Podium registries on Kubernetes behind a load balancer, backed by Postgres, object storage, and an OIDC identity provider, with per-org tenant isolation and high availability.
---

# Clustered

The clustered deployment mode runs the standard stack: Podium registry replicas behind a load balancer, backed by Postgres, object storage, and an OIDC identity provider. It fits organizations with 20 or more users, multi-tenant requirements, governed environments, or compliance constraints. This page covers the reference topology, what the mode adds over [single node](single-node), the setup procedure with the Helm chart, and the chart upgrade from v0.4.0.

For day-two operations covering capacity, backup, and upgrades, see [Operator guide](operator-guide). For audit destinations, metrics, tracing, and alerts, see [Audit and observability](observability). For a staged on-ramp from a permissive deployment to enforced governance, see [Progressive adoption](progressive-adoption).

---

## Reference topology

- **Stateless registry replicas.** Three or more replicas behind an HTTP load balancer.
- **Postgres.** Managed (RDS, Cloud SQL, or Aurora) or self-run, with a primary and read replicas. It holds manifest metadata, dependency edges, layer config, and admin grants. It also holds embeddings when the default vector backend, pgvector, is in use.
- **Object storage.** S3-compatible: S3, GCS, MinIO, or R2.
- **Vector backend.** `pgvector` by default, collocated in the Postgres deployment with no separate service to run. The built-in `pinecone`, `weaviate-cloud`, and `qdrant-cloud` backends are selectable per deployment. [Vector backends](vector-backends) has the per-backend recipes.
- **Embedding provider.** `openai` by default. The built-ins also include `voyage`, `cohere`, and `ollama`.
- **Identity provider.** An OIDC IdP that supports the device-code flow, such as Okta, Entra ID, Google Workspace, Auth0, or Keycloak. SCIM push is optional and recommended for group-based visibility.
- **Audit destinations.** Each replica writes its audit records to the destinations that `PODIUM_AUDIT_LOG_PATH` lists, and the Helm chart sets `stdout` by default. [Audit across replicas](#audit-across-replicas) describes the options.
- **Helm chart** at `deploy/helm/podium` in the repository, alongside the failure-scenario runbook at `deploy/runbook.md`.

[Server-side integrations](integrations) lists each backing service with what ships by default and what can replace it.

---

## What clustered adds over single node

| Capability | What it provides |
|:--|:--|
| [Multi-tenancy and quotas](#multi-tenancy-and-quotas) | Per-tenant layer lists, admin grants, audit streams, and quotas, with one Postgres schema per org. |
| [Visibility against a verified identity](#visibility-against-a-verified-identity) | Layer visibility evaluated against a verified caller on every call. |
| [Audit across replicas](#audit-across-replicas) | A hash-chained audit stream from every replica, delivered to the destinations each replica lists. |
| [Freeze windows](#freeze-windows) | Ingest rejected during declared critical periods, with a two-approver override. |
| [Signing](#signing) | Every replica signs at ingest under one key mounted from one Secret. |
| [SCIM 2.0](#scim-20) | Group membership pushed from the IdP for layer visibility to reference. |
| [GDPR erasure](#gdpr-erasure) | One command that unregisters a user's layers and redacts the user's identity in the audit records. |

Several of these capabilities also run on a single-node deployment once it is configured for them. Each subsection says which.

### Multi-tenancy and quotas

The tenant boundary is the org. Each tenant has its own layer list, admin grants, audit stream, and quotas.

Multi-tenant mode runs on any registry server started with `PODIUM_MULTI_TENANT=true` and an identity provider the registry verifies. The registry routes each request to the tenant its organization names. On the clustered Postgres backend, each org has its own schema and cross-org tables use row-level security. A single-node SQLite store scopes each row by its tenant.

Quotas are per-tenant limits on storage, search QPS, materialization rate, and audit volume. Search QPS, materialization rate, and audit volume are charged to the tenant each request resolves to. Requests that resolve to no tenant share one set of budgets at the deployment defaults.

### Visibility against a verified identity

A clustered deployment sets the registry's own identity provider to `oidc-jwt` or `trusted-headers`. The registry then evaluates `public`, `organization`, OIDC `groups`, and `users` against a verified caller on every call. A single-node deployment runs the same evaluator once it configures one of those providers.

Authoring rights stay in the Git provider's branch protection. That scope covers writing content into a source the registry already reads. Declaring a layer that makes the registry read a given filesystem path is authorized to a tenant admin, because the registry process reads that path with its own rights rather than with the registrant's. See [Access control](access-control) and [Who may register a local-source layer](layer-sources#who-may-register-a-local-source-layer).

### Audit across replicas

Every read, ingest, and admin action carries the same hash-chain integrity a single-node deployment writes. Each replica chains its own records and delivers them to the destinations its list names. A receiver that collects every replica's stream therefore holds one chain per replica process.

The chart sets `config.auditLogPath`, which renders `PODIUM_AUDIT_LOG_PATH`, to `stdout` by default. Each pod then writes its audit records to the container log, and the cluster's log pipeline holds them under the platform's retention and outside the reach of erasure. A file path on a writable volume, an `http(s)` URL, or an `otlp+http(s)` URL replaces the default or joins it in a comma-separated list. [Audit destinations](observability#audit-destinations) covers the setup of each destination, and [Audit events](../reference/audit-events) describes each destination and the delivery rules.

Chain verification depends on whether the list includes a file destination:

- With the chart's `stdout` default, or any list without a file destination, the registry runs no local chain-head anchoring and no scheduled verification. The receiving system applies the `prev_hash` check to the shipped stream, as [Receiver checks](../reference/audit-events#receiver-checks) describes.
- Local chain-head anchoring (`PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS`) runs only on a replica whose list includes a file destination, because the anchor and verify passes walk the file. It signs the chain head with the replica's anchor key and submits nothing to a public transparency log.

Retention and erasure rewrite the file destination, so ship the stream through a stdout, HTTP, or OTLP destination listed beside the file rather than by tailing the file. A tool that tails the file re-ships or skips records when a retention or erasure rewrite renames a new file over the old one. The records it already shipped keep their unredacted identities, because erasure rewrites only the file.

Each registry audit record carries a `tenant` attribute naming the org whose audit stream it belongs to, and a SIEM separates the per-tenant streams by filtering on it. The following records carry no tenant: records that describe the registry as a whole, operator `tenant.managed` records, records of requests that resolve to no provisioned tenant, and records written before the attribute existed.

### Freeze windows

A `freeze_windows:` list under `registry:` in `registry.yaml` rejects ingest with `ingest.frozen` during critical periods, such as year-end close and release cuts. A single-node deployment reads the same list.

`podium layer reingest --break-glass --justification <text> --approver <approver-id> --approver <approver-id> <layer-id>` overrides an active window. The override needs a justification and two distinct approvers, each named by its own `--approver` flag. The caller who runs the command does not count as an approver.

### Signing

The registry signs every artifact at ingest with its registry-managed Ed25519 key by default. In steady state every replica signs under the one key the chart mounts from one Secret, as [Deploy the registry](#2-deploy-the-registry) shows. During a rotation, every replica trusts the new key before any replica signs under it, as [Rotating the signing key across replicas](#rotating-the-signing-key-across-replicas) states. No registry signing mode produces a Sigstore-keyless envelope.

Each `podium-mcp` consumer verifies what it loads under `PODIUM_VERIFY_SIGNATURES`, whose values are `always`, the default, and `never`.

### SCIM 2.0

An OIDC IdP that supports SCIM pushes group membership to the registry, and layer visibility references group claims directly. A single-node deployment mounts the same receiver when `PODIUM_SCIM_TOKENS` lists a bearer token.

### GDPR erasure

`podium admin erase --salt <tenant-salt> <user-id>` performs the erasure. It unregisters the user's user-defined layers and redacts the user's identity in the audit records of the tenant the request acts in, behind a `redacted-<sha256(user_id+salt)>` tombstone. It returns the purged layer ids plus the count of redacted audit events.

What the erase rewrites depends on the destination list of the replica that serves the request:

- When that replica's list names a file destination, the erase rewrites that file.
- With a list that names no file destination, such as the chart's `stdout` default, the erase rewrites no record. The receiving system redacts the records already delivered to it.

In both cases, every destination of that replica's stream receives `user.erased`.

On a registry started with `PODIUM_MULTI_TENANT=true`, the erase acts in the tenant the caller's organization selects and redacts only the audit records labeled with that tenant. Records that carry no tenant, such as those written before the `tenant` attribute existed, are left unchanged, and the response reports nothing about them. Operator `tenant.managed` records also carry no tenant. When the replica's list names a file destination, `podium admin erase --local --audit-path` redacts the records that carry no tenant in that file while the replica is stopped. With a list that names no file destination, the receiving system owns their redaction.

---

## Per-tenant layer model

Each tenant has its own layer list. On a multi-tenant registry, the layer endpoints act in the tenant the caller's organization selects, and a tenant's admin manages that tenant's layer list. Layers are an explicit ordered list configured per tenant, with no fixed `org / team / user` hierarchy. [Layered composition](layers) covers the composition rules that apply in every deployment mode, and [Connecting layer sources](layer-sources) covers each source type.

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

User-defined layers, which individual users register at runtime, sit above admin-defined layers in precedence. The workspace local overlay sits above those. The default cap is 3 user-defined layers per identity, configurable per tenant. `PODIUM_MAX_USER_LAYERS` sets the cap for a tenant whose own value is zero.

---

## Setup

The procedure below deploys the registry on Kubernetes with the Helm chart. To try the standard stack on one machine first, see [Evaluating with Docker Compose](#evaluating-with-docker-compose).

### Before you start

- A Kubernetes cluster, with `kubectl` and `helm` configured against it.
- A checkout of the Podium repository, which holds the chart at `deploy/helm/podium`.
- The `podium` CLI, which generates the signing key and administers tenants.

> [!IMPORTANT]
> A store that a v0.4.0 registry wrote needs the stored-row migration before any pod of this release serves it. Follow [Upgrading the chart from v0.4.0](#upgrading-the-chart-from-v040) instead of step 2 for such a store.

### 1. Provision dependencies

- Postgres 14+ with the pgvector extension, or the vector backend chosen instead.
- An object storage bucket on S3, GCS, MinIO, or R2.
- An OIDC IdP with device-code flow support.

### 2. Deploy the registry

The chart's templates render the backend selectors from the `config.*.type` values. They read every credential and per-backend setting from the Kubernetes secret that `existingSecret` names.

The registry signs at ingest by default, and every replica signs under a key that every other replica's verification key set contains. In steady state every replica signs under the one key the chart mounts from one Secret. During a rotation, every replica trusts the new key before any replica signs under it (see [Rotating the signing key across replicas](#rotating-the-signing-key-across-replicas)). The chart therefore mounts an operator-supplied signing key from a Secret rather than letting each pod generate its own.

**Generate the signing key.** Write the key file in a scratch directory:

```bash
KEY_DIR="$(mktemp -d)"
podium admin signing-key generate --key-file "$KEY_DIR/registry-signing.key"
```

`podium admin signing-key generate` writes only the file `--key-file` names. It reads no environment variable and no default path, opens no store, and refuses to replace an existing file. Its first line is the registry's verification key set, which is one key for a new file, and a `key_id=<hex> role=signing` line follows it. The key file, `$KEY_DIR/registry-signing.key`, is written with mode `0600` and carries a `private:` line and a `public:` line.

Copy the key file into the deployment's backup. Once the key is lost, the registry refuses every row the key signed unless a `verify:` line lists its public half. Delete `$KEY_DIR` after the Secret below is created.

**Extract the public key.** Every consumer of the deployment verifies under the public half:

```bash
awk '/^public:/{print $2}' "$KEY_DIR/registry-signing.key"
```

The output is the base64 text after the `public:` prefix, without the prefix. It is the form `PODIUM_SIGNATURE_VERIFY_KEY` requires, and the same key as the first line `generate` printed. Every consumer sets it in `PODIUM_SIGNATURE_VERIFY_KEY` (see [Configure your harness](../consuming/configure-your-harness)).

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

The chart runs `ghcr.io/lennylabs/podium-server` at the chart's `appVersion`, which is the release the checkout is at. Set `image.tag` to run another published release, or `image.repository` and `image.tag` to run an image from a private registry.

Among the chart's other defaults, `config.identityProvider.type` is `oidc-jwt`, which the registry verifies at request time. Supply its issuer and audience through the secret named in `existingSecret`, which reaches the pod via `envFrom`. Hybrid search is off by default: `config.vectorBackend.type` and `config.embeddingProvider.type` are both `none`, so the registry starts without an embedding-provider credential. Set both and supply the key in the same secret to turn it on.

[Helm chart reference](#helm-chart-reference) covers the signing values, which values reach the pod from `--set` and which from the secret, and the audit destination values.

Alternatively, run the binary directly with a `registry.yaml` config file (see [CLI reference](../reference/cli) for the environment variables a registry.yaml maps to).

### 3. Configure the IdP

See the [OIDC cookbooks](oidc/) for per-IdP setup steps:

- [Okta](oidc/okta)
- [Entra ID](oidc/entra-id)
- [Google Workspace](oidc/google-workspace)
- [Auth0](oidc/auth0)
- [Keycloak](oidc/keycloak)

Each cookbook covers client registration, scopes and audience, group claim mapping, and optional SCIM push.

### 4. Create the first tenant and admin

Run the registry in multi-tenant mode with `PODIUM_MULTI_TENANT=true`, and seed the first instance operator with `PODIUM_OPERATOR_ADMINS` (comma-separated identities). The operator role authorizes tenant management. It is distinct from the per-tenant `admin` role and from `PODIUM_BOOTSTRAP_ADMINS`.

A registry started with `PODIUM_MULTI_TENANT=true` needs an identity provider it verifies. [Multi-tenant startup refusals](#multi-tenant-startup-refusals) lists the configurations it refuses.

With the registry running, the operator provisions a tenant at runtime and grants the first per-tenant admin:

```bash
podium admin tenant create acme --registry https://podium.acme.com
podium admin grant --registry https://podium.acme.com alice@acme.com
```

`podium admin tenant create` derives the org ID from the name and is idempotent. Use `podium admin tenant list`, `podium admin tenant update <id>`, and `podium admin tenant deactivate <id>` to list, adjust, and deactivate tenants. See the [CLI reference](../reference/cli#podium-admin-tenant) for the full flag set.

### 5. Configure the tenant's layer list

Register the org's layer sources and their visibility with `podium layer register`, or with `POST /v1/layers` directly. `podium layer update` patches a registered layer afterwards. [Connecting layer sources](layer-sources#git-repositories) covers registering Git repositories and local paths.

Each request acts in the tenant the caller's organization selects, so the tenant's admin manages that tenant's layer list. [Per-tenant layer model](#per-tenant-layer-model) shows an example list.

### 6. Set up Git webhooks

For each `git`-source layer, register the webhook URL the registry returned at layer creation on the source repository. The registry validates the webhook signature and ingests on each merge to the tracked ref. On a multi-tenant registry the webhook URL carries the layer's tenant ID: `/v1/ingest/webhook/<tenant-id>/<layer-id>`.

[Keeping layers current](layer-sources#keeping-layers-current) covers webhooks and the other ways to keep a layer current.

### 7. Configure CI

Each layer's source repo runs `podium lint` as a required check on PRs. Use the in-repo CI tooling, such as GitHub Actions, GitLab CI, or Buildkite. Podium runs as a CLI dependency within the existing CI framework.

### Next steps

- Follow the [authoring loop](#authoring-loop) to publish the first artifacts.
- Configure audit destinations, metrics, and tracing in [Audit and observability](observability).
- Plan capacity, alerting, and backups with the [Operator guide](operator-guide).

---

## Evaluating with Docker Compose

The repository ships a `docker-compose.yml` that brings up an evaluation stack with `docker compose up -d`. The stack runs the registry, a pgvector Postgres, MinIO object storage, a Dex OIDC IdP, and a one-shot bootstrap container that creates the MinIO bucket. It wires the same components a clustered deployment wires, so consumers exercise the same code paths. The stack runs single-replica services with default credentials on local volumes, so it is unsuitable for production.

The stack differs from a production deployment in the following settings:

- **Search.** The stack sets `PODIUM_NO_EMBEDDINGS`, so search runs over manifest text with no embedding provider and no API key. Remove that variable and supply a provider with its key to exercise hybrid search.
- **Identity.** The registry seeds the first tenant and admin grant itself at boot, from the default tenant plus the identity in `PODIUM_BOOTSTRAP_ADMINS`. The registry service selects no identity provider, so it authenticates no caller, and the seeded grant is unreachable until an operator configures one. The service publishes its port on the host loopback interface for that reason.
- **Signing.** The stack pins `PODIUM_SIGN: "none"`, because it has no key management, and a recreated registry container would otherwise generate a fresh signing key on every recreate. A `podium-mcp` consumer pointed at the stack therefore sets `PODIUM_VERIFY_SIGNATURES=never`.

A stack whose `postgres_data` volume holds rows a v0.4.0 registry wrote needs the stored-row migration before it starts, as [Upgrade a Docker Compose evaluation stack](#upgrade-a-docker-compose-evaluation-stack) shows.

---

## Identity flow

The MCP server, SDKs, and `podium sync` use the same identity providers:

- **`oauth-device-code`** for developer machines. The interactive device-code flow runs on first use, and tokens are cached in the OS keychain and refreshed transparently. The MCP server surfaces the verification URL via MCP elicitation, and the CLI prints it to stderr.
- **`injected-session-token`** for managed runtimes, such as Bedrock Agents, OpenAI Assistants, and custom orchestrators. The runtime issues a signed JWT per session, and the registry verifies the signature on every call.

The registry process reads its own `PODIUM_IDENTITY_PROVIDER` separately. Set it to `oidc-jwt` to verify the IdP-issued tokens developer machines present, or to `trusted-headers` when a gateway authenticates callers and forwards identity headers. Setting the registry's provider to `oauth-device-code` stops startup with `config.identity_provider_unverified`, because that value names the acquisition flow a consumer completes. See [Gateway-delegated identity](gateway-delegated-identity) and the [OIDC cookbooks](oidc/).

Setting the registry's provider to `injected-session-token` also requires `PODIUM_RUNTIME_KEYS_PATH`, which names the JSON file holding the trusted runtime signing keys. Write a record into it with `podium admin runtime register --keys-file`, ship the same file to every replica, and restart the process to pick a new key up. A registry that cannot read or parse the file aborts startup with `config.runtime_keys_unavailable`, as does an `injected-session-token` registry whose key set is empty.

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
- Its consumer paths run as in [Configure your harness](../consuming/configure-your-harness).
- Its effective view composes the admin layers (visibility-filtered), the user-defined layers, and the workspace local overlay.

---

## Signing key operations

### Rotating the signing key across replicas

The [Operator guide](operator-guide#rotating-the-signing-key) gives the rotation procedure. Across replicas, every replica trusts the new key before any replica signs under it, so the chart's Secret changes twice:

1. Copy the current key file out of the Secret into a scratch directory, with `kubectl get secret podium-signing-key -o jsonpath='{.data.registry-signing\.key}' | base64 -d`, and write it with mode `0600`.
2. Run `podium admin signing-key rotate --key-file <copy> --staged-out <staged>`. The staged file is the previous key file plus a `verify:` line for the new key. The copy becomes the rotated file: the new signing key plus a `verify:` line for each previous key. Add the new key to every consumer's `PODIUM_SIGNATURE_VERIFY_KEY`, for example as the comma-separated list the command prints on its first line.
3. Replace the Secret's content with the staged file, with `kubectl create secret generic podium-signing-key --from-file=registry-signing.key=<staged> --dry-run=client -o yaml | kubectl apply -f -`. Restart the Deployment with `kubectl rollout restart deployment/podium-podium`, and wait for the restart to finish with `kubectl rollout status deployment/podium-podium`. Every replica still signs under the previous key and now trusts the new one. Start step 4 only after the rollout status command returns, because a pod from before the restart does not trust the new key and refuses the rows a pod signing under it ingests.
4. Replace the Secret's content with the rotated file, restart the Deployment again with `kubectl rollout restart deployment/podium-podium`, and wait with `kubectl rollout status deployment/podium-podium`. Every replica now signs under the new key and trusts the previous one. Start step 5 only after the rollout status command returns, because a pod from before this restart still signs under the previous key, and a row it signs after a step 5 run is not counted by that run.
5. Run `kubectl exec deployment/podium-podium -- /usr/local/bin/podium-server sign-stored-rows`, which re-signs the stored rows under the new key while the replicas serve. The command may take `--plan-digest=<digest>` from a preceding `sign-stored-rows --dry-run` run, which binds the run to the reviewed plan and refuses it with exit status 3, writing nothing, when the plan changed. The digest is optional here, because the run passes no `--include-unsigned`.
6. For each consumer that runs with `PODIUM_CACHE_MODE=offline-only`, keep the previous key in its set, or clear its cache directory and refill it in `always-revalidate` or `offline-first`, before the previous key leaves that consumer's set. An `offline-only` consumer refuses a cached delivery signature made under a key it no longer trusts.
7. Remove the previous key only after a run of step 5 that exits 0 reports `0 row(s) still signed under it` for its `key_id`. Delete its `verify:` line from the key file, replace the Secret's content, restart the Deployment with `kubectl rollout restart deployment/podium-podium`, wait with `kubectl rollout status deployment/podium-podium`, and remove the key from every consumer's set.

Back up the rotated file, and delete the scratch copies once the Secret holds it.

---

## Migration from single node

`podium admin migrate-to-standard` exports a [single-node](single-node) deployment into the standard stack:

```bash
podium admin migrate-to-standard --postgres <dsn> --object-store <url>
```

The artifact directory is unchanged. Layer config moves from `~/.podium/registry.yaml` to the tenant config, and the same artifacts ingest into the new metadata store. The command clears the target store's record of the rewrite of stored content hashes. The target store is outside the SQLite store in the key file's directory, so in either signing mode the target registry's start is refused until `sign-stored-rows` records the rewrite. A refused start rewrites no manifest row, signs no row, and records no completion.

The target registry signs with the source's key. Migrate in this order:

1. On a chart deployment, run `migrate-to-standard` before any chart install on the target store. A chart release that already serves the target store is held at zero replicas, and its pods are deleted, as step 2 of [Upgrade with signing on](#upgrade-with-signing-on) shows, before the command runs.
2. Run the export command above.
3. Before the target's first start, create the signing Secret from the source's key file, replacing any Secret created from another key, as [Single node](single-node#migrating-to-clustered) shows.
4. Run `sign-stored-rows --dry-run`, review its report, and run `sign-stored-rows` with its `--plan-digest` before the target's first start. With signing on, repeat the dry run's `--include-unsigned` setting. On a chart deployment, the rewrite runs in the migrate Job: install, or upgrade, at zero replicas with `migration.previousImage` set to an image other than this release's, such as `ghcr.io/lennylabs/podium-server:v0.4.0`. Then follow steps 3 to 5 of [Upgrade with signing on](#upgrade-with-signing-on), with `signing.mode=none` and `migration.includeUnsigned=false` for a deployment with signing off.
5. Switch consumer endpoints to the new registry URL and decommission the old host.

A serving pod over the target store before the `sign-stored-rows` run succeeds exits at start and names `sign-stored-rows`.

For the staged rollout of governance features covering identity, sensitivity labels, signing, and freeze windows, follow [Progressive adoption](progressive-adoption).

### Signing key mismatches after migration

The target must hold the source's key before the `sign-stored-rows` run. [Single node](single-node#migrating-to-clustered) states both repair cases below.

| Situation | Result | Repair |
|:--|:--|:--|
| The target registry has no key file. | The target is refused at start. | Place the source's key. |
| The target holds any other key. | The target is refused at start. The `sign-stored-rows --dry-run` it requires lists every copied signed row as `class=signature_unverified` before any write. | Place the source's key before the run. |
| A run under another key completed. | The run leaves those rows untouched and records completion. A row the source had already rewritten is then refused with `materialize.signature_invalid`. | Create the Secret from the source's key and restart the target. |
| A run under another key completed, and the source never started on this release. | A row the source still held at the previous content hash is refused with `materialize.content_hash_mismatch`. | Put the source key's public half on a `verify:` line of the target's key file, restart, and run `sign-stored-rows`. Alternatively, recreate the target store empty, run the migration again with the source's key in place, and run the `sign-stored-rows` dry run and run before the target's start. |

---

## Helm chart reference

This section describes how the chart's values reach the registry pod. [Deploy the registry](#2-deploy-the-registry) gives the install procedure.

### Signing values

A render that names no signing Secret fails with a message naming `signing.secretName`. The chart installs with its other defaults once `signing.secretName` names a Secret, or once `signing.mode=none` turns signing off.

- A consumer of a registry with signing off, such as `podium-mcp` or a server-source `podium sync` in CI, sets `PODIUM_VERIFY_SIGNATURES=never`.
- A server-source `podium sync` against a signing registry sets `PODIUM_SIGNATURE_VERIFY_KEY`.

An upgrade of a store that already holds signed rows creates the Secret from the key that signed them. The registry refuses every row whose envelope does not verify under a key of its verification key set: the key file's `public:` key and every `verify:` key. An upgrade from v0.4.0 runs the stored-row migration before any pod of the new release serves, as [Upgrading the chart from v0.4.0](#upgrading-the-chart-from-v040) shows.

Every registry process resolves `PODIUM_SIGN_KEY_PATH` to a key file on the store's persistent storage, and the chart mounts that one file from its Secret. A hand-rolled deployment arranges the same. A hand-rolled deployment with signing on, a Postgres store, and no `PODIUM_SIGN_KEY_PATH` is refused at start.

### Stored-row migration at start

In either signing mode, unless the store is the SQLite store in the key file's directory, a registry pod checks the store at start. A pod over a store that holds manifest rows and has not completed this release's stored-row migration exits at start, and its log names `sign-stored-rows`. It rewrites no manifest row, signs no row, and records no completion. A pod over a store that holds no manifest row records the migration at its first start. An install over a store that v0.4.0 wrote follows [Upgrading the chart from v0.4.0](#upgrading-the-chart-from-v040).

### How values reach the pod

The container `env:` block takes precedence over `envFrom:`, so any value the chart renders as `env:` is set through `--set` rather than through the secret.

| Values | When they render | Consequence |
|:--|:--|:--|
| `PODIUM_BIND`, `PODIUM_REGISTRY_STORE`, `PODIUM_OBJECT_STORE`, `PODIUM_VECTOR_BACKEND`, `PODIUM_EMBEDDING_PROVIDER`, `PODIUM_IDENTITY_PROVIDER`, and `PODIUM_SIGN`, from `config.bind`, the `type` values, and `signing.mode` | On every install | Blanking one in `values.yaml` emits an empty value that shadows the secret rather than deferring to it. Leave them at their defaults or set them with `--set`. |
| `PODIUM_SIGN_KEY_PATH` | Whenever `signing.mode` is `registry-key` | The chart mounts the key file from the signing Secret. |
| The OIDC issuer, audience, groups claim, and group mapping, the bootstrap admins, and the default layer visibility | Only where `values.yaml` carries a value | A key left blank renders nothing, so the secret supplies it through `envFrom`. Setting it in `values.yaml` overrides whatever the secret carries for the same name. |
| The S3 bucket, region, endpoint, and path-style flag | Only when set and `config.objectStore.type` is `s3` | As the row above. |
| The filesystem root | Only when set and `config.objectStore.type` is `filesystem` | As the row above. |
| `config.publicMode` and `config.allowPublicBind` | Only when set to true | None. |
| `config.auditLogPath` | By default, as `stdout` | See [Audit destination values](#audit-destination-values). |

Three `config` keys are rendered by no template:

- For `config.store.dsn`, supply the connection string as `PODIUM_POSTGRES_DSN` in the secret, or set `postgresql.enabled` to run the bundled evaluation database. The bundled database derives the DSN from the service it creates and the password in `postgresql.existingSecret`. Enabling it resolves the DSN on the container, so it takes precedence over one the secret carries.
- Setting `config.endpoint` has no effect.
- Setting `config.embeddingProvider.model` has no effect.

### Audit destination values

`config.auditLogPath` carries a value by default, and its `stdout` default renders as an `env:` entry. It therefore overrides a `PODIUM_AUDIT_LOG_PATH` that the secret carries. An installation that set `PODIUM_AUDIT_LOG_PATH` in the secret moves the value to `config.auditLogPath` on upgrade, or its audit records go to the container log instead of its receiver.

[Configure the Helm chart](observability#configure-the-helm-chart) covers the same values as part of the audit setup.

An explicit empty `config.auditLogPath` renders no `PODIUM_AUDIT_LOG_PATH`, so the secret's value applies when the secret carries one. When the secret carries none, each pod, the migrate Job's included, falls back to `~/.podium/audit.log`, which the read-only root filesystem cannot hold. Each registry pod and the migrate Job's `sign-stored-rows` run then refuse startup with `config.audit_sink_unavailable`, naming `PODIUM_AUDIT_LOG_PATH` and the path, whatever the anchoring setting.

An `extraEnv` entry that sets `PODIUM_TRACING=stdout` beside a `stdout` audit destination also refuses startup with `config.audit_sink_unavailable`, because both would write to standard output.

The HTTP and OTLP audit destinations take their request headers, CA bundle, and client certificate from files. Set `audit.transportSecret.name` to a Secret, and list the keys it holds in `audit.transportSecret.keys`:

| Key | Contents |
|:--|:--|
| `headers` | One `Name: value` request header per line. |
| `ca.crt` | A PEM CA bundle that replaces the system trust store. |
| `tls.crt` and `tls.key`, listed together | A mutual-TLS client certificate and its key. |

The chart mounts the Secret read-only at `/etc/podium/audit` in the registry pods and the migrate Job. It sets `PODIUM_AUDIT_HEADERS_FILE`, `PODIUM_AUDIT_TLS_CA_FILE`, `PODIUM_AUDIT_TLS_CERT_FILE`, and `PODIUM_AUDIT_TLS_KEY_FILE` for the keys it lists. A destination URL that carries userinfo refuses startup, so credentials go in the `headers` key.

### Multi-tenant startup refusals

A registry started with `PODIUM_MULTI_TENANT=true` fails to start with `config.multitenant_no_verifier` in the following cases, unless another startup check refuses the configuration first:

- It configures no identity provider.
- It names a provider value the registry does not recognize.
- It runs in public mode with no identity provider.

On this chart, `config.publicMode` reaches the `config.multitenant_no_verifier` refusal only in one combination. The other combinations fail with a different code:

| `config.identityProvider.type` | `config.allowPublicBind` | Startup result with `config.publicMode` |
|:--|:--|:--|
| Empty or `none` | Set | `config.multitenant_no_verifier` |
| Empty or `none` | Not set | `config.public_bind_refused`, because the chart renders a `0.0.0.0` bind |
| Any other type, including the default `oidc-jwt` | Either | `config.public_mode_with_idp` |

---

## Upgrading the chart from v0.4.0

This section applies to a clustered deployment whose store a v0.4.0 registry wrote. It moves the store onto this release through a migrate Job that runs before any pod of this release serves. A deployment already past this migration follows [Later upgrades](#later-upgrades).

### How the upgrade works

This release changes the stored content hash of every row (see the upgrade note in `CHANGELOG.md`). Outside the SQLite store in the key file's directory, a registry start over a store that holds manifest rows and no record that the rewrite of stored content hashes completed is refused, in either signing mode. The pod exits at start, rewriting no manifest row, signing no row, and recording no completion. A store that holds no manifest row records the rewrite at its first start.

The rewrite requires every registry process on the previous release to be stopped first. The chart runs it as a Kubernetes Job while the Deployment is held at zero replicas, so no probe restarts the pass and no v0.4.0 pod writes beside it. The procedure runs the Job before any pod of this release starts, with signing on and with signing off. The Job runs `podium-server sign-stored-rows`, which performs the whole rewrite when the store holds no record of it, and records its completion. Registry pods that start afterwards find the record and skip the pass.

The chart renders the Job as a Helm `post-install` and `post-upgrade` hook when `migration.mode` is `dry-run` or `run`. Render-time checks, which read values only, refuse each step that would break the procedure:

- A `dry-run` or `run` render requires `replicaCount=0` and `migration.previousImage`.
- It refuses an image equal to `migration.previousImage`, because the v0.4.0 `podium-server` ignores its arguments, and a Job on that image would start a full v0.4.0 registry that never exits.
- With `signing.mode=none` it requires `migration.includeUnsigned=false`.
- A `run` render requires `migration.planDigest`, the value on the reviewed dry run's last line.

The chart reads nothing from the cluster, so the binding between the reviewed dry run and the run lives in the command. Given the plan digest, the run refuses with exit status 3 and writes nothing when any value the digest covers differs from the dry run's. The digest covers the completion-record mode, the `--include-unsigned` setting, the signing and verification `key_id`s, and each covered row's tenant, ID, version, stored hash, outcome, target, sign decision, and signature state. The digest covers every row except a row the dry run reports as `class=migrated`: one whose stored bytes reproduce its stored hash, whose signature is absent or verifies under the signing key (with signing off, whose signature is not checked), and that the run neither writes nor signs. A store, DSN, bucket, or Secret change that leaves those values identical is not detected by the digest.

In either signing mode, a registry pod over a store that holds manifest rows and no completion record exits at start and names `sign-stored-rows` in its log. Before it exits, it applies this release's additive schema and seeds the default tenant and the bootstrap grants. It rewrites no manifest row, signs no row, and records no completion. A serving render over an unmigrated store therefore starts no pod that serves or rewrites, and its rollout does not progress.

Nothing checks that registry processes are stopped, so step 2 waits for them. A registry process on the same store under another release name, in another namespace, or outside Kubernetes is outside that wait, so stop every such process before step 3. A process that writes a row the digest covers between the dry run and the run changes the plan, and the run refuses. A process that writes after the run builds its plan leaves its rows at the previous content hash, and admission refuses them with `materialize.content_hash_mismatch`.

Every step below passes the same values file with `-f` and sets its per-step values with `--set`. No step passes `--reuse-values`, so no per-step value carries into the next command. No step passes `--rollback-on-failure` (Helm v4) or `--atomic` (Helm v3), because a rollback reinstalls the v0.4.0 binary over a rewritten store. The only route back to v0.4.0 is the backup restore that [Rollback](#rollback) describes. Helm waits on a hook Job only up to `--timeout`, whose default is 5 minutes. Every step that runs the Job therefore passes a `--timeout` larger than the time the pass takes, and larger than `migration.activeDeadlineSeconds` when that value is set.

The commands assume the release `podium` in the current namespace, the signing Secret `podium-signing-key`, and the values file `podium-values.yaml`.

### Upgrade with signing on

**Step 1.** Prepare the values file and the signing Secret.

```bash
helm get values podium -o yaml > podium-values.yaml
```

`helm get values` returns the v0.4.0 image. Edit `podium-values.yaml` as follows:

- Set `migration.previousImage` to that image, such as `ghcr.io/lennylabs/podium-server:v0.4.0`. A migration render refuses to run the Job on the image `migration.previousImage` names.
- Set `image.repository` and `image.tag` to this release's published image.
- Add `signing.secretName: podium-signing-key`.

Create the signing Secret as [Deploy the registry](#2-deploy-the-registry) shows. With the chart's `stdout` default for `config.auditLogPath`, the `artifact.signed` audit events the run appends for every row it signs reach the Job's container log beside its `plan:`, `dry-run:`, and `rehash:` lines, and each audit line begins with `{`. An `http(s)` or `otlp+http(s)` value ships those events to a receiver instead.

Note the current revision, which is the v0.4.0 revision that [Rollback](#rollback) returns to, and lift Helm's revision history limit for the rest of the procedure:

```bash
helm history podium --max 1
export HELM_MAX_HISTORY=0
```

Helm keeps 10 revisions per release by default and prunes the oldest on each upgrade. Each step that renders records a revision, and so does a failed dry run or run, so a procedure with retries can prune the v0.4.0 revision before a rollback needs it. `HELM_MAX_HISTORY=0` sets the default of `--history-max` to no limit for every `helm upgrade` in the shell.

**Step 2.** Stop every registry process, then back up the store.

```bash
helm upgrade podium ./deploy/helm/podium -f podium-values.yaml --set replicaCount=0
kubectl wait --for=delete pod --timeout=10m \
  -l app.kubernetes.io/name=podium,app.kubernetes.io/instance=podium \
  --field-selector=status.phase!=Succeeded,status.phase!=Failed
```

A scaled-down Deployment stops counting a pod while the pod is still in its termination grace period and can still write to the store, so the `kubectl wait --for=delete` line waits for the pods themselves. The field selector skips pods in a terminal phase, such as evicted pods, which a scale-down never deletes. Back up Postgres and the object store (the bucket or the objects volume) once the wait returns. That backup holds every write the v0.4.0 binary made.

**Step 3.** Run the dry run, capture its log, and review it.

```bash
kubectl delete job podium-podium-migrate --ignore-not-found --wait=true
helm upgrade podium ./deploy/helm/podium -f podium-values.yaml --set replicaCount=0 \
  --set migration.mode=dry-run --timeout 2h &
helm_pid=$!
until kubectl get job podium-podium-migrate >/dev/null 2>&1 || ! kill -0 "$helm_pid" 2>/dev/null; do
  sleep 2
done
if kubectl get job podium-podium-migrate >/dev/null 2>&1; then
  kubectl logs -f job/podium-podium-migrate --pod-running-timeout=10m > dry-run.log
fi
wait "$helm_pid"
```

The first line removes a Job a previous attempt left, so the capture reads the new Job's log. The loop also ends when `helm` exits, because a render check that refuses the step exits before any Job exists. The last line returns the exit status of `helm upgrade`. A non-zero status with a render check's refusal on the terminal means the step did not run. The log is streamed to a local file while the Job runs, because the kubelet rotates container logs (at 10 MiB by default), and a `kubectl logs` after the Job ends returns only the current file. Check that the capture is complete:

```bash
grep -cE '^dry-run: .* class=' dry-run.log
grep -E '^dry-run: [0-9]+ row\(s\) planned' dry-run.log
grep -E '^dry-run: .* class=' dry-run.log | grep -vc ' class=migrated '
grep -E '^dry-run: plan digest ' dry-run.log
```

A complete capture meets these conditions:

- It holds one row line per planned row, so the first count equals `N` in the `dry-run: N row(s) planned` line.
- It ends with the `dry-run: plan digest <digest> over <K> row(s)` line.
- The third count, which leaves out the `class=migrated` rows the digest does not cover, equals `K`.

When a count differs or the digest line is absent, rerun this step. Each row line reads `<tenant>/<artifact>@<version> class=<class> target=<hash> write=<bool> sign=<bool> signed_by=<state> stored=<hash>`, and the rows are listed in ascending order of tenant, artifact ID, and version. The `dry-run: plan mode=... include_unsigned=... signing_key=... verify_keys=...` line after them is the plan header the digest also covers. Then review `dry-run.log`:

- Each row with `sign=true` and `signed_by=unsigned` is an unsigned row that step 4 attests under `--include-unsigned`. Confirm that each such row is one the deployment stored. A row the operator cannot account for stops the upgrade.
- No row reports `class=body_unavailable`. Such a row means an object-store read failed, and the run would leave the completion record unset.
- No row reports `class=signature_unverified` or `signed_by=unverified`. Such a row means that the signing Secret does not hold the key that signed the stored rows, neither as its key nor as a `verify:` key. The run would leave every such row at its previous content hash, record completion, and exit 0, and the registries would then refuse those rows. Fix the Secret and rerun this step.
- No row reports `class=unreproducible` or `class=body_missing`. The run leaves such a row at its previous content hash and still records completion, so account for each one before step 4.
- The `would be signed` count in the summary line matches the rows reviewed.

Keep `dry-run.log` as the record of the reviewed plan. The `sha256:` value on its last line is the plan digest that step 4 passes as `migration.planDigest`.

`migration.includeUnsigned` is true by default, so the dry run and the run both pass `--include-unsigned`. A deployment that does not attest its unsigned rows sets `migration.includeUnsigned=false` for both steps. Those rows then stay unsigned and are refused with `materialize.signature_missing` until they are signed or republished. The run repeats the dry run's setting, because the plan digest covers it.

**Step 4.** Run the reviewed plan, and capture its log.

```bash
kubectl delete job podium-podium-migrate --ignore-not-found --wait=true
helm upgrade podium ./deploy/helm/podium -f podium-values.yaml --set replicaCount=0 \
  --set migration.mode=run --set migration.planDigest=<digest> --timeout 6h &
helm_pid=$!
until kubectl get job podium-podium-migrate >/dev/null 2>&1 || ! kill -0 "$helm_pid" 2>/dev/null; do
  sleep 2
done
if kubectl get job podium-podium-migrate >/dev/null 2>&1; then
  kubectl logs -f job/podium-podium-migrate --pod-running-timeout=10m > run.log
fi
wait "$helm_pid"
```

Replace `<digest>` with the `sha256:` value from the last line of `dry-run.log`. The Job runs `sign-stored-rows --plan-digest=<digest>`, with `--include-unsigned` when `migration.includeUnsigned` is true. The log carries the `rehash:` summary line and, with `migration.includeUnsigned` true, a `rehash: 0 unsigned left` line. The command exits 0 only when the rewrite is complete and its completion is recorded.

When the plan the run builds has a digest other than `<digest>`, the command exits with status 3 before any write, and `helm upgrade` exits non-zero. It writes no row, no audit event, and no completion record. It prints its own plan as `plan:` lines in the dry-run format, one per planned row, followed by its header and a `plan: plan digest <digest> over <K> row(s)` line. The log is streamed to `run.log` while the Job runs for the same reason as step 3's. Check that `run.log` holds the `plan: plan digest` line, then compare the two plans:

```bash
grep -E '^plan: plan digest ' run.log
grep -E '^(dry-run|plan): plan mode=' dry-run.log run.log
diff <(sed -n 's/^dry-run: \(.* class=.*\)/\1/p' dry-run.log) <(sed -n 's/^plan: \(.* class=.*\)/\1/p' run.log)
```

The second command prints both plan headers, and the `diff` lists the rows that changed since the dry run. The two digest lines differ by construction, because the refusal fires only when they do. Account for each change, then rerun step 3 and run this step with the new digest. When the command exits with status 1, read [Recovering from a failed run](#recovering-from-a-failed-run).

**Step 5.** Serve.

```bash
helm upgrade podium ./deploy/helm/podium -f podium-values.yaml
```

The Deployment scales to `replicaCount`. The pods find the completion record and skip the pass, so the chart's startup budget covers their start. A pod over a store without the record exits at start and names `sign-stored-rows` in its log, rewriting no manifest row, signing no row, and recording no completion. In that case, return the release to zero replicas as step 2 shows, and rerun steps 3 and 4.

### Upgrade with signing off

A deployment with `signing.mode=none` follows [Upgrade with signing on](#upgrade-with-signing-on), with `signing.mode: none` and `migration.includeUnsigned: false` in the values file in place of the signing Secret. The chart renders `PODIUM_SIGN=none` into the migrate Job, so the command rewrites without signing. The command refuses `--include-unsigned` with signing off, which is why the value is false.

The dry run reports every row with `sign=false`, a signed row with `signed_by=unchecked`, and a header with `signing_key=-`. A dry run that exits 1 naming `PODIUM_SIGN=none` means a signed row needs its hash rewritten. The deployment then supplies the key that signed it, by turning signing on with the signing Secret. A serving render that skips the migrate Job starts pods that exit at start and name `sign-stored-rows`, rewriting no manifest row, signing no row, and recording no completion, as with signing on.

### Install over an existing v0.4.0 store

A new release name, a new namespace, or a reinstall after `helm uninstall` over a store v0.4.0 wrote is an upgrade from the store's point of view. The objects volume carries `helm.sh/resource-policy: keep`, and the bundled Postgres keeps its volume claim, so both survive `helm uninstall`.

Stop every v0.4.0 process that uses the store. Write `podium-values.yaml` and create the signing Secret as step 1 of [Upgrade with signing on](#upgrade-with-signing-on) describes, with two differences. Skip `helm get values`, because no release exists to read the values from, and set `migration.previousImage` to the image the v0.4.0 registry ran. With signing off, the file carries `signing.mode: none` and `migration.includeUnsigned: false` in place of the signing Secret. Install at zero replicas on this release's image:

```bash
helm install podium ./deploy/helm/podium -f podium-values.yaml --set replicaCount=0
```

Then follow [Upgrade with signing on](#upgrade-with-signing-on) from step 2, with either signing mode. An install at zero replicas starts no registry pod. Adding `--set migration.mode=dry-run` to the install, in place of the separate step, runs step 3's dry run as a `post-install` hook. Take the backup before such an install, and capture its log as step 3 does, with `helm install` in place of `helm upgrade`.

### Upgrade a Docker Compose evaluation stack

Signing off does not exempt the [Docker Compose evaluation stack](#evaluating-with-docker-compose) from the stored-row migration of this release. A stack whose `postgres_data` volume holds rows a v0.4.0 registry wrote is refused at the upgraded registry's first start. A registry start outside the SQLite store in the key file's directory does not rewrite stored content hashes over a store that holds manifest rows, in either signing mode. The refused start applies the additive schema and seeds the default tenant and the bootstrap grants, and it rewrites no manifest row, signs no row, and records no completion.

Upgrade such a stack by stopping the registry, rebuilding its image, running `sign-stored-rows` once through the registry service, and then starting the stack:

```bash
docker compose stop registry
docker compose build registry
docker compose run --rm registry sign-stored-rows --dry-run
docker compose run --rm registry sign-stored-rows --plan-digest=<digest>
docker compose up -d
```

Review the dry run's report before the run, and pass the `sha256:` value from its last line, `dry-run: plan digest <digest> over <K> row(s)`, as `<digest>`. The service environment carries `PODIUM_SIGN: "none"`, so the command rewrites without signing and needs no other setting. A stack started on a new volume holds no manifest row at its first start and records the migration then.

### Layers and webhooks registered before 0.5.0

On a multi-tenant registry, each layer request acts in the tenant the caller's organization selects. Layers and webhooks registered on a multi-tenant registry before 0.5.0 need the following changes:

- **Layers.** Layers that a caller registered before 0.5.0 remain in the default tenant, because the registry previously stored every layer there. The owner registers the layer again from the owning tenant. An admin of the default tenant, such as a bootstrap admin whose organization is `default`, removes the stale row with `podium layer unregister`, because the owner's own requests no longer reach the default tenant.
- **Git webhooks.** Git layers registered before 0.5.0 need their webhook re-registered on the source repository with the URL `/v1/ingest/webhook/<tenant-id>/<layer-id>`. This includes the default tenant's layers, whose segment is the default tenant's ID rather than the name `default`.

### Later upgrades

A later upgrade runs `helm upgrade podium ./deploy/helm/podium -f podium-values.yaml` with the new image tag. No Job renders, and the default rolling update applies.

- A release whose changelog names a new stored-value migration follows this procedure again.
- A release whose changelog names a rollback floor rolls with the default rolling update from a version at or above the floor. From a version below the floor, scale the Deployment to zero replicas before the upgrade, and pass neither `--rollback-on-failure` nor `--atomic`. [Rollback floor for the layer credential columns](operator-guide#rollback-floor-for-the-layer-credential-columns) states the floor for 0.7.0.

Key rotation runs `sign-stored-rows` on a serving pod with `kubectl exec`, as [Rotating the signing key across replicas](#rotating-the-signing-key-across-replicas) shows. A rerun of the migration command after step 5 takes the same route, with a dry run first and its plan digest passed to the run:

```bash
kubectl exec deployment/podium-podium -- /usr/local/bin/podium-server sign-stored-rows --include-unsigned --dry-run
kubectl exec deployment/podium-podium -- /usr/local/bin/podium-server sign-stored-rows --include-unsigned --plan-digest=<digest>
```

Over the migrated store, the rerun rewrites no content hash. `--include-unsigned` attests every unsigned row the reviewed dry run lists, such as a row a failed run held back. Review each row the dry run lists with `signed_by=unsigned` and `sign=true`, and pass the `sha256:` value from its last line as `<digest>`. The run refuses with exit status 3, writing nothing, when its plan differs from the reviewed one. A deployment that set `migration.includeUnsigned=false`, including every deployment with `signing.mode=none`, drops `--include-unsigned` from both commands, and its run then takes `--plan-digest` optionally.

### Recovering from a failed run

A run Job that fails leaves the completion record unset and the Deployment at zero replicas, and `helm upgrade` exits non-zero. A serving render before a later run succeeds starts pods that exit at start, rewriting no manifest row, signing no row, and recording no completion.

| Symptom | Cause | Fix |
|:--|:--|:--|
| The run exits with status 3, and its log carries `plan:` lines with `class=body_unavailable` rows and no `rehash:` summary or unsigned-left line. | An object-store outage began after a clean step-3 dry run and changed the plan. | Fix the cause and rerun steps 3 and 4. |
| A rerun of step 3 still lists `class=body_unavailable` rows while the object store's own status reports it healthy. | Object-store reads that do not finish within the migration read timeout, or an object-store fault its status does not report. | Raise `config.migrationObjectReadTimeout`, which sets `PODIUM_MIGRATION_OBJECT_READ_TIMEOUT` (default `30s`) on the Job pod, in `podium-values.yaml`. Otherwise restore the object store or correct its values or Secret. |
| A run whose digest matched exits with status 1. | The run held a row back or failed a write. | With signing on, follow the log's `rehash: N unsigned left; run sign-stored-rows --include-unsigned --dry-run, review it, and pass its plan digest to sign them` line, which names steps 3 and 4. When rows point at the wrong object-store root, correct the object-store values or Secret. |
| The Job runs out of memory or reaches `migration.activeDeadlineSeconds`. | The pass needs more memory or time. | Raise `migration.resources.limits.memory` or the deadline, and rerun steps 3 and 4. |
| Helm stops waiting at `--timeout` while the Job still runs. | The pass takes longer than `--timeout`. | Wait for the Job, as described below. |
| Registry pods exit at their next start after a backup restore that followed step 5. | The restored store lacks the completion record. | Hold the release at zero replicas, wait for its pods to be deleted as step 2 of [Upgrade with signing on](#upgrade-with-signing-on) shows, and rerun steps 3 to 5. A restore that keeps the v0.4.0 release instead follows [Rollback](#rollback). |

The command reads object storage only while it builds its plan. It logs the per-row `left at its stored content hash` lines, the `rehash:` summary line, and the unsigned-left line only while it writes. Neither the refused run's log nor the dry run's carries the per-row read error, so neither shows whether a read timed out. Each unavailable read still spends the object-store client's retry budget, so an outage lengthens the refused run by that budget for every object-held row. Each write is a compare-and-swap on the stored hash and signature, and a rerun plans every row again.

When Helm stops waiting while the Job still runs, the Job keeps running. Wait for it with `kubectl wait job/podium-podium-migrate --for=condition=Complete --timeout=6h`. When that wait times out, read `kubectl get job podium-podium-migrate -o jsonpath='{.status.failed}'`: a value of `1` or more means the run failed, and the rows above apply. Run step 5 only after the wait returns `condition met`.

### Service mesh sidecars

On a service mesh that injects a sidecar into the Job pod, a sidecar that keeps running after the command exits holds the Job open until Helm times out. Native sidecars, on Kubernetes 1.28 or later with the mesh's native-sidecar mode, stop with the main container and need no change.

Otherwise, set the mesh's opt-out in `migration.podAnnotations`, such as `sidecar.istio.io/inject: "false"` or `linkerd.io/inject: disabled`, when the store is reachable without the mesh. Istio reads the `sidecar.istio.io/inject` pod label ahead of the annotation of the same name. When `podLabels` enables injection through that label, set the opt-out label in `migration.podLabels` instead. The Job pod takes `podLabels` and `podAnnotations`, and `migration.podLabels` and `migration.podAnnotations` win over them per key. The chart sets no opt-out by default, because a mesh that enforces mTLS to Postgres or to the object store would then block the Job.

### Rollback

`helm rollback` after step 4 reinstalls the v0.4.0 binary over rows it cannot read, and the chart cannot block it. Roll back only through the step 2 backup:

1. Stop the registry.
2. Restore Postgres and the object store from that backup.
3. Run `helm rollback podium <revision>` with the v0.4.0 revision noted in step 1.
4. Delete the migrate Job:

```bash
kubectl delete job -l app.kubernetes.io/instance=podium,app.kubernetes.io/component=migrate
```

The hook Job is outside the release manifest, so it also remains after `helm uninstall`, and the same command removes it.

### GitOps controllers

The chart reads nothing from the cluster, so a controller that renders it with `helm template`, such as Argo CD, renders the manifests `helm upgrade` renders from the same values. Argo CD runs the migrate Job, a Helm `post-install` and `post-upgrade` hook, as a PostSync hook, and applies `before-hook-creation` to it, so each sync that renders the Job replaces the previous one. Run the upgrade as four value commits, and let each sync finish before the next commit:

1. Commit `replicaCount: 0` with this release's image, the signing Secret, or `signing.mode: none` with `migration.includeUnsigned: false`, and `migration.previousImage`. After the sync, run the `kubectl wait` of step 2 and take the backup.
2. Commit `migration.mode: dry-run`, and capture the Job's log to `dry-run.log` as step 3 does once the Job appears. Review it as step 3 states.
3. Commit `migration.mode: run` and `migration.planDigest: <digest>`, and stream the Job's log to `run.log`.
4. Commit the serving values, without `migration.mode` or `migration.planDigest`.

For the dry-run and run commits, run the first line below before pushing the commit, so the capture reads the new Job rather than the previous one. Run the other two lines after pushing it. The run commit writes the log to `run.log` in place of `dry-run.log`:

```bash
kubectl delete job podium-podium-migrate --ignore-not-found --wait=true
until kubectl get job podium-podium-migrate >/dev/null 2>&1; do sleep 2; done
kubectl logs -f job/podium-podium-migrate --pod-running-timeout=10m > dry-run.log
```

A sync that repeats a hook reruns the committed step. A repeated dry run writes nothing, and a repeated run after a successful one exits with status 3 and writes nothing, because the recorded completion changes its plan.

---

## Related pages

- [Server-side integrations](integrations): the backing services and their alternatives.
- [Connecting layer sources](layer-sources): hooking Git repositories and local paths into a registry.
- [Layered composition](layers): composing the catalog from several sources.
- [Access control](access-control): declaring and debugging who can see what.
- [Audit and observability](observability): audit destinations, metrics, tracing, and notifications.
- [Operator guide](operator-guide): capacity, backup and restore, upgrades, security review, and troubleshooting.
- [Audit and observability](observability): audit destinations, metrics, tracing, notifications, and alerts.
- [Progressive adoption](progressive-adoption): staged on-ramp for governance features.
- [Extending](extending): SPI plugins, the forward-compatibility constraints, and external-extension patterns.
- [OIDC cookbooks](oidc/): per-IdP setup recipes.
