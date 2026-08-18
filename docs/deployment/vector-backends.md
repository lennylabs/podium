---
title: Vector backends
nav_order: 10
description: Configure Pinecone, Weaviate Cloud, or Qdrant Cloud as the registry's vector backend, on a single-node or clustered deployment.
---

# Vector backends

Vector search runs inside the registry process. The vector backend is selected per deployment through `PODIUM_VECTOR_BACKEND`. The default is `pgvector` once the metadata store is Postgres and `sqlite-vec` otherwise. The default binary also ships adapters for Pinecone, Weaviate Cloud, and Qdrant Cloud. Custom backends register through the `RegistrySearchProvider` SPI, which [Extending](extending) covers.

`podium sync`, the SDKs, and the MCP server never connect to the registry's vector backend directly. They reach the registry over HTTP, and the registry handles its own vector store. The MCP server opens a vector backend of its own only for the workspace-local overlay index described under [Local-overlay search on the MCP server](#local-overlay-search-on-the-mcp-server) below. A filesystem-source registry has no registry-side vector search at all because there is no registry process running to query.

---

## What you need

- A registry process, running either on the [single node](single-node) tier through `podium serve --standalone` or on the [clustered](clustered) tier.
- An account on the managed service and an empty index or collection prepared per that service's documentation.
- The API key and endpoint for the index or collection.

The [local](local) tier cannot use a managed vector backend. `podium sync` materializes and never queries a vector store. For hybrid search against a managed backend without standing up the full clustered stack, run `podium serve --standalone` against the same directory and configure the vector backend on that process.

---

## Self-embedding and storage-only modes

Each managed backend supports two modes, selected by whether an inference-model variable is set:

- **Self-embedding.** The backend computes the embedding from the text projection the registry submits. Pinecone Integrated Inference, Weaviate Cloud vectorizers, and Qdrant Cloud Inference all support this. No external embedding provider is required.
- **Storage-only.** The backend stores vectors that the registry computes through a configured `EmbeddingProvider`. The provider is selected via `PODIUM_EMBEDDING_PROVIDER` (`openai`, `voyage`, `cohere`, or `ollama`) and is required in this mode.

Setting `PODIUM_EMBEDDING_PROVIDER` to the empty string, or to `none`, disables the registry-side embedder. On a storage-only backend, search then degrades to BM25 over manifest text. On a self-embedding backend (`pinecone`, `weaviate-cloud`, or `qdrant-cloud` with its inference-model or vectorizer variable set) the backend keeps embedding server-side, so hybrid search continues. Set `PODIUM_NO_EMBEDDINGS=true`, or pass `--no-embeddings` to `podium serve`, to force BM25-only regardless of the configured backend.

---

## Pinecone

Server-side environment variables:

| Variable | Description | Default |
|:--|:--|:--|
| `PODIUM_VECTOR_BACKEND` | Set to `pinecone`. | — |
| `PODIUM_PINECONE_API_KEY` | Pinecone API key. | required |
| `PODIUM_PINECONE_INDEX` | Index name. | required |
| `PODIUM_PINECONE_HOST` | Index host URL (Pinecone serverless). | auto-resolved from the index name |
| `PODIUM_PINECONE_NAMESPACE` | Namespace prefix used per tenant. | `default` |
| `PODIUM_PINECONE_INFERENCE_MODEL` | Hosted model name to enable Integrated Inference. | unset (storage-only mode) |

### Single node with Pinecone

```bash
export PODIUM_VECTOR_BACKEND=pinecone
export PODIUM_PINECONE_API_KEY=pcn-...
export PODIUM_PINECONE_INDEX=podium-dev
export PODIUM_PINECONE_INFERENCE_MODEL=multilingual-e5-large  # optional

podium serve --standalone --layer-path ~/podium-artifacts/
```

This setup runs as a single binary with embedded SQLite metadata and filesystem object storage. Pinecone holds the artifact embeddings. Postgres is not required, and no separate embedding service is needed when self-embedding is on.

