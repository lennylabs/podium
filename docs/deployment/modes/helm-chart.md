---
title: Helm chart reference
nav_order: 4
description: "The values of the Podium Helm chart, how they reach the registry pod, and the configurations the chart and the registry refuse."
---

# Helm chart reference

The Helm chart at `deploy/helm/podium` in the repository deploys a [clustered](clustered) registry: a Deployment of registry replicas, a Service, an optional Ingress, an optional evaluation Postgres, and a migrate Job for releases that rewrite stored rows. This page describes the chart's values and how they reach the registry. [Deploy the registry](clustered#2-deploy-the-registry) gives the install procedure, and `deploy/helm/podium/values.yaml` documents every value with its default.

## Installing

The chart installs from a checkout of the repository with `helm install <release> ./deploy/helm/podium`. An install needs two Secrets in the release namespace:

- The Secret that `existingSecret` names, `podium-secrets` by default. It holds the registry's credentials and per-backend settings, and it reaches the pod through `envFrom`.
- The signing Secret that `signing.secretName` names, unless `signing.mode` is `none`. [Signing values](#signing-values) describes it.

The chart runs `ghcr.io/lennylabs/podium-server` at the chart's `appVersion`, which is the release the checkout is at. Set `image.tag` to run another published release, or `image.repository` and `image.tag` to run an image from a private registry.

## Values

The following values select what the registry runs. The defaults are those in `values.yaml`.

| Value | Default | Effect |
|:--|:--|:--|
| `replicaCount` | `3` | The number of registry replicas. |
| `config.store.type` | `postgres` | The metadata store. Supply the DSN as `PODIUM_POSTGRES_DSN` in the secret, or set `postgresql.enabled`. |
| `config.objectStore.type` | `s3` | The object store. `config.objectStore.bucket`, `region`, `endpoint`, and `s3ForcePathStyle` configure S3 and S3-compatible services. `filesystem` requires `objects.enabled`, which mounts a persistent volume claim at `config.objectStore.filesystemRoot`. |
| `config.vectorBackend.type` and `config.embeddingProvider.type` | `none` | Hybrid search. Set both, and supply the provider's key in the secret, to turn it on. A registry that selects an embedding provider without its key exits at start. |
| `config.identityProvider.type` | `oidc-jwt` | The registry's identity provider. `config.identityProvider.issuer`, `audience`, `groupsClaim`, and `idpGroupMapping` configure `oidc-jwt`. An empty type selects no provider, so the registry resolves every caller as anonymous and applies no layer visibility. |
| `config.bootstrapAdmins` | Empty | Comma-separated identities granted the admin role in the `default` tenant at each start. |
| `config.defaultLayerVisibility` | Empty | The visibility of a layer registered without one. Empty keeps the registry's default. |
| `config.publicMode` and `config.allowPublicBind` | `false` | Public mode. [Multi-tenant startup refusals](#multi-tenant-startup-refusals) lists the combinations that fail. |
| `config.auditLogPath` | `stdout` | The audit destination list. [Audit destination values](#audit-destination-values) describes it. |
| `config.migrationObjectReadTimeout` | Empty | The deadline on each object-store read during the stored-row rewrite, as a Go duration. Empty keeps the registry's 30-second default. |
| `runtimeKeys.enabled` and `runtimeKeys.secretName` | Disabled | Mounts the runtime key set that the `injected-session-token` provider reads, and sets `PODIUM_RUNTIME_KEYS_PATH` to it. |
| `postgresql.enabled` | `false` | Runs a bundled single-replica Postgres for evaluation, with the password from `postgresql.existingSecret`. |
| `extraEnv` | Empty | Additional container environment entries, rendered after every other entry. |
| `strategy.type` | `RollingUpdate` | The Deployment strategy, `RollingUpdate` or `Recreate`. |
| `startupProbe.periodSeconds` and `startupProbe.failureThreshold` | `10` and `90` | The startup budget, fifteen minutes by default. |

Some registry settings have no chart value:

- Multi-tenant mode and the instance operators, `PODIUM_MULTI_TENANT` and `PODIUM_OPERATOR_ADMINS`, go in the secret or in `extraEnv`. [Tenants and admins](../identity/tenants) describes both.
- The SCIM 2.0 receiver's bearer tokens, `PODIUM_SCIM_TOKENS`, go in the secret. [Groups and SCIM](../identity/groups) describes the receiver.
- Configuration-file-only keys, such as the `layers:`, `freeze_windows:`, and `discovery:` blocks, have no environment variable. The registry reads them only from a `registry.yaml` file, which `PODIUM_CONFIG_FILE` can name, and the chart has no value that mounts one. [Configuration](../../reference/configuration) lists the keys and their variables.

## Signing values

The registry signs every artifact at ingest by default, and the chart mounts the signing key from a Secret rather than letting each pod generate its own. The Secret holds the key file that `podium admin signing-key generate --key-file <path>` writes, under the key that `signing.key` names, `registry-signing.key` by default. The chart mounts it read-only at `signing.mountPath`, `/signing` by default, and sets `PODIUM_SIGN_KEY_PATH` to the file. [Signing keys](../operations/signing-keys) covers generating, backing up, and rotating the key.

| Value | Default | Effect |
|:--|:--|:--|
| `signing.mode` | `registry-key` | `registry-key` signs at ingest. `none` turns signing off. |
| `signing.secretName` | Empty | The Secret that holds the key file. A render that names no signing Secret fails with a message naming `signing.secretName`, unless `signing.mode` is `none`. |
| `signing.key` | `registry-signing.key` | The key within the Secret that holds the key file. |
| `signing.mountPath` | `/signing` | The directory the Secret mounts at. |

Consumers of the registry configure verification to match:

- A consumer of a registry with signing off, such as `podium-mcp` or a server-source `podium sync` in CI, sets `PODIUM_VERIFY_SIGNATURES=never`.
- A server-source `podium sync` against a signing registry sets `PODIUM_SIGNATURE_VERIFY_KEY`.

An install over a store that already holds signed rows creates the Secret from the key that signed them. The registry refuses every row whose envelope does not verify under a key of its verification key set: the key file's `public:` key and every `verify:` key.

Every registry process resolves `PODIUM_SIGN_KEY_PATH` to a key file on the store's persistent storage, and the chart mounts that one file from its Secret. A hand-rolled deployment arranges the same. A hand-rolled deployment with signing on, a Postgres store, and no `PODIUM_SIGN_KEY_PATH` is refused at start, as [Signing keys](../operations/signing-keys#the-registry-refuses-to-start-because-the-signing-key-would-be-generated-under-the-processs-home) describes.

## Stored-row migration

A release that migrates stored values rewrites every stored row before it serves. In either signing mode, a registry pod over a store that holds manifest rows and has not completed the current release's stored-row migration exits at start, and its log names `sign-stored-rows`. [The registry refuses to start and names `sign-stored-rows`](../operations/signing-keys#the-registry-refuses-to-start-and-names-sign-stored-rows) describes the refusal. A pod over a store that holds no manifest row records the migration at its first start.

The chart runs the migration as a Job, `podium-server sign-stored-rows`, rendered as a Helm post-install and post-upgrade hook while the Deployment is held at zero replicas. The `migration` values control it:

| Value | Default | Effect |
|:--|:--|:--|
| `migration.mode` | `disabled` | `dry-run` lists every row the run would write and ends its log with the plan digest. `run` performs the rewrite and records its completion. Either mode requires `replicaCount=0` and `migration.previousImage`. |
| `migration.includeUnsigned` | `true` | Passes `--include-unsigned`, which signs the unsigned rows the run attests. The run uses the setting of the dry run whose digest it passes. `signing.mode=none` requires `false`. |
| `migration.planDigest` | Empty | The `sha256:` value on the `dry-run: plan digest` line of the reviewed dry run. `run` requires it. |
| `migration.previousImage` | Empty | The image the store's previous registry ran, such as `ghcr.io/lennylabs/podium-server:v0.4.0`. The render refuses to run the Job on that image. |
| `migration.activeDeadlineSeconds` | `0` | The Job's deadline. `0` sets none. Pass Helm a `--timeout` larger than this value. |
| `migration.resources`, `migration.podAnnotations`, and `migration.podLabels` | Empty | Per-key overrides for the Job pod, such as a larger memory limit or a service mesh sidecar opt-out. |

[Upgrade notes](../operations/upgrade-notes#upgrading-the-chart-from-v040) gives the procedure that sets these values step by step, including an install over a store that v0.4.0 wrote.

## How values reach the pod

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
- Setting `config.endpoint` has no effect. Set the registry's external URL as `PODIUM_PUBLIC_URL` in the secret instead, as [Exposing the registry](#exposing-the-registry) describes.
- Setting `config.embeddingProvider.model` has no effect.

## Audit destination values

`config.auditLogPath` renders `PODIUM_AUDIT_LOG_PATH` for the registry pods and the migrate Job. Its `stdout` default writes each audit record as one JSON line to the container log. [Configure the Helm chart](../operations/observability#configure-the-helm-chart) covers the destinations it can name.

The value renders as an `env:` entry, so it overrides a `PODIUM_AUDIT_LOG_PATH` that the secret carries. An installation that set `PODIUM_AUDIT_LOG_PATH` in the secret moves the value to `config.auditLogPath` on upgrade, or its audit records go to the container log instead of its receiver.

An explicit empty `config.auditLogPath` renders no `PODIUM_AUDIT_LOG_PATH`, so the secret's value applies when the secret carries one. When the secret carries none, each pod, the migrate Job's included, falls back to `~/.podium/audit.log`, which the read-only root filesystem cannot hold. Each registry pod and the migrate Job's `sign-stored-rows` run then refuse startup with `config.audit_sink_unavailable`, naming `PODIUM_AUDIT_LOG_PATH` and the path, whatever the anchoring setting.

An `extraEnv` entry that sets `PODIUM_TRACING=stdout` beside a `stdout` audit destination also refuses startup with `config.audit_sink_unavailable`, because both would write to standard output.

The HTTP and OTLP audit destinations take their request headers, CA bundle, and client certificate from files. Set `audit.transportSecret.name` to a Secret, and list the keys it holds in `audit.transportSecret.keys`:

| Key | Contents |
|:--|:--|
| `headers` | One `Name: value` request header per line. |
| `ca.crt` | A PEM CA bundle that replaces the system trust store. |
| `tls.crt` and `tls.key`, listed together | A mutual-TLS client certificate and its key. |

The chart mounts the Secret read-only at `/etc/podium/audit` in the registry pods and the migrate Job. It sets `PODIUM_AUDIT_HEADERS_FILE`, `PODIUM_AUDIT_TLS_CA_FILE`, `PODIUM_AUDIT_TLS_CERT_FILE`, and `PODIUM_AUDIT_TLS_KEY_FILE` for the keys it lists. A render that lists only one of `tls.crt` and `tls.key` fails. A destination URL that carries userinfo refuses startup, so credentials go in the `headers` key.

## Exposing the registry

The chart's Service is `ClusterIP` on port 8080 by default, so the registry is reachable only inside the cluster. The registry listens on plain HTTP, so TLS terminates in front of it. The chart offers two ways to expose it:

- **Ingress.** Set `ingress.enabled` to `true` and `ingress.host` to the registry's host name. `ingress.className` selects the ingress controller, `ingress.annotations` passes controller settings such as a cert-manager issuer, and `ingress.tls` takes the Ingress `tls:` list, each entry naming the hosts and the TLS Secret that serves them. `ingress.path` and `ingress.pathType` default to `/` and `Prefix`. A render with the Ingress enabled and no host fails.
- **Load balancer.** Set `service.type` to `LoadBalancer`, and set the cloud provider's TLS settings in `service.annotations`.

Set `PODIUM_PUBLIC_URL` in the secret to the external URL, such as `https://podium.acme.com`. The registry builds the absolute URLs it advertises from it, such as the webhook URL `podium layer register` returns, and falls back to `http://` and the bind address when it is unset.

## Multi-tenant startup refusals

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
