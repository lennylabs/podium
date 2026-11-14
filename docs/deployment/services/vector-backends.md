---
title: Vector backends
nav_order: 1
description: Choose, configure, and migrate the vector backend a single-node or clustered registry uses for hybrid search.
---

# Vector backends

This page covers the vector backend behind the registry's hybrid search. It explains how to choose a backend, how to configure Pinecone, Weaviate Cloud, or Qdrant Cloud, and how to move a running deployment to another backend or embedding model. It is for operators of a single-node or clustered registry.

---

## How vector search runs

Vector search runs inside the registry process. `podium sync`, the SDKs, and the MCP server never connect to the registry's vector backend directly. They reach the registry over HTTP, and the registry queries its own vector store.

The MCP server opens a vector backend of its own only for the workspace-local overlay index. [Local-overlay search on the MCP server](#local-overlay-search-on-the-mcp-server) covers that path.

A filesystem-source registry has no registry-side vector search, because no registry process runs to answer a query.

---

## Choosing a backend

`PODIUM_VECTOR_BACKEND` selects the backend for a deployment. The default binary ships every backend in this table.

| Backend | `PODIUM_VECTOR_BACKEND` | Default |
|:--|:--|:--|
| pgvector, collocated with the Postgres metadata store | `pgvector` | When the metadata store is Postgres |
| sqlite-vec, collocated with the embedded SQLite metadata store | `sqlite-vec` | When the metadata store is not Postgres |
| Pinecone | `pinecone` | No |
| Weaviate Cloud | `weaviate-cloud` | No |
| Qdrant Cloud | `qdrant-cloud` | No |

The choice affects operations in these ways:

- The collocated backends need no external service. They tag each vector with its embedding model, so `podium admin reembed` can change the model in place.
- A managed backend needs a fresh index or collection for any embedding-model change. [Switching backends on a running deployment](#switching-backends-on-a-running-deployment) has the procedure.
- A managed backend can compute embeddings itself, which removes the need for an external embedding provider. [Self-embedding and storage-only modes](#self-embedding-and-storage-only-modes) explains the two modes.
- The managed service's costs, identity, and quotas are the operator's responsibility.

A custom backend registers through the `RegistrySearchProvider` SPI, which [Extending](extending#spis) covers.

The [local](../modes/local) deployment mode cannot use a managed vector backend, because `podium sync` materializes and never queries a vector store. For hybrid search against a managed backend without the full clustered stack, run `podium serve --standalone` against the same directory and configure the vector backend on that process.

---

## Self-embedding and storage-only modes

Each managed backend runs in one of two modes. The backend's inference-model variable selects the mode: set means self-embedding, unset means storage-only.

| Mode | What computes the embeddings | Embedding provider |
|:--|:--|:--|
| Self-embedding | The backend, from the text projection the registry submits. Pinecone Integrated Inference, Weaviate Cloud vectorizers, and Qdrant Cloud Inference all provide this. | Not required |
| Storage-only | The registry, through the configured `EmbeddingProvider`. The backend stores the vectors. | Required: `PODIUM_EMBEDDING_PROVIDER` set to `openai`, `voyage`, `cohere`, or `ollama` |

### Embedding-provider precedence

An explicitly configured embedding provider overrides the mode selection. When `PODIUM_EMBEDDING_PROVIDER` names a provider, or `registry.yaml` carries an `embedding_provider:` block, the registry builds that embedder. It opens the backend at the embedder's dimension and writes vectors it computed itself, even though the inference-model variable is set. The managed index is sized for the hosted model, so it rejects vectors of a different dimension.

On a self-embedding deployment, leave `PODIUM_EMBEDDING_PROVIDER` unset or set it to `none`. The per-mode default (`ollama` on a single node, `openai` on a clustered deployment) applies only while the variable is unset and no `embedding_provider:` block is present, so the default is not an override.

### Disabling embeddings

Setting `PODIUM_EMBEDDING_PROVIDER` to the empty string or to `none` disables the registry-side embedder. The effect depends on the backend:

- On a storage-only backend, search degrades to BM25 over manifest text.
- On a self-embedding backend (`pinecone`, `weaviate-cloud`, or `qdrant-cloud` with its inference-model or vectorizer variable set), the backend keeps embedding server-side, so hybrid search continues.

To force BM25-only search regardless of the configured backend, set `PODIUM_NO_EMBEDDINGS=true` or pass `--no-embeddings` to `podium serve`.

---

## Prerequisites for a managed backend

- A registry process, either on a [single node](../modes/single-node) through `podium serve --standalone` or on a [clustered](../modes/clustered) deployment.
- An account on the managed service, with an empty index or collection prepared as that service's documentation describes.
- The API key and endpoint for the index or collection.

---

## Pinecone

Set these variables in the registry's environment:

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

This setup runs as a single binary with embedded SQLite metadata and filesystem object storage, and Pinecone holds the artifact embeddings. It requires no Postgres, and with self-embedding on it requires no separate embedding service.

For storage-only mode, omit `PODIUM_PINECONE_INFERENCE_MODEL` and configure an embedding provider:

```bash
export PODIUM_EMBEDDING_PROVIDER=openai
export OPENAI_API_KEY=sk-...
```

### Clustered with Pinecone

Configure the backend in the registry's config file. The registry reads `~/.podium/registry.yaml` unless `PODIUM_CONFIG_FILE` names another path. The `podium-server` binary has no `--config` option. A clustered deployment that keeps the file at `/etc/podium/registry.yaml` therefore sets `PODIUM_CONFIG_FILE=/etc/podium/registry.yaml` in the registry's environment. `podium serve --config <path>` sets the same variable.

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

`PODIUM_SIGN_KEY_PATH` has no config-file key. Set it in the registry's environment to name the deployment's one signing key file, or set `PODIUM_SIGN=none`. The registry refuses to start with signing on, a Postgres store, and no key path. [Clustered](../modes/clustered#2-deploy-the-registry) shows how to generate the key file.

---

## Weaviate Cloud

Set these variables in the registry's environment:

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

Set these variables in the registry's environment:

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

Migration between backends works in either direction. When the configured backend or embedding model changes, run `podium admin reembed` to repopulate the vectors from the canonical text projections:

```bash
podium admin reembed --registry https://podium.acme.com
# or scope to a window
podium admin reembed --since 2026-01-01T00:00:00Z --registry https://podium.acme.com
```

With no `--artifact`, the command runs a tenant-wide pass, and `--only-missing` and `--since` compose to scope it. `--artifact` re-embeds a single artifact and requires `--version` alongside it. The registry emits `embedding.reembed_in_progress` events for progress monitoring.

The previous backend can stay in place during cut-over, or it can be torn down after re-embedding completes.

### Model changes on pgvector and sqlite-vec

Model versioning applies to the collocated backends only. `pgvector` and `sqlite-vec` tag each row with the embedding model. While a re-embed is in flight, they restrict a query to the current model. They drop the previous model's rows once a full pass completes. A pass scoped with `--only-missing` or `--since` purges nothing.

### Model changes on Pinecone, Weaviate Cloud, and Qdrant Cloud

The managed backends carry no model tag, so the registry neither filters their query results by model nor purges the previous model's vectors. Any embedding-model change on one of them needs a fresh index or collection, whether or not the change alters the dimension.

Every tenant shares the configured index or collection. Repointing the registry therefore affects every tenant at once, while a re-embed pass covers only the caller's tenant.

To change the embedding model on a managed backend:

1. Create a fresh index or collection for the new model's dimension and, on a self-embedding deployment, for its hosted model.
2. Point the registry at it through `PODIUM_PINECONE_INDEX`, `PODIUM_WEAVIATE_COLLECTION`, or `PODIUM_QDRANT_COLLECTION`. When `PODIUM_PINECONE_HOST` is set explicitly, point it at the new index as well.
3. Restart the registry.
4. In each provisioned tenant, run `podium admin reembed` with no scoping flag, as an identity that holds the `admin` role in that tenant. The command returns when the pass ends.
5. Check that the `failed` list in the result is empty. Re-run the command when it is not.
6. Delete the old index only after every tenant's pass reports an empty `failed` list. Until then, the old index is the only populated index to roll back to.

A new `PODIUM_PINECONE_NAMESPACE` prefix does not substitute for a new index, because every namespace lives in the same index and shares its dimension and hosted model.

### Search during a re-embed

| Situation | Search behavior |
|:--|:--|
| Fresh index, the tenant's pass is in progress | BM25 over the whole catalog, fused with vector matches from only the artifacts already re-embedded |
| Fresh index, the tenant's pass has not started | BM25 matches only |
| Existing managed index, dimension unchanged | Vectors from both models are scored together |
| Existing managed index, dimension changed | The index rejects the new vectors, and search runs BM25 only |

The search response reports none of these conditions (see [Operational notes](#operational-notes)).

### Write consistency on managed backends

Every managed service is a non-collocated backend. For these backends, ingest writes go through a transactional outbox. The manifest commit and a `vector_pending` row land in the same metadata-store transaction, and a background worker drives the write to the vector backend. The outbox keeps the metadata commit and the vector write consistent under failure.

---

## Local-overlay search on the MCP server

The MCP server can use a managed vector backend independently of the registry, to improve semantic recall for the workspace-local overlay (`.podium/overlay/`). The same `PODIUM_VECTOR_BACKEND` and per-backend variables apply when the MCP server is configured with `LocalSearchProvider` against an external backend.

The overlay index also requires `PODIUM_EMBEDDING_PROVIDER` to name a provider, because the MCP server computes overlay vectors locally. The MCP server leaves the overlay BM25-only when either variable is unset or set to `none`. The self-embedding mode described above does not apply to this path.

This index is independent of the registry-side backend and affects only how local-overlay manifests are indexed. The MCP server merges registry-side `search_artifacts` results in by reciprocal rank fusion regardless. The MCP server still requires a server-backed registry, so this path does not add search to the [local](../modes/local) deployment mode.

---

## Operational notes

- Podium does not proxy credentials to the managed service and does not enforce per-tenant cost ceilings on the backend.
- If the configured vector backend is unreachable and no embedding provider is configured for storage-only fallback, search degrades to BM25 over manifest text.
- A caller cannot detect that fallback from the response. The `search_artifacts` and `search_domains` response bodies carry no degraded field, and no response header reports it.
- The registry records the condition in its startup log: `warning: vector search disabled: <error>` when the backend fails to open, and `keyword search: embeddings disabled (BM25-only; no embedding provider)` when no backend is wired.
- A backend that becomes unreachable after startup logs nothing per request, so alert on the backend service's own availability.
- Search QPS, latency, and recall depend on the backend's index configuration (shard count, replicas, and dimensionality). The [Operator guide](../operations/troubleshooting) covers capacity planning across the registry as a whole.