For storage-only mode, omit `PODIUM_PINECONE_INFERENCE_MODEL` and configure an embedding provider:

```bash
export PODIUM_EMBEDDING_PROVIDER=openai
export OPENAI_API_KEY=sk-...
```

### Clustered with Pinecone

In `/etc/podium/registry.yaml`:

```yaml
registry:
  endpoint: https://podium.acme.com
  bind: 0.0.0.0:8080

  store:
    type: postgres
    dsn: ${PODIUM_POSTGRES_DSN}

  object_store:
    type: s3
    bucket: acme-podium
    region: us-east-1

  vector_backend:
    type: pinecone
    api_key: ${PODIUM_PINECONE_API_KEY}
    index: acme-prod
    namespace: acme  # prefix; the registry appends the tenant ID
    inference_model: multilingual-e5-large  # enables self-embedding

  # Omitted because the vector backend above self-embeds.
  # embedding_provider:
  #   type: openai
  #   api_key: ${OPENAI_API_KEY}
  #   model: text-embedding-3-large

  identity_provider:
    type: oidc-jwt
    issuer: https://acme.okta.com/oauth2/default
    audience: https://podium.acme.com
```

Environment variables and CLI flags override file values. Use `${ENV_VAR}` interpolation for secrets.

---

## Weaviate Cloud

Server-side environment variables:

| Variable | Description | Default |
|:--|:--|:--|
| `PODIUM_VECTOR_BACKEND` | Set to `weaviate-cloud`. | — |
| `PODIUM_WEAVIATE_URL` | Cluster REST URL. | required |
| `PODIUM_WEAVIATE_API_KEY` | API key. | required |
| `PODIUM_WEAVIATE_COLLECTION` | Collection name. | required |
| `PODIUM_WEAVIATE_GRPC_URL` | gRPC endpoint. Reserved and not currently read; the Weaviate backend uses the REST data plane. | derived from the REST URL |
| `PODIUM_WEAVIATE_VECTORIZER` | Vectorizer module (`text2vec-openai`, `text2vec-weaviate`, and similar) to enable self-embedding. | unset (storage-only mode) |

### Single node with Weaviate Cloud

```bash
export PODIUM_VECTOR_BACKEND=weaviate-cloud
export PODIUM_WEAVIATE_URL=https://acme.weaviate.network
export PODIUM_WEAVIATE_API_KEY=wv-...
export PODIUM_WEAVIATE_COLLECTION=PodiumArtifacts
export PODIUM_WEAVIATE_VECTORIZER=text2vec-weaviate  # optional

podium serve --standalone --layer-path ~/podium-artifacts/
```

### Clustered with Weaviate Cloud

```yaml
  # Extract from registry.yaml; nests under the top-level `registry:` mapping.
  vector_backend:
    type: weaviate-cloud
    url: ${PODIUM_WEAVIATE_URL}
    api_key: ${PODIUM_WEAVIATE_API_KEY}
    collection: PodiumArtifacts
    inference_model: text2vec-weaviate  # the vectorizer module; enables self-embedding
```

The config-file key is `inference_model:` for every self-embedding backend. `PODIUM_WEAVIATE_VECTORIZER` is the environment-variable form of the same setting.

For storage-only mode, omit `inference_model:` and add an `embedding_provider:` block as in the Pinecone example.

---

## Qdrant Cloud

Server-side environment variables:

| Variable | Description | Default |
|:--|:--|:--|
| `PODIUM_VECTOR_BACKEND` | Set to `qdrant-cloud`. | — |
| `PODIUM_QDRANT_URL` | Cluster REST URL. | required |
| `PODIUM_QDRANT_API_KEY` | API key. | required |
| `PODIUM_QDRANT_COLLECTION` | Collection name. | required |
| `PODIUM_QDRANT_GRPC_PORT` | gRPC port. Reserved and not currently read; the Qdrant backend uses the REST data plane. | `6334` |
| `PODIUM_QDRANT_INFERENCE_MODEL` | Hosted Cloud Inference model name to enable self-embedding. | unset (storage-only mode) |

