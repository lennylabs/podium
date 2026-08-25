---
title: Server-side integrations
nav_order: 4
description: The backing services a Podium registry uses. Metadata store, object storage, vector index, embeddings, identity, and layer sources, each with what ships by default and what can replace it.
---

# Server-side integrations

A registry process reaches out to several backing services. Each one has a default that a [single-node](single-node) deployment runs without extra infrastructure, and each one is selectable per deployment through an environment variable or the matching `registry.yaml` key.

The [local](local) tier runs no registry process, so nothing on this page applies to it.

| Integration | Out of the box | Compatible alternatives | Selected by |
|:--|:--|:--|:--|
| Metadata store | SQLite | Postgres | `PODIUM_REGISTRY_STORE` |
| Object storage | Local filesystem | S3 or any S3-compatible service | `PODIUM_OBJECT_STORE` |
| [Vector index](vector-backends) | `sqlite-vec` | `pgvector`, Pinecone, Weaviate Cloud, and Qdrant Cloud | `PODIUM_VECTOR_BACKEND` |
| [Embeddings](vector-backends) | `ollama`, falling back to BM25 when unreachable | OpenAI, Voyage, Cohere, and Ollama, or a self-embedding vector backend | `PODIUM_EMBEDDING_PROVIDER` |
| [Identity](oidc/) | None | `oidc-jwt`, `trusted-headers`, and `injected-session-token` | `PODIUM_IDENTITY_PROVIDER` |
| [Layer sources](layers) | Git and local paths | Custom sources through the `LayerSourceProvider` SPI | Per layer, in the layer's config |

Nothing in the alternatives column is required to start. At cluster scale, Postgres and object storage become requirements, because registry replicas need shared state.

---

## Metadata store

The metadata store holds manifest metadata, dependency edges, layer config, and admin grants. `PODIUM_REGISTRY_STORE` selects it, and the interface is the `RegistryStore` SPI described in [Extending](extending). The audit stream is separate: the registry writes an append-only hash-chained file at `~/.podium/audit.log` unless `PODIUM_AUDIT_LOG_PATH` names another path, or an `http(s)` URL, which routes the stream to that endpoint instead.

- **SQLite** is the default. The file lives at `~/.podium/standalone/podium.db` unless `PODIUM_SQLITE_PATH` moves it. One process owns the file, so it does not survive being shared between replicas.
- **Postgres** is selected with `PODIUM_REGISTRY_STORE=postgres` and a DSN. Registry replicas share it, which is what makes horizontal scaling possible. The [clustered](clustered) tier requires it.

`podium admin migrate-to-standard --postgres <dsn> --object-store <url>` exports a SQLite-backed deployment into Postgres and object storage.

---

## Object storage

Object storage holds bundled resource bytes and serves them through presigned URLs. Manifest bodies under the 256 KB inline cutoff stay in the metadata store, so object-storage growth tracks bundled resources rather than artifact count. `PODIUM_OBJECT_STORE` selects the backend.

- **Local filesystem** is the default, rooted at `~/.podium/standalone/objects/` unless `PODIUM_FILESYSTEM_ROOT` moves it.
- **S3 or an S3-compatible service** is selected with `PODIUM_OBJECT_STORE=s3` plus `PODIUM_S3_BUCKET` and the endpoint, region, and credential variables. S3, GCS, MinIO, and R2 all work through this path. The URL scheme on `PODIUM_S3_ENDPOINT` selects TLS.

---

## Vector index

The vector index stores the embedding for each artifact's text projection and answers the vector half of a `search_artifacts` query. `PODIUM_VECTOR_BACKEND` selects it.

- **`sqlite-vec`** is the default on a single-node deployment. It is collocated in the same SQLite file that holds manifests, so it adds no service to run.
- **`pgvector`** is the default once the metadata store is Postgres. It lives in the Postgres deployment, so it likewise adds no service.
- **Pinecone, Weaviate Cloud, and Qdrant Cloud** are managed alternatives. Each can either store vectors the registry computes or embed the submitted text itself. [Vector backends](vector-backends) has the per-backend configuration and the procedure for switching backends on a running deployment.

Switching backends re-embeds the catalog. `podium admin reembed` runs the pass, and `--only-missing` and `--since` scope it.

---

## Embeddings

