---
title: Backing services
nav_order: 5
description: An inventory of the services behind a Podium registry, from the metadata store to notifications, with the default for each and what can replace it.
---

# Backing services

This page lists the services a Podium registry process depends on, the default each one ships with, and what can replace it. It is for operators planning a [single-node](../modes/single-node) or [clustered](../modes/clustered) deployment. The [local](../modes/local) deployment mode runs no registry process, so nothing on this page applies to it.

Each section names the setting that selects the service and links to the page that covers its configuration.

## Overview

Each service has a default that a single-node deployment runs without extra infrastructure. An environment variable selects the backend per deployment, as the last column shows. The storage, search, and identity settings also have a `registry.yaml` key, as [Configuration](../../reference/configuration) lists. The audit, tracing, and notification settings are environment variables only.

| Service | Default | Alternatives | Selected by |
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

No alternative is required to start. At cluster scale, Postgres and object storage become requirements, because registry replicas need shared state. A replacement for any service beyond the listed alternatives is an SPI plugin compiled into a registry build, as [Extending](extending) describes.

## Metadata store

The metadata store holds manifest metadata, dependency edges, layer config, and admin grants. `PODIUM_REGISTRY_STORE` selects it, and its interface is the `RegistryStore` SPI.

- **SQLite** is the default. The file lives at `~/.podium/standalone/podium.db` unless `PODIUM_SQLITE_PATH` moves it. One process owns the file, so replicas cannot share it.
- **Postgres** is selected with `PODIUM_REGISTRY_STORE=postgres` and a DSN. Registry replicas share it, which is what makes horizontal scaling possible. The [clustered](../modes/clustered) deployment mode requires it.

[Moving between modes](../modes/migrating) covers exporting a SQLite-backed deployment into Postgres and object storage with `podium admin migrate-to-standard`.

### Signing key location

The registry signs every artifact at ingest with a key file that defaults to `~/.podium/standalone/registry-signing.key`. Moving the SQLite file with `PODIUM_SQLITE_PATH`, or selecting Postgres, requires `PODIUM_SIGN_KEY_PATH` to name a key file on the store's persistent storage, or `PODIUM_SIGN=none`. A registry started with signing on and neither setting is refused at start. `PODIUM_SIGN_KEY_PATH` has no `registry.yaml` key. [Signing keys](../operations/signing-keys) covers generating, backing up, and rotating the key.

## Object storage

Object storage holds the bundled resource bytes of each artifact version and serves them through presigned URLs. Manifest bodies stay in the metadata store, so object-storage growth tracks bundled resources rather than artifact count. `PODIUM_OBJECT_STORE` selects the backend, and its interface is the `RegistryObjectStore` SPI.

- **Local filesystem** is the default, rooted at `~/.podium/standalone/objects/` unless `PODIUM_FILESYSTEM_ROOT` moves it.
- **S3 or an S3-compatible service** is selected with `PODIUM_OBJECT_STORE=s3` plus `PODIUM_S3_BUCKET` and the endpoint, region, and credential variables. S3, GCS, MinIO, and R2 all work through this path. The URL scheme on `PODIUM_S3_ENDPOINT` selects TLS.

[Handling artifact responses](../../consuming/handling-artifact-responses) describes how resource bytes reach a consumer.

## Vector index

The vector index stores the embedding of each artifact's text projection and answers the vector half of a `search_artifacts` query. `PODIUM_VECTOR_BACKEND` selects it. `sqlite-vec` is the default on a single node and `pgvector` once the metadata store is Postgres, and each lives in the metadata store's own database. Pinecone, Weaviate Cloud, and Qdrant Cloud are managed alternatives. [Vector backends](vector-backends) covers choosing and configuring a backend and switching backends on a running deployment.

## Embeddings

An embedding provider turns ingest text and query text into vectors. `PODIUM_EMBEDDING_PROVIDER` selects it, and its interface is the `EmbeddingProvider` SPI. The built-in providers are `openai`, `voyage`, `cohere`, and `ollama`. Without an explicit choice, a single-node deployment names `ollama` and a Postgres-backed deployment names `openai`. A managed vector backend can embed the submitted text itself and take this role instead.

A registry with no embedding provider serves BM25 keyword search over manifest text, and `PODIUM_NO_EMBEDDINGS=true` selects that mode explicitly. With embeddings on, the registry fuses vector hits with BM25 hits through reciprocal rank fusion, and the startup log records which search mode is in effect. [Vector backends → Self-embedding and storage-only modes](vector-backends#self-embedding-and-storage-only-modes) covers how the provider and the backend interact and every way to disable embeddings.

## Identity

The default is no identity provider. A registry that boots without one treats every caller as anonymous, and the visibility evaluator admits every layer. `PODIUM_IDENTITY_PROVIDER` selects a provider the registry verifies at request time, and each one needs configuration the deployment supplies:

- `oidc-jwt` needs an issuer and an audience, and a client registered with the IdP.
- `trusted-headers` needs a fronting gateway.
- `injected-session-token` needs a trusted runtime key set, named by `PODIUM_RUNTIME_KEYS_PATH`.

`oauth-device-code` names the interactive flow a person completes from the CLI. As the registry's provider it stops startup with `config.identity_provider_unverified`, because the registry has no request-time verifier for it. [Identity](../identity/) covers each provider, the default layer visibility that changes once one is configured, and the startup guards.

### Group provisioning with SCIM

SCIM 2.0 provisions group membership alongside an identity provider rather than replacing one. Without it, group membership comes from the OIDC `groups` claim. [Groups and SCIM](../identity/groups) covers both paths.

## Layer sources

A layer's source is where the registry fetches that layer's artifacts. The built-in `git` and `local` sources are configured per layer rather than per deployment. Other sources, such as S3 versioned buckets, OCI registries, and internal CMS bridges, are reachable through the `LayerSourceProvider` SPI, which [Extending](extending) covers. [Connecting layer sources](../content/layer-sources) covers registering each kind and keeping it current.

## Audit log

The registry writes a hash-chained audit stream, separate from the metadata store, to the destinations `PODIUM_AUDIT_LOG_PATH` lists: a file, `stdout`, an HTTP receiver, or an OpenTelemetry Collector. Unset, it writes the file `~/.podium/audit.log`. [Audit destinations](../operations/observability#audit-destinations) covers each destination.

## Tracing

The registry and `podium-mcp` export OpenTelemetry traces over OTLP/HTTP, or to standard output, when `PODIUM_TRACING` or an `OTEL_EXPORTER_OTLP_*` endpoint turns tracing on. Tracing is off by default. [Tracing](../operations/observability#tracing) covers the settings and the span set.

## Notifications

A notification provider alerts an operator when a layer ingest fails. `PODIUM_NOTIFICATION_PROVIDER` selects `log`, `webhook`, `email`, or `multi`, and no provider is configured by default. [Notifications](../operations/observability#notifications) covers each provider's settings and the payload.