### Single node with Qdrant Cloud

```bash
export PODIUM_VECTOR_BACKEND=qdrant-cloud
export PODIUM_QDRANT_URL=https://acme.eu-central.aws.cloud.qdrant.io:6333
export PODIUM_QDRANT_API_KEY=qdr-...
export PODIUM_QDRANT_COLLECTION=podium_artifacts
export PODIUM_QDRANT_INFERENCE_MODEL=bge-small-en  # optional

podium serve --standalone --layer-path ~/podium-artifacts/
```

### Clustered with Qdrant Cloud

```yaml
  # Extract from registry.yaml; nests under the top-level `registry:` mapping.
  vector_backend:
    type: qdrant-cloud
    url: ${PODIUM_QDRANT_URL}
    api_key: ${PODIUM_QDRANT_API_KEY}
    collection: podium_artifacts
    inference_model: bge-small-en  # enables self-embedding
```

---

## Switching backends on a running deployment

The vector store records `(model_id, dimensions)` per artifact. When the configured backend or embedding model changes, run `podium admin reembed` to repopulate:

```bash
podium admin reembed --registry https://podium.acme.com
# or scope to a window
podium admin reembed --since 2026-01-01T00:00:00Z --registry https://podium.acme.com
```

With no `--artifact`, the command runs a tenant-wide pass, and `--only-missing` and `--since` compose to scope it. `--artifact` re-embeds a single artifact and requires `--version` alongside it.

During re-embedding, the store may transiently hold mixed dimensions. Query time restricts results to vectors matching the currently-configured model. The registry emits `embedding.reembed_in_progress` events for progress monitoring. Stale-dimension rows are purged when re-embedding completes.

For non-collocated backends (every managed service falls in this category), ingest writes are coordinated through a transactional outbox: the manifest commit and a `vector_pending` row land in the same metadata-store transaction, and a background worker drives the write to the vector backend. This keeps the metadata commit and the vector write consistent under failure.

---

## Local-overlay search on the MCP server

The MCP server can use a managed vector backend independently of the registry, to give the workspace-local overlay (`.podium/overlay/`) better semantic recall. The same `PODIUM_VECTOR_BACKEND` and per-backend variables apply when the MCP server is configured with `LocalSearchProvider` against an external backend. The overlay index also requires `PODIUM_EMBEDDING_PROVIDER` to name a provider: the MCP server computes overlay vectors locally, and it leaves the overlay BM25-only when either variable is unset or set to `none`. The self-embedding mode above does not apply to this path.

This is independent of the registry-side backend. It affects how local-overlay manifests are indexed, and registry-side `search_artifacts` results are merged in by reciprocal rank fusion regardless. The MCP server still requires a server-backed registry, so this side path does not enable search on the [local](local) tier.

---

## Operational notes

- The managed service's costs, identity, and quotas are the operator's responsibility. Podium does not proxy credentials and does not enforce per-tenant cost ceilings on the backend.
- If the configured vector backend is unreachable and no embedding provider is configured for storage-only fallback, search degrades to BM25 over manifest text. The `search_artifacts` and `search_domains` response bodies carry no degraded field, and no response header reports the fallback, so a caller cannot detect it from the response. The registry records the condition in its startup log: `warning: vector search disabled: <error>` when the backend fails to open, and `keyword search: embeddings disabled (BM25-only; no embedding provider)` when no backend is wired. A backend that becomes unreachable after startup logs nothing per request, so alert on the backend service's own availability.
- Migration in either direction is supported. `podium admin reembed` repopulates the newly-configured backend from the canonical text projections. The previous backend can stay in place during cut-over or be torn down after re-embedding completes.
- Search QPS, latency, and recall depend on the backend's index configuration (shard count, replicas, dimensionality). The [Operator guide](operator-guide) covers capacity planning across the registry as a whole.
