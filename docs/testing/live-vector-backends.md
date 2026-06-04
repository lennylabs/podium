---
layout: default
title: Live vector backends
parent: Testing
nav_order: 1
description: Set up Pinecone, Weaviate Cloud, and Qdrant Cloud for Podium's live integration tests, with storage-only and self-embedding collections.
---

# Live vector backends

Podium's live integration tests exercise the managed vector backends Pinecone, Weaviate Cloud, and Qdrant Cloud. This page lists the accounts, indexes, and collections to create and the environment variables to set so the tests run against real services.

Credentials live in `test.env` at the repository root, copied from `test.env.example`. The file is gitignored. The tests run on the live-external lane:

```bash
make test-live-external
```

Each backend self-skips when its variables are absent, so you can set up one backend at a time. The vector-backend tests do not need an embedding-provider key. Storage mode uses a synthetic embedder, and self-embedding uses the backend's own hosted model. The OpenAI, Cohere, Voyage, and Ollama keys in `test.env.example` drive a separate set of embedding-provider tests.

For configuring a backend in a running deployment rather than for tests, see [Vector backends](../deployment/vector-backends).

## Collections per backend

Each backend uses up to two objects.

- A **storage collection** at dimension 1536 with cosine distance. The conformance, storage-only, and managed semantic-search tests share it. The registry computes the vectors, so the dimension is the only fixed requirement, and 1536 matches the managed semantic-search e2e so one object serves every storage test.
- An optional **self-embedding collection**. The backend computes its vectors from text, so it is a separate object from the storage collection: Pinecone Integrated Inference is a different index type, and a Weaviate vectorizer class differs from a vectorizer-none class. Its dimension is fixed by the inference model and is generally not 1536.

You create these objects yourself. The backends do not create them on demand, except that Weaviate's auto-schema creates the storage class on first write.

## Pinecone

1. Create an account at [app.pinecone.io](https://app.pinecone.io). The free Starter tier is sufficient for one small index.
2. Create an API key in the console.
3. Create the storage index as a Serverless index with dimension 1536 and metric Cosine. The example below names it `podium-test`.
4. For self-embedding, create a second Serverless index with integrated embedding and select a hosted inference model such as `multilingual-e5-large`. Pinecone fixes the dimension from the model. The example below names it `podium-test-selfembed`.
5. Set the variables in `test.env`:

   ```bash
   PODIUM_PINECONE_API_KEY=pcsk_...
   PODIUM_PINECONE_INDEX=podium-test
   # self-embedding (optional):
   PODIUM_PINECONE_SELFEMBED_INDEX=podium-test-selfembed
   PODIUM_PINECONE_INFERENCE_MODEL=multilingual-e5-large
   ```

The host resolves from the index name, so leave `PODIUM_PINECONE_HOST` unset.

## Weaviate Cloud

1. Create a cluster at [console.weaviate.cloud](https://console.weaviate.cloud). A Sandbox cluster is free and expires after a set period; a Serverless cluster is steadier for a recurring lane.
2. Copy the cluster's REST endpoint and create an API key.
3. The storage class uses the `none` vectorizer at dimension 1536 with cosine distance. Weaviate's auto-schema creates it on first write, so the test can create it for you. The example below names it `PodiumTest`.
4. For self-embedding, create a second class configured with a vectorizer module. The `text2vec-weaviate` module is hosted by Weaviate, needs no external key, and sets the dimension itself. The example below names it `PodiumTestSelfEmbed`.
5. Set the variables in `test.env`:

   ```bash
   PODIUM_WEAVIATE_URL=https://your-cluster.weaviate.cloud
   PODIUM_WEAVIATE_API_KEY=...
   PODIUM_WEAVIATE_COLLECTION=PodiumTest
   # self-embedding (optional):
   PODIUM_WEAVIATE_SELFEMBED_COLLECTION=PodiumTestSelfEmbed
   PODIUM_WEAVIATE_VECTORIZER=text2vec-weaviate
   ```

Weaviate title-cases class names, so use a capitalized name.

## Qdrant Cloud

1. Create a cluster at [cloud.qdrant.io](https://cloud.qdrant.io). The free 1 GB tier is sufficient.
2. Copy the cluster URL, which uses REST port 6333, and create an API key.
3. Create the storage collection with vector size 1536 and Cosine distance:

   ```bash
   curl -X PUT "$PODIUM_QDRANT_URL/collections/podium_test" \
     -H "api-key: $PODIUM_QDRANT_API_KEY" -H 'content-type: application/json' \
     -d '{"vectors": {"size": 1536, "distance": "Cosine"}}'
   ```

4. For self-embedding, create a second collection sized to the inference model's output dimension. The example uses `bge-small-en`, which outputs 384:

   ```bash
   curl -X PUT "$PODIUM_QDRANT_URL/collections/podium_test_selfembed" \
     -H "api-key: $PODIUM_QDRANT_API_KEY" -H 'content-type: application/json' \
     -d '{"vectors": {"size": 384, "distance": "Cosine"}}'
   ```

5. Set the variables in `test.env`:

   ```bash
   PODIUM_QDRANT_URL=https://your-cluster.cloud.qdrant.io:6333
   PODIUM_QDRANT_API_KEY=...
   PODIUM_QDRANT_COLLECTION=podium_test
   # self-embedding (optional):
   PODIUM_QDRANT_SELFEMBED_COLLECTION=podium_test_selfembed
   PODIUM_QDRANT_INFERENCE_MODEL=bge-small-en
   ```

## Run

Set the master switch in `test.env`, then run the lane:

```bash
# in test.env:
PODIUM_LIVE_EXTERNAL=1
```

```bash
make test-live-external
```

For each configured backend, the run executes its storage suites and, when the self-embedding collection and inference model are set, its self-embedding test. A backend without credentials skips with a reason.

## What each test exercises

- **Conformance** runs the `RegistrySearchProvider` contract: put, query, tenant-boundary isolation, upsert replacement, delete, dimension-mismatch rejection, and bounded top-k.
- **StorageOnly** and **TenantIsolation** cover the precomputed-vector recall path and per-tenant query isolation.
- **SelfEmbedding** submits text, has the backend compute the vectors, and asserts nearest-neighbour recall.

The production-dimension round trip for the local backends is covered by the pgvector depth tests.
