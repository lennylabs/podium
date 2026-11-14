---
title: Docker Compose evaluation
nav_order: 5
description: "Run the clustered stack on one machine with Docker Compose to evaluate it before deploying to Kubernetes."
---

# Docker Compose evaluation

The repository ships a `docker-compose.yml` that runs the components of a [clustered](clustered) deployment on one machine. It wires the same components a clustered deployment wires, so consumers exercise the same code paths. The stack is for evaluation pilots, CI integration tests, and adapter or SDK development. It runs single-replica services with default credentials on local volumes, so it is unsuitable for production. For solo or non-production use, [Single node](single-node) runs one binary instead.

## Evaluating with Docker Compose

The stack runs the following services:

| Service | What it runs | Host address |
|:--|:--|:--|
| `registry` | The registry, built from the repository's `Dockerfile` | `http://127.0.0.1:8080` |
| `postgres` | Postgres with the pgvector extension | `localhost:5432` |
| `minio` | MinIO object storage, with its web console | `localhost:9000`, console at `http://localhost:9001` |
| `dex` | A Dex OIDC identity provider | `http://localhost:5556/dex` |
| `bootstrap` | A one-shot container that creates the MinIO bucket and exits | None |

To evaluate the stack:

1. In a checkout of the repository, run `docker compose up -d`. The registry image builds from the checkout, so no published image is needed.
2. Point the CLI at the registry with `podium init --global --registry http://localhost:8080`.
3. Register a layer and run a sync or the MCP server against the registry, as [Connecting layer sources](../content/layer-sources) and [Configure your harness](../../consuming/configure-your-harness) describe. A `podium-mcp` consumer sets `PODIUM_VERIFY_SIGNATURES=never`, because the stack signs nothing.
4. Stop the stack with `docker compose down`, which keeps the volumes. `docker compose down -v` also deletes the volumes and every row and object the stack stored.

## Differences from a production deployment

The stack differs from a production deployment in the following settings:

- **Search.** The stack sets `PODIUM_NO_EMBEDDINGS`, so search runs over manifest text with no embedding provider and no API key. Remove that variable and supply a provider with its key to exercise hybrid search.
- **Identity.** The registry service selects no identity provider, so it authenticates no caller. Every caller resolves as anonymous, every layer is visible whatever its `visibility:` declares, and the layer-management endpoints admit any request. The service publishes its port on the host loopback interface for that reason. The registry seeds the default tenant and an admin grant for the identity in `PODIUM_BOOTSTRAP_ADMINS` at boot, and that grant takes effect once an operator configures an identity provider. `PODIUM_DEFAULT_LAYER_VISIBILITY` is `private`, so a layer registered against the stack keeps a private declaration when a provider is added later.
- **Dex.** Dex holds a public device-code client and an example user, `alice@acme.com`. The registry service does not consult it, and `podium login` against `http://localhost:8080` reports that the registry needs no authentication and exits.
- **Signing.** The stack sets `PODIUM_SIGN` to `none`, because it has no key management, and a recreated registry container would otherwise generate a fresh signing key on every recreate.

[Identity](../identity/) covers configuring a verified identity provider, which the stack needs before it is exposed beyond the host.

## Upgrading the stack

A stack whose `postgres_data` volume holds rows a v0.4.0 registry wrote needs the stored-row migration before it starts, as [Upgrade a Docker Compose evaluation stack](../operations/upgrade-notes#upgrade-a-docker-compose-evaluation-stack) shows.
