---
title: Backing services
nav_order: 5
description: The backing services a Podium registry uses, from the metadata store to notifications, with what ships by default and what can replace each one.
---

# Backing services

This page lists the backing services a Podium registry process uses and how to select each one. It is for operators planning a [single-node](../modes/single-node) or [clustered](../modes/clustered) deployment. The [local](../modes/local) deployment mode runs no registry process, so nothing on this page applies to it.

## Overview

Each integration has a default that a single-node deployment runs without extra infrastructure. An environment variable selects the backend per deployment, as the last column shows. The storage, search, and identity settings also have a matching `registry.yaml` key. The audit, tracing, and notification settings are environment variables only.

| Integration | Out of the box | Compatible alternatives | Selected by |
|:--|:--|:--|:--|
| [Metadata store](#metadata-store) | SQLite | Postgres | `PODIUM_REGISTRY_STORE` |
| [Object storage](#object-storage) | Local filesystem | S3 or any S3-compatible service | `PODIUM_OBJECT_STORE` |
| [Vector index](#vector-index) | `sqlite-vec` | `pgvector`, Pinecone, Weaviate Cloud, and Qdrant Cloud | `PODIUM_VECTOR_BACKEND` |
| [Embeddings](#embeddings) | `ollama`, falling back to BM25 when unreachable | OpenAI, Voyage, Cohere, and Ollama, or a self-embedding vector backend | `PODIUM_EMBEDDING_PROVIDER` |
| [Identity](#identity) | None | `oidc-jwt`, `trusted-headers`, and `injected-session-token` | `PODIUM_IDENTITY_PROVIDER` |
| [Layer sources](#layer-sources) | Git and local paths | Custom sources through the `LayerSourceProvider` SPI | Per layer, in the layer's config |
| [Audit log](#audit-log) | Hash-chained file | stdout, HTTP receivers, and an OpenTelemetry Collector | `PODIUM_AUDIT_LOG_PATH` |
| [Tracing](#tracing) | None | OTLP/HTTP and stdout | `PODIUM_TRACING` or `OTEL_EXPORTER_OTLP_ENDPOINT` |
| [Notifications](#notifications) | None | Webhook, email over SMTP, and log | `PODIUM_NOTIFICATION_PROVIDER` |

Nothing in the alternatives column is required to start. At cluster scale, Postgres and object storage become requirements, because registry replicas need shared state.

## Metadata store

The metadata store holds manifest metadata, dependency edges, layer config, and admin grants. `PODIUM_REGISTRY_STORE` selects it. Its interface is the `RegistryStore` SPI described in [Extending](extending).

- **SQLite** is the default. The file lives at `~/.podium/standalone/podium.db` unless `PODIUM_SQLITE_PATH` moves it. One process owns the file, so it does not survive being shared between replicas.
- **Postgres** is selected with `PODIUM_REGISTRY_STORE=postgres` and a DSN. Registry replicas share it, which is what makes horizontal scaling possible. The [clustered](../modes/clustered) deployment mode requires it.

`podium admin migrate-to-standard --postgres <dsn> --object-store <url>` exports a SQLite-backed deployment into Postgres and object storage.

### Signing key location

The registry signs every artifact at ingest by default. The key file defaults to `~/.podium/standalone/registry-signing.key`.

Two store settings also require a key location: moving the SQLite file out of `~/.podium/standalone/` with `PODIUM_SQLITE_PATH`, and selecting Postgres. Either one requires `PODIUM_SIGN_KEY_PATH` naming a key file on the store's persistent storage, or `PODIUM_SIGN=none`. A key lost while the rows it signed survive leaves those rows unloadable. A registry started with signing on and neither setting is refused at start with an error naming both. `PODIUM_SIGN_KEY_PATH` has no `registry.yaml` key.

## Object storage

Object storage holds bundled resource bytes and serves them through presigned URLs. Manifest bodies under the 256 KB inline cutoff stay in the metadata store, so object-storage growth tracks bundled resources rather than artifact count. `PODIUM_OBJECT_STORE` selects the backend.

- **Local filesystem** is the default, rooted at `~/.podium/standalone/objects/` unless `PODIUM_FILESYSTEM_ROOT` moves it.
- **S3 or an S3-compatible service** is selected with `PODIUM_OBJECT_STORE=s3` plus `PODIUM_S3_BUCKET` and the endpoint, region, and credential variables. S3, GCS, MinIO, and R2 all work through this path. The URL scheme on `PODIUM_S3_ENDPOINT` selects TLS.

## Vector index

The vector index stores the embedding for each artifact's text projection and answers the vector half of a `search_artifacts` query. `PODIUM_VECTOR_BACKEND` selects it.

- **`sqlite-vec`** is the default on a single-node deployment. It is collocated in the same SQLite file that holds manifests, so it adds no service to run.
- **`pgvector`** is the default once the metadata store is Postgres. It lives in the Postgres deployment, so it likewise adds no service.
- **Pinecone, Weaviate Cloud, and Qdrant Cloud** are managed alternatives. Each can either store vectors the registry computes or embed the submitted text itself. [Vector backends](vector-backends) has the per-backend configuration and the procedure for switching backends on a running deployment.

Switching backends re-embeds the catalog. `podium admin reembed` runs the pass, and `--only-missing` and `--since` scope it.

## Embeddings

An embedding provider turns ingest text and query text into vectors. `PODIUM_EMBEDDING_PROVIDER` selects it, and its interface is the `EmbeddingProvider` SPI.

The built-in providers are `openai`, `voyage`, `cohere`, and `ollama`. Each reaches an external service, so hybrid search requires either an API key or a locally running Ollama. Without an explicit choice, a single-node deployment names `ollama` and a Postgres-backed deployment names `openai`.

A managed vector backend can take this role instead and embed the submitted text with its own hosted model, which removes the separate provider. Pinecone Integrated Inference, Weaviate Cloud vectorizers, and Qdrant Cloud Inference all embed on ingest.

A registry with no embedding provider wired serves BM25 keyword search over manifest text. The following settings select BM25-only search explicitly:

- `PODIUM_NO_EMBEDDINGS=true`
- the `--no-embeddings` flag
- `PODIUM_EMBEDDING_PROVIDER` set to the empty string or to `none`

The startup log records which search mode is in effect. However the vectors are produced, the registry fuses vector hits with BM25 hits through reciprocal rank fusion before returning results.

## Identity

The default is no identity provider. A registry that boots without one treats every caller as anonymous, and the visibility evaluator admits every layer.

Identity is off by default because each verifier needs configuration the deployment supplies:

- `oidc-jwt` needs an issuer and an audience. It is the only provider that requires registering a client with an external IdP.
- `trusted-headers` needs a fronting gateway.
- `injected-session-token` needs a trusted runtime key set, which the deployment configures at startup through `PODIUM_RUNTIME_KEYS_PATH`.

The registry verifies `injected-session-token`, `oidc-jwt`, and `trusted-headers` at request time. `oauth-device-code` names the interactive flow a person completes from the CLI. Setting it as the registry's provider stops startup with `config.identity_provider_unverified`, because this build ships no request-time verifier for it.

The registry process reads `PODIUM_IDENTITY_PROVIDER` for its own value. A consumer process reads the same variable for the credential it presents, and the two are configured separately.

| Registry `PODIUM_IDENTITY_PROVIDER` | What the registry does | Where it is documented |
|:--|:--|:--|
| unset | Every caller is anonymous. Admin-defined layers registered through the endpoint default to `public` visibility. | [Access control](../content/access-control) |
| `oidc-jwt` | Callers present a JWT the configured IdP issued, and the registry verifies it against the IdP's JWKS. A CLI, an SDK, or another API client obtains the token through the device-code flow. On a registry that enables the browser flow, a browser obtains it through the registry's own authorization-code exchange, which returns it in the `__Host-podium_session` cookie. | [OIDC cookbooks](../identity/oidc/) |
| `oidc-jwt` | The registry verifies a gateway-forwarded IdP-signed JWT against the issuer's JWKS on every request. | [Gateway-delegated identity](../identity/trusted-headers) |
| `trusted-headers` | The registry trusts gateway-injected identity headers without verifying them. | [Gateway-delegated identity](../identity/trusted-headers) |
| `injected-session-token` | A managed runtime signs a per-session JWT, and the registry verifies it on every call. | [Clustered](../modes/clustered#identity-flow) |

Once an identity provider is configured, the default visibility for an endpoint-registered admin-defined layer changes from `public` to `private`, so those layers do not leak by accident. `PODIUM_DEFAULT_LAYER_VISIBILITY` overrides it. See [Access control](../content/access-control#deployment-defaults).

### Group provisioning with SCIM

SCIM 2.0 provisions groups alongside an identity provider rather than replacing one. It pushes group membership from the IdP, so layer visibility can reference group names directly. The registry mounts its SCIM receiver at `/scim/v2/` on a single-node or clustered deployment when `PODIUM_SCIM_TOKENS` lists a bearer token.

Without SCIM, group membership comes from the OIDC `groups` claim. `PODIUM_IDP_GROUP_MAPPING` translates IdP-specific group identifiers into the names layer config uses.

## Layer sources

A layer's source is where the registry fetches that layer's artifacts. The built-in sources are configured per layer rather than per deployment:

- `git` mirrors a tracked ref and ingests on webhook.
- `local` reads a filesystem path the registry process can see.

[Connecting layer sources](../content/layer-sources) covers registering each kind, private repositories, and [keeping layers current](../content/layer-sources#keeping-layers-current). [Layered composition](../content/layers) covers how layers combine.

Other sources are reachable through the `LayerSourceProvider` SPI, such as S3 versioned buckets, OCI registries, HTTP archives, and internal CMS bridges. A custom source is a Go module compiled into a registry build, which [Extending](extending) covers.

## Audit log

The registry writes every read, ingest, and admin action to a hash-chained audit stream, separate from the metadata store. `PODIUM_AUDIT_LOG_PATH` lists the stream's destinations, alone or in a comma-separated list:

- a file path, written as a hash-chained file
- an `http://` or `https://` URL, which receives the records as NDJSON
- an `otlp+http://` or `otlp+https://` URL, such as an OpenTelemetry Collector, which receives them as OTLP logs
- the token `stdout`

Unset, the registry writes the file `~/.podium/audit.log`. Every destination in the list receives the same hash-chained stream. `PODIUM_AUDIT_LOG_PATH` has no `registry.yaml` key. [Audit destinations](../operations/observability#audit-destinations) covers each destination, its transport settings, and its delivery rules.

## Tracing

The registry and the MCP server export OpenTelemetry traces when tracing is on. Tracing is off by default.

- `PODIUM_TRACING=otlp`, or a set `OTEL_EXPORTER_OTLP_ENDPOINT` or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`, exports spans over OTLP/HTTP. The exporter reads the standard `OTEL_EXPORTER_OTLP_*` variables.
- `PODIUM_TRACING=stdout` writes spans to standard output for local inspection.

Spans carry W3C Trace Context, and the propagator is installed even when export is off. URL query strings are removed before export. [Tracing](../operations/observability#tracing) covers the span set and the export settings.

## Notifications

A notification provider delivers an alert when an ingest of a layer fails, on either a manual reingest or a Git webhook trigger. It is the only event that sends a notification. No provider is configured by default. `PODIUM_NOTIFICATION_PROVIDER` selects one:

| Value | Delivery | Required settings |
|:--|:--|:--|
| `log` | Writes the notification to the registry log. | None |
| `webhook` | Sends a JSON `POST` to a URL. With a secret set, the `X-Podium-Signature` header carries `sha256=` followed by the hex HMAC-SHA256 of the body. | `PODIUM_NOTIFICATION_WEBHOOK_URL`, and optionally `PODIUM_NOTIFICATION_WEBHOOK_SECRET` |
| `email` or `smtp` | Sends an email over SMTP. | `PODIUM_NOTIFICATION_SMTP_HOST` and `PODIUM_NOTIFICATION_SMTP_FROM`, plus the recipient list `PODIUM_NOTIFICATION_SMTP_TO` |
| `multi` | Runs the log provider, plus the webhook and email providers when each one's settings are present. | The settings of each provider to include |

The email provider also reads `PODIUM_NOTIFICATION_SMTP_PORT`, which defaults to 587, and `PODIUM_NOTIFICATION_SMTP_USERNAME` and `PODIUM_NOTIFICATION_SMTP_PASSWORD` for SMTP authentication. A `webhook` or `email` value whose required settings are missing logs a warning, and the registry starts with no notification provider. [Notifications](../operations/observability#notifications) covers the notification payload.