An embedding provider turns ingest text and query text into vectors. `PODIUM_EMBEDDING_PROVIDER` selects it, and the interface is the `EmbeddingProvider` SPI.

Pinecone, Weaviate Cloud, and Qdrant Cloud can take this role instead of a provider, embedding the submitted text with their own hosted model. A registry with no embedding provider wired serves BM25 keyword search over manifest text. `PODIUM_NO_EMBEDDINGS=true` and the `--no-embeddings` flag select that explicitly, and so does setting `PODIUM_EMBEDDING_PROVIDER` to the empty string or to `none`. The startup log records which one is in effect.

The built-in providers are `openai`, `voyage`, `cohere`, and `ollama`. Each reaches an external service, so hybrid search requires either an API key or a locally running Ollama. Without an explicit choice, a single-node deployment names `ollama` and a Postgres-backed deployment names `openai`.

A managed vector backend can also embed on ingest, which removes the separate provider. Pinecone Integrated Inference, Weaviate Cloud vectorizers, and Qdrant Cloud Inference all support this.

However the vectors are produced, the registry fuses vector hits with BM25 hits through reciprocal rank fusion before returning results.

---

## Identity

The default is no identity provider. A registry that boots without one treats every caller as anonymous, and the visibility evaluator admits every layer.

Identity is off by default, because each verifier needs configuration the deployment supplies: an issuer and an audience for `oidc-jwt`, a fronting gateway for `trusted-headers`, or a trusted runtime key set for `injected-session-token`, which the deployment configures at startup through `PODIUM_RUNTIME_KEYS_PATH`. Only `oidc-jwt` requires registering a client with an external IdP.

The registry verifies `injected-session-token`, `oidc-jwt`, and `trusted-headers` at request time. `oauth-device-code` names the interactive flow a person completes from the CLI; setting it as the registry's provider stops startup with `config.identity_provider_unverified`, because this build ships no request-time verifier for it.

The registry process reads `PODIUM_IDENTITY_PROVIDER` for its own value. A consumer process reads the same variable for the credential it presents, and the two are configured separately.

| Registry `PODIUM_IDENTITY_PROVIDER` | What the registry does | Where it is documented |
|:--|:--|:--|
| unset | Every caller is anonymous. Admin-defined layers registered through the endpoint default to `public` visibility. | [Access control](access-control) |
| `oidc-jwt` | Callers present a JWT the configured IdP issued, which the registry verifies against its JWKS. A CLI, an SDK, or another API client obtains that token by completing the device-code flow, and on a registry that enables the browser flow a browser obtains it through the registry's own authorization-code exchange, which the registry returns in the `__Host-podium_session` cookie. | [OIDC cookbooks](oidc/) |
| `oidc-jwt` | The registry verifies a gateway-forwarded IdP-signed JWT against the issuer's JWKS on every request. | [Gateway-delegated identity](gateway-delegated-identity) |
| `trusted-headers` | The registry trusts gateway-injected identity headers without verifying them. | [Gateway-delegated identity](gateway-delegated-identity) |
| `injected-session-token` | A managed runtime signs a per-session JWT and the registry verifies it on every call. | [Clustered](clustered#identity-flow) |

Once an identity provider is configured, the default visibility for an endpoint-registered admin-defined layer flips from `public` to `private` so those layers do not leak by accident. `PODIUM_DEFAULT_LAYER_VISIBILITY` overrides it. See [Access control](access-control#deployment-defaults).

SCIM 2.0 is group provisioning rather than an identity provider. It pushes group membership from the IdP so layer visibility can reference group names directly, and it is available on the [clustered](clustered) tier. Without SCIM, group membership comes from the OIDC `groups` claim, and `PODIUM_IDP_GROUP_MAPPING` translates IdP-specific group identifiers into the names layer config uses.

---

## Layer sources

A layer's source is where the registry fetches that layer's artifacts. The built-ins are `git`, which mirrors a tracked ref and ingests on webhook, and `local`, which reads a filesystem path the registry process can see. Both are configured per layer rather than per deployment. See [Layered composition](layers).

Other sources are reachable through the `LayerSourceProvider` SPI: S3 versioned buckets, OCI registries, HTTP archives, and internal CMS bridges. A custom source is a Go module compiled into a registry build, which [Extending](extending) covers.
