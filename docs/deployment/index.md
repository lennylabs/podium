---
layout: default
title: Deployment
nav_order: 4
has_children: true
description: Pick a deployment mode, run it, and migrate as the catalog grows. Filesystem for catalogs without access control or progressive disclosure needs, standalone for runtime discovery on a single binary, and standard for multi-tenant governance.
---

# Deployment

Pick a deployment mode based on the audience and operational tolerance:

| Mode | Audience | What's running | Page |
|:--|:--|:--|:--|
| **Filesystem** | Any team or individual whose catalog does not require access control or progressive disclosure. Includes solo work, prototypes, CI build steps, and Git-shared catalogs. | The `podium` CLI runs without a daemon, port, or authentication. The catalog is a folder. | [Solo / filesystem](solo-filesystem) |
| **Standalone server** | Anyone wanting runtime discovery or a single audit log without the full standard stack; single VM behind a VPN; offline or air-gapped environment. | One binary (`podium serve --standalone`). Embedded SQLite, sqlite-vec, and bundled embedding model. | [Small team](small-team) |
| **Standard** | Larger team; multi-tenant environment; governed catalog. | Replicated registry behind a load balancer; Postgres, object storage, and OIDC. | [Organization](organization) |

The modes share artifacts, author flow, and the underlying shared library. Migration between modes is mechanical: `podium serve --standalone --layer-path /path/to/dir` against a filesystem catalog turns it into a server source without changing the authoring loop, and `podium admin migrate-to-standard` exports a standalone deployment to a standard one.

---

## Other pages

| Page | What it covers |
|:--|:--|
| [Progressive adoption](progressive-adoption) | An on-ramp from permissive standalone to enforced governance, in stages. Use it once you have a standalone deployment running and want to add identity, sensitivity labels, signing, freeze windows, etc. |
| [Operator guide](operator-guide) | Day-two operations for a standard deployment: capacity planning, monitoring, alerting, backup and restore, upgrades, security review checklist, common pitfalls. |
| [Extending](extending) | Plugin SPIs, the forward-compatibility constraints that keep out-of-process plugins on the table, and the external-extension patterns. |
| [OIDC cookbooks](oidc/) | Per-IdP setup recipes for Okta, Entra ID, Google Workspace, Auth0, and Keycloak. |
