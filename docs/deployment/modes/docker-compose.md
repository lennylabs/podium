---
title: Docker Compose evaluation
nav_order: 5
description: "Run the clustered stack on one machine with Docker Compose to evaluate it before deploying to Kubernetes."
---

# Docker Compose evaluation

## Evaluating with Docker Compose

The repository ships a `docker-compose.yml` that brings up an evaluation stack with `docker compose up -d`. The stack runs the registry, a pgvector Postgres, MinIO object storage, a Dex OIDC IdP, and a one-shot bootstrap container that creates the MinIO bucket. It wires the same components a clustered deployment wires, so consumers exercise the same code paths. The stack runs single-replica services with default credentials on local volumes, so it is unsuitable for production.

The stack differs from a production deployment in the following settings:

- **Search.** The stack sets `PODIUM_NO_EMBEDDINGS`, so search runs over manifest text with no embedding provider and no API key. Remove that variable and supply a provider with its key to exercise hybrid search.
- **Identity.** The registry seeds the first tenant and admin grant itself at boot, from the default tenant plus the identity in `PODIUM_BOOTSTRAP_ADMINS`. The registry service selects no identity provider, so it authenticates no caller, and the seeded grant is unreachable until an operator configures one. The service publishes its port on the host loopback interface for that reason.
- **Signing.** The stack pins `PODIUM_SIGN: "none"`, because it has no key management, and a recreated registry container would otherwise generate a fresh signing key on every recreate. A `podium-mcp` consumer pointed at the stack therefore sets `PODIUM_VERIFY_SIGNATURES=never`.

A stack whose `postgres_data` volume holds rows a v0.4.0 registry wrote needs the stored-row migration before it starts, as [Upgrade a Docker Compose evaluation stack](../operations/upgrade-notes#upgrade-a-docker-compose-evaluation-stack) shows.

---
