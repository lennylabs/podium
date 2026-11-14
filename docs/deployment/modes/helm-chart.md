---
title: Helm chart reference
nav_order: 4
description: "The values of the Podium Helm chart: signing, stored-row migration, audit destinations, and the multi-tenant startup refusals."
---

# Helm chart reference


This section describes how the chart's values reach the registry pod. [Deploy the registry](clustered#2-deploy-the-registry) gives the install procedure.

## Signing values

A render that names no signing Secret fails with a message naming `signing.secretName`. The chart installs with its other defaults once `signing.secretName` names a Secret, or once `signing.mode=none` turns signing off.

- A consumer of a registry with signing off, such as `podium-mcp` or a server-source `podium sync` in CI, sets `PODIUM_VERIFY_SIGNATURES=never`.
- A server-source `podium sync` against a signing registry sets `PODIUM_SIGNATURE_VERIFY_KEY`.

An upgrade of a store that already holds signed rows creates the Secret from the key that signed them. The registry refuses every row whose envelope does not verify under a key of its verification key set: the key file's `public:` key and every `verify:` key. An upgrade from v0.4.0 runs the stored-row migration before any pod of the new release serves, as [Upgrading the chart from v0.4.0](../operations/upgrade-notes#upgrading-the-chart-from-v040) shows.

Every registry process resolves `PODIUM_SIGN_KEY_PATH` to a key file on the store's persistent storage, and the chart mounts that one file from its Secret. A hand-rolled deployment arranges the same. A hand-rolled deployment with signing on, a Postgres store, and no `PODIUM_SIGN_KEY_PATH` is refused at start.

## Stored-row migration at start

In either signing mode, unless the store is the SQLite store in the key file's directory, a registry pod checks the store at start. A pod over a store that holds manifest rows and has not completed this release's stored-row migration exits at start, and its log names `sign-stored-rows`. It rewrites no manifest row, signs no row, and records no completion. A pod over a store that holds no manifest row records the migration at its first start. An install over a store that v0.4.0 wrote follows [Upgrading the chart from v0.4.0](../operations/upgrade-notes#upgrading-the-chart-from-v040).

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
- Setting `config.endpoint` has no effect.
- Setting `config.embeddingProvider.model` has no effect.

## Audit destination values

`config.auditLogPath` carries a value by default, and its `stdout` default renders as an `env:` entry. It therefore overrides a `PODIUM_AUDIT_LOG_PATH` that the secret carries. An installation that set `PODIUM_AUDIT_LOG_PATH` in the secret moves the value to `config.auditLogPath` on upgrade, or its audit records go to the container log instead of its receiver.

[Configure the Helm chart](../operations/observability#configure-the-helm-chart) covers the same values as part of the audit setup.

An explicit empty `config.auditLogPath` renders no `PODIUM_AUDIT_LOG_PATH`, so the secret's value applies when the secret carries one. When the secret carries none, each pod, the migrate Job's included, falls back to `~/.podium/audit.log`, which the read-only root filesystem cannot hold. Each registry pod and the migrate Job's `sign-stored-rows` run then refuse startup with `config.audit_sink_unavailable`, naming `PODIUM_AUDIT_LOG_PATH` and the path, whatever the anchoring setting.

An `extraEnv` entry that sets `PODIUM_TRACING=stdout` beside a `stdout` audit destination also refuses startup with `config.audit_sink_unavailable`, because both would write to standard output.

The HTTP and OTLP audit destinations take their request headers, CA bundle, and client certificate from files. Set `audit.transportSecret.name` to a Secret, and list the keys it holds in `audit.transportSecret.keys`:

| Key | Contents |
|:--|:--|
| `headers` | One `Name: value` request header per line. |
| `ca.crt` | A PEM CA bundle that replaces the system trust store. |
| `tls.crt` and `tls.key`, listed together | A mutual-TLS client certificate and its key. |

The chart mounts the Secret read-only at `/etc/podium/audit` in the registry pods and the migrate Job. It sets `PODIUM_AUDIT_HEADERS_FILE`, `PODIUM_AUDIT_TLS_CA_FILE`, `PODIUM_AUDIT_TLS_CERT_FILE`, and `PODIUM_AUDIT_TLS_KEY_FILE` for the keys it lists. A destination URL that carries userinfo refuses startup, so credentials go in the `headers` key.

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

---
