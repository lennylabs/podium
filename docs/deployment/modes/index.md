---
title: Deployment modes
nav_order: 1
description: "The local, single-node, and clustered deployment modes, what runs in each, and how to choose between them."
---

# Deployment modes

## Choosing a deployment mode

The deployment modes are Local, Single node, and Clustered. Each mode keeps everything the mode before it does and adds server-side capability.

| Deployment mode | Server-side deployment | Catalog source | Materialization | What the mode adds |
|:--|:--|:--|:--|:--|
| [Local](local) | None | A folder, read from disk | User-driven sync | Authoring, lint, sync, domains, profiles, and ordered layers from disk |
| [Single node](single-node) | One binary | One or more folders or remote Git repos | User-driven sync, or agent-driven on demand | Everything in local, plus discovery through MCP or the SDKs, hybrid search, registered and remote layers with visibility, and one audit log |
| [Clustered](clustered) | Replicas, Postgres, and object storage | One or more folders or remote Git repos | User-driven sync, or agent-driven on demand | Everything in single node, plus Postgres schema-per-org tenant isolation and high availability across registry replicas |

Pick the mode that matches the requirement:

- **Local** fits a team or an individual whose catalog needs no access control and no progressive discovery. The catalog can live in a Git repository that every member syncs from.
- **Single node** fits a team that wants agents to discover artifacts at runtime through MCP, or wants one audit log for every load, without running Postgres, object storage, and an identity provider.
- **Clustered** fits an organization with 20 or more users, multi-tenant requirements, a governed environment, or compliance constraints.

Artifacts are the same in every mode. The catalog on disk does not change when the deployment changes. The same shared Go library parses, composes, and materializes it everywhere, so a given target and profile produce bit-identical output. [Local](migrating#skills-that-declare-no-version) lists the exceptions for skills that declare no version.

## What runs in each mode

- **Local** runs the `podium` CLI against a directory. There is no server process, no database, and no identity provider.
- **Single node** runs `podium serve --standalone`, one process with SQLite, sqlite-vec, and filesystem object storage embedded.
- **Clustered** runs the standard stack: stateless registry replicas behind a load balancer, Postgres, S3-compatible object storage, and an OIDC identity provider.

`--standalone` and the standard stack are configurations of the same registry binary. They are not separate builds.
