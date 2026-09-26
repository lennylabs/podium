---
title: Clustered
nav_order: 3
description: Replicated registry behind a load balancer, backed by Postgres, object storage, and an OIDC IdP. Adds multi-tenancy, freeze windows, hash-chained audit, and SCIM.
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
- **Signing.** The registry signs every artifact at ingest with its registry-managed Ed25519 key by default. In steady state every replica signs under the one key the chart mounts from one Secret (see [Deploy the registry](#2-deploy-the-registry)). During a rotation, every replica trusts the new key before any replica signs under it, as [Rotating the signing key across replicas](#rotating-the-signing-key-across-replicas) states. No registry signing mode produces a Sigstore-keyless envelope. Each `podium-mcp` consumer verifies what it loads under `PODIUM_VERIFY_SIGNATURES`, whose values are `always`, the default, and `never`.
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

For a quick stand-up, the repo ships a `docker-compose.yml` that brings up the evaluation stack with `docker compose up -d`: the registry, a pgvector Postgres, MinIO object storage, a Dex OIDC IdP, and a one-shot bootstrap container that creates the MinIO bucket. The stack sets `PODIUM_NO_EMBEDDINGS`, so search runs over manifest text with no embedding provider and no API key. Remove that variable and supply a provider with its key to exercise hybrid search. The registry seeds the first tenant and admin grant itself at boot, from the default tenant plus the identity in `PODIUM_BOOTSTRAP_ADMINS`. The registry service selects no identity provider, so it authenticates no caller and the seeded grant is unreachable until an operator configures one; the service publishes its port on the host loopback interface for that reason. The stack pins `PODIUM_SIGN: "none"`, because it has no key management and a recreated registry container would otherwise generate a fresh signing key on every recreate, so a `podium-mcp` consumer pointed at it sets `PODIUM_VERIFY_SIGNATURES=never`. The stack runs single-replica services with default credentials on local volumes, so it is unsuitable for production. It wires the same components a clustered deployment wires, so consumers exercise the same code paths.

### 2. Deploy the registry

The chart lives at `deploy/helm/podium`. Its templates render the backend selectors from the `config.*.type` values and read every credential and per-backend setting from the Kubernetes secret named by `existingSecret`.

The registry signs at ingest by default, and every replica signs under a key that every other replica's verification key set contains. In steady state every replica signs under the one key the chart mounts from one Secret, and during a rotation every replica trusts the new key before any replica signs under it (see [Rotating the signing key across replicas](#rotating-the-signing-key-across-replicas)). The chart therefore mounts an operator-supplied signing key from a Secret rather than letting each pod generate its own. Generate the key file in a scratch directory:

```bash
KEY_DIR="$(mktemp -d)"
podium admin signing-key generate --key-file "$KEY_DIR/registry-signing.key"
```

`podium admin signing-key generate` writes only the file `--key-file` names, reads no environment variable and no default path, opens no store, and refuses to replace an existing file. It prints the registry's verification key set, which is one key for a new file, on its first line, and a `key_id=<hex> role=signing` line after it. The key file, `$KEY_DIR/registry-signing.key`, is written with mode `0600` and carries a `private:` line and a `public:` line. Copy it into the deployment's backup, because the registry refuses every row the key signed once the key is lost and no `verify:` line lists its public half, and delete `$KEY_DIR` after the Secret below is created.

Every consumer of the deployment verifies under the public half. Extract it:

```bash
awk '/^public:/{print $2}' "$KEY_DIR/registry-signing.key"
```

The output is the base64 text after the `public:` prefix, without the prefix, which is the form `PODIUM_SIGNATURE_VERIFY_KEY` requires and the same key as the first line `generate` printed. Every consumer sets it in `PODIUM_SIGNATURE_VERIFY_KEY` (see [Configure your harness](../consuming/configure-your-harness)).

Create the Secrets and install the chart:

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
  --set signing.secretName=podium-signing-key \
  --set migration.storeReady=true
```

`migration.storeReady=true` states that the store is empty or has already completed this release's stored-row migration. A `helm install` with `replicaCount` above zero fails without it, and so does a `helm upgrade` with `replicaCount` above zero that finds no live Deployment, because the chart cannot see the store and a store outlives `helm uninstall`. A `helm install` at zero replicas requires either this value or `migration.previousImage`. An install over a store that v0.4.0 wrote follows [Upgrading the chart from v0.4.0](#upgrading-the-chart-from-v040) instead.

A render that names no signing Secret fails with a message naming `signing.secretName`. The chart installs with its other defaults once `signing.secretName` names a Secret, or once `signing.mode=none` turns signing off; a consumer of a registry with signing off sets `PODIUM_VERIFY_SIGNATURES=never`. An upgrade of a store that already holds signed rows creates the Secret from the key that signed them, because the registry refuses every row whose envelope does not verify under a key of its verification key set: the key file's `public:` key and every `verify:` key. An upgrade from v0.4.0 runs the stored-row migration before any pod of the new release serves, as [Upgrading the chart from v0.4.0](#upgrading-the-chart-from-v040) shows. Every registry process resolves `PODIUM_SIGN_KEY_PATH` to a key file on the store's persistent storage, and the chart mounts that one file from its Secret. The chart satisfies that, a hand-rolled deployment arranges it, and a hand-rolled deployment with signing on, a Postgres store, and no `PODIUM_SIGN_KEY_PATH` is refused at start.

Among those defaults, `config.identityProvider.type` is `oidc-jwt`, which the registry verifies at request time; supply its issuer and audience through the secret named in `existingSecret`, which reaches the pod via `envFrom`. Hybrid search is off by default (`config.vectorBackend.type` and `config.embeddingProvider.type` are both `none`) so the registry starts without an embedding-provider credential; set both and supply the key in the same secret to turn it on. The container `env:` block takes precedence over `envFrom:`, so any value the chart renders as `env:` is set through `--set` rather than through the secret.

The templates set `PODIUM_BIND`, `PODIUM_REGISTRY_STORE`, `PODIUM_OBJECT_STORE`, `PODIUM_VECTOR_BACKEND`, `PODIUM_EMBEDDING_PROVIDER`, `PODIUM_IDENTITY_PROVIDER`, and `PODIUM_SIGN` on every install, from `config.bind`, those `type` values, and `signing.mode`, and they set `PODIUM_SIGN_KEY_PATH` whenever `signing.mode` is `registry-key`. Because they always render, blanking one in `values.yaml` emits an empty value that shadows the secret rather than deferring to it. Leave them at their defaults or set them with `--set`.

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
- `PODIUM_SIGNATURE_PROVIDER=registry-managed`, the default, and `PODIUM_SIGNATURE_VERIFY_KEY` set to the registry's public key, extracted as [Deploy the registry](#2-deploy-the-registry) shows. Without it, and without `PODIUM_VERIFY_SIGNATURES=never`, `podium-mcp` refuses to start with `config.signature_provider_unavailable`.
- Consumer paths run as in [Configure your harness](../consuming/configure-your-harness).
- Effective view composes admin layers (visibility-filtered) + user-defined layers + workspace local overlay.

---

## Upgrading the chart from v0.4.0

This release changes the stored content hash of every row (see the upgrade note in `CHANGELOG.md`). The first registry start on this release rewrites every stored row before it serves, and the rewrite requires every registry process on the previous release to be stopped first. The chart runs that rewrite as a Kubernetes Job while the Deployment is held at zero replicas, so no probe restarts the pass and no v0.4.0 pod writes beside it. The Job runs `podium-server sign-stored-rows`, which performs the whole rewrite in place of the first start when the store holds no record of it, and records its completion. Registry pods that start afterwards find the record and skip the pass.

The chart renders the Job as a Helm `post-upgrade` hook when `migration.mode` is `dry-run` or `run`. Render-time checks refuse each step that would break the procedure:

- The migrated-store gate refuses a `dry-run` or `run` render once the live Deployment carries the `podium.lennylabs.dev/stored-row-format: content_hash_framing` annotation, which step 5 writes. The store has then completed the migration, and a rerun of `sign-stored-rows` runs on a serving pod, as [Later upgrades](#later-upgrades) shows.
- The stop gate refuses a `dry-run` or `run` render while any registry pod of the release is running, pending, or terminating. A pod in phase `Succeeded` or `Failed`, such as an evicted pod, runs no process and does not count. When the live Deployment still has replicas, the refusal names the zero-replica step of step 2, because a wait for the pods to be deleted never returns until that step runs.
- The image gate refuses a `dry-run` or `run` render whose image is the one the Deployment ran before the migration. The v0.4.0 `podium-server` ignores its arguments, so a Job on that image would start a full v0.4.0 registry that never exits.
- The reviewed-dry-run gate refuses a `run` render unless `migration.reviewedDryRun` names the UID of a succeeded dry-run Job from the immediately preceding release revision of the live Deployment, with the same image, the same `migration.includeUnsigned` setting, and the same pod configuration. The pod configuration covers the `existingSecret` reference, the environment (the store, object-store, and signing values among it), the mounts, the volumes, and the `uid` and `resourceVersion` of every Secret the pod reads: `existingSecret`, `signing.secretName`, `runtimeKeys.secretName` when runtime keys are on, `postgresql.existingSecret` when the bundled database is on, and each Secret an `extraEnv` entry reads through `secretKeyRef`. A run under a changed object-store endpoint, an edited DSN in `existingSecret`, or a replaced signing key therefore cannot attest rows the dry run did not list. Any write to a referenced Secret after the dry run, including one that leaves its data unchanged, fails the run, and step 3 is rerun.
- The serving preflight refuses a render that scales the Deployment above zero over a store the migration has not completed. It passes when the live Deployment carries the `podium.lennylabs.dev/stored-row-format: content_hash_framing` annotation, or when the migrate Job is a succeeded `run` from the immediately preceding release revision of the live Deployment with the same image and the same pod configuration. The pod-configuration condition, which includes the version of every referenced Secret, refuses a serving render whose values or Secrets point at a store other than the one the run migrated. A `helm upgrade` that finds no live Deployment, such as one after the Deployment was deleted by hand, cannot tell whether the store is migrated, so it requires `migration.storeReady=true`, as an install does. The preflight writes the annotation on the Deployment when it passes.
- A `dry-run` or `run` render requires `replicaCount=0`, `signing.mode=registry-key`, and `helm upgrade`.

The checks read the cluster through Helm's `lookup`, so the Helm user needs `get` on Deployments, Jobs, and Secrets and `list` on Pods in the release namespace. The Secret read returns each referenced Secret's metadata to the render, which records only its `uid` and `resourceVersion`. Without those permissions the render fails with Helm's permission error. `migration.preflight=false` turns the lookup checks off and asserts that the operator performed each of them by hand. A render with `preflight=false` writes the `stored-row-format` annotation only when it scales the Deployment above zero, so a zero-replica render with it cannot mark the store migrated before the run.

The stop gate sees only the registry pods of this release in this namespace. A registry process on the same store under another release name, in another namespace, or outside Kubernetes is invisible to it, so stop every such process before step 3.

Every step below passes the same values file with `-f` and sets its per-step values with `--set`. No step passes `--reuse-values`, so no per-step value carries into the next command. No step passes `--rollback-on-failure` (Helm v4) or `--atomic` (Helm v3), because a rollback reinstalls the v0.4.0 binary over a rewritten store, and the only route back to v0.4.0 is the backup restore that [Rollback](#rollback) describes. Helm waits on a hook Job only up to `--timeout`, whose default is 5 minutes, so every step that runs the Job passes a `--timeout` larger than the time the pass takes, and larger than `migration.activeDeadlineSeconds` when that value is set.

The commands assume the release `podium` in the current namespace, the signing Secret `podium-signing-key`, and the values file `podium-values.yaml`.

### Upgrade with signing on

**Step 1.** Prepare the values file and the signing Secret.

```bash
helm get values podium -o yaml > podium-values.yaml
```

In `podium-values.yaml`, set `image.repository` and `image.tag` to this release's published image, and add `signing.secretName: podium-signing-key`. `helm get values` returns the v0.4.0 image, and the image gate refuses a migration render that keeps it. Create the signing Secret as [Deploy the registry](#2-deploy-the-registry) shows. To keep the `artifact.signed` audit events the run appends for every row it signs, set `config.auditLogPath` to an `http(s)` endpoint, because the default file lands on the pod's read-only root filesystem and the events are dropped.

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

A scaled-down Deployment stops counting a pod while the pod is still in its termination grace period and can still write to the store, so the `kubectl wait --for=delete` line waits for the pods themselves. The field selector skips pods in a terminal phase, such as evicted pods, which a scale-down never deletes. This render records the v0.4.0 image in the Deployment's `podium.lennylabs.dev/pre-migration-image` annotation, which the image gate reads. Back up Postgres and the object store (the bucket or the objects volume) once the wait returns. That backup holds every write the v0.4.0 binary made.

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

The first line removes a Job a previous attempt left, so the capture reads the new Job's log. The loop also ends when `helm` exits, because a render check that refuses the step exits before any Job exists. The last line returns the exit status of `helm upgrade`; a non-zero status with a gate's refusal on the terminal means the step did not run. The log is streamed to a local file while the Job runs, because the kubelet rotates container logs (at 10 MiB by default) and a `kubectl logs` after the Job ends returns only the current file. Check that the capture is complete:

```bash
grep -cE '^dry-run: .* class=' dry-run.log
grep -E '^dry-run: [0-9]+ row\(s\) planned' dry-run.log
```

The first count equals `N` in the `dry-run: N row(s) planned` line of a complete capture. When they differ, rerun this step. Then review `dry-run.log`:

- Each row with `sign=true` and `signed_by=unsigned` is an unsigned row that step 4 attests under `--include-unsigned`. Confirm that each such row is one the deployment stored. A row the operator cannot account for stops the upgrade.
- No row reports `class=body_unavailable`. Such a row means an object-store read failed, and the run would leave the completion record unset.
- No row reports `class=signature_unverified` or `signed_by=unverified`. Such a row means that the signing Secret holds, neither as its key nor as a `verify:` key, the key that signed the stored rows. The run would leave every such row at its previous content hash, record completion, and exit 0, and the registries would then refuse those rows. Fix the Secret and rerun this step.
- No row reports `class=unreproducible` or `class=body_missing`. The run leaves such a row at its previous content hash and still records completion, so account for each one before step 4.
- The `would be signed` count in the summary line matches the rows reviewed.

Keep `dry-run.log` as the record of the reviewed plan, and read the dry-run Job's UID:

```bash
kubectl get job podium-podium-migrate -o jsonpath='{.metadata.uid}'
```

`migration.includeUnsigned` is true by default, so the dry run and the run both pass `--include-unsigned`. A deployment that does not attest its unsigned rows sets `migration.includeUnsigned=false` for both steps, and those rows stay unsigned and are refused with `materialize.signature_missing` until they are signed or republished.

**Step 4.** Run the reviewed plan.

```bash
helm upgrade podium ./deploy/helm/podium -f podium-values.yaml --set replicaCount=0 \
  --set migration.mode=run --set migration.reviewedDryRun=<UID> --timeout 6h
kubectl logs job/podium-podium-migrate
```

The reviewed-dry-run gate requires the step 3 Job to have succeeded under the same image, `migration.includeUnsigned` setting, and pod configuration, with no release revision in between. A change to `podium-values.yaml` after step 3 that reaches the pod, such as a corrected object-store endpoint, fails this step, and so does any edit to a Secret the pod reads, such as the DSN in `existingSecret` or the signing Secret. Rerun step 3 with the corrected file or Secret and review its log. The log carries the `rehash:` summary line and, with `migration.includeUnsigned` true, a `rehash: 0 unsigned left` line. The command exits 0 only when the rewrite is complete and its completion is recorded. When it exits non-zero, read [Recovering from a failed run](#recovering-from-a-failed-run).

**Step 5.** Serve.

```bash
helm upgrade podium ./deploy/helm/podium -f podium-values.yaml
```

The serving preflight finds the succeeded run Job from the preceding revision, writes the `stored-row-format` annotation on the Deployment, and the Deployment scales to `replicaCount`. The pods find the completion record and skip the pass, so the chart's startup budget covers their start. Any other upgrade between steps 4 and 5 invalidates the run Job for the preflight. Rerun steps 3 and 4 in that case. Over a store step 4 migrated, the dry run plans no write and the run rewrites no row, and the run records nothing new. Steps 3 and 4 render only until step 5 writes the `stored-row-format` annotation; after that the migrated-store gate refuses them.

### Install over an existing v0.4.0 store

A new release name, a new namespace, or a reinstall after `helm uninstall` over a store v0.4.0 wrote is an upgrade from the store's point of view. The objects volume carries `helm.sh/resource-policy: keep`, and the bundled Postgres keeps its volume claim, so both survive `helm uninstall`. Stop every v0.4.0 process that uses the store. Write `podium-values.yaml` and create the signing Secret as step 1 of [Upgrade with signing on](#upgrade-with-signing-on) describes, without `helm get values`, because no release exists to read the values from. Install at zero replicas on this release's image, with `migration.previousImage` naming the image v0.4.0 ran, and then follow [Upgrade with signing on](#upgrade-with-signing-on) from step 2, or, with `signing.mode=none`, [Upgrade with signing off](#upgrade-with-signing-off) from its step 2:

```bash
helm install podium ./deploy/helm/podium -f podium-values.yaml --set replicaCount=0 \
  --set migration.previousImage=ghcr.io/lennylabs/podium-server:v0.4.0
```

An install has no live Deployment to read the previous image from, so a `helm install` at zero replicas requires `migration.previousImage` and records it in the `pre-migration-image` annotation, and the image gate then refuses a migrate Job on that image. Name the image the v0.4.0 registry ran. Do not set `migration.storeReady=true` for such a store, because that value states that the store is empty or already migrated.

### Upgrade with signing off

`sign-stored-rows` refuses with `signing.mode=none`, so a signing-off deployment runs the rewrite at the first start instead. `migration.unprobedStart=true` renders the Deployment without the startup and liveness probes, with one replica and the `Recreate` strategy, so the kubelet never restarts the pod during the pass. The readiness probe stays, so the pod receives no traffic until it serves.

**Step 1.** Run steps 1 and 2 of [Upgrade with signing on](#upgrade-with-signing-on), with `signing.mode: none` in the values file in place of the signing Secret. Step 2 must complete first, because its render gives the release ownership of `strategy.rollingUpdate`, which the switch to `Recreate` removes.
**Step 2.** Run the pass at boot.

```bash
helm upgrade podium ./deploy/helm/podium -f podium-values.yaml --set replicaCount=1 \
  --set strategy.type=Recreate --set migration.unprobedStart=true
kubectl logs -f deployment/podium-podium
```

Wait for the `rehash:` summary line and confirm that it reports no `body_unavailable` row. When it reports held rows, fix the cause and delete the pod; the next start repeats the pass.
**Step 3.** Restore the probes and the strategy, and serve.

```bash
helm upgrade podium ./deploy/helm/podium -f podium-values.yaml --set migration.preflight=false
```

`migration.preflight=false` asserts that the pass completed, so the render writes the `stored-row-format` annotation without a run Job. Because the step passes the values file with `-f`, the value does not persist into a later upgrade.

### Later upgrades

A later upgrade runs `helm upgrade podium ./deploy/helm/podium -f podium-values.yaml` with the new image tag. The live Deployment carries the annotation, no Job renders, and the default rolling update applies. A release whose changelog names a new stored-value migration changes the annotation's value, and it follows this procedure again. Key rotation runs `sign-stored-rows` on a serving pod with `kubectl exec`, as [Rotating the signing key across replicas](#rotating-the-signing-key-across-replicas) shows, and no gate applies to it. A rerun of the migration command after step 5 takes the same route, with a dry run first:

```bash
kubectl exec deployment/podium-podium -- /usr/local/bin/podium-server sign-stored-rows --include-unsigned --dry-run
kubectl exec deployment/podium-podium -- /usr/local/bin/podium-server sign-stored-rows --include-unsigned
```

Over the migrated store, the rerun rewrites no content hash. `--include-unsigned` still attests every unsigned row the store holds, such as a row a failed run held back, so review each row the dry run lists with `signed_by=unsigned` and `sign=true` before the second command. A deployment that set `migration.includeUnsigned=false` drops `--include-unsigned` from both commands.

### Recovering from a failed run

A run Job that fails leaves the completion record unset and the Deployment at zero replicas, and `helm upgrade` exits non-zero. The serving preflight refuses to scale the Deployment until a later run succeeds. An object-store outage during the run holds each object-held row back as `body_unavailable` and fails the Job. Each such row spends the object-store client's retry budget before it is held, so an outage lengthens the run by that budget for every object-held row. A held row also stays unsigned, and the log's `rehash: N unsigned left; run sign-stored-rows --include-unsigned to sign them` line names the command the Job runs. On the chart, fix the cause, then rerun steps 3 and 4. When the log shows object-store reads that timed out, raise `config.migrationObjectReadTimeout`, which sets `PODIUM_MIGRATION_OBJECT_READ_TIMEOUT` (default `30s`) on the Job pod, in `podium-values.yaml` before the rerun. When rows point at the wrong object-store root, correct the object-store values or Secret. Each write is a compare-and-swap on the stored hash and signature, and the rerun plans every row again.

A Job that runs out of memory or reaches `migration.activeDeadlineSeconds` fails the same way. Raise `migration.resources.limits.memory` or the deadline, and rerun steps 3 and 4. When Helm stops waiting at `--timeout` while the Job still runs, the Job keeps running. Wait for it with `kubectl wait job/podium-podium-migrate --for=condition=Complete --timeout=6h`. When that wait times out, read `kubectl get job podium-podium-migrate -o jsonpath='{.status.failed}'`: a value of `1` or more means the run failed, and the paragraph above applies. Run step 5 only after the wait returns `condition met`.

A backup restore after step 5 returns the store to a state with no completion record, while the Deployment still carries the `podium.lennylabs.dev/stored-row-format` annotation. The serving preflight then passes, and the pods would run the whole rewrite at boot under the startup probe. Before the restored store serves, hold the release at zero replicas and wait for its pods to be deleted, as step 2 of [Upgrade with signing on](#upgrade-with-signing-on) shows, then remove the annotation and record the previous image:

```bash
kubectl annotate deployment/podium-podium podium.lennylabs.dev/stored-row-format- \
  podium.lennylabs.dev/pre-migration-image=ghcr.io/lennylabs/podium-server:v0.4.0 --overwrite
```

Then rerun steps 3 to 5. A restore that keeps the v0.4.0 release instead follows [Rollback](#rollback).

### Service mesh sidecars

On a service mesh that injects a sidecar into the Job pod, a sidecar that keeps running after the command exits holds the Job open until Helm times out. Native sidecars, on Kubernetes 1.28 or later with the mesh's native-sidecar mode, stop with the main container and need no change. Otherwise set the mesh's opt-out in `migration.podAnnotations`, such as `sidecar.istio.io/inject: "false"` or `linkerd.io/inject: disabled`, when the store is reachable without the mesh. Istio reads the `sidecar.istio.io/inject` pod label ahead of the annotation of the same name, so when `podLabels` enables injection through that label, set the opt-out label in `migration.podLabels` instead. The Job pod takes `podLabels` and `podAnnotations`, and `migration.podLabels` and `migration.podAnnotations` win over them per key. The chart sets no opt-out by default, because a mesh that enforces mTLS to Postgres or to the object store would then block the Job.

### Rollback

`helm rollback` after step 4 reinstalls the v0.4.0 binary over rows it cannot read, and the chart cannot block it. Roll back only through the step 2 backup: stop the registry, restore Postgres and the object store from that backup, run `helm rollback podium <revision>` with the v0.4.0 revision noted in step 1, and delete the migrate Job:

```bash
kubectl delete job -l app.kubernetes.io/instance=podium,app.kubernetes.io/component=migrate
```

The hook Job is outside the release manifest, so it also remains after `helm uninstall`, and the same command removes it. The serving preflight accepts a run Job only from the immediately preceding release revision, so it does not accept a leftover Job after a rollback. A reinstall after `helm uninstall` restarts the revision numbers at 1, so the Job also records the UID of the Deployment it ran against, and the gates ignore a Job whose recorded UID differs from the live Deployment's.

### GitOps renderers

A controller that renders the chart with `helm template`, such as Argo CD and some Flux settings, skips `lookup`, so the image and reviewed-dry-run gates fail closed, the stop gate checks nothing, and the serving preflight checks only `migration.storeReady`. Such a controller also maps Helm hooks to its own semantics. It is unsupported for this upgrade. Its values carry `migration.storeReady: true` permanently, because it renders every revision as an install. The `kubectl wait` line of step 2 remains the stop check for an operator who renders without cluster access.

---

## Signing key operations

### Rotating the signing key across replicas

The [Operator guide](operator-guide#rotating-the-signing-key) gives the rotation procedure. Across replicas, every replica trusts the new key before any replica signs under it, so the chart's Secret changes twice:

1. Copy the current key file out of the Secret into a scratch directory, with `kubectl get secret podium-signing-key -o jsonpath='{.data.registry-signing\.key}' | base64 -d`, and write it with mode `0600`.
2. Run `podium admin signing-key rotate --key-file <copy> --staged-out <staged>`. The staged file is the previous key file plus a `verify:` line for the new key, and the copy becomes the rotated file: the new signing key plus a `verify:` line for each previous key. Add the new key to every consumer's `PODIUM_SIGNATURE_VERIFY_KEY`, for example as the comma-separated list the command prints on its first line.
3. Replace the Secret's content with the staged file, with `kubectl create secret generic podium-signing-key --from-file=registry-signing.key=<staged> --dry-run=client -o yaml | kubectl apply -f -`, restart the Deployment with `kubectl rollout restart deployment/podium-podium`, and wait for the restart to finish with `kubectl rollout status deployment/podium-podium`. Every replica still signs under the previous key and now trusts the new one. Start step 4 only after the rollout status command returns, because a pod from before the restart does not trust the new key and refuses the rows a pod signing under it ingests.
4. Replace the Secret's content with the rotated file, restart the Deployment again with `kubectl rollout restart deployment/podium-podium`, and wait with `kubectl rollout status deployment/podium-podium`. Every replica now signs under the new key and trusts the previous one. Start step 5 only after the rollout status command returns, because a pod from before this restart still signs under the previous key, and a row it signs after a step 5 run is not counted by that run.
5. Run `kubectl exec deployment/podium-podium -- /usr/local/bin/podium-server sign-stored-rows`, which re-signs the stored rows under the new key while the replicas serve.
6. For each consumer that runs with `PODIUM_CACHE_MODE=offline-only`, keep the previous key in its set, or clear its cache directory and refill it in `always-revalidate` or `offline-first`, before the previous key leaves that consumer's set, because an `offline-only` consumer refuses a cached delivery signature made under a key it no longer trusts.
7. Remove the previous key only after a run of step 5 that exits 0 reports `0 row(s) still signed under it` for its `key_id`: delete its `verify:` line from the key file, replace the Secret's content, restart the Deployment with `kubectl rollout restart deployment/podium-podium`, wait with `kubectl rollout status deployment/podium-podium`, and remove the key from every consumer's set.

Back up the rotated file, and delete the scratch copies once the Secret holds it.

## Migration from single node

`podium admin migrate-to-standard` exports a [single-node](single-node) deployment into the standard stack:

```bash
podium admin migrate-to-standard --postgres <dsn> --object-store <url>
```

The artifact directory is unchanged. Layer config moves from `~/.podium/registry.yaml` to the tenant config, and the same artifacts ingest into the new metadata store. After the export, switch consumer endpoints to the new registry URL and decommission the old host.

The target registry signs with the source's key. Before the target's first start after the migration, create the signing Secret from the source's key file, replacing any Secret created from another key, as [Single node](single-node#migrating-to-clustered) shows. A target registry with no key file is refused at start. A target holding any other key starts and leaves every copied signed row untouched. A row the source had already rewritten is then refused with `materialize.signature_invalid`, and creating the Secret from the source's key and restarting the target repairs it. A row a source that never started on this release still held at the previous content hash is refused with `materialize.content_hash_mismatch`, and it needs either the source key's public half on a `verify:` line of the target's key file, a restart, and a `sign-stored-rows` run, or the target store recreated empty and the migration run again with the source's key in place. [Single node](single-node#migrating-to-clustered) states both cases.

On a chart deployment, the target's rewrite runs in the migrate Job, because a first start that runs it under the startup probe can be restarted mid-pass on a large store. Run `migrate-to-standard` before any chart install on the target store, or while the chart release has not yet written the `podium.lennylabs.dev/stored-row-format` annotation. Then install at zero replicas with `migration.previousImage` set to an image other than this release's, such as `ghcr.io/lennylabs/podium-server:v0.4.0`, and follow steps 3 to 5 of [Upgrade with signing on](#upgrade-with-signing-on), or steps 2 and 3 of [Upgrade with signing off](#upgrade-with-signing-off) with `signing.mode=none`. Do not set `migration.storeReady=true`, because the copied rows have not completed the rewrite.

A chart release that already carries the `stored-row-format` annotation, such as one installed with `migration.storeReady=true` ahead of the migration, cannot see that the command cleared the store's record, so its serving preflight passes and its pods would run the rewrite at boot. Such a release does not serve the target until the rewrite completes. Hold it at zero replicas and wait for its pods to be deleted, as step 2 of [Upgrade with signing on](#upgrade-with-signing-on) shows, before the command runs. After the command exits 0, remove the annotation and record a previous image, then follow steps 3 to 5 of [Upgrade with signing on](#upgrade-with-signing-on), or steps 2 and 3 of [Upgrade with signing off](#upgrade-with-signing-off):

```bash
kubectl annotate deployment/podium-podium podium.lennylabs.dev/stored-row-format- \
  podium.lennylabs.dev/pre-migration-image=ghcr.io/lennylabs/podium-server:v0.4.0 --overwrite
```

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
